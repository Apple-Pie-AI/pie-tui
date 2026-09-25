package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// approveModel is a screen parked at fix-review: two queued comments, one
// fixed with a drafted reply, one declined with a reason.
func approveModel(t *testing.T) monitorModel {
	t.Helper()
	st := openTestStore(t)
	if _, err := st.Claim("A-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("A-1", "https://github.com/o/r/pull/482",
		[]review.Thread{
			{ID: "T1", Kind: review.KindThread, Author: "bob", Path: "a.kt", Line: 1, Body: "guard the token", LastCommentID: "c1"},
			{ID: "T2", Kind: review.KindThread, Author: "maria", Path: "b.kt", Line: 2, Body: "bump the version", LastCommentID: "c2"},
		}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueComments("A-1", []string{"T1", "T2"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDraftReply("T1", "Added the null guard and a regression test."); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCommentAgentNote("T2", "version bumps are out of scope for this PR"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("A-1", store.StateFixReview, 0); err != nil {
		t.Fatal(err)
	}

	m := baseModel()
	m.store = st
	m.reload()
	got, _ := m.openCommentsView(store.Session{
		Ticket: "A-1", Repo: "/repo", State: store.StateFixReview,
		PRURL: "https://github.com/o/r/pull/482",
	})
	return got.(monitorModel)
}

// A session parked at fix-review draws the approve screen, before any "done"
// bookkeeping - the preview is not a result.
func TestFixReviewIsTheApprovePhase(t *testing.T) {
	m := approveModel(t)
	m.comments.ran = map[string]bool{"T1": true} // even with a stale batch marker
	if m.phase() != phaseApprove {
		t.Fatalf("phase = %d, want phaseApprove", m.phase())
	}
}

// The card screen states the deal: one fix, its reply, the actions, and that
// nothing ships until the last card.
func TestRenderCards(t *testing.T) {
	m := approveModel(t)
	m.width, m.height = 120, 40
	out := ansiRe.ReplaceAllString(m.View(), "")
	// The declined fix leads: it needs a decision before anything else is
	// worth reading. Captions render letter-spaced.
	for _, want := range []string{
		"fix 1 of 2",
		letterSpace("WHAT CHANGED"),
		letterSpace("THE AGENT DECLINED"),
		"version bumps are out of scope",
		"POST THE ANSWER & RESOLVE", // a declined thread's primary posts the answer
		"Ask for a different fix",
		"Edit the reply",
		"Skip this one",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("card screen is missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// Approving advances to the next undecided card; deciding the last lands on
// the ship card; SHIP IT queues exactly the approved fixes and spawns the run.
func TestApproveAdvancesAndShips(t *testing.T) {
	m := approveModel(t)
	m.width, m.height = 120, 40
	// Card 1 is the declined one (declined fixes come first); approve is inert
	// there, so skip it.
	m.comments.cardSel = 4 // Skip this one (Reply & resolve took row 1)
	got, _ := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	// Now on the drafted fix: approve it → all decided → the ship card.
	m.comments.cardSel = 0
	got, _ = m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if m.comments.cardIdx != len(m.cardFixes()) {
		t.Fatalf("cardIdx = %d, want the ship card (%d)", m.comments.cardIdx, len(m.cardFixes()))
	}
	out := ansiRe.ReplaceAllString(m.View(), "")
	for _, want := range []string{"ready to ship", "SHIP IT", "1 fix approved", "1 skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("ship card missing %q\n%s", want, out)
		}
	}
	got, cmd := m.updateComments(keyOf("enter")) // SHIP IT
	m = got.(monitorModel)
	if cmd == nil {
		t.Fatal("SHIP IT should spawn the ship run")
	}
	if m.view != viewDashboard {
		t.Fatalf("view = %v after shipping, want the dashboard", m.view)
	}
	q, err := m.store.QueuedComments("A-1")
	if err != nil || len(q) != 1 || q[0].ID != "T1" {
		t.Fatalf("queued = %v (err %v), want exactly the approved fix T1", len(q), err)
	}
}

// Regression (user-reported release blocker): "the SHIP IT button doesn't
// allow to skip the process - after pressing it nothing happens." When every
// fix is skipped/declined (0 approved), the ship card is not blocked (no
// card is left undecided) but shipApproved used to just set a notice and
// return - no queue clear, no state change, no navigation. The ticket stayed
// wedged at fix-review with a live-looking button that silently did nothing.
// The fix: releasing the ticket back to review, the same way
// comments_fetch.go already does when GitHub resolves every queued thread.
func TestShipCardAllSkippedReleasesTheTicket(t *testing.T) {
	m := approveModel(t)
	m.width, m.height = 120, 40
	// Skip both cards - the declined one (T2) first, then the drafted one (T1).
	m.comments.cardSel = 4
	got, _ := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	m.comments.cardSel = 4
	got, _ = m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if m.comments.cardIdx != len(m.cardFixes()) {
		t.Fatalf("cardIdx = %d, want the ship card (%d)", m.comments.cardIdx, len(m.cardFixes()))
	}

	out := ansiRe.ReplaceAllString(m.View(), "")
	if !strings.Contains(out, "RELEASE") {
		t.Errorf("the ship card must not still say SHIP IT when nothing is approved - it doesn't ship anything:\n%s", out)
	}
	if strings.Contains(out, "verify · commit · push") {
		t.Errorf("the button's meta must not promise a ship that won't happen:\n%s", out)
	}

	got, cmd := m.updateComments(keyOf("enter")) // the release row
	m = got.(monitorModel)
	if cmd != nil {
		t.Error("releasing with nothing approved must not spawn a run - nothing to verify/commit/push")
	}
	if m.view != viewDashboard {
		t.Fatalf("view = %v after releasing, want the dashboard", m.view)
	}
	sess, err := m.store.Get("A-1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.State != store.StateReview {
		t.Fatalf("state = %q, want review - the ticket must not stay wedged at fix-review", sess.State)
	}
	q, err := m.store.QueuedComments("A-1")
	if err != nil || len(q) != 0 {
		t.Fatalf("queued = %v (err %v), want none - a declined fix must not haunt the next round", q, err)
	}
}

// The ship card cannot ship while anything is undecided, and says which row
// takes you there.
func TestShipGateHoldsWhileUndecided(t *testing.T) {
	m := approveModel(t)
	m.width, m.height = 120, 40
	m.comments.cardIdx = len(m.cardFixes()) // jump straight to the ship card
	out := ansiRe.ReplaceAllString(m.View(), "")
	for _, want := range []string{"CAN'T SHIP YET", "Go to the first undecided fix"} {
		if !strings.Contains(out, want) {
			t.Errorf("blocked ship card missing %q\n%s", want, out)
		}
	}
	got, cmd := m.updateComments(keyOf("enter")) // enter on CAN'T SHIP YET
	m = got.(monitorModel)
	if cmd != nil || m.view == viewDashboard {
		t.Fatal("a blocked ship card must not ship")
	}
	m.comments.cardSel = 1
	got, _ = m.updateComments(keyOf("enter")) // go to the first undecided
	m = got.(monitorModel)
	if m.comments.cardIdx >= len(m.cardFixes()) {
		t.Fatal("go-to-undecided should land on a fix card")
	}
}

// Editing the reply happens at the block itself and saves to the store - the
// ship posts whatever is there.
func TestEditReplyInPlace(t *testing.T) {
	m := approveModel(t)
	// Move to the drafted fix (declined ones come first).
	fixes := m.cardFixes()
	for i, c := range fixes {
		if c.ID == "T1" {
			m.comments.cardIdx = i
		}
	}
	m.comments.cardSel = 3 // Edit the reply
	got, _ := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if m.comments.cardMode != cardReply {
		t.Fatal("Edit the reply should open the in-place editor")
	}
	// The draft must be visible and editable - not collapsed into a paste chip.
	if r := m.comments.nb.render(); !strings.Contains(r, "Added the null guard") ||
		strings.Contains(r, "line added]") {
		t.Fatalf("editor shows %q, want the draft's own words", r)
	}
	m.comments.nb.clear()
	m = press(m, "D", "o", "n", "e")
	got, _ = m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	rows, _ := m.store.Comments("A-1")
	for _, c := range rows {
		if c.ID == "T1" && c.DraftReply != "Done" {
			t.Fatalf("DraftReply = %q, want the rewrite to win", c.DraftReply)
		}
	}
}

// Ask for a different fix collects the note, marks the card sent back, and
// dispatches one revise run; the ship gate holds while it is out.
func TestAskForADifferentFix(t *testing.T) {
	m := approveModel(t)
	m.flat = []store.Session{{Ticket: "A-1", Repo: "/repo", State: store.StateFixReview}}
	m.comments.cardSel = 2 // Ask for a different fix
	got, _ := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if m.comments.cardMode != cardAsk {
		t.Fatal("Ask should open the what-should-it-do-instead field")
	}
	m = press(m, "u", "s", "e", " ", "x")
	got, cmd := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if cmd == nil {
		t.Fatal("the note should dispatch a revise run")
	}
	fixes := m.cardFixes()
	if m.comments.decisions[fixes[0].ID] != cardRedo {
		t.Fatalf("decision = %q, want sent-back", m.comments.decisions[fixes[0].ID])
	}
	if m.view != viewComments {
		t.Fatal("deciding other cards must continue while the agent reworks")
	}
}

// Undo puts an approved card back to undecided and does not advance.
func TestUndoStaysPut(t *testing.T) {
	m := approveModel(t)
	fixes := m.cardFixes()
	var drafted int
	for i, c := range fixes {
		if c.ID == "T1" {
			drafted = i
		}
	}
	m.comments.cardIdx, m.comments.cardSel = drafted, 0
	m.comments.decisions["T1"] = cardOK
	got, _ := m.updateComments(keyOf("enter")) // UNDO
	m = got.(monitorModel)
	if m.comments.decisions["T1"] != "" {
		t.Fatal("undo should clear the decision")
	}
	if m.comments.cardIdx != drafted {
		t.Fatal("undo must not advance")
	}
}

// ←→ browse cards without deciding, wrapping through the ship card.
func TestArrowsBrowseWithoutDeciding(t *testing.T) {
	m := approveModel(t)
	n := len(m.cardFixes())
	got, _ := m.updateComments(keyOf("right"))
	m = got.(monitorModel)
	if m.comments.cardIdx != 1 {
		t.Fatalf("cardIdx = %d after →, want 1", m.comments.cardIdx)
	}
	for i := 0; i < n+1; i++ { // a full lap: every fix plus the ship card
		got, _ = m.updateComments(keyOf("right"))
		m = got.(monitorModel)
	}
	if m.comments.cardIdx != 1 {
		t.Fatalf("a full lap of rights should return to card 1, got %d", m.comments.cardIdx)
	}
	if len(m.comments.decisions) != 0 {
		t.Fatal("browsing must not decide anything")
	}
}

// The unified diff splits per file, keeps hunks, and drops file metadata.
func TestSplitDiffByFile(t *testing.T) {
	diff := "diff --git a/a.kt b/a.kt\nindex 111..222 100644\n--- a/a.kt\n+++ b/a.kt\n" +
		"@@ -1,2 +1,3 @@\n ctx\n+added\n" +
		"diff --git a/b.kt b/b.kt\nindex 333..444 100644\n--- a/b.kt\n+++ b/b.kt\n" +
		"@@ -5,1 +5,1 @@\n-old\n+new\n"
	files := splitDiffByFile(diff)
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	if got := strings.Join(files["a.kt"], "\n"); got != "@@ -1,2 +1,3 @@\n ctx\n+added" {
		t.Fatalf("a.kt = %q", got)
	}
	if got := strings.Join(files["b.kt"], "\n"); !strings.Contains(got, "-old") || strings.Contains(got, "index") {
		t.Fatalf("b.kt = %q", got)
	}
}

// Esc leaves the approve screen without shipping anything - the preview is
// durable, parked in the store and the worktree.
func TestEscLeavesApproveWithoutShipping(t *testing.T) {
	m := approveModel(t)
	m = press(m, "esc")
	if m.view != viewDashboard {
		t.Fatal("esc should return to the dashboard")
	}
	c, err := m.store.Comments("A-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, cm := range c {
		if cm.ID == "T1" && (cm.Replied || !cm.AddressedAt.IsZero()) {
			t.Fatal("nothing may be marked shipped by merely looking at the preview")
		}
	}
}

// A fix is allowed to land in a different file than the comment that asked for
// it. The pane used to answer that with "see the other rows" - the whole diff
// was already in memory, so now it shows it: grouped by file for a summary
// comment or an untouched anchor, focused with a see-also hint otherwise.
func TestApproveDiffShowsTheWholeFixWhenTheAnchorIsUntouched(t *testing.T) {
	m := baseModel()
	m.height = 60
	m.comments.diffLoaded = true
	m.comments.diff = map[string][]string{
		"app/HomeViewModel.kt": {"@@ -1,2 +1,3 @@", " class HomeViewModel {", "+    val fixed = true", " }"},
		"app/Repo.kt":          {"@@ -8,1 +8,2 @@", "+    retry()"},
	}
	joined := func(rows []string) string { return stripAnsi(strings.Join(rows, "\n")) }

	// The user's case: the comment anchors a test file the fix never touched.
	anchored := &store.Comment{ID: "T1", Path: "app/HomeViewModelTest.kt", Line: 87}
	got := joined(m.approveDiffRows(anchored, 120))
	for _, want := range []string{"no changes in app/HomeViewModelTest.kt itself",
		"every change the fix made", "app/HomeViewModel.kt", "app/Repo.kt", "val fixed = true", "retry()"} {
		if !strings.Contains(got, want) {
			t.Errorf("untouched anchor: missing %q in:\n%s", want, got)
		}
	}

	// A summary comment has no file at all - same answer.
	summary := &store.Comment{ID: "T2", Path: ""}
	got = joined(m.approveDiffRows(summary, 120))
	for _, want := range []string{"review summary", "app/HomeViewModel.kt", "app/Repo.kt"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary: missing %q in:\n%s", want, got)
		}
	}

	// An anchored file the fix DID touch leads, and every other changed file
	// still follows - ticket 1232's "use a localized string" fix was one line
	// in the anchored screen file and one in strings.xml, and a focused-only
	// view hid the strings.xml half with no row anywhere able to show it.
	hit := &store.Comment{ID: "T3", Path: "app/HomeViewModel.kt", Line: 2}
	got = joined(m.approveDiffRows(hit, 120))
	if !strings.Contains(got, "val fixed = true") {
		t.Errorf("touched anchor: its own diff missing:\n%s", got)
	}
	if !strings.Contains(got, "retry()") {
		t.Errorf("touched anchor: the rest of the fix is hidden:\n%s", got)
	}
	// The anchor leads; the other file follows under its header.
	if strings.Index(got, "val fixed = true") > strings.Index(got, "retry()") {
		t.Errorf("the anchored file should lead the diff:\n%s", got)
	}
	if !strings.Contains(got, "app/Repo.kt") {
		t.Errorf("the other file's section has no header:\n%s", got)
	}
	// A blank row separates the sections - butted together they read as one
	// file with a stray path in the middle.
	if !strings.Contains(got, "\n\n") {
		t.Errorf("no blank row between file sections:\n%s", got)
	}

	// No changes anywhere: say that, not an empty pane.
	m.comments.diff = map[string][]string{}
	if got = joined(m.approveDiffRows(anchored, 120)); !strings.Contains(got, "no uncommitted changes - the fix may already be committed") {
		t.Errorf("empty worktree: missing the honest empty message:\n%s", got)
	}
}

// The user's PR #56: the fix was a brand-new test file, and the approve pane
// said "the worktree has no uncommitted changes". This drives the real command
// against a real repo with an upstream, holding every kind of change approve
// would ship at once: a commit the agent made (against its instructions - the
// preview must not depend on obedience), an unstaged edit, a staged new file,
// an untracked new file, and a deletion.
func TestApproveDiffSeesEverythingApproveWouldShip(t *testing.T) {
	origin, dir := t.TempDir(), t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	run("init", "-q")
	run("remote", "add", "origin", origin)
	write("Tracked.kt", "old\n")
	write("ToDelete.kt", "doomed\n")
	run("add", ".")
	run("commit", "-q", "-m", "the PR as pushed")
	run("push", "-q", "-u", "origin", "HEAD")

	run("commit", "-q", "--allow-empty", "-m", "placeholder") // keep HEAD moving honest
	run("reset", "-q", "--hard", "HEAD~1")                    // (and back - upstream unchanged)

	write("Committed.kt", "the agent committed this itself\n")
	run("add", "Committed.kt")
	run("commit", "-q", "-m", "agent went rogue and committed") // 1. local commit
	write("Tracked.kt", "edited\n")                             // 2. unstaged edit
	write("Staged.kt", "staged\n")
	run("add", "Staged.kt")                                                              // 3. staged new file
	write("GetFeedUseCaseTest.kt", "class GetFeedUseCaseTest {\n    fun test() {}\n}\n") // 4. untracked
	run("rm", "-q", "ToDelete.kt")                                                       // 5. deletion

	msg := approveDiffCmd(dir, "T-1")().(approveDiffMsg)
	if msg.err != nil {
		t.Fatalf("approveDiffCmd: %v", msg.err)
	}
	for _, path := range []string{"Committed.kt", "Tracked.kt", "Staged.kt", "GetFeedUseCaseTest.kt", "ToDelete.kt"} {
		if len(msg.files[path]) == 0 {
			t.Errorf("%s missing from the diff - files seen: %v", path, keysOf(msg.files))
		}
	}
	joined := strings.Join(msg.files["GetFeedUseCaseTest.kt"], "\n")
	if !strings.Contains(joined, "+class GetFeedUseCaseTest {") {
		t.Errorf("untracked file's content not rendered as added lines:\n%s", joined)
	}
	if !strings.Contains(strings.Join(msg.files["ToDelete.kt"], "\n"), "-doomed") {
		t.Errorf("deletion not rendered as removed lines: %v", msg.files["ToDelete.kt"])
	}
}

// A worktree with no upstream (nothing ever pushed) falls back to HEAD - the
// diff must degrade to "uncommitted changes", never to an error.
func TestApproveDiffFallsBackToHEADWithoutUpstream(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "A.kt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "A.kt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := approveDiffCmd(dir, "T-1")().(approveDiffMsg)
	if msg.err != nil {
		t.Fatalf("approveDiffCmd: %v", msg.err)
	}
	if len(msg.files["A.kt"]) == 0 {
		t.Errorf("unstaged edit missing without an upstream - files: %v", keysOf(msg.files))
	}
}

// The whole pipeline at once, entering at the same door the user does: a real
// worktree, the real diff command, the real message routing, the rendered
// screen. The PR #56 report slipped between two green half-tests - the render
// was tested on a hand-built map and the map-builder was tested alone - so
// this is the test that refuses to let that seam open again.
func TestApprovePipelineFromWorktreeToScreen(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "base.kt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "base")
	// The fix: one brand-new untracked test file, exactly the PR #56 shape.
	if err := os.WriteFile(filepath.Join(dir, "GetFeedUseCaseTest.kt"),
		[]byte("class GetFeedUseCaseTest {\n    fun feedHasSixItems() {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := approveModel(t)
	m.width, m.height = 140, 44
	// openCommentsView already issued a fetch for the session's own (fake)
	// worktree path; point the screen at the real repo and drop the in-flight
	// marker, exactly as the cache invalidation after a revise run does.
	m.comments.worktree = dir
	m.comments.diffFetching, m.comments.diffLoaded, m.comments.diff = false, false, nil

	cmd := m.approveDiffIfNeeded()
	if cmd == nil {
		t.Fatal("approveDiffIfNeeded returned no command with the diff unloaded")
	}
	got, _ := m.Update(cmd()) // the real msg through the real router
	m = got.(monitorModel)

	screen := stripAnsi(m.View())
	// The focused comment anchors a.kt, which the fix never touched - the pane
	// must show the whole fix: the file's name, and its contents rendered as
	// numbered added lines ("1 + class …").
	for _, want := range []string{
		"every change the fix made",
		"GetFeedUseCaseTest.kt",
		"+ class GetFeedUseCaseTest {",
		"fun feedHasSixItems()",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("rendered screen missing %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "the fix may already be committed") {
		t.Errorf("screen claims a clean worktree with the fix sitting in it:\n%s", screen)
	}
}

func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The card goldens at the spec's four widths, plus the ship card - and the
// acceptance rule that no rendered line exceeds the terminal at any of them.
func TestGoldenCards(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
		ship  bool
	}{
		{"card-60", 60, false}, {"card-88", 88, false},
		{"card-104", 104, false}, {"card-152", 152, false},
		{"card-ship-104", 104, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := approveModel(t)
			m.width, m.height = tc.width, 40
			if tc.ship {
				for _, c := range m.cardFixes() {
					m.comments.decisions[c.ID] = cardOK
				}
				m.comments.cardIdx = len(m.cardFixes())
			}
			got := stripAnsi(m.View())
			for _, ln := range strings.Split(got, "\n") {
				if w := lipWidth(strings.TrimRight(ln, " ")); w > tc.width {
					t.Errorf("line runs to column %d on a %d-column terminal: %q", w, tc.width, ln)
				}
			}
			path := filepath.Join("testdata", tc.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run: go test ./internal/tui -update)", err)
			}
			if got != string(want) {
				t.Errorf("%s changed. Run `go test ./internal/tui -update` if intended.\n--- want ---\n%s\n--- got ---\n%s",
					tc.name, want, got)
			}
		})
	}
}

// The screenshot bug: a long diff pushed the actions off the bottom of the
// terminal - including "See the whole diff", the one row that would have shown
// the rest. The card's diff budget must always leave room for every action.
func TestLongDiffNeverEatsTheActions(t *testing.T) {
	m := approveModel(t)
	m.width, m.height = 120, 40
	long := []string{"@@ -1,120 +1,120 @@"}
	for i := 1; i <= 120; i++ {
		long = append(long, fmt.Sprintf("+line %d of a very long fix", i))
	}
	m.comments.diffLoaded = true
	m.comments.diff = map[string][]string{"a.kt": long}

	out := ansiRe.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		// Card 0 is the declined thread, whose primary posts the answer.
		"POST THE ANSWER & RESOLVE", "Ask for a different fix", "Edit the reply",
		"Skip this one", "See the whole diff", "↕ 1–",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("long-diff card is missing %q - the diff ate it", want)
		}
	}
	if n := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); n > m.height {
		t.Errorf("card renders %d lines on a %d-line terminal - the frame will clamp the actions", n, m.height)
	}

	// PgDn scrolls the inline window; the wheel drives the same scroll.
	got, _ := m.updateComments(keyOf("pgdown"))
	m2 := got.(monitorModel)
	if m2.comments.diffScroll == 0 {
		t.Fatal("pgdown should scroll the inline diff")
	}
	scrolled := ansiRe.ReplaceAllString(m2.View(), "")
	if !strings.Contains(scrolled, "POST THE ANSWER & RESOLVE") {
		t.Error("scrolling the diff must never hide the actions")
	}
	if strings.Contains(scrolled, "↕ 1–") {
		t.Error("the window did not move")
	}

	// And the viewer shows everything the card could not.
	m.comments.cardMode = cardDiff
	viewer := ansiRe.ReplaceAllString(m.View(), "")
	if !strings.Contains(viewer, "esc back to the card") {
		t.Errorf("the whole-diff viewer did not open:\n%s", truncate(viewer, 400))
	}
}

// Mouse reporting is scoped to the whole-diff viewer: entering turns it on
// (wheel scrolls, no action rows to fight), leaving turns it off (the mouse
// goes back to native select-and-copy everywhere else).
func TestViewerScopesMouseReporting(t *testing.T) {
	m := approveModel(t)
	m.width, m.height = 120, 24
	long := []string{"@@ -1,120 +1,120 @@"}
	for i := 1; i <= 120; i++ {
		long = append(long, fmt.Sprintf("+line %d", i))
	}
	m.comments.diffLoaded = true
	m.comments.diff = map[string][]string{"a.kt": long}
	if !m.cardDiffTruncated() {
		t.Fatal("fixture should overflow the card")
	}
	m.comments.cardSel = 5 // See the whole diff
	got, cmd := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if m.comments.cardMode != cardDiff || cmd == nil {
		t.Fatal("entering the viewer must switch mode and enable the mouse")
	}
	got, cmd = m.updateComments(keyOf("esc"))
	m = got.(monitorModel)
	if m.comments.cardMode != cardView || cmd == nil {
		t.Fatal("leaving the viewer must restore the mode and disable the mouse")
	}
}
