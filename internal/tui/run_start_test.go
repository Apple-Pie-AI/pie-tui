package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/git"
)

func startFixture(mut func(r *runState)) monitorModel {
	return monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{kind: "content", title: "t", lines: 1, id: "T-1"}}
			mut(r)
		}),
	}
}

// Esc in the branch-first picker cancels the whole creation - there is no
// prior step to step back to (unlike the old chip-flow picker, which could
// return to a start-mode choice; this entry point has none).
func TestInitialBranchPickEscCancels(t *testing.T) {
	m := startFixture(func(r *runState) {
		r.tickets = nil
		r.branchFirstPick = true
		r.stackBranches = []git.Branch{{Name: "main"}}
	})
	nm, _ := m.updateRunBranchPick(tea.KeyMsg{Type: tea.KeyEsc})
	hub := nm.(monitorModel)
	if hub.view != viewDashboard {
		t.Errorf("Esc should cancel back to the dashboard, view=%d", hub.view)
	}
}

// The launch flag shape. Chips only ever emit --branch now: the checkout flow
// replaced from-branch chips, and --from-branch survives as a CLI-only flag.
// reviewPlan only becomes a flag when reviewPlanSet is true (the picker was
// actually answered) - and an explicit "No" must pass --review-plan=false, not
// just omit the flag, or an enabled config default would silently override it.
func TestChipRunFlags(t *testing.T) {
	explicitNo := strings.Join(chipRunFlags(pendingTicket{branch: "fix/x", reviewPlan: false, reviewPlanSet: true}), " ")
	if explicitNo != "--branch fix/x --review-plan=false" {
		t.Errorf("explicit-no flags = %q", explicitNo)
	}
	scratch := strings.Join(chipRunFlags(pendingTicket{branch: "pie/t-1", base: "parent"}), " ")
	if scratch != "--base parent --branch pie/t-1" {
		t.Errorf("scratch flags = %q", scratch)
	}
}

// The dashboard's branch-first entry (no chip exists yet) drives the picker
// through branchFirstPick. renderRunInput used to only check pickPrompt (a
// since-removed chip-flow field), so this state fell through to the generic
// "Write or paste the ticket content" content-mode screen with no editor
// armed yet (m.run.content is still nil at this point) - a blank screen that
// silently ate keystrokes meant for the invisible branch picker underneath.
func TestBranchFirstPickRendersThePicker(t *testing.T) {
	m := startFixture(func(r *runState) {
		r.tickets = nil // no chip exists yet in this entry point
		r.branchFirstPick = true
		r.stackBranches = []git.Branch{{Name: "fix/plex-1-login"}}
	})
	v := m.renderRunInput(80)
	if !strings.Contains(v, "fix/plex-1-login") || !strings.Contains(v, "no agent runs") {
		t.Errorf("branch-first render should show the branch picker, not fall through:\n%s", v)
	}
	if strings.Contains(v, "Write or paste the ticket content") {
		t.Errorf("branch-first render fell through to the generic content screen:\n%s", v)
	}
}

// Enter on a branch hands off to the checkout cmd and returns to the
// dashboard - no editor, no chip, no run.
func TestBranchPickHandsOffToCheckout(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	m := baseModel()
	m.run.branchFirstPick = true
	m.run.stackBranches = []git.Branch{{Name: "feature/x"}}
	got, cmd := m.updateRunBranchPick(tea.KeyMsg{Type: tea.KeyEnter})
	hub := got.(monitorModel)
	if hub.run.content != nil {
		t.Fatal("no editor may open - checkout replaced the content flow")
	}
	if hub.view != viewDashboard {
		t.Fatalf("view = %v, want the dashboard", hub.view)
	}
	if cmd == nil || !strings.Contains(hub.notice, "checking out") {
		t.Fatalf("checkout cmd not spawned (notice %q)", hub.notice)
	}
	// Deliberately NOT executing cmd here: it would `git fetch origin` against
	// a repo this fixture never configured. checkout_test covers the real run.
}

// The authorize-in-tool flow: picking a branch held by the user's own worktree
// prompts instead of proceeding; enter detaches the holder and continues into
// the checkout; esc walks away leaving the holder untouched.
func TestBranchPickAsksBeforeDetachingTheUsersWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIE_HOME", home)
	repo := t.TempDir()
	gitc := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	gitc(repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(repo, "add", ".")
	gitc(repo, "commit", "-q", "-m", "base")
	held := filepath.Join(t.TempDir(), "users-checkout")
	gitc(repo, "worktree", "add", "-b", "feature/busy", held)
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("[[repo]]\npath = \""+repo+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := baseModel()
	m.run.branchFirstPick = true
	m.run.stackBranches = []git.Branch{{Name: "feature/busy"}}

	// Enter on the held branch: prompt, not editor.
	got, _ := m.updateRunBranchPick(tea.KeyMsg{Type: tea.KeyEnter})
	m = got.(monitorModel)
	if m.run.holderPath == "" {
		t.Fatal("picking a held branch must raise the authorization prompt")
	}
	if m.run.content != nil {
		t.Fatal("the editor must not open before authorization")
	}
	out := ansiRe.ReplaceAllString(m.renderRunBranchPick(100), "")
	for _, want := range []string{"feature/busy", "users-checkout", "uncommitted work untouched", "enter", "esc"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q:\n%s", want, out)
		}
	}

	// Esc: walk away; the holder still holds the branch.
	got, _ = m.updateRunBranchPick(tea.KeyMsg{Type: tea.KeyEsc})
	m2 := got.(monitorModel)
	if m2.run.holderPath != "" || git.BranchHolder(repo, "feature/busy") == "" {
		t.Fatal("esc must cancel without touching the holder")
	}

	// Re-pick, then enter to authorize: holder detached, and the flow hands
	// off to the checkout cmd - no editor anywhere in this flow anymore.
	got, _ = m2.updateRunBranchPick(tea.KeyMsg{Type: tea.KeyEnter})
	m3 := got.(monitorModel)
	got, cmd := m3.updateRunBranchPick(tea.KeyMsg{Type: tea.KeyEnter})
	m4 := got.(monitorModel)
	if git.BranchHolder(repo, "feature/busy") != "" {
		t.Fatal("authorize must free the branch")
	}
	if m4.run.content != nil {
		t.Fatal("no editor may open - checkout replaced the content flow")
	}
	if cmd == nil || m4.view != viewDashboard {
		t.Fatalf("authorize must hand off to the checkout cmd (view %v)", m4.view)
	}
	// Not executing cmd: it would fetch origin in this remote-less fixture;
	// checkout_test covers the real checkout.
}
