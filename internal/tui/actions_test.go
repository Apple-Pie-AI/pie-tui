package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func itemIDs(items []paletteItem) map[actionID]bool {
	m := map[actionID]bool{}
	for _, it := range items {
		m[it.key] = true
	}
	return m
}

// mkWorktree creates a fake worktree (a dir with a .git link) for a ticket.
// Empty repo → the agent-home fallback path, matching the repo-less Sessions
// the action tests build.
func mkWorktree(t *testing.T, ticket string) {
	t.Helper()
	wt := paths.WorktreeFor("", ticket)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAgentActionsWithWorktree(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	mkWorktree(t, "K1")

	// failed + worktree → resume, studio, claude, stop.
	ids := itemIDs(agentActions(store.Session{Ticket: "K1", State: "failed"}))
	for _, want := range []actionID{actResume, actStudio, actClaude, actStop} {
		if !ids[want] {
			t.Errorf("failed+worktree: missing %q", want)
		}
	}
	if ids[actRerun] {
		t.Error("failed+worktree should not offer rerun")
	}

	// awaiting + worktree → answer + studio/claude, not resume.
	ids = itemIDs(agentActions(store.Session{Ticket: "K1", State: "awaiting-answer"}))
	if !ids[actAnswer] || !ids[actStudio] || !ids[actClaude] {
		t.Errorf("awaiting+worktree menu = %v", ids)
	}
	if ids[actResume] {
		t.Error("awaiting should not offer resume")
	}
}

// A NEEDS YOU ticket's menu lists its actions in the intended order and nothing
// else (no global commands when scoped to the agent).
func TestNeedsYouActionOrder(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	mkWorktree(t, "NY1")

	var got []actionID
	for _, it := range agentActions(store.Session{Ticket: "NY1", State: "needs-you"}) {
		got = append(got, it.key)
	}
	// The change screen leads: reviewing the worktree is the row's most
	// important action, everywhere it exists.
	want := []actionID{actViewChange, actClaude, actStudio, actShip, actStop, actResume}
	if len(got) != len(want) {
		t.Fatalf("needs-you actions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("action[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}

	// Scoped agent menu shows only those actions - no globals.
	m := monitorModel{flat: []store.Session{{Ticket: "NY1", State: "needs-you"}}, rows: flatRows(1), cursor: 3, paletteAgentOnly: true}
	ids := itemIDs(m.paletteItems())
	for _, no := range []actionID{actRun, actDoctor, actConfig, actSetup, actQuit, actDaemonStart} {
		if ids[no] {
			t.Errorf("scoped agent menu must not contain global %q", no)
		}
	}
}

// A PR-ready row with open review comments leads with "Address PR feedback"
// - that's the row's actual reason for needing attention - ahead of the more
// general "Review and make changes", which stays right behind it.
func TestAddressPRFeedbackLeadsOverReviewAndMakeChanges(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	mkWorktree(t, "R1")

	items := agentActions(store.Session{Ticket: "R1", State: store.StateReview, PRURL: "https://x/pull/1", OpenComments: 1})
	if len(items) < 2 || items[0].key != actViewComments || items[1].key != actViewChange {
		t.Fatalf("menu = %+v, want Address PR feedback leading, Review and make changes right behind", items)
	}

	// With nothing to address, "Review and make changes" leads as usual.
	clean := agentActions(store.Session{Ticket: "R1", State: store.StateReview, PRURL: "https://x/pull/1"})
	if len(clean) == 0 || clean[0].key != actViewChange {
		t.Fatalf("menu = %+v, want Review and make changes leading with no open comments", clean)
	}
}

func TestAgentActionsStoppedNoWorktree(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir()) // no worktree on disk

	// stopped + no worktree → Run again (fresh) only; no resume/studio/claude/stop.
	ids := itemIDs(agentActions(store.Session{Ticket: "K1", State: store.StateStopped}))
	if !ids[actRerun] {
		t.Error("stopped+no worktree should offer rerun")
	}
	for _, no := range []actionID{actResume, actStudio, actClaude, actStop} {
		if ids[no] {
			t.Errorf("stopped+no worktree should NOT offer %q", no)
		}
	}

	// review (terminal) → no Stop.
	if itemIDs(agentActions(store.Session{Ticket: "K1", State: "review"}))[actStop] {
		t.Error("review (terminal) should not offer stop")
	}

	// A resolved PR's row still links to the PR, but offers no comment actions.
	for _, state := range []string{store.StateMerged, store.StateClosed} {
		ids := itemIDs(agentActions(store.Session{Ticket: "K2", State: state, PRURL: "https://x/pull/1", OpenComments: 2}))
		if !ids[actOpenPR] {
			t.Errorf("%s with a PR should offer Open the pull request", state)
		}
		if ids[actViewComments] || ids[actFetchComments] {
			t.Errorf("%s should not offer comment actions - nothing left to address", state)
		}
	}
}

func TestPaletteItemsIncludeGlobals(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir()) // no pidfile → daemon stopped
	m := monitorModel{flat: []store.Session{{Ticket: "K1", State: "awaiting-answer"}}, rows: flatRows(1), cursor: 3}
	ids := itemIDs(m.paletteItems())
	for _, want := range []actionID{actAnswer, actRun, actDoctor, actConfig, actSetup, actDaemonStart, actQuit} {
		if !ids[want] {
			t.Errorf("menu missing %q", want)
		}
	}
	if ids[actDaemonStop] {
		t.Error("daemon stopped should not offer daemon-stop")
	}
}

func TestDoActionTransitions(t *testing.T) {
	if mm, _ := (monitorModel{}).doAction(actMenu); mm.(monitorModel).view != viewPalette {
		t.Error("menu → palette view")
	}
	// No Jira configured → a single method, so run skips the picker and goes
	// straight into the paste-content input.
	runM, _ := (monitorModel{}).doAction(actRun)
	if hub := runM.(monitorModel); hub.view != viewRunInput || hub.run.mode != "content" {
		t.Errorf("run (single method) → content input; got view=%d mode=%q", hub.view, hub.run.mode)
	}
	branchM, _ := (monitorModel{}).doAction(actRunFromBranch)
	if hub := branchM.(monitorModel); hub.view != viewRunInput || !hub.run.branchFirstPick {
		t.Errorf("run-from-branch → initial branch picker; got view=%d mode=%q branchFirstPick=%v", hub.view, hub.run.mode, hub.run.branchFirstPick)
	}
	m := monitorModel{flat: []store.Session{{Ticket: "K1", State: "failed"}}, rows: flatRows(1), cursor: 3}
	mm, _ := m.doAction(actStop)
	got := mm.(monitorModel)
	if got.confirm.ticket != "K1" {
		t.Error("stop → confirming set to the selected ticket")
	}
	if got.confirm.cursor != 0 {
		t.Error("stop → confirmCursor reset to 0 (Yes)")
	}
}

// Local tickets must be re-run from their .md file: the ticket id is a derived
// label, and `pie run <id>` falls through to the Jira path and fails. Every
// spawn site goes through runArg - openClaude's auto-chain used to bypass it and
// hand back the id right after the human finished a manual fix.
func TestRunArgPrefersSourcePath(t *testing.T) {
	cases := []struct {
		name string
		sess store.Session
		want string
	}{
		{"jira ticket", store.Session{Ticket: "KAN-1"}, "KAN-1"},
		{"local md ticket", store.Session{Ticket: "FIX-LOGIN", SourcePath: "/tmp/fix-login.md"}, "/tmp/fix-login.md"},
		{"empty source path falls back", store.Session{Ticket: "KAN-2", SourcePath: ""}, "KAN-2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runArg(c.sess); got != c.want {
				t.Errorf("runArg() = %q, want %q", got, c.want)
			}
		})
	}
}

// The chained "finish the fix, then ship" command must carry the same argument
// the explicit Resume action would, or the auto-chain fails for local tickets.
func TestOpenClaudeChainUsesSourcePathForLocalTickets(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	s := store.Session{
		Ticket: "FIX-LOGIN", SourcePath: "/tmp/fix-login.md",
		State: "failed", Repo: "/repo",
	}
	if got := runArg(s); got != s.SourcePath {
		t.Fatalf("runArg = %q, want the .md path %q", got, s.SourcePath)
	}
	// shellQuote must not mangle a path that already contains a quote.
	if q := shellQuote("/tmp/Mike's/fix.md"); q != `'/tmp/Mike'\''s/fix.md'` {
		t.Errorf("shellQuote = %s", q)
	}
}

// "Allow denied commands & re-run" appears ONLY when denials exist AND allowing
// would change something. A stuck ticket without denials, or whose denied
// commands already match the allowlist, must not offer a no-op action.
func TestAllowRerunOfferedOnlyWithActionableDenials(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{}); err != nil {
		t.Fatal(err)
	}
	mkWorktree(t, "K1")

	// needs-you without denials → not offered.
	ids := itemIDs(agentActions(store.Session{Ticket: "K1", State: "needs-you"}))
	if ids[actAllowRerun] {
		t.Error("no denials → allow-rerun must not be offered")
	}

	// needs-you with an actionable denial → offered.
	withDenial := store.Session{Ticket: "K1", State: "needs-you",
		Denials: agent.MarshalDenials([]agent.Denial{{Tool: "Bash", Command: "bundle exec fastlane test"}})}
	if !itemIDs(agentActions(withDenial))[actAllowRerun] {
		t.Error("actionable denial → allow-rerun should be offered")
	}

	// denial already covered by the default allowlist → STILL offered: the
	// suggester escalates to the exact-command rule (the CLI refused a shape
	// the head rule should have matched, so the tighter rule is the next
	// rung), and the action stays armed instead of dead-ending the user.
	covered := store.Session{Ticket: "K1", State: "needs-you",
		Denials: agent.MarshalDenials([]agent.Denial{{Tool: "Bash", Command: "gradle help"}})}
	if !itemIDs(agentActions(covered))[actAllowRerun] {
		t.Error("head already allowed → the exact-rule escalation should keep allow-rerun offered")
	}

	// denial whose exact rule is ALSO present → truly nothing to add → not
	// offered (this is where the flavor message and approval callback own it).
	if err := config.Save(&config.Config{ExtraAllowedTools: "Bash(gradle help)"}); err != nil {
		t.Fatal(err)
	}
	if itemIDs(agentActions(covered))[actAllowRerun] {
		t.Error("head and exact rule both present → a config change would be a no-op, must not be offered")
	}
}

// Applying the action appends the suggested rules to extra_allowed_tools (the
// append-only key) and clears the stored denials.
func TestDoAllowRerunAppendsExtraAllowedTools(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{ExtraAllowedTools: "Bash(make:*)"}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(paths.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("K2", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	denials := agent.MarshalDenials([]agent.Denial{{Tool: "Bash", Command: "bundle exec fastlane test"}})
	if err := st.SetDenials("K2", denials, true); err != nil {
		t.Fatal(err)
	}

	m := monitorModel{store: st, selfPath: "pie"}
	m.doAllowRerun(store.Session{Ticket: "K2", State: "needs-you", Denials: denials})

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExtraAllowedTools != "Bash(make:*) Bash(bundle:*)" {
		t.Errorf("extra_allowed_tools = %q, want existing value preserved + new rule appended", cfg.ExtraAllowedTools)
	}
	if got, _ := st.Get("K2"); got.Denials != "" {
		t.Errorf("denials not cleared: %q", got.Denials)
	}
}

// The dashboard's needs-you explanation names the blocked commands and the
// one-keystroke action instead of the generic "agent got stuck".
func TestWhyLinesShowsDeniedCommands(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	// The pane reads the runner's park-time verdict (DenialFixable), never
	// re-deriving it with different inputs than the menu.
	s := store.Session{Ticket: "K3", State: "needs-you", DenialFixable: true,
		Denials: agent.MarshalDenials([]agent.Denial{{Tool: "Bash", Command: "gradle help"}})}
	all := strings.Join(whyLines(s), "\n")
	for _, want := range []string{"BLOCKED by permissions", "gradle help", "Allow denied commands & re-run"} {
		if !strings.Contains(all, want) {
			t.Errorf("whyLines missing %q:\n%s", want, all)
		}
	}
	// And without denials the classic message survives.
	plain := strings.Join(whyLines(store.Session{Ticket: "K3", State: "needs-you"}), "\n")
	if !strings.Contains(plain, "agent got stuck") {
		t.Errorf("plain needs-you lost its message:\n%s", plain)
	}
}

// The pane and the menu must agree: for every fixability the runner can park
// with, the pane promises the allow-rerun action exactly when the menu offers
// it. This is the contradiction the field user hit - the pane said the menu
// offered the fix while the menu hid it.
func TestWhyPaneAgreesWithMenu(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{}); err != nil {
		t.Fatal(err)
	}
	mkWorktree(t, "K12")
	for _, fixable := range []bool{true, false} {
		den := []agent.Denial{{Tool: "Bash", Command: "bundle exec fastlane"}}
		if !fixable {
			// A denial the ladder cannot express: guarded git state.
			den = []agent.Denial{{Tool: "Bash", Command: "git checkout main"}}
		}
		s := store.Session{Ticket: "K12", State: "needs-you",
			DenialFixable: fixable, Denials: agent.MarshalDenials(den)}
		panePromises := strings.Contains(strings.Join(whyLines(s), "\n"), "Allow denied commands & re-run")
		menuOffers := itemIDs(agentActions(s))[actAllowRerun]
		if panePromises != menuOffers {
			t.Errorf("fixable=%v: pane promises %v but menu offers %v", fixable, panePromises, menuOffers)
		}
	}
}

// A fix-review row drafted under denials warns before the human approves.
func TestWhyLinesFixReviewShowsDenialWarning(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	s := store.Session{Ticket: "K13", State: store.StateFixReview,
		Denials: agent.MarshalDenials([]agent.Denial{{Tool: "Bash", Command: "cd Compass && ./gradlew test"}})}
	all := strings.Join(whyLines(s), "\n")
	for _, want := range []string{"may be unverified", "cd Compass && ./gradlew test"} {
		if !strings.Contains(all, want) {
			t.Errorf("fix-review whyLines missing %q:\n%s", want, all)
		}
	}
	// Without denials, no warning.
	clean := strings.Join(whyLines(store.Session{Ticket: "K13", State: store.StateFixReview}), "\n")
	if strings.Contains(clean, "unverified") {
		t.Errorf("clean fix-review must not warn:\n%s", clean)
	}
}

// Garbage in the denials column must never offer the action or panic.
func TestAllowRerunIgnoresGarbageDenials(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{}); err != nil {
		t.Fatal(err)
	}
	mkWorktree(t, "K9")
	s := store.Session{Ticket: "K9", State: "needs-you", Denials: "{not json"}
	if itemIDs(agentActions(s))[actAllowRerun] {
		t.Error("garbage denials JSON must not offer allow-rerun")
	}
}

// A denial whose head rule AND exact rule are both already in
// extra_allowed_tools is a no-op grant - the ladder is exhausted and the
// action must not be offered (same honesty rule as default-covered commands).
func TestAllowRerunNotOfferedWhenExtraAlreadyCovers(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{
		ExtraAllowedTools: "Bash(bundle:*) Bash(bundle exec fastlane)"}); err != nil {
		t.Fatal(err)
	}
	mkWorktree(t, "K10")
	s := store.Session{Ticket: "K10", State: "needs-you",
		Denials: agent.MarshalDenials([]agent.Denial{{Tool: "Bash", Command: "bundle exec fastlane"}})}
	if itemIDs(agentActions(s))[actAllowRerun] {
		t.Error("head and exact rule already in extra_allowed_tools - a rewrite would change nothing")
	}
}
