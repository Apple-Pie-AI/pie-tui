// The hidden `pie --demo` mode: the real hub, driven by fixture tickets on a
// fast scripted timeline, for recording launch videos (demo/*.tape).
//
// Nothing here fakes the UI. The demo seeds a throwaway ~/.pie (via PIE_HOME)
// with sessions, agent files and log lines, then writes state changes into the
// store exactly as `pie run` would; the hub's 1Hz reload picks them up like any
// other run. The only seams are the ones that would reach outside: the GitHub
// review poll and fetch are never run, and answering a question or launching a
// run feeds the scenario's timeline instead of spawning `pie run`.
//
// This file is the driver; each scenario (its fixtures and its timeline) is a
// demo_<name>.go file.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/telemetry"
)

// demoRepo is the fixture repository every demo ticket claims.
const demoRepo = "acme/shop-app"

// demoScenario is one recordable story. seed writes the opening frame, play
// starts the part of the timeline that runs on its own, and the two hooks
// stand in for the hub's only ways of starting work: answering a question
// (answered) and spawning `pie run <args>` (spawned). Either hook may be nil.
type demoScenario struct {
	seed     func(d *demoDriver) error
	play     func(d *demoDriver)
	answered func(d *demoDriver, ticket string)
	spawned  func(d *demoDriver, args []string)
}

// demoScenarios is what `pie --demo=<name>` accepts.
var demoScenarios = map[string]demoScenario{
	"pipeline": pipelineDemo, // ticket → NEEDS YOU → PR ready (demo_pipeline.go)
	"review":   reviewDemo,   // PR comments → agent fixes → fixes ready (demo_review.go)
}

// DemoScenarios lists the scenario names, for the flag's help and errors.
func DemoScenarios() []string {
	names := make([]string, 0, len(demoScenarios))
	for n := range demoScenarios {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// demoTicket is one fixture: its id, title, and the log lines each stage
// streams while it is current.
type demoTicket struct {
	id, title string
	lines     map[string][]string
}

// demoDriver owns the fixture store and plays a scenario's timeline into it.
type demoDriver struct {
	sc      demoScenario
	st      *store.Store
	ctx     context.Context
	mu      sync.Mutex
	stage   map[string]string // ticket → current state, for the log chatter
	cursor  map[string]int    // ticket → next line of its current stage
	tickets map[string]demoTicket
	wg      sync.WaitGroup
}

func newDemoDriver(ctx context.Context, sc demoScenario, st *store.Store) *demoDriver {
	return &demoDriver{sc: sc, st: st, ctx: ctx, stage: map[string]string{},
		cursor: map[string]int{}, tickets: map[string]demoTicket{}}
}

// RunDemo opens the hub on a scenario's fixture tickets in a throwaway ~/.pie
// and plays its timeline until the user quits. The real ~/.pie is never touched.
func RunDemo(version, scenario string) error {
	sc, ok := demoScenarios[scenario]
	if !ok {
		return fmt.Errorf("unknown demo %q (have: %s)", scenario, strings.Join(DemoScenarios(), ", "))
	}
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
	d := newDemoDriver(ctx, sc, st)
	if err := sc.seed(d); err != nil {
		cancel()
		return err
	}
	sc.play(d)
	d.goChatter()

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

// answered replaces submitAnswer's `pie run` relaunch, and returns the same
// message so the hub's notice reads exactly as it does for a real answer.
func (d *demoDriver) answered(ticket string) tea.Cmd {
	return func() tea.Msg {
		if d.sc.answered != nil {
			d.sc.answered(d, ticket)
		}
		return answerDoneMsg{ticket: ticket}
	}
}

// spawned replaces spawnRun. It returns no message on purpose: the caller has
// already set the notice that describes the run, and "launched run: <path>"
// would overwrite it with a fixture path.
func (d *demoDriver) spawned(args []string) tea.Cmd {
	return func() tea.Msg {
		if d.sc.spawned != nil {
			d.sc.spawned(d, args)
		}
		return nil
	}
}

// claim inserts a fixture ticket with a local .md source (what makes "Answer
// the questions" available) and its worktree directory, and returns the
// worktree. The scenario decides what makes the worktree "ready".
func (d *demoDriver) claim(t demoTicket) (string, error) {
	d.tickets[t.id] = t
	if _, err := d.st.Claim(t.id, demoRepo, t.title); err != nil {
		return "", err
	}
	wt := paths.WorktreeFor(demoRepo, t.id)
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		return "", err
	}
	src := filepath.Join(paths.Pasted(), t.id+".md")
	if err := os.WriteFile(src, []byte("# "+t.title+"\n"), 0o644); err != nil {
		return "", err
	}
	_ = d.st.SetSourcePath(t.id, src)
	_ = d.st.SetFields(t.id, "pie/"+t.id, wt, "")
	return wt, nil
}

// stubGit gives a fixture worktree the .git entry paths.WorktreeReady looks
// for; without one the detail pane reports the worktree as removed.
func stubGit(wt string) error {
	return os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wt+".git\n"), 0o644)
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

// prURL is a fixture ticket's pull request.
func prURL(pr int) string { return fmt.Sprintf("https://github.com/%s/pull/%d", demoRepo, pr) }

// ship is the orchestrator's half: commit, push, open the PR, park at review.
func (d *demoDriver) ship(ticket string, pr int) {
	url := prURL(pr)
	d.log(ticket, "⚙ git push -u origin pie/"+ticket)
	d.log(ticket, "✓ PR opened - "+strings.TrimPrefix(url, "https://"))
	_ = d.st.SetFields(ticket, "pie/"+ticket, paths.WorktreeFor(demoRepo, ticket), url)
	d.set(ticket, store.StateReview)
}

// goChatter streams log lines for every running ticket until the demo exits.
func (d *demoDriver) goChatter() {
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
