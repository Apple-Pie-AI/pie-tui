package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	tea "github.com/charmbracelet/bubbletea"
)

// The image-attach field is a path field - long, and typo-prone in the middle.
// It was append-only (no ←→, backspace only at the end); it now edits at the
// caret like the id and branch prompts.
func TestImgAttachCursorMovesLeftRight(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{id: "LOCAL-9", kind: "content", title: "t", lines: 1}}
			r.idPrompt = -1
			r.branchPrompt = -1
			r.imgPrompt = 0
			r.text = "/tmp/shot.png"
			r.promptCursor = len("/tmp/shot.png")
		}),
	}
	// Park the caret before ".png" and fix the name in place.
	for i := 0; i < 4; i++ {
		nm, _ := m.updateRunImgAttach(tea.KeyMsg{Type: tea.KeyLeft})
		m = nm.(monitorModel)
	}
	nm, _ := m.updateRunImgAttach(tea.KeyMsg{Type: tea.KeyBackspace})
	m = nm.(monitorModel)
	nm, _ = m.updateRunImgAttach(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	m = nm.(monitorModel)
	if m.run.text != "/tmp/sho2.png" {
		t.Errorf("mid-string edit = %q, want %q", m.run.text, "/tmp/sho2.png")
	}
	if got := m.renderRunInput(80); !strings.Contains(got, "/tmp/sho2▌.png") {
		t.Errorf("caret should render at the edit position:\n%s", got)
	}
}

// Enter on an emptied id field must not advance - the id is required (it names
// the dashboard row, worktree, and logs).
func TestIDPromptEnterRejectsEmpty(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{kind: "content", title: "t", lines: 1, id: ""}}
			r.idPrompt = 0
			r.branchPrompt = -1
			r.imgPrompt = -1
		}),
	}
	nm, _ := m.updateRunIDPrompt(tea.KeyMsg{Type: tea.KeyEnter})
	hub := nm.(monitorModel)
	if hub.run.idPrompt != 0 || hub.run.branchPrompt != -1 {
		t.Errorf("empty id should keep the prompt open: idPrompt=%d branchPrompt=%d",
			hub.run.idPrompt, hub.run.branchPrompt)
	}
}

// Esc on the id prompt drops the chip entirely.
func TestIDPromptEscDropsChip(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{kind: "content", title: "t", lines: 1, id: "LOCAL-9"}}
			r.idPrompt = 0
			r.branchPrompt = -1
			r.imgPrompt = -1
		}),
	}
	nm, _ := m.updateRunIDPrompt(tea.KeyMsg{Type: tea.KeyEsc})
	hub := nm.(monitorModel)
	if len(hub.run.tickets) != 0 || hub.run.idPrompt != -1 {
		t.Errorf("Esc should drop the chip: tickets=%+v idPrompt=%d",
			hub.run.tickets, hub.run.idPrompt)
	}
}

// Pasting into the id prompt inserts at the caret with whitespace stripped -
// the whole point of the prompt is pasting a ticket key, and a trailing
// newline from the clipboard must not leak into the field. (These fields used
// to discard pastes outright - user feedback.)
func TestIDPromptPasteInserts(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{kind: "content", title: "t", lines: 1, id: ""}}
			r.idPrompt = 0
			r.branchPrompt = -1
			r.imgPrompt = -1
			r.promptCursor = 0
		}),
	}
	nm, _ := m.updateRunIDPrompt(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" PLEX-123\n"), Paste: true})
	hub := nm.(monitorModel)
	if got := hub.run.tickets[0].id; got != "PLEX-123" {
		t.Errorf("pasted id = %q, want %q", got, "PLEX-123")
	}
	if hub.run.promptCursor != len("PLEX-123") {
		t.Errorf("caret after paste = %d, want %d", hub.run.promptCursor, len("PLEX-123"))
	}
}

// Pasting into the branch prompt inserts at the caret with whitespace
// collapsed to "-", the same rule Enter applies.
func TestBranchPromptPasteInserts(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{id: "LOCAL-9", kind: "content", title: "t", lines: 1, branch: "fix/"}}
			r.idPrompt = -1
			r.branchPrompt = 0
			r.imgPrompt = -1
			r.promptCursor = len("fix/")
		}),
	}
	nm, _ := m.updateRunBranchPrompt(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("plex 123 login\n"), Paste: true})
	hub := nm.(monitorModel)
	if got := hub.run.tickets[0].branch; got != "fix/plex-123-login" {
		t.Errorf("pasted branch = %q, want %q", got, "fix/plex-123-login")
	}
}

// Enter on the branch prompt confirms the (possibly edited) name - collapsing
// whitespace, which git forbids in branch names - and advances to the image
// step. The confirmed value is final; it is passed through as --branch.
func TestBranchPromptEnterAdvances(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{id: "LOCAL-9", kind: "content", title: "t", lines: 1, branch: " pie/local-9  fix "}}
			r.branchPrompt = 0
			r.imgPrompt = -1
		}),
	}
	nm, _ := m.updateRunBranchPrompt(tea.KeyMsg{Type: tea.KeyEnter})
	hub := nm.(monitorModel)
	if got := hub.run.tickets[0].branch; got != "pie/local-9-fix" {
		t.Errorf("confirmed branch: got %q", got)
	}
	if hub.run.branchPrompt != -1 || hub.run.imgPrompt != 0 {
		t.Errorf("Enter should advance to images: branchPrompt=%d imgPrompt=%d",
			hub.run.branchPrompt, hub.run.imgPrompt)
	}
}

// The branch field supports in-place caret editing: ←→ move the cursor and
// typing inserts at it (shared formField editor), so the middle of the name
// can be fixed without deleting from the end.
func TestBranchPromptCursorEditing(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{id: "LOCAL-9", kind: "content", title: "t", lines: 1, branch: "main"}}
			r.idPrompt = -1
			r.branchPrompt = 0
			r.imgPrompt = -1
			r.promptCursor = len("main")
		}),
	}
	step := func(msg tea.KeyMsg) {
		nm, _ := m.updateRunBranchPrompt(msg)
		m = nm.(monitorModel)
	}
	step(tea.KeyMsg{Type: tea.KeyLeft})
	step(tea.KeyMsg{Type: tea.KeyLeft})
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("XY")})
	if got := m.run.tickets[0].branch; got != "maXYin" {
		t.Errorf("insert mid-value = %q, want %q", got, "maXYin")
	}
	step(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.run.tickets[0].branch; got != "maXin" {
		t.Errorf("backspace mid-value = %q, want %q", got, "maXin")
	}
	step(tea.KeyMsg{Type: tea.KeyHome})
	step(tea.KeyMsg{Type: tea.KeyDelete})
	if got := m.run.tickets[0].branch; got != "aXin" {
		t.Errorf("delete at home = %q, want %q", got, "aXin")
	}
}

// Enter on an emptied branch field must not advance - the branch is required.
func TestBranchPromptEnterRejectsEmpty(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{id: "LOCAL-9", kind: "content", title: "t", lines: 1, branch: "   "}}
			r.branchPrompt = 0
			r.imgPrompt = -1
		}),
	}
	nm, _ := m.updateRunBranchPrompt(tea.KeyMsg{Type: tea.KeyEnter})
	hub := nm.(monitorModel)
	if hub.run.branchPrompt != 0 || hub.run.imgPrompt != -1 {
		t.Errorf("empty branch should keep the prompt open: branchPrompt=%d imgPrompt=%d",
			hub.run.branchPrompt, hub.run.imgPrompt)
	}
}

// The branch prompt screen: no chip row, the full ticket content rendered
// above, and the header sits directly above the branch field.
func TestBranchPromptRender(t *testing.T) {
	m := monitorModel{
		view:   viewRunInput,
		height: 30,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{
				id: "LOCAL-9", kind: "content", title: "t", lines: 2,
				body: "Title: fix the crash\n\nDetails here", branch: "pie/local-9",
			}}
			r.branchPrompt = 0
			r.promptCursor = len("pie/local-9") // caret at the end, as when the prompt opens
		}),
	}
	// Strip ANSI styling: glamour splits words across escape-coded segments,
	// which would defeat plain substring checks.
	out := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(m.renderRunInput(80), "")
	if !strings.Contains(out, "Update or Approve the branch name") {
		t.Errorf("header missing:\n%s", out)
	}
	if !strings.Contains(out, "branch › pie/local-9") {
		t.Errorf("branch field missing:\n%s", out)
	}
	if !strings.Contains(out, "Details here") {
		t.Errorf("full ticket content should render:\n%s", out)
	}
	if strings.Contains(out, "· 2 lines") {
		t.Errorf("chip row must not render on the branch prompt:\n%s", out)
	}
	// The header must sit below the content, directly above the branch field.
	if strings.Index(out, "Update or Approve") < strings.Index(out, "Details here") {
		t.Errorf("header should render below the ticket content:\n%s", out)
	}
}

// applyTicketEdit folds an editor rewrite back into the chip and re-infers the
// branch when the user hadn't customized it.
func TestApplyTicketEditReinfersBranch(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{
		Repos: []config.Repo{{Path: "/r", Branch: "pie/{ticket}"}},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "edited.md")
	if err := os.WriteFile(path, []byte("PROJ-77 New title\n\nnew body"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := monitorModel{
		run: newRun(func(r *runState) {
			r.tickets = []pendingTicket{{
				id: "PROJ-1", kind: "content", title: "Old title", lines: 1,
				body: "PROJ-1 Old title", branch: inferBranch("PROJ-1", "Old title"),
			}}
			r.branchPrompt = 0
		}),
	}
	nm, _ := m.applyTicketEdit(ticketEditedMsg{i: 0, path: path})
	hub := nm.(monitorModel)
	got := hub.run.tickets[0]
	if got.id != "PROJ-77" || got.lines != 3 || !strings.Contains(got.body, "new body") {
		t.Errorf("edit not applied: %+v", got)
	}
	if got.branch != "pie/proj-77" {
		t.Errorf("uncustomized branch should re-infer, got %q", got.branch)
	}
}

// applyTicketEdit must NOT overwrite a branch the user already customized.
func TestApplyTicketEditKeepsCustomBranch(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "edited.md")
	if err := os.WriteFile(path, []byte("PROJ-77 New title"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := monitorModel{
		run: newRun(func(r *runState) {
			r.tickets = []pendingTicket{{
				id: "PROJ-1", kind: "content", title: "Old title", lines: 1,
				body: "PROJ-1 Old title", branch: "my/custom-name",
			}}
			r.branchPrompt = 0
		}),
	}
	nm, _ := m.applyTicketEdit(ticketEditedMsg{i: 0, path: path})
	if got := nm.(monitorModel).run.tickets[0].branch; got != "my/custom-name" {
		t.Errorf("customized branch must survive an edit, got %q", got)
	}
}

// An edit that empties the ticket is discarded (the chip keeps its old body).
func TestApplyTicketEditRejectsEmpty(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "edited.md")
	if err := os.WriteFile(path, []byte("   \n  "), 0o644); err != nil {
		t.Fatal(err)
	}
	m := monitorModel{
		run: newRun(func(r *runState) {
			r.tickets = []pendingTicket{{id: "PROJ-1", kind: "content", body: "keep me", branch: "b"}}
			r.branchPrompt = 0
		}),
	}
	nm, _ := m.applyTicketEdit(ticketEditedMsg{i: 0, path: path})
	hub := nm.(monitorModel)
	if hub.run.tickets[0].body != "keep me" {
		t.Errorf("empty edit should be discarded, got body %q", hub.run.tickets[0].body)
	}
	if hub.notice == "" {
		t.Error("discarding an empty edit should set a notice")
	}
}

// Esc on the branch prompt drops the chip entirely.
func TestBranchPromptEscDropsChip(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.tickets = []pendingTicket{{id: "LOCAL-9", kind: "content", title: "t", lines: 1, branch: "b"}}
			r.branchPrompt = 0
			r.imgPrompt = -1
		}),
	}
	nm, _ := m.updateRunBranchPrompt(tea.KeyMsg{Type: tea.KeyEsc})
	hub := nm.(monitorModel)
	if len(hub.run.tickets) != 0 || hub.run.branchPrompt != -1 {
		t.Errorf("Esc should drop the chip: tickets=%+v branchPrompt=%d",
			hub.run.tickets, hub.run.branchPrompt)
	}
}
