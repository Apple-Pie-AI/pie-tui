package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func TestCleanActivityAndStrip(t *testing.T) {
	lines := []string{"[KAN-6] queued → working", "  ⚙ Edit(/path/HomeScreen.kt)"}
	got := cleanActivity(lines, "KAN-6")
	if got != "Edit(/path/HomeScreen.kt)" {
		t.Errorf("cleanActivity = %q", got)
	}
	if stripPrefix("[KAN-6] hi", "KAN-6") != "hi" {
		t.Error("stripPrefix failed")
	}
	if cleanActivity(nil, "X") != "" {
		t.Error("empty input should be empty")
	}
}

// The ⚑ ● ✓ ■ column was deleted - it read as a keyboard shortcut, and the
// section header and the phase word already said what it said. The one thing it
// carried that nothing else did was colour, which moved to the phase word; this
// is that mapping, so deleting the glyph cannot quietly take the state with it.
func TestPhaseColourReplacesTheGlyph(t *testing.T) {
	now := time.Now()
	cases := []struct {
		s    store.Session
		want lipgloss.TerminalColor
	}{
		{store.Session{State: "awaiting-answer", UpdatedAt: now}, amberC},
		{store.Session{State: "failed", UpdatedAt: now}, redC},
		{store.Session{State: "needs-you", UpdatedAt: now}, amberC},
		{store.Session{State: "review", UpdatedAt: now}, accent},
		{store.Session{State: "review", UpdatedAt: now, OpenComments: 2}, amberC},
		{store.Session{State: "working", UpdatedAt: now}, cyanC},
		{store.Session{State: "working", UpdatedAt: now.Add(-time.Hour)}, redC}, // stale → stopped
	}
	for _, c := range cases {
		if got := phaseStyle(c.s).GetForeground(); got != c.want {
			t.Errorf("phaseStyle(%q, comments=%d) = %v, want %v",
				c.s.State, c.s.OpenComments, got, c.want)
		}
	}
}

// No row may carry a leading glyph. The ▸ cursor is the exception: it is the
// only selection cue that survives NO_COLOR.
func TestRowsCarryNoGlyphs(t *testing.T) {
	m := baseModel()
	m.width, m.height = 120, 30
	m.cursor = len(dashCommands) // on the ticket, so the selected styling renders too
	m.groups = newGroups()
	for _, s := range []store.Session{
		{Ticket: "PIE-9", State: "review", ShortDesc: "add a subtitle"},
		{Ticket: "PIE-7", State: "needs-you", ShortDesc: "the agent got stuck"},
	} {
		for _, g := range m.groups {
			if g.match(s) {
				g.sessions = append(g.sessions, s)
				break
			}
		}
	}
	for _, g := range m.groups {
		m.flat = append(m.flat, g.sessions...)
	}

	for _, ln := range strings.Split(stripAnsi(m.renderDashboardBody(120)), "\n") {
		for _, bad := range []string{"⚑", "●", "✓", "■", "✗", "▮", "◎", "▾", "＋", "⑂", "⚙"} {
			if strings.Contains(ln, bad) {
				t.Errorf("row carries the %q glyph: %q", bad, ln)
			}
		}
	}
}

func TestIsStopped(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	dead := 2_000_000_000 // almost certainly not a live process
	live := os.Getpid()

	// Known pid, dead → stopped (deterministic, even if recently updated).
	if !isStopped(store.Session{State: "working", PID: dead, UpdatedAt: now}) {
		t.Error("running session with a dead pid should be stopped")
	}
	// Known pid, alive → NOT stopped, even if idle a long time.
	if isStopped(store.Session{State: "working", PID: live, UpdatedAt: old}) {
		t.Error("running session with a live pid should not be stopped")
	}
	// No pid (old row), idle long → stopped via the activity-heuristic fallback.
	if !isStopped(store.Session{Ticket: "NOPE-1", State: "working", UpdatedAt: old}) {
		t.Error("idle running session with no pid should be stopped (fallback)")
	}
	// No pid, fresh → not stopped.
	if isStopped(store.Session{Ticket: "NOPE-2", State: "working", UpdatedAt: now}) {
		t.Error("fresh running session should not be stopped")
	}
	// Terminal/needs-you states are never "stopped", even with a dead pid.
	if isStopped(store.Session{State: "failed", PID: dead, UpdatedAt: old}) {
		t.Error("failed is not a running state; never stopped")
	}
	if isStopped(store.Session{State: "review", UpdatedAt: old}) {
		t.Error("review is terminal; never stopped")
	}
}

// whyLines for a plan-review session renders the approach and steps read from
// the worktree's .agent/plan.json.
func TestWhyLinesPlanReview(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	s := store.Session{Ticket: "K-42", State: store.StatePlanReview}

	// Stub a plan.json under the ticket's worktree path.
	wt := paths.WorktreeFor(s.Repo, s.Ticket)
	agentDir := filepath.Join(wt, ".agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planJSON := `{"plan":"Add pull to refresh on the feed","steps":["Wrap the list in SwipeRefresh","Wire the refresh callback"],"confidence":"high","type":"feature"}`
	if err := os.WriteFile(filepath.Join(agentDir, "plan.json"), []byte(planJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// The detail pane is now a short teaser (approach + counts); the full plan,
	// including the numbered steps, lives in the dedicated full-screen viewer.
	lines := whyLines(s)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"Plan ready", "read the full plan",
		"Add pull to refresh on the feed",
		"2 step(s)", "Confidence: high", "Type: feature",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("whyLines(plan-review) missing %q in:\n%s", want, joined)
		}
	}
}

// A long free-form markdown plan must NOT flood the detail pane: whyLines shows
// a one-line teaser and whyRowsFor caps the total rows regardless of content.
func TestWhyLinesPlanReviewLongPlanIsCapped(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	s := store.Session{Ticket: "K-43", State: store.StatePlanReview}
	wt := paths.WorktreeFor(s.Repo, s.Ticket)
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A 60-line markdown plan (the new free-form format).
	longPlan := "## Approach\\nFirst line of the actual plan."
	for i := 0; i < 60; i++ {
		longPlan += fmt.Sprintf("\\n- detail line %d", i)
	}
	planJSON := `{"plan":"` + longPlan + `","confidence":"high","type":"feature"}`
	if err := os.WriteFile(filepath.Join(wt, ".agent", "plan.json"), []byte(planJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	lines := whyLines(s)
	if len(lines) > 4 {
		t.Errorf("plan-review whyLines should be a short teaser, got %d lines:\n%s",
			len(lines), strings.Join(lines, "\n"))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "First line of the actual plan") {
		t.Errorf("teaser should show the plan's first real line:\n%s", joined)
	}
	if strings.Contains(joined, "detail line 5") {
		t.Errorf("teaser must not include the plan body:\n%s", joined)
	}

	// The render-level cap holds even for pathological content.
	if rows := whyRowsFor(s, 80); len(rows) > 9 {
		t.Errorf("whyRowsFor must cap rows, got %d", len(rows))
	}
}

// Many questions collapse to a capped list with a "+N more" line.
func TestWhyLinesQuestionsCapped(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	s := store.Session{Ticket: "K-44", State: "awaiting-answer"}
	wt := paths.WorktreeFor(s.Repo, s.Ticket)
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	planJSON := `{"plan":"p","questions":["q1?","q2?","q3?","q4?","q5?","q6?"],"confidence":"high","type":"bug"}`
	if err := os.WriteFile(filepath.Join(wt, ".agent", "plan.json"), []byte(planJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(whyLines(s), "\n")
	if !strings.Contains(joined, "Q4") || strings.Contains(joined, "q5?") {
		t.Errorf("questions should cap at 4:\n%s", joined)
	}
	if !strings.Contains(joined, "+2 more") {
		t.Errorf("capped questions should say how many more:\n%s", joined)
	}
}

func TestReloadGrouping(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	seed := []struct{ ticket, state string }{
		{"KAN-1", "working"},         // running
		{"KAN-2", "awaiting-answer"}, // needs you
		{"KAN-3", "failed"},          // needs you
		{"KAN-4", "review"},          // done
		{"KAN-5", "planning"},        // running
	}
	for _, s := range seed {
		if _, err := st.Claim(s.ticket, "/repo", s.ticket+" summary"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetState(s.ticket, s.state, 0); err != nil {
			t.Fatal(err)
		}
	}

	m := monitorModel{store: st}
	m.reload()

	if m.err != nil {
		t.Fatalf("reload err: %v", m.err)
	}
	// Keyed on the labels newGroups actually emits. This map used to say "DONE",
	// which no group is called, so the review-state assertion never ran.
	want := map[string]int{"NEEDS YOU": 2, "RUNNING": 2, "NO PULL REQUEST YET": 0, "PR READY FOR REVIEW": 1, "STOPPED": 0, "CLOSED": 0}
	got := map[string]int{}
	for _, g := range m.groups {
		got[g.label] = len(g.sessions)
	}
	for label, n := range want {
		if _, ok := got[label]; !ok {
			t.Errorf("no group labelled %q; groups are %v", label, got)
			continue
		}
		if got[label] != n {
			t.Errorf("group %s has %d, want %d", label, got[label], n)
		}
	}
	for label := range got {
		if _, ok := want[label]; !ok {
			t.Errorf("unexpected group %q - update this test's want map", label)
		}
	}
	if len(m.flat) != 5 {
		t.Fatalf("flat = %d, want 5", len(m.flat))
	}
	// Flat order must be needs-you first (triage), so the selectable cursor
	// starts on something that needs attention.
	if m.flat[0].State != "awaiting-answer" && m.flat[0].State != "failed" {
		t.Errorf("flat[0] state = %q, want a needs-you state first", m.flat[0].State)
	}
	// Cursor 0 is the Start-new row → no agent selected.
	m.cursor = 0
	if m.selected() != nil {
		t.Error("cursor 0 (Start-new row) should select no agent")
	}
	// Cursor 1 is the Start-from-branch row → no agent selected.
	m.cursor = 1
	if m.selected() != nil {
		t.Error("cursor 1 (Start-from-branch row) should select no agent")
	}
	m.cursor = 2
	if m.selected() != nil {
		t.Error("cursor 2 (Edit-config row) should select no agent")
	}
	// Cursor 3 selects the first agent.
	m.cursor = 3
	if m.selected() == nil || m.selected().Ticket != m.flat[0].Ticket {
		t.Error("cursor 3 should select the first agent")
	}
	// Clamp: max cursor is len(flat)+2 (three pinned rows + N agents).
	m.cursor = 99
	m.reload()
	if m.cursor != 7 {
		t.Errorf("cursor not clamped: %d, want 7", m.cursor)
	}
}

func TestDashboardRendersStartFromBranch(t *testing.T) {
	m := baseModel()
	m.cursor = 1
	if out := m.renderDashboardBody(100); !strings.Contains(out, "Checkout a branch") {
		t.Errorf("dashboard is missing the checkout command:\n%s", out)
	}
}

func TestCancelAgentMarksStopped(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("KAN-9", "", "x"); err != nil { // empty repo → worktree removal skipped
		t.Fatal(err)
	}
	_ = st.SetState("KAN-9", "failed", 0)

	m := monitorModel{store: st}
	// No pid, no repo: cancelAgent only marks the session stopped.
	msg := m.cancelAgent(store.Session{Ticket: "KAN-9", State: "failed"})()
	if done, ok := msg.(cancelDoneMsg); !ok || done.ticket != "KAN-9" {
		t.Fatalf("unexpected msg: %#v", msg)
	}
	got, err := st.Get("KAN-9")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.StateStopped {
		t.Errorf("state = %q, want %q", got.State, store.StateStopped)
	}

	// And it now buckets under STOPPED, not NEEDS YOU.
	m.reload()
	for _, g := range m.groups {
		if g.label == "NEEDS YOU" {
			for _, s := range g.sessions {
				if s.Ticket == "KAN-9" {
					t.Error("KAN-9 should have left NEEDS YOU after stop")
				}
			}
		}
	}
}

// TestRenderRowDescPreference verifies the priority order for the third column:
// ShortDesc → Summary → log fallback.
func TestRenderRowDescPreference(t *testing.T) {
	m := monitorModel{}
	w := 80

	// ShortDesc wins over Summary.
	s1 := store.Session{
		Ticket:    "K-1",
		State:     "working",
		Summary:   "Long verbose Jira summary that should not appear",
		ShortDesc: "upload paper to storage",
		UpdatedAt: time.Now(),
	}
	row1 := m.renderRow(s1, w, false)
	if !strings.Contains(row1, "upload paper to storage") {
		t.Errorf("ShortDesc should appear in row: %q", row1)
	}
	if strings.Contains(row1, "Long verbose") {
		t.Errorf("Summary should be hidden when ShortDesc is set: %q", row1)
	}

	// Summary shows when ShortDesc is empty.
	s2 := store.Session{
		Ticket:    "K-2",
		State:     "working",
		Summary:   "Fix login crash",
		ShortDesc: "",
		UpdatedAt: time.Now(),
	}
	row2 := m.renderRow(s2, w, false)
	if !strings.Contains(row2, "Fix login crash") {
		t.Errorf("Summary should appear when ShortDesc is empty: %q", row2)
	}
}

// Exactly one row on the screen carries a filled background. Two filled rows is
// two cursors, and no way to tell which one enter will act on - which is what
// the two start commands used to do.
func TestExactlyOneRowIsFilled(t *testing.T) {
	m := baseModel()
	m.width, m.height = 120, 30
	m.groups = newGroups()
	for _, s := range []store.Session{
		{Ticket: "PIE-9", State: "review", ShortDesc: "add a subtitle", UpdatedAt: time.Now()},
		{Ticket: "PIE-7", State: "needs-you", ShortDesc: "stuck", UpdatedAt: time.Now()},
	} {
		for _, g := range m.groups {
			if g.match(s) {
				g.sessions = append(g.sessions, s)
				break
			}
		}
	}
	for _, g := range m.groups {
		m.flat = append(m.flat, g.sessions...)
	}
	m.rows = buildRows(m.groups, m.expanded)
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	for _, cursor := range []int{0, 1, 2, len(dashCommands), len(dashCommands) + 1} {
		m.cursor = cursor
		filled := 0
		for _, ln := range strings.Split(m.renderDashboardBody(120), "\n") {
			if strings.Contains(ln, "48;2;") { // a truecolor background SGR
				filled++
			}
		}
		if filled != 1 {
			t.Errorf("cursor=%d: %d filled rows, want exactly 1", cursor, filled)
		}
	}
}

// TestHeaderCounts guards the header's two live tallies. "N running" counts
// only the RUNNING group (in-progress agents), and "N need you" counts only
// the NEEDS YOU group. STOPPED and DONE sessions are historical and must
// inflate neither - the header reflects what's happening now, not all time.
func TestHeaderCounts(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	seed := []struct{ ticket, state string }{
		{"KAN-1", "needs-you"},        // need you
		{"KAN-2", "failed"},           // need you
		{"KAN-3", store.StateStopped}, // stopped - counts toward neither
		{"KAN-4", store.StateStopped}, // stopped - counts toward neither
		{"KAN-5", store.StateStopped}, // stopped - counts toward neither
		{"KAN-6", "review"},           // done - counts toward neither
		{"KAN-7", "working"},          // running
		{"KAN-8", "planning"},         // running
	}
	for _, s := range seed {
		if _, err := st.Claim(s.ticket, "/repo", s.ticket+" summary"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetState(s.ticket, s.state, 0); err != nil {
			t.Fatal(err)
		}
	}

	m := monitorModel{store: st}
	m.reload()
	if m.err != nil {
		t.Fatalf("reload err: %v", m.err)
	}

	out := m.View()
	if !strings.Contains(out, "2 running") {
		t.Errorf("header should report 2 running (in-progress only); got:\n%s", out)
	}
	if !strings.Contains(out, "2 needs you") {
		t.Errorf("header should report 2 needs you (stopped excluded); got:\n%s", out)
	}
	// The all-time total (8) must not leak into either tally.
	if strings.Contains(out, fmt.Sprintf("%d running", len(seed))) ||
		strings.Contains(out, "5 needs you") {
		t.Errorf("header counted historical sessions as live; got:\n%s", out)
	}
}

// More than three denials collapse into a "… and N more" line so the detail
// pane cannot be flooded by a chatty failure.
func TestWhyLinesCapsDeniedCommandList(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	var ds []agent.Denial
	for i := 0; i < 6; i++ {
		ds = append(ds, agent.Denial{Tool: "Bash", Command: fmt.Sprintf("cmd-%d", i)})
	}
	all := strings.Join(whyLines(store.Session{Ticket: "K11", State: "needs-you",
		Denials: agent.MarshalDenials(ds)}), "\n")
	if !strings.Contains(all, "… and 3 more") {
		t.Errorf("expected the cap line, got:\n%s", all)
	}
	if strings.Contains(all, "cmd-4") {
		t.Errorf("commands past the cap should not render:\n%s", all)
	}
}
