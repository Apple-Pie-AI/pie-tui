package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/runapp"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// runAppModel: a configured repo, ticket PLEX-12 with a worktree, the device
// running and free, and the spawn seam recording its args.
func runAppModel(t *testing.T) (monitorModel, *[]string) {
	t.Helper()
	m := deviceModel("free")
	home := os.Getenv("PIE_HOME")
	repo := t.TempDir()
	if err := config.Save(&config.Config{Repos: []config.Repo{{Path: repo}}}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(home, "worktrees", "PLEX-12")
	_ = os.MkdirAll(wt, 0o755)
	_ = os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /x"), 0o644)
	m.flat = append(m.flat, store.Session{Ticket: "PLEX-12", Summary: "Login screen", Branch: "plex-12-login", Repo: repo})

	var spawned []string
	old := spawnRunApp
	spawnRunApp = func(_ string, args []string) error { spawned = args; return nil }
	t.Cleanup(func() { spawnRunApp = old })
	return m, &spawned
}

// "▶ Run app on this device" opens the wizard on the code step: the main
// checkout, then every ticket with a worktree.
func TestRunAppFromDeviceAction(t *testing.T) {
	m, _ := runAppModel(t)
	m, _ = m.runDeviceActionModel(daRunApp)
	if m.view != viewRunApp || m.runApp.step != raPickCode {
		t.Fatalf("view=%v step=%d", m.view, m.runApp.step)
	}
	var labels []string
	for _, c := range m.runApp.codes {
		labels = append(labels, c.label)
	}
	if strings.Join(labels, ",") != "main checkout,PLEX-12" {
		t.Errorf("codes = %v", labels)
	}
	out := stripAnsi(m.renderRunApp(240))
	for _, want := range []string{"Which code should run on the device?", "builds your local edits too", "Login screen · plex-12-login"} {
		if !strings.Contains(out, want) {
			t.Errorf("code step missing %q:\n%s", want, out)
		}
	}
}

// No recipe yet: one row that syncs and runs the default. With a recipe:
// its variants (last used preselected, release dimmed), then Sync project.
func TestRunAppVariantStep(t *testing.T) {
	m, spawned := runAppModel(t)
	mm, _ := m.openRunApp("PLEX-12")
	m = mm.(monitorModel)
	if m.runApp.step != raPickVariant || len(m.runApp.choices) != 1 || !m.runApp.choices[0].sync {
		t.Fatalf("first run should offer one sync-and-run row: %+v", m.runApp.choices)
	}
	if out := stripAnsi(m.renderRunApp(120)); !strings.Contains(out, "Sync project & run") {
		t.Errorf("first-run row:\n%s", out)
	}
	m, cmd := send(m, "enter")
	cmd() // the spawn runs inside the Cmd, off the update loop
	if m.runApp.step != raProgress || strings.Join(*spawned, " ") != "--job "+m.runApp.jobID+" --target PLEX-12 --sync" {
		t.Fatalf("spawned %v", *spawned)
	}

	// A learned recipe: its variants, the last one preselected.
	cfg, _ := config.Load()
	r := &runapp.Recipe{Model: runapp.Model{Apps: []runapp.AppModule{{Path: ":app",
		Variants: []string{"prodDebug", "stagingDebug", "stagingRelease"}}}}, LastModule: ":app", LastVariant: "stagingDebug"}
	if err := runapp.SaveRecipe(cfg.Repos[0].Path, r); err != nil {
		t.Fatal(err)
	}
	mm, _ = m.openRunApp("PLEX-12")
	m = mm.(monitorModel)
	if len(m.runApp.choices) != 4 || m.runApp.choices[m.runApp.sel].variant != "stagingDebug" {
		t.Fatalf("choices=%+v sel=%d", m.runApp.choices, m.runApp.sel)
	}
	out := stripAnsi(m.renderRunApp(120))
	for _, want := range []string{"Build variant", "stagingDebug", "last used", "needs signing", "Sync project"} {
		if !strings.Contains(out, want) {
			t.Errorf("variant step missing %q:\n%s", want, out)
		}
	}
	m, cmd = send(m, "enter")
	cmd()
	if got := strings.Join(*spawned, " "); !strings.Contains(got, "--target PLEX-12 --module :app --variant stagingDebug") {
		t.Errorf("spawned %q", got)
	}

	// Opened for one ticket, Esc from the variant step goes straight back.
	mm, _ = m.openRunApp("PLEX-12")
	m, _ = send(mm.(monitorModel), "esc")
	if m.view != viewDashboard {
		t.Errorf("esc from a ticket's variant step should leave, view=%v", m.view)
	}
}

// One agent at a time: running the app while an agent tests on the device
// asks to take it back first.
func TestRunAppAsksToTakeBackFromAnAgent(t *testing.T) {
	m, spawned := runAppModel(t)
	m.device.scan.dev.Holder = "PLEX-64818"
	mm, _ := m.openRunApp("PLEX-12")
	m, _ = send(mm.(monitorModel), "enter")
	if m.runApp.step != raConfirmTakeBack || m.runApp.sel != 1 {
		t.Fatalf("step=%d sel=%d - want the take-back confirm with Cancel selected", m.runApp.step, m.runApp.sel)
	}
	out := stripAnsi(m.renderRunApp(120))
	if !strings.Contains(out, "PLEX-64818's agent is testing on the device") || !strings.Contains(out, "Take it back and run") {
		t.Errorf("confirm:\n%s", out)
	}
	m, _ = send(m, "enter") // Cancel
	if m.runApp.step != raPickVariant || len(*spawned) != 0 {
		t.Errorf("cancel must not run: step=%d spawned=%v", m.runApp.step, *spawned)
	}
}

func TestRunAppProgressStates(t *testing.T) {
	m, _ := runAppModel(t)
	log := filepath.Join(t.TempDir(), "run.log")
	_ = os.WriteFile(log, []byte("> Task :app:packageDebug\nBUILD SUCCESSFUL\n"), 0o644)
	m.view, m.runApp.step = viewRunApp, raProgress
	m.runApp.code = raCode{label: "PLEX-12", target: "PLEX-12"}
	base := store.AppRun{Target: "PLEX-12", Variant: "debug", LogPath: log, StartedAt: time.Now().Unix()}

	running := base
	running.State, running.Step, running.Detail = store.AppRunRunning, runapp.StepBuild, "./gradlew :app:assembleDebug"
	m.runApp.run = running
	out := stripAnsi(m.renderRunApp(120))
	for _, want := range []string{"✓ Prepare", "✓ Sync", "● Build", "./gradlew :app:assembleDebug", "○ Launch", "L O G", "BUILD SUCCESSFUL", "Stop", "keeps running"} {
		if !strings.Contains(out, want) {
			t.Errorf("running missing %q:\n%s", want, out)
		}
	}

	repairing := running
	repairing.Step, repairing.Detail = runapp.StepRepair, "build failed - asking the agent what changed"
	m.runApp.run = repairing
	if out := stripAnsi(m.renderRunApp(120)); !strings.Contains(out, "↻ Build") || !strings.Contains(out, "Repair · build failed") {
		t.Errorf("repair:\n%s", out)
	}

	done := base
	done.State, done.Step, done.Detail, done.Repaired = store.AppRunDone, runapp.StepLaunch, "com.acme (debug) running on emulator-5554", true
	m.runApp.run = done
	out = stripAnsi(m.renderRunApp(120))
	if !strings.Contains(out, "✓ com.acme (debug) running on emulator-5554") || !strings.Contains(out, "learned for next time") || !strings.Contains(out, "▶ Run again") {
		t.Errorf("done:\n%s", out)
	}

	failed := base
	failed.State, failed.Step, failed.Detail = store.AppRunFailed, runapp.StepBuild, "the project's code doesn't build - LoginViewModel.kt:42"
	m.runApp.run = failed
	if out := stripAnsi(m.renderRunApp(120)); !strings.Contains(out, "✗ Build") || !strings.Contains(out, "LoginViewModel.kt:42") {
		t.Errorf("failed:\n%s", out)
	}
}
