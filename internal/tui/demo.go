// The hidden `pie --demo` mode: the real hub, driven by fixture tickets on a
// fast scripted timeline, for recording the launch video (demo/apple-pie.tape).
//
// Nothing here fakes the UI. The demo seeds a throwaway ~/.pie (via PIE_HOME)
// with sessions, plan.json questions and log lines, then writes state changes
// into the store exactly as `pie run` would; the hub's 1Hz reload picks them up
// like any other run. The only seams are the ones that would reach outside:
// the GitHub review poll is never armed, and answering a question or relaunching
// a run feeds the timeline instead of spawning `pie run`.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/telemetry"
)

// demoRepo is the fixture repository every demo ticket claims.
const demoRepo = "acme/shop-app"

// demoTicket is one fixture: its id, title, and the log lines each stage
// streams while it is current.
type demoTicket struct {
	id, title string
	lines     map[string][]string
}

var (
	demoCrash = demoTicket{id: "AND-207", title: "Fix crash when rotating the checkout screen"}
	demoDark  = demoTicket{id: "AND-214", title: "Dark mode toggle in Settings", lines: map[string][]string{
		store.StateWorking: {
			"⚙ Read app/src/main/java/com/acme/shop/settings/SettingsScreen.kt",
			"⚙ Read app/src/main/res/values/themes.xml",
			"⚙ Edit SettingsScreen.kt - add a ThemeToggleRow under Appearance",
			"⚙ Write ThemePreferences.kt - DataStore-backed theme mode",
			"⚙ Edit MainActivity.kt - collect the theme mode before setContent",
		},
		store.StateBuilding: {
			"⚙ Bash ./gradlew :app:assembleDebug",
			"• > Task :app:compileDebugKotlin",
			"✓ BUILD SUCCESSFUL in 41s",
		},
		store.StateTesting: {
			"⚙ Bash ./gradlew :app:testDebugUnitTest",
			"✓ ThemePreferencesTest > persists dark mode PASSED",
			"⚙ Bash adb shell am start -n com.acme.shop/.MainActivity",
			"✓ verified on emulator - Settings › Appearance › Dark theme toggles",
		},
	}}
	demoOffline = demoTicket{id: "AND-219", title: "Offline cache for the home feed", lines: map[string][]string{
		store.StatePlanning: {
			"🤖 planning AND-219",
			"⚙ Read feature/home/HomeRepository.kt",
			"⚙ Grep \"Room\" - no local database in the app yet",
		},
		store.StateWorking: {
			"🤖 implementing - Room cache, 24h TTL",
			"⚙ Write data/cache/FeedDao.kt",
			"⚙ Write data/cache/FeedCacheDatabase.kt",
			"⚙ Edit HomeRepository.kt - serve cache first, refresh in background",
		},
		store.StateBuilding: {
			"⚙ Bash ./gradlew :app:assembleDebug",
			"✓ BUILD SUCCESSFUL in 38s",
		},
		store.StateTesting: {
			"⚙ Bash ./gradlew :app:testDebugUnitTest",
			"✓ HomeRepositoryTest > serves cached feed offline PASSED",
			"✓ verified on emulator - airplane mode, feed still loads",
		},
	}}
	demoSpanish = demoTicket{id: "AND-221", title: "Spanish translations for onboarding", lines: map[string][]string{
		store.StatePlanning: {
			"🤖 planning AND-221",
			"⚙ Glob app/src/main/res/values*/strings.xml",
		},
		store.StateWorking: {
			"⚙ Read res/values/strings.xml - 34 onboarding strings",
			"⚙ Write res/values-es/strings.xml",
			"⚙ Edit OnboardingScreen.kt - drop two hard-coded strings",
			"⚙ Bash ./gradlew :app:lintDebug",
			"• checking plurals and placeholders",
			"⚙ Edit res/values-es/strings.xml - fix %1$d placeholder",
		},
	}}
)

// demoQuestions are the ones AND-219 stops on: the NEEDS YOU beat of the demo.
var demoQuestions = []string{
	"Should the cached feed expire after 24 hours, or stay until the next successful refresh?",
}

// demoDriver owns the fixture store and plays the timeline into it.
type demoDriver struct {
	st      *store.Store
	ctx     context.Context
	mu      sync.Mutex
	stage   map[string]string // ticket → current state, for the log chatter
	cursor  map[string]int    // ticket → next line of its current stage
	tickets map[string]demoTicket
	once    sync.Once // the answer resumes AND-219 exactly once
	wg      sync.WaitGroup
}

// RunDemo opens the hub on fixture tickets in a throwaway ~/.pie and plays the
// demo timeline until the user quits. The real ~/.pie is never touched.
func RunDemo(version string) error {
	home, err := os.MkdirTemp("", "pie-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	if err := os.Setenv("PIE_HOME", home); err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	st, err := store.Open(paths.StateDB())
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	d := &demoDriver{st: st, ctx: ctx, stage: map[string]string{}, cursor: map[string]int{},
		tickets: map[string]demoTicket{}}
	if err := d.seed(); err != nil {
		cancel()
		return err
	}
	d.goPlay()

	// No config is written, so the hub opens straight on the dashboard with
	// telemetry off - the consent screen is only for a configured install.
	m := monitorModel{store: st, tel: &telemetry.Client{}, selfPath: "pie", version: version,
		run: newRunState(), demo: d}
	m.reload()
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	cancel()
	d.wg.Wait() // the store closes only once no writer is left
	return err
}

// seed writes the opening frame: one PR already up, three agents at work.
func (d *demoDriver) seed() error {
	for _, t := range []demoTicket{demoCrash, demoDark, demoOffline, demoSpanish} {
		d.tickets[t.id] = t
		if _, err := d.st.Claim(t.id, demoRepo, t.title); err != nil {
			return err
		}
		wt := paths.WorktreeFor(demoRepo, t.id)
		if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
			return err
		}
		// A .git entry is what paths.WorktreeReady looks for; without one the
		// detail pane reports every fixture's worktree as removed.
		if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wt+".git\n"), 0o644); err != nil {
			return err
		}
		// A local .md source is what makes "Answer the questions" available.
		src := filepath.Join(paths.Pasted(), t.id+".md")
		if err := os.WriteFile(src, []byte("# "+t.title+"\n"), 0o644); err != nil {
			return err
		}
		_ = d.st.SetSourcePath(t.id, src)
		_ = d.st.SetFields(t.id, "pie/"+t.id, wt, "")
	}
	d.log(demoCrash.id, "✓ PR opened - github.com/"+demoRepo+"/pull/412")
	_ = d.st.SetFields(demoCrash.id, "pie/"+demoCrash.id, paths.WorktreeFor(demoRepo, demoCrash.id),
		"https://github.com/"+demoRepo+"/pull/412")
	d.set(demoCrash.id, store.StateReview)
	d.set(demoDark.id, store.StateWorking)
	d.set(demoOffline.id, store.StatePlanning)
	d.set(demoSpanish.id, store.StateQueued)
	return nil
}

// goPlay runs the fixed part of the timeline and the log chatter. AND-219's
// second half waits on the human: see answered.
func (d *demoDriver) goPlay() {
	d.after(
		step{1500 * time.Millisecond, func() { d.set(demoSpanish.id, store.StatePlanning) }},
		step{3 * time.Second, d.askOffline},
		step{5 * time.Second, func() { d.set(demoDark.id, store.StateBuilding) }},
		step{6 * time.Second, func() { d.set(demoSpanish.id, store.StateWorking) }},
		step{9 * time.Second, func() { d.set(demoDark.id, store.StateTesting) }},
		step{16 * time.Second, func() { d.ship(demoDark.id, 415) }},
	)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		t := time.NewTicker(700 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-d.ctx.Done():
				return
			case <-t.C:
				d.chatter()
			}
		}
	}()
}

// askOffline parks AND-219 on its question, the way the plan stage does.
func (d *demoDriver) askOffline() {
	wt := paths.WorktreeFor(demoRepo, demoOffline.id)
	b, _ := json.Marshal(map[string]any{"questions": demoQuestions})
	_ = os.WriteFile(filepath.Join(wt, ".agent", "plan.json"), b, 0o644)
	d.log(demoOffline.id, "🤖 plan has 1 open question - waiting for you")
	d.set(demoOffline.id, store.StateAwaiting)
}

// answered resumes AND-219 through to its PR. It replaces submitAnswer's
// `pie run` relaunch, and returns the same message so the hub's notice reads
// exactly as it does for a real answer.
func (d *demoDriver) answered(ticket string) tea.Cmd {
	return func() tea.Msg {
		if ticket != demoOffline.id {
			return answerDoneMsg{ticket: ticket}
		}
		d.once.Do(func() {
			d.log(ticket, "✓ answer received - resuming")
			d.after(
				step{300 * time.Millisecond, func() { d.set(ticket, store.StatePlanning) }},
				step{1500 * time.Millisecond, func() { d.set(ticket, store.StateWorking) }},
				step{3500 * time.Millisecond, func() { d.set(ticket, store.StateBuilding) }},
				step{5 * time.Second, func() { d.set(ticket, store.StateTesting) }},
				step{7 * time.Second, func() { d.ship(ticket, 416) }},
			)
		})
		return answerDoneMsg{ticket: ticket}
	}
}

// step is one beat of a timeline, at an offset from when the timeline starts.
type step struct {
	at time.Duration
	do func()
}

// after plays steps in order, each at its offset, until the demo exits.
func (d *demoDriver) after(steps ...step) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		start := time.Now()
		for _, s := range steps {
			select {
			case <-d.ctx.Done():
				return
			case <-time.After(time.Until(start.Add(s.at))):
				s.do()
			}
		}
	}()
}

// set moves a ticket to state, and points its chatter at that stage's lines.
// Active states carry this process's pid, so the hub sees a live driver.
func (d *demoDriver) set(ticket, state string) {
	pid := 0
	if store.IsActive(state) {
		pid = os.Getpid()
	}
	_ = d.st.SetPID(ticket, pid)
	_ = d.st.SetState(ticket, state, 0)
	d.mu.Lock()
	d.stage[ticket], d.cursor[ticket] = state, 0
	d.mu.Unlock()
}

// ship is the orchestrator's half: commit, push, open the PR, park at review.
func (d *demoDriver) ship(ticket string, pr int) {
	url := fmt.Sprintf("https://github.com/%s/pull/%d", demoRepo, pr)
	d.log(ticket, "⚙ git push -u origin pie/"+ticket)
	d.log(ticket, "✓ PR opened - "+url[len("https://"):])
	_ = d.st.SetFields(ticket, "pie/"+ticket, paths.WorktreeFor(demoRepo, ticket), url)
	d.set(ticket, store.StateReview)
}

// chatter streams the next line of every running ticket's current stage.
func (d *demoDriver) chatter() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, state := range d.stage {
		lines := d.tickets[id].lines[state]
		if i := d.cursor[id]; i < len(lines) {
			d.log(id, lines[i])
			d.cursor[id] = i + 1
		}
	}
}

// log appends one line to the ticket's log, in the runner's "[TICKET] " form.
func (d *demoDriver) log(ticket, line string) {
	f, err := os.OpenFile(paths.LogFor(ticket), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] %s\n", ticket, line)
}
