// The pipeline demo (`pie --demo`, demo/apple-pie.tape): one ticket parks on a
// question, the human answers it, and it runs on to a PR while the rest of the
// fleet works alongside. Its fixture tickets are shared with the review demo.
package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

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

// pipelineResumed makes the answer resume AND-219 exactly once.
var pipelineResumed sync.Once

var pipelineDemo = demoScenario{
	seed:     pipelineSeed,
	play:     pipelinePlay,
	answered: pipelineAnswered,
}

// pipelineSeed writes the opening frame: one PR already up, three agents at work.
func pipelineSeed(d *demoDriver) error {
	for _, t := range []demoTicket{demoCrash, demoDark, demoOffline, demoSpanish} {
		wt, err := d.claim(t)
		if err != nil {
			return err
		}
		if err := stubGit(wt); err != nil {
			return err
		}
	}
	d.log(demoCrash.id, "✓ PR opened - github.com/"+demoRepo+"/pull/412")
	_ = d.st.SetFields(demoCrash.id, "pie/"+demoCrash.id, paths.WorktreeFor(demoRepo, demoCrash.id), prURL(412))
	d.set(demoCrash.id, store.StateReview)
	d.set(demoDark.id, store.StateWorking)
	d.set(demoOffline.id, store.StatePlanning)
	d.set(demoSpanish.id, store.StateQueued)
	return nil
}

// pipelinePlay runs the fixed part of the timeline. AND-219's second half
// waits on the human: see pipelineAnswered.
func pipelinePlay(d *demoDriver) {
	d.after(
		step{1500 * time.Millisecond, func() { d.set(demoSpanish.id, store.StatePlanning) }},
		step{3 * time.Second, func() { askOffline(d) }},
		step{5 * time.Second, func() { d.set(demoDark.id, store.StateBuilding) }},
		step{6 * time.Second, func() { d.set(demoSpanish.id, store.StateWorking) }},
		step{9 * time.Second, func() { d.set(demoDark.id, store.StateTesting) }},
		step{16 * time.Second, func() { d.ship(demoDark.id, 415) }},
	)
}

// askOffline parks AND-219 on its question, the way the plan stage does.
func askOffline(d *demoDriver) {
	wt := paths.WorktreeFor(demoRepo, demoOffline.id)
	b, _ := json.Marshal(map[string]any{"questions": demoQuestions})
	_ = os.WriteFile(filepath.Join(wt, ".agent", "plan.json"), b, 0o644)
	d.log(demoOffline.id, "🤖 plan has 1 open question - waiting for you")
	d.set(demoOffline.id, store.StateAwaiting)
}

// pipelineAnswered resumes AND-219 through to its PR.
func pipelineAnswered(d *demoDriver, ticket string) {
	if ticket != demoOffline.id {
		return
	}
	pipelineResumed.Do(func() {
		d.log(ticket, "✓ answer received - resuming")
		d.after(
			step{300 * time.Millisecond, func() { d.set(ticket, store.StatePlanning) }},
			step{1500 * time.Millisecond, func() { d.set(ticket, store.StateWorking) }},
			step{3500 * time.Millisecond, func() { d.set(ticket, store.StateBuilding) }},
			step{5 * time.Second, func() { d.set(ticket, store.StateTesting) }},
			step{7 * time.Second, func() { d.ship(ticket, 416) }},
		)
	})
}
