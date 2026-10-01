package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func approvalModel(t *testing.T) (monitorModel, *store.Store) {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	// PLEX-1 is the fixture ticket every test files prompts under; the ghost
	// reaper only shows prompts whose ticket has a live active driver, so
	// give it one: an active state with this test process as the pid.
	if _, err := st.Claim("PLEX-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	_ = st.SetState("PLEX-1", "building", 0)
	_ = st.SetPID("PLEX-1", os.Getpid())
	m := monitorModel{store: st}
	return m, st
}

func enter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func down() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyDown} }

// selectRow drives the cursor (from 0) onto the row whose label contains
// substr and returns the model there - a test picks rows by MEANING, not by
// a magic index that shifts as RememberOptions offers more or fewer scopes.
func selectRow(t *testing.T, m monitorModel, a store.Approval, substr string) monitorModel {
	t.Helper()
	rows := approvalRows(a)
	for i, r := range rows {
		if strings.Contains(r.label, substr) {
			for m.approval.cursor < i {
				mm, _ := m.updateApprovals(down())
				m = mm.(monitorModel)
			}
			return m
		}
	}
	t.Fatalf("no row containing %q among %v", substr, rowLabels(rows))
	return m
}

func rowLabels(rows []approvalRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.label)
	}
	return out
}

// T4.1 - a pending approval surfaces on the dashboard: header banner, and the
// ticket row's phase word flips to the approval badge.
func TestPendingApprovalSurfacesOnDashboard(t *testing.T) {
	m, st := approvalModel(t)
	if _, err := st.CreateApproval("PLEX-1", "Bash", "cd Compass && ./gradlew test"); err != nil {
		t.Fatal(err)
	}
	m.reload()
	if got := m.approvalsFor("PLEX-1"); got != 1 {
		t.Fatalf("approvalsFor = %d, want 1", got)
	}
	head := m.renderDashboardHeader(100)
	if !strings.Contains(head, "waiting for your approval") || !strings.Contains(head, "press A") {
		t.Errorf("header lacks the approval banner:\n%s", head)
	}
	m.flat = []store.Session{{Ticket: "PLEX-1", State: "building"}}
	row := m.renderRow(m.flat[0], 104, false)
	if !strings.Contains(row, "approve?") {
		t.Errorf("row lacks the approval badge:\n%q", row)
	}
}

// T4.2 - Allow once writes the allowed decision and nothing to config.
func TestApprovalAllowOnce(t *testing.T) {
	m, st := approvalModel(t)
	id, _ := st.CreateApproval("PLEX-1", "Bash", "adb devices")
	m.reload()
	m.openApprovals("")
	mm, _ := m.updateApprovals(enter()) // cursor 0 = allow once, always first
	m = mm.(monitorModel)
	if state, _ := st.ApprovalState(id); state != store.ApprovalAllowed {
		t.Fatalf("state = %q, want allowed", state)
	}
	if m.approval.open {
		t.Error("overlay still open with nothing pending")
	}
	cfg, _ := config.Load()
	if cfg.ExtraAllowedTools != "" {
		t.Errorf("allow-once wrote config: %q", cfg.ExtraAllowedTools)
	}
}

// T4.3 - "Allow & remember all" writes exactly the head rules it displayed,
// nothing narrower or broader.
func TestApprovalAllowAndRememberAll(t *testing.T) {
	m, st := approvalModel(t)
	id, _ := st.CreateApproval("PLEX-1", "Bash", "bundle exec fastlane test")
	m.reload()
	pending, _ := st.PendingApprovals("PLEX-1")
	m.openApprovals("PLEX-1")
	m = selectRow(t, m, pending[0], "remember all")
	mm, _ := m.updateApprovals(enter())
	m = mm.(monitorModel)
	if state, _ := st.ApprovalState(id); state != store.ApprovalAllowed {
		t.Fatalf("state = %q, want allowed", state)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.ExtraAllowedTools, "Bash(bundle:*)") {
		t.Errorf("extra_allowed_tools = %q, want the remembered head rule", cfg.ExtraAllowedTools)
	}
	if strings.Contains(cfg.ExtraAllowedTools, "Bash(bundle exec fastlane test)") {
		t.Errorf("extra_allowed_tools = %q, 'remember all' must not also write the exact rule", cfg.ExtraAllowedTools)
	}
}

// "Allow & remember exact" writes ONLY the verbatim command rule, not the
// generalized heads - the two rows are separate consents.
func TestApprovalAllowAndRememberExact(t *testing.T) {
	m, st := approvalModel(t)
	id, _ := st.CreateApproval("PLEX-1", "Bash", "bundle exec fastlane test")
	m.reload()
	pending, _ := st.PendingApprovals("PLEX-1")
	m.openApprovals("PLEX-1")
	m = selectRow(t, m, pending[0], "remember exact")
	mm, _ := m.updateApprovals(enter())
	m = mm.(monitorModel)
	if state, _ := st.ApprovalState(id); state != store.ApprovalAllowed {
		t.Fatalf("state = %q, want allowed", state)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.ExtraAllowedTools, "Bash(bundle exec fastlane test)") {
		t.Errorf("extra_allowed_tools = %q, want the exact rule", cfg.ExtraAllowedTools)
	}
	if strings.Contains(cfg.ExtraAllowedTools, "Bash(bundle:*)") {
		t.Errorf("extra_allowed_tools = %q, 'remember exact' must not also write the head rule", cfg.ExtraAllowedTools)
	}
}

// The remember-all path on a command whose heads are already allowed still
// writes something useful, and remembered rules survive Load (the
// validRuleToken exact-rule fix).
func TestApprovalRememberExactRuleRoundTrips(t *testing.T) {
	m, st := approvalModel(t)
	st.CreateApproval("PLEX-1", "Bash", "cd Compass && ./gradlew --version")
	m.reload()
	pending, _ := st.PendingApprovals("PLEX-1")
	m.openApprovals("PLEX-1")
	m = selectRow(t, m, pending[0], "remember exact")
	mm, _ := m.updateApprovals(enter())
	_ = mm
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := "Bash(cd Compass && ./gradlew --version)"
	if !strings.Contains(cfg.ExtraAllowedTools, want) {
		t.Errorf("extra_allowed_tools = %q, want the exact rule %q to survive Load", cfg.ExtraAllowedTools, want)
	}
}

// T4.4 - Deny writes the denied decision; the callback will relay a clean
// refusal to the agent.
func TestApprovalDeny(t *testing.T) {
	m, st := approvalModel(t)
	id, _ := st.CreateApproval("PLEX-1", "Bash", "rm -rf build")
	m.reload()
	pending, _ := st.PendingApprovals("PLEX-1")
	m.openApprovals("PLEX-1")
	m = selectRow(t, m, pending[0], "Deny")
	mm, _ := m.updateApprovals(enter())
	m = mm.(monitorModel)
	_ = m
	if state, _ := st.ApprovalState(id); state != store.ApprovalDenied {
		t.Fatalf("state = %q, want denied", state)
	}
}

// A guarded command (git state mutation) offers only Allow once / Deny -
// never a remember row that would silently degrade to allow-once.
func TestApprovalGuardedCommandHasNoRememberRow(t *testing.T) {
	a := store.Approval{Tool: "Bash", Command: "cd App && git checkout main"}
	rows := approvalRows(a)
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want exactly [Allow once, Deny]", rowLabels(rows))
	}
	if rows[0].label != "Allow once" || rows[1].label != "Deny" {
		t.Errorf("rows = %v", rowLabels(rows))
	}
}

// A compound gradle command offers all four rows, each showing its verbatim
// rule(s) - the disclosure the QA session flagged as missing.
func TestApprovalCompoundCommandShowsBothRememberScopes(t *testing.T) {
	a := store.Approval{Tool: "Bash", Command: "cd App && ./gradlew test"}
	rows := approvalRows(a)
	if len(rows) != 4 {
		t.Fatalf("rows = %v, want 4", rowLabels(rows))
	}
	if !strings.Contains(rows[1].label, "Bash(cd App && ./gradlew test)") {
		t.Errorf("exact row label = %q, want the verbatim command", rows[1].label)
	}
	if !strings.Contains(rows[2].label, "Bash(cd:*)") || !strings.Contains(rows[2].label, "Bash(./gradlew:*)") {
		t.Errorf("general row label = %q, want both head rules", rows[2].label)
	}
}

// Esc leaves the request pending - the callback keeps waiting - and multiple
// pending approvals are answered oldest-first without reopening the overlay.
func TestApprovalEscAndQueue(t *testing.T) {
	m, st := approvalModel(t)
	first, _ := st.CreateApproval("PLEX-1", "Bash", "adb devices")
	second, _ := st.CreateApproval("PLEX-1", "Bash", "java -version")
	m.reload()
	m.openApprovals("PLEX-1")

	mm, _ := m.updateApprovals(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(monitorModel)
	if m.approval.open {
		t.Fatal("esc should close the overlay")
	}
	if state, _ := st.ApprovalState(first); state != store.ApprovalPending {
		t.Fatalf("esc decided the request: %q", state)
	}

	m.openApprovals("PLEX-1")
	mm, _ = m.updateApprovals(enter())
	m = mm.(monitorModel)
	if !m.approval.open {
		t.Fatal("second request should keep the overlay open")
	}
	if a := m.currentApproval(); a == nil || a.ID != second {
		t.Fatalf("current = %+v, want the second request next", a)
	}
	if m.approval.cursor != 0 {
		t.Errorf("cursor = %d, want reset to 0 for the next approval", m.approval.cursor)
	}
}

// A parked ticket retries the flow that parked it: ship-comments parks must
// not degrade to a bare --resume (which re-verified but never shipped the
// approved replies).
func TestRetryArgsFollowParkedFlow(t *testing.T) {
	cases := []struct {
		flow string
		want string
	}{
		{"ship-comments", "--ship-comments"},
		{"address-comments", "--address-comments"},
		{"", "--resume"},
	}
	for _, c := range cases {
		s := store.Session{Ticket: "K-9", ParkedFlow: c.flow}
		args := retryArgs(s)
		if len(args) != 2 || args[0] != "K-9" || args[1] != c.want {
			t.Errorf("retryArgs(flow=%q) = %v, want [K-9 %s]", c.flow, args, c.want)
		}
	}
}

// The ghost-prompt reaper (field QA finding: prompts from the previous day
// survived their agents overnight; the user answered them and nothing
// happened). A prompt is shown only while a live active driver could be
// polling for the answer; anything else is expired on reload, not displayed.
func TestReloadReapsGhostApprovals(t *testing.T) {
	m, st := approvalModel(t)

	// A live run: active state, this test process as the (alive) driver pid.
	if _, err := st.Claim("LIVE-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	st.SetState("LIVE-1", "building", 0)
	st.SetPID("LIVE-1", os.Getpid())
	liveID, _ := st.CreateApproval("LIVE-1", "Bash", "./gradlew test")

	// A ghost: the session parked needs-you, its driver long dead.
	if _, err := st.Claim("GHOST-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	st.SetState("GHOST-1", "needs-you", 0)
	st.SetPID("GHOST-1", 0)
	ghostID, _ := st.CreateApproval("GHOST-1", "Bash", "./gradlew build")

	m.reload()

	if got := m.approvalsFor("LIVE-1"); got != 1 {
		t.Errorf("live prompt reaped: approvalsFor(LIVE-1) = %d, want 1", got)
	}
	if got := m.approvalsFor("GHOST-1"); got != 0 {
		t.Errorf("ghost prompt still displayed: approvalsFor(GHOST-1) = %d, want 0", got)
	}
	if state, _ := st.ApprovalState(ghostID); state != store.ApprovalExpired {
		t.Errorf("ghost row = %q, want expired in the store, not merely hidden", state)
	}
	if state, _ := st.ApprovalState(liveID); state != store.ApprovalPending {
		t.Errorf("live row = %q, want still pending", state)
	}
}

// Enter on a row with a pending prompt opens the agent MENU, with "Approve
// pending command" as its first row - not the overlay directly. The hijack
// made Stop/Pause unreachable without letter shortcuts (field QA: "when I
// open I only have options to allow or deny"), violating the arrows/Enter/
// Esc convention. Choosing that first row is what opens the overlay.
func TestApprovalReachableViaMenuNotHijack(t *testing.T) {
	m, st := approvalModel(t)
	if _, err := st.CreateApproval("PLEX-1", "Bash", "./gradlew test"); err != nil {
		t.Fatal(err)
	}
	m.reload()
	m.flat = []store.Session{{Ticket: "PLEX-1", State: "building"}}
	m.cursor = len(dashCommands) // first agent row

	// The menu leads with the approve item...
	items := m.paletteItems()
	if len(items) == 0 || items[0].key != actApprove {
		t.Fatalf("menu = %v, want actApprove first", items)
	}
	if !strings.Contains(items[0].label, "1 waiting") {
		t.Errorf("approve row label = %q, want the pending count", items[0].label)
	}
	// ...Stop is still on the same menu (the whole point of un-hijacking)...
	found := false
	for _, it := range items {
		if it.key == actStop {
			found = true
		}
	}
	if !found {
		t.Errorf("menu %v lacks actStop - stopping mid-prompt is the case that broke", items)
	}
	// ...and choosing the approve row opens the overlay.
	mm, _ := m.doAction(actApprove)
	got := mm.(monitorModel)
	if !got.approval.open || got.approval.ticket != "PLEX-1" {
		t.Fatalf("actApprove did not open the overlay: %+v", got.approval)
	}
}
