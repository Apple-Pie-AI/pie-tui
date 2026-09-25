package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// repoWithLateRemoteBranch builds a repo with a bare origin, a first commit
// pushed to it, and a SECOND branch pushed to origin from a separate clone -
// so repo's own git has never fetched since that push and git.Branches(repo)
// (no network call by design) must not see it yet. This is the exact bug:
// without a fetch on open, that branch never appears in the picker.
func repoWithLateRemoteBranch(t *testing.T) (repo string) {
	t.Helper()
	origin, repo, other := t.TempDir(), t.TempDir(), t.TempDir()
	gitc := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	gitc(repo, "init", "-q", "-b", "main")
	gitc(repo, "remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(repo, "add", ".")
	gitc(repo, "commit", "-q", "-m", "base")
	gitc(repo, "push", "-q", "-u", "origin", "main")

	// A second clone pushes a new branch straight to origin - repo (the one
	// the picker will use) never sees this until it fetches.
	gitc(other, "clone", "-q", origin, ".")
	gitc(other, "checkout", "-q", "-b", "feature/just-pushed")
	gitc(other, "push", "-q", "origin", "feature/just-pushed")

	before, _ := git.Branches(repo)
	for _, b := range before {
		if b.Name == "feature/just-pushed" {
			t.Fatal("test setup bug: repo already knows about feature/just-pushed before any fetch")
		}
	}
	return repo
}

// runFetchCmd unwraps and runs the tea.Cmd loadStackBranchesCmd returns,
// mirroring the real tea.Program's own handling.
func runFetchCmd(cmd tea.Cmd) stackBranchesMsg {
	return cmd().(stackBranchesMsg)
}

// The regression this fixes: Checkout a branch (openInitialBranchPick) must
// fetch before listing, so a branch pushed since the last fetch shows up
// without the user leaving the screen or doing anything else first.
func TestOpenInitialBranchPickFetchesBeforeListing(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	repo := repoWithLateRemoteBranch(t)
	if err := config.Save(&config.Config{Repos: []config.Repo{{Path: repo}}}); err != nil {
		t.Fatal(err)
	}

	m := monitorModel{}
	got, cmd := m.openInitialBranchPick()
	m = got.(monitorModel)
	if !m.run.stackLoading {
		t.Error("opening the picker must set stackLoading")
	}
	if cmd == nil {
		t.Fatal("openInitialBranchPick must return the fetch+list command")
	}
	msg := runFetchCmd(cmd)
	if !msg.branchFirst {
		t.Error("the message must be tagged branchFirst")
	}
	found := false
	for _, b := range msg.branches {
		if b.Name == "feature/just-pushed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the fetch must reveal the just-pushed branch, got %+v", msg.branches)
	}

	got2, _ := m.Update(msg)
	m = got2.(monitorModel)
	if m.run.stackLoading {
		t.Error("stackLoading must clear once the fetch resolves")
	}
	found = false
	for _, b := range m.run.stackBranches {
		if b.Name == "feature/just-pushed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the picker's branch list must include the just-pushed branch, got %+v", m.run.stackBranches)
	}
}

// Same fix, the other entry point: openStackPicker (ctrl+s / a content
// ticket's finalize chain).
func TestOpenStackPickerFetchesBeforeListing(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	repo := repoWithLateRemoteBranch(t)
	if err := config.Save(&config.Config{Repos: []config.Repo{{Path: repo}}}); err != nil {
		t.Fatal(err)
	}

	m := monitorModel{run: newRun(func(r *runState) {
		r.tickets = []pendingTicket{{id: "LOCAL-1", kind: "content"}}
	})}
	got, cmd := m.openStackPicker(0)
	m = got.(monitorModel)
	if cmd == nil {
		t.Fatal("openStackPicker must return the fetch+list command")
	}
	msg := runFetchCmd(cmd)
	if msg.branchFirst || msg.chipIndex != 0 {
		t.Errorf("the message must be tagged for chip 0, not branchFirst, got %+v", msg)
	}

	got2, _ := m.Update(msg)
	m = got2.(monitorModel)
	found := false
	for _, b := range m.run.stackBranches {
		if b.Name == "feature/just-pushed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the picker's branch list must include the just-pushed branch, got %+v", m.run.stackBranches)
	}
}

// A stackBranchesMsg that lands after the user has already closed the picker
// (or opened a DIFFERENT chip's) must be discarded, not applied to whatever
// happens to be open now.
func TestStackBranchesMsgStaleGuard(t *testing.T) {
	branches := []git.Branch{{Name: "feature/just-pushed"}}

	// branchFirst: the user left the branch-first picker before the fetch resolved.
	m := monitorModel{run: newRun(func(r *runState) { r.branchFirstPick = false })}
	got, _ := m.Update(stackBranchesMsg{branchFirst: true, branches: branches})
	m = got.(monitorModel)
	if m.run.stackBranches != nil {
		t.Errorf("a stale branchFirst result must not populate stackBranches, got %+v", m.run.stackBranches)
	}

	// chipIndex: the user closed chip 0's stack picker (or opened chip 1's)
	// before chip 0's fetch resolved.
	m2 := monitorModel{run: newRun(func(r *runState) { r.stackPrompt = 1 })}
	got2, _ := m2.Update(stackBranchesMsg{chipIndex: 0, branches: branches})
	m2 = got2.(monitorModel)
	if m2.run.stackBranches != nil {
		t.Errorf("a stale chip-0 result must not apply while chip 1's picker is open, got %+v", m2.run.stackBranches)
	}
}

// stackModel builds a monitorModel parked on the stack-picker sub-step for one
// pending ticket of the given kind, with a single selectable branch.
func stackModel(kind string) monitorModel {
	return monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = kind
			r.tickets = []pendingTicket{{id: "LOCAL-9", kind: kind, title: "t", lines: 1}}
			r.stackPrompt = 0
			r.stackBranches = []git.Branch{{Name: "ai/parent"}}
		}),
	}
}

// Regression: pressing Esc in the stack picker must CANCEL the creation, never
// launch the ticket. Previously Esc on a content ticket auto-launched it.
func TestStackEscCancelsContentDoesNotLaunch(t *testing.T) {
	m := stackModel("content")
	nm, cmd := m.updateRunStack(tea.KeyMsg{Type: tea.KeyEsc})
	hub := nm.(monitorModel)
	if cmd != nil {
		t.Error("Esc on a content stack step returned a command - it must not launch")
	}
	if hub.view != viewDashboard {
		t.Errorf("Esc should return to the dashboard, got view=%d", hub.view)
	}
	if hub.run.tickets != nil {
		t.Errorf("Esc should clear pending tickets, got %+v", hub.run.tickets)
	}
	if hub.run.stackPrompt != -1 {
		t.Errorf("Esc should reset runStackPrompt to -1, got %d", hub.run.stackPrompt)
	}
}

// Enter on a content stack step advances to the final plan-review toggle step
// (no launch yet - the review step's Enter launches).
func TestStackEnterContentOpensReviewStep(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	m := stackModel("content")
	m.run.stackCursor = 0 // the "- none -" row
	nm, cmd := m.updateRunStack(tea.KeyMsg{Type: tea.KeyEnter})
	hub := nm.(monitorModel)
	if cmd != nil {
		t.Error("Enter on a content stack step should not launch yet - it opens the review step")
	}
	if hub.run.reviewPrompt != 0 {
		t.Errorf("stack Enter should open the review step (runReviewPrompt=0), got %d", hub.run.reviewPrompt)
	}
}

// The plan-review step launches on Enter for either choice (returns a non-nil
// command and consumes the pending tickets back to the dashboard).
func TestReviewStepEnterLaunches(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	for _, cursor := range []int{0, 1} {
		m := monitorModel{
			view: viewRunInput,
			run: newRun(func(r *runState) {
				r.mode = "content"
				r.tickets = []pendingTicket{{id: "LOCAL-9", kind: "content", title: "t", lines: 1}}
				r.reviewPrompt = 0
				r.reviewCursor = cursor
			}),
		}
		nm, cmd := m.updateRunReview(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Errorf("cursor=%d: review-step Enter should launch (non-nil command)", cursor)
		}
		if nm.(monitorModel).view != viewDashboard {
			t.Errorf("cursor=%d: review-step Enter should return to dashboard", cursor)
		}
	}
}

// Esc in the review step cancels the whole creation (content ticket).
func TestReviewStepEscCancels(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{id: "LOCAL-9", kind: "content", title: "t", lines: 1}}
			r.reviewPrompt = 0
		}),
	}
	nm, cmd := m.updateRunReview(tea.KeyMsg{Type: tea.KeyEsc})
	hub := nm.(monitorModel)
	if cmd != nil {
		t.Error("Esc in the review step must not launch")
	}
	if hub.view != viewDashboard || hub.run.tickets != nil {
		t.Errorf("Esc should cancel creation: view=%d tickets=%+v", hub.view, hub.run.tickets)
	}
}

// The review step's cursor is pre-selected from the config default: review_plans
// = true → cursor on "Yes" (0); unset → "No" (1).
func TestReviewPickerCursorFollowsConfig(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	base := monitorModel{
		run: newRun(func(r *runState) {
			r.tickets = []pendingTicket{{id: "LOCAL-9", kind: "content", title: "t", lines: 1}}
		}),
	}

	if err := config.Save(&config.Config{ReviewPlans: true, Repos: []config.Repo{{Path: "/r"}}}); err != nil {
		t.Fatal(err)
	}
	nm, _ := base.openReviewPicker(0)
	if got := nm.(monitorModel).run.reviewCursor; got != 0 {
		t.Errorf("review_plans=true should pre-select Yes (0), got %d", got)
	}

	if err := config.Save(&config.Config{ReviewPlans: false, Repos: []config.Repo{{Path: "/r"}}}); err != nil {
		t.Fatal(err)
	}
	nm, _ = base.openReviewPicker(0)
	if got := nm.(monitorModel).run.reviewCursor; got != 1 {
		t.Errorf("review_plans=false should pre-select No (1), got %d", got)
	}
}

// Enter selecting a real branch records it on the chip. Using a jira chip so the
// picker just closes (no launch) and we can inspect the resulting base.
func TestStackEnterSetsBase(t *testing.T) {
	m := stackModel("jira")
	m.run.stackCursor = 1 // list = [none, ai/parent] → pick ai/parent
	nm, cmd := m.updateRunStack(tea.KeyMsg{Type: tea.KeyEnter})
	hub := nm.(monitorModel)
	if cmd != nil {
		t.Error("Enter on a jira stack step should not launch")
	}
	if len(hub.run.tickets) != 1 || hub.run.tickets[0].base != "ai/parent" {
		t.Errorf("expected base ai/parent, got %+v", hub.run.tickets)
	}
	if hub.run.stackPrompt != -1 {
		t.Errorf("picker should close (runStackPrompt=-1), got %d", hub.run.stackPrompt)
	}
}

// Enter on the "- none -" row leaves the base empty (default) and does not launch.
func TestStackEnterNoneKeepsDefault(t *testing.T) {
	m := stackModel("jira")
	m.run.stackCursor = 0 // the "- none -" row
	nm, _ := m.updateRunStack(tea.KeyMsg{Type: tea.KeyEnter})
	hub := nm.(monitorModel)
	if len(hub.run.tickets) != 1 || hub.run.tickets[0].base != "" {
		t.Errorf("none row should leave base empty, got %+v", hub.run.tickets)
	}
}

// Esc on a jira/file stack step (opened via ctrl+s over a chip list) only closes
// the picker; it must NOT cancel the whole batch or drop the chips.
func TestStackEscJiraKeepsChips(t *testing.T) {
	m := stackModel("jira")
	nm, cmd := m.updateRunStack(tea.KeyMsg{Type: tea.KeyEsc})
	hub := nm.(monitorModel)
	if cmd != nil {
		t.Error("Esc on a jira stack step should not launch")
	}
	if hub.view != viewRunInput {
		t.Errorf("Esc on jira should stay in run-input, got view=%d", hub.view)
	}
	if len(hub.run.tickets) != 1 {
		t.Errorf("Esc on jira should keep the chips, got %+v", hub.run.tickets)
	}
	if hub.run.stackPrompt != -1 {
		t.Errorf("picker should close (runStackPrompt=-1), got %d", hub.run.stackPrompt)
	}
}

func TestBaseLabel(t *testing.T) {
	sentinel := git.Branch{Name: defaultBaseSentinel}

	// Default row shows the resolved default branch name + "(default)".
	m := monitorModel{
		run: newRun(func(r *runState) {
			r.stackDefault = "main"
		}),
	}
	if got := m.baseLabel(sentinel); got != "main (default)" {
		t.Errorf("main default: got %q, want %q", got, "main (default)")
	}
	m.run.stackDefault = "develop"
	if got := m.baseLabel(sentinel); got != "develop (default)" {
		t.Errorf("develop default: got %q", got)
	}

	// Unknown default (couldn't resolve) → a clear fallback, never "none".
	m.run.stackDefault = ""
	if got := m.baseLabel(sentinel); got != "default base" {
		t.Errorf("empty default: got %q, want %q", got, "default base")
	}

	// A real branch row renders its own name unchanged.
	if got := m.baseLabel(git.Branch{Name: "ai/parent"}); got != "ai/parent" {
		t.Errorf("real branch: got %q", got)
	}
}

// Selecting the default row must leave base empty (use the repo default), even
// though it now displays a real branch name.
func TestStackEnterDefaultRowKeepsBaseEmpty(t *testing.T) {
	m := stackModel("jira")
	m.run.stackDefault = "main"
	m.run.stackCursor = 0 // the default row
	nm, _ := m.updateRunStack(tea.KeyMsg{Type: tea.KeyEnter})
	hub := nm.(monitorModel)
	if len(hub.run.tickets) != 1 || hub.run.tickets[0].base != "" {
		t.Errorf("default row should leave base empty, got %+v", hub.run.tickets)
	}
}
