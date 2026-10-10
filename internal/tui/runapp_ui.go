// Run app, as a screen: pick the code (main checkout or a ticket), pick the
// variant (Studio's Build Variants), then follow the run. The run itself is a
// detached `pie run-app` process, so leaving the screen - or the hub - never
// stops it; this screen only reads its store row and log.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/runapp"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// Run app wizard steps.
const (
	raPickCode = iota
	raPickVariant
	raConfirmTakeBack
	raProgress
)

// runAppState is the screen's state (one field on monitorModel).
type runAppState struct {
	step    int
	sel     int
	codes   []raCode
	code    raCode
	recipe  *runapp.Recipe
	choices []raChoice
	choice  raChoice
	jobID   string
	run     store.AppRun
	err     string
	direct  bool // opened for one ticket: Esc from the variant step leaves
}

// raCode is something to build: the main checkout or a ticket's worktree.
type raCode struct {
	label, meta, target, dir string
}

// raChoice is one variant row, or the "rescan" row.
type raChoice struct {
	module, variant, meta string
	sync                  bool
}

type runAppSpawnedMsg struct {
	id  string
	err error
}

// spawnRunApp is the seam that starts the detached `pie run-app`.
var spawnRunApp = func(self string, args []string) error {
	c := exec.Command(self, append([]string{"run-app"}, args...)...)
	c.Env = append(os.Environ(), "PIE_RUN_APP_QUIET=1")
	return startDetached(c)
}

// openRunApp opens the wizard; target "" asks for the code first.
func (m monitorModel) openRunApp(target string) (tea.Model, tea.Cmd) {
	m.runApp = runAppState{}
	m.runApp.codes = m.runAppCodes()
	m.view = viewRunApp
	m.device.focus = false
	if target != "" {
		for _, c := range m.runApp.codes {
			if c.target == target {
				m.runApp.direct = true
				return m.pickCode(c)
			}
		}
		m.notice = target + " has no worktree to build"
	}
	return m, nil
}

// runAppCodes lists the main checkout and every ticket with a worktree.
func (m monitorModel) runAppCodes() []raCode {
	cfg, err := config.Load()
	if err != nil || len(cfg.Repos) == 0 {
		return nil
	}
	repo := cfg.Repos[0].Path
	codes := []raCode{{label: "main checkout", meta: tildify(repo) + " · builds your local edits too", target: "main", dir: repo}}
	for _, s := range m.flat {
		wt := paths.WorktreeFor(s.Repo, s.Ticket)
		if !paths.WorktreeReady(wt) {
			continue
		}
		meta := s.Branch
		if s.Summary != "" {
			meta = s.Summary + " · " + s.Branch
		}
		codes = append(codes, raCode{label: s.Ticket, meta: meta, target: s.Ticket, dir: wt})
	}
	return codes
}

// pickCode moves to the variant step, from what the recipe already knows.
func (m monitorModel) pickCode(c raCode) (tea.Model, tea.Cmd) {
	m.runApp.code, m.runApp.sel, m.runApp.step = c, 0, raPickVariant
	cfg, _ := config.Load()
	repo := ""
	if cfg != nil && len(cfg.Repos) > 0 {
		repo = cfg.Repos[0].Path
	}
	r, err := runapp.LoadRecipe(repo)
	if err != nil {
		m.runApp.err = err.Error()
		r = &runapp.Recipe{}
	}
	m.runApp.recipe = r
	m.runApp.choices = nil
	for _, mod := range r.Model.Apps {
		for _, v := range mod.Variants {
			meta := mod.Path
			if v == r.LastVariant && mod.Path == firstNonEmptyStr(r.LastModule, mod.Path) {
				meta += " · last used"
			}
			if !isDebug(v) {
				meta += " · needs signing"
			}
			m.runApp.choices = append(m.runApp.choices, raChoice{module: mod.Path, variant: v, meta: meta})
		}
	}
	// The last-used variant leads, like Studio keeps your Build Variant.
	for i, ch := range m.runApp.choices {
		if ch.variant == r.LastVariant {
			m.runApp.sel = i
		}
	}
	if len(m.runApp.choices) == 0 {
		m.runApp.choices = []raChoice{{sync: true, meta: "first run: scans the Gradle tasks, then runs the default (debug) variant"}}
	} else {
		m.runApp.choices = append(m.runApp.choices, raChoice{sync: true, meta: "rescan the Gradle tasks (it's automatic when a build file changes), then run the default"})
	}
	return m, nil
}

// updateRunApp routes keys: ↑↓ Enter Esc only.
func (m monitorModel) updateRunApp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ra := &m.runApp
	n := 0
	switch ra.step {
	case raPickCode:
		n = len(ra.codes)
	case raPickVariant:
		n = len(ra.choices)
	case raConfirmTakeBack:
		n = 2
	case raProgress:
		n = 2
	}
	switch msg.String() {
	case "up":
		if ra.sel > 0 {
			ra.sel--
		}
	case "down":
		if ra.sel < n-1 {
			ra.sel++
		}
	case "esc":
		// Back one step; from the first step (or a finished run), the dashboard.
		switch {
		case ra.step == raConfirmTakeBack:
			ra.step, ra.sel = raPickVariant, 0
		case ra.step == raPickVariant && !ra.direct:
			ra.step, ra.sel = raPickCode, 0
		default:
			m.view = viewDashboard
		}
	case "enter":
		return m.enterRunApp()
	}
	return m, nil
}

func (m monitorModel) enterRunApp() (tea.Model, tea.Cmd) {
	ra := &m.runApp
	switch ra.step {
	case raPickCode:
		if ra.sel < len(ra.codes) {
			return m.pickCode(ra.codes[ra.sel])
		}
	case raPickVariant:
		if ra.sel >= len(ra.choices) {
			return m, nil
		}
		ra.choice = ra.choices[ra.sel]
		// One agent at a time: an agent using the device must be taken back first.
		if h := m.device.scan.dev.Holder; h != "" && h != store.HolderYou {
			ra.step, ra.sel = raConfirmTakeBack, 1
			return m, nil
		}
		return m.startRunApp(nil)
	case raConfirmTakeBack:
		if ra.sel == 0 {
			return m.startRunApp(m.takeBackCmd())
		}
		ra.step, ra.sel = raPickVariant, 0
	case raProgress:
		if ra.run.State == store.AppRunRunning {
			if ra.sel == 0 {
				pid := ra.run.PID
				m.notice = "stopping the run…"
				return m, func() tea.Msg { proc.TerminateGroup(pid, killGrace); return deviceOpMsg{notice: "run stopped"} }
			}
			m.view = viewDashboard
			m.notice = "Run app keeps running in the background"
			return m, nil
		}
		if ra.sel == 0 {
			return m.startRunApp(nil) // Run again
		}
		m.view = viewDashboard
	}
	return m, nil
}

// startRunApp spawns the run (after an optional take-back).
func (m monitorModel) startRunApp(first tea.Cmd) (tea.Model, tea.Cmd) {
	ra := &m.runApp
	id := time.Now().Format("0102-150405.000")
	args := []string{"--job", id, "--target", ra.code.target}
	if ra.choice.sync {
		args = append(args, "--sync")
	} else {
		args = append(args, "--module", ra.choice.module, "--variant", ra.choice.variant)
	}
	ra.jobID, ra.step, ra.sel, ra.err = id, raProgress, 0, ""
	ra.run = store.AppRun{ID: id, Target: ra.code.target, State: store.AppRunRunning, Step: runapp.StepPrepare,
		Variant: ra.choice.variant, StartedAt: time.Now().Unix()}
	self := m.selfPath
	spawn := func() tea.Msg { return runAppSpawnedMsg{id: id, err: spawnRunApp(self, args)} }
	if first != nil {
		return m, tea.Sequence(first, spawn)
	}
	return m, spawn
}

// refreshRunApp re-reads the followed run (the 1s tick, while on screen).
func (m *monitorModel) refreshRunApp() {
	if m.view != viewRunApp || m.runApp.jobID == "" || m.store == nil {
		return
	}
	if r, err := m.store.GetAppRun(m.runApp.jobID); err == nil {
		if r.State == store.AppRunRunning && r.PID > 0 && !proc.Alive(r.PID) {
			r.State, r.Detail = store.AppRunFailed, "the run process exited unexpectedly - see the log"
		}
		m.runApp.run = r
	}
}

func (m monitorModel) handleRunAppSpawned(msg runAppSpawnedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.runApp.run.State = store.AppRunFailed
		m.runApp.run.Detail = fmt.Sprintf("could not start pie run-app: %v", msg.err)
	}
	return m, nil
}

func isDebug(v string) bool { return len(v) >= 5 && v[len(v)-5:] == "Debug" || v == "debug" }

func firstNonEmptyStr(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
