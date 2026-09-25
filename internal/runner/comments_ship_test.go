package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// fakeGH puts a gh on PATH that answers the two review mutations with canned
// GraphQL responses and logs every invocation - so a test can assert both what
// reached GitHub and what came back from it, with no network anywhere.
func fakeGH(t *testing.T) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> '" + logPath + "'\n" +
		"case \"$*\" in\n" +
		"  *addPullRequestReviewThreadReply*) echo '{\"data\":{\"addPullRequestReviewThreadReply\":{\"comment\":{\"id\":\"PRRC_FAKE1\"}}}}' ;;\n" +
		"  *resolveReviewThread*) echo '{\"data\":{\"resolveReviewThread\":{\"thread\":{\"id\":\"T1\",\"isResolved\":true}}}}' ;;\n" +
		"  *) echo '{}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// The full write-back seam that the own-reply-resurrection fix spans: gh's
// response carries the new comment's id, review.Reply parses it, and
// replyOnGitHub stores it as the thread's last_comment - so the next poll,
// seeing the same id, keeps the thread closed. The store-level test pins the
// SQL; this one pins that the id actually makes the journey from a gh
// response into that SQL.
func TestReplyOnGitHubAdvancesLastComment(t *testing.T) {
	logPath := fakeGH(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertThreads("K-1", "https://pr/1", []review.Thread{{
		ID: "T1", Kind: review.KindThread, Author: "alice",
		Path: "a.kt", Line: 3, Body: "fix this", LastCommentID: "C1",
	}}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkAddressed([]string{"T1"}, "abc123"); err != nil {
		t.Fatal(err)
	}

	task := Task{
		Ticket: "K-1",
		Repo:   &config.Repo{Path: t.TempDir()},
		Cfg:    &config.Config{}, // reply and resolve both default on
		Store:  st,
	}
	cmts := []store.Comment{{ID: "T1", Kind: review.KindThread, DraftReply: "Done."}}
	replyOnGitHub(task, cmts, []string{"T1"}, nil, "abc123", func(string, ...interface{}) {})

	rows, err := st.Comments("K-1")
	if err != nil || len(rows) != 1 {
		t.Fatalf("Comments: %v (%d rows)", err, len(rows))
	}
	c := rows[0]
	if !c.Replied {
		t.Error("thread not marked replied")
	}
	if c.LastCommentID != "PRRC_FAKE1" {
		t.Errorf("last_comment = %q, want the id gh returned (PRRC_FAKE1)", c.LastCommentID)
	}

	// The point of the journey: the next poll reports our own reply as the last
	// comment, and the thread must stay closed.
	if err := st.UpsertThreads("K-1", "https://pr/1", []review.Thread{{
		ID: "T1", Kind: review.KindThread, Author: "alice",
		Path: "a.kt", Line: 3, Body: "fix this", LastCommentID: "PRRC_FAKE1",
	}}, true); err != nil {
		t.Fatal(err)
	}
	if open, _ := st.OpenComments("K-1"); len(open) != 0 {
		t.Fatalf("our own reply reopened the thread after the poll: %d open", len(open))
	}

	// And both mutations actually went out.
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "addPullRequestReviewThreadReply") {
		t.Error("no reply mutation reached gh")
	}
	if !strings.Contains(string(log), "resolveReviewThread") {
		t.Error("no resolve mutation reached gh")
	}
}

// A gh whose response carries no comment id must still record the reply -
// last_comment stays put, the thread reopens on the next poll, and nothing
// double-posts. Reopening is the safe failure; double-posting is not.
func TestReplyOnGitHubWithoutAnIDStillMarksReplied(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho '{}'\n" // mutation "succeeds", shape unexpected
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertThreads("K-1", "https://pr/1", []review.Thread{{
		ID: "T1", Kind: review.KindThread, Author: "alice",
		Path: "a.kt", Line: 3, Body: "fix this", LastCommentID: "C1",
	}}, true); err != nil {
		t.Fatal(err)
	}

	task := Task{Ticket: "K-1", Repo: &config.Repo{Path: t.TempDir()},
		Cfg: &config.Config{}, Store: st}
	cmts := []store.Comment{{ID: "T1", Kind: review.KindThread, DraftReply: "Done."}}
	replyOnGitHub(task, cmts, []string{"T1"}, nil, "abc123", func(string, ...interface{}) {})

	rows, _ := st.Comments("K-1")
	if len(rows) != 1 || !rows[0].Replied {
		t.Fatal("reply must be recorded even when its id cannot be read")
	}
	if rows[0].LastCommentID != "C1" {
		t.Errorf("last_comment = %q, want C1 untouched when no id came back", rows[0].LastCommentID)
	}
}

// The bug every approve ever pressed hit: the launcher resets the session to
// `queued` at startup, and the ship gate then read the row and concluded no
// approval had happened - so shipping bounced to needs-you, always. The gate
// now reads the state the launcher FOUND (Task.PriorState); the row is only
// the fallback for direct callers.
func TestShipGateReadsTheStateTheLauncherFound(t *testing.T) {
	fakeGH(t) // commentsPreflight probes the PR state via gh

	newTask := func(prior string) (Task, *store.Store) {
		st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		if _, err := st.Claim("K-1", "/repo", "x"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetFields("K-1", "b", "", "https://github.com/o/r/pull/9"); err != nil {
			t.Fatal(err)
		}
		// What the launcher does the moment any run starts:
		if err := st.SetState("K-1", store.StateQueued, 0); err != nil {
			t.Fatal(err)
		}
		return Task{Ticket: "K-1", Repo: &config.Repo{Path: t.TempDir()},
			Cfg: &config.Config{}, Store: st, ShipComments: true, PriorState: prior}, st
	}

	// Approved: the launcher saw fix-review. The gate must let the ship
	// proceed - it then fails on the missing worktree, which is the proof it
	// got PAST the approval gate.
	task, _ := newTask(store.StateFixReview)
	out := shipComments(t.Context(), task, Hooks{})
	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want needs-you (missing worktree)", out.State)
	}

	// Not approved: the launcher saw review (no preview was ever parked). The
	// gate must refuse even though the row says `queued` either way.
	var logs []string
	task2, _ := newTask(store.StateReview)
	out = shipComments(t.Context(), task2, Hooks{
		Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) },
	})
	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want needs-you (gate refusal)", out.State)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "before local fixes were approved") {
		t.Fatalf("expected the gate refusal, got:\n%s", joined)
	}

	// And the fallback: no launcher involved (PriorState empty), row parked at
	// fix-review - direct invocation still passes the gate.
	task3, st3 := newTask("")
	if err := st3.SetState("K-1", store.StateFixReview, 0); err != nil {
		t.Fatal(err)
	}
	out = shipComments(t.Context(), task3, Hooks{})
	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want needs-you (missing worktree, past the gate)", out.State)
	}
}

// Ticket 1232 with model_impl misspelled "Haiky": the fix agent died on its
// first breath, and the run still parked at fix-review announcing "the fixes
// are in the worktree and the replies are drafted" - an empty preview wearing
// a success message. A dead agent may only park fix-review when there is
// evidence to review: a contract entry for this batch, or a worktree change.
func TestDeadFixAgentDoesNotParkAVictoryState(t *testing.T) {
	fakeGH(t)
	// A claude that fails exactly like a misconfigured model does.
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "claude"),
		[]byte("#!/bin/sh\necho \"There's an issue with the selected model (Haiky).\" >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PIE_HOME", t.TempDir())

	// The ticket's worktree: a real repo so evidence checks run for real.
	wt := paths.WorktreeFor("", "K-1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wt}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(wt, "a.kt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "base")

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("K-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFields("K-1", "b", wt, "https://github.com/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("K-1", "https://github.com/o/r/pull/9", []review.Thread{{
		ID: "NEW1", Kind: review.KindThread, Author: "a", Path: "a.kt", Line: 1, Body: "fix", LastCommentID: "c1",
	}}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueComments("K-1", []string{"NEW1"}); err != nil {
		t.Fatal(err)
	}

	task := Task{Ticket: "K-1", Repo: &config.Repo{Path: "/repo"},
		Cfg: &config.Config{}, Store: st, AddressComments: true}
	var logs []string
	out := addressComments(t.Context(), task, Hooks{
		Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) },
	})
	joined := strings.Join(logs, "\n")

	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want needs-you - a dead agent parked a victory state:\n%s", out.State, joined)
	}
	if !strings.Contains(joined, "failed before producing anything") {
		t.Errorf("the failure is not named in the log:\n%s", joined)
	}
	if strings.Contains(joined, "the fixes are in the worktree") {
		t.Errorf("the success message printed for a dead agent:\n%s", joined)
	}

	// A stale contract from an EARLIER batch is not evidence for this one.
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := `{"fixes":[{"id":"OLD-ROUND-ID","action":"fixed","note":"n","reply":"r"}]}`
	if err := os.WriteFile(filepath.Join(wt, ".agent", "comment_fixes.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	// .agent/ is untracked; it must not count as "the worktree has changes".
	if err := os.WriteFile(filepath.Join(wt, ".gitignore"), []byte(".agent/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".gitignore")
	git("commit", "-q", "-m", "ignore agent scratch")
	out = addressComments(t.Context(), task, Hooks{})
	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want needs-you - a stale contract counted as evidence", out.State)
	}

	// A contract covering THIS batch is evidence: agent wrote it, then crashed.
	fresh := `{"fixes":[{"id":"NEW1","action":"fixed","note":"n","reply":"r"}]}`
	if err := os.WriteFile(filepath.Join(wt, ".agent", "comment_fixes.json"), []byte(fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	out = addressComments(t.Context(), task, Hooks{})
	if out.State != store.StateFixReview {
		t.Fatalf("state = %q, want fix-review - agent wrote the contract before dying", out.State)
	}
}

// shipFixture is a ship-comments world: a worktree with one pushed commit (the
// PR head) on branch b and a real bare origin, one queued thread with a draft
// reply, and a claude stub the test chooses. withEdit plants the approved
// (uncommitted) fix exactly as fix-review leaves it.
type shipFixture struct {
	wt, preSHA string
	ghLog      string
	st         *store.Store
	task       Task
	git        func(args ...string) string
}

func newShipFixture(t *testing.T, withEdit bool, claudeScript func(wt string) string) shipFixture {
	t.Helper()
	ghLog := fakeGH(t)
	t.Setenv("PIE_HOME", t.TempDir())

	wt := paths.WorktreeFor("", "K-1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wt}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("bare init: %v: %s", err, out)
	}
	git("init", "-q", "-b", "b")
	if err := os.WriteFile(filepath.Join(wt, "a.kt"), []byte("open class UploadPhotosUseCase\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "base")
	git("remote", "add", "origin", origin)
	git("push", "-q", "-u", "origin", "b")
	preSHA := git("rev-parse", "HEAD")

	if withEdit {
		if err := os.WriteFile(filepath.Join(wt, "a.kt"), []byte("class UploadPhotosUseCase\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(claudeScript(wt)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	// A real (empty) repo dir: gh runs with cwd here, so the reply/resolve
	// mutations actually reach the fake and the log assertions bite.
	repoDir := t.TempDir()
	if _, err := st.Claim("K-1", repoDir, "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFields("K-1", "b", wt, "https://github.com/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("K-1", "https://github.com/o/r/pull/9", []review.Thread{{
		ID: "T1", Kind: review.KindThread, Author: "a", Path: "a.kt", Line: 1,
		Body: "must this class be open?", LastCommentID: "c1",
	}}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueComments("K-1", []string{"T1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDraftReply("T1", "Good catch - removed the modifier."); err != nil {
		t.Fatal(err)
	}
	return shipFixture{wt: wt, preSHA: preSHA, ghLog: ghLog, st: st, git: git,
		task: Task{Ticket: "K-1", Repo: &config.Repo{Path: repoDir}, Cfg: &config.Config{},
			Store: st, ShipComments: true, PriorState: store.StateFixReview}}
}

// greenVerifyStub certifies verified:true without touching the code.
func greenVerifyStub(wt string) string {
	return "#!/bin/sh\n" +
		"mkdir -p '" + filepath.Join(wt, ".agent") + "'\n" +
		"cat > '" + filepath.Join(wt, ".agent", "report.json") + "' <<'EOJ'\n" +
		`{"status":"ready_for_build","verified":true,"summary":"verified","prBody":"body"}` + "\n" +
		"EOJ\necho '{}'\n"
}

// revertingVerifyStub is the ASKME-TEST verify agent: it reverts the approved
// edit to make the build green, then certifies verified:true.
func revertingVerifyStub(wt string) string {
	return "#!/bin/sh\n" +
		"git -C '" + wt + "' checkout -q -- a.kt\n" +
		greenVerifyStub(wt)[len("#!/bin/sh\n"):]
}

// The ASKME-TEST bug, end to end: the human approved a fix (an uncommitted
// edit in the worktree), the ship's verify agent - allowed to fix its own red
// build - REVERTED that edit to get tests green and certified verified:true.
// finishShip then had nothing to commit, and the pipeline still replied
// "Fixed in <sha>" with the PRE-EXISTING head, resolved the thread, and marked
// the comment addressed. The fix existed nowhere.
//
// The honest behavior this test demands: when the approved fixes vanish before
// shipping, the run parks needs-you, posts NOTHING on GitHub, and keeps the
// batch queued so the work can be redone.
func TestShipNeverClaimsAFixThatDidNotShip(t *testing.T) {
	fx := newShipFixture(t, true, revertingVerifyStub)
	st, ghLog, preSHA, git := fx.st, fx.ghLog, fx.preSHA, fx.git

	var logs []string
	out := shipComments(t.Context(), fx.task, Hooks{
		Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) },
	})
	joined := strings.Join(logs, "\n")

	if out.State != store.StateNeedsYou {
		t.Errorf("state = %q, want needs-you - the fixes vanished, yet the ship claimed success:\n%s", out.State, joined)
	}
	log, _ := os.ReadFile(ghLog)
	if strings.Contains(string(log), "addPullRequestReviewThreadReply") {
		t.Errorf("a reply reached GitHub for a fix that does not exist:\n%s", log)
	}
	if strings.Contains(string(log), "resolveReviewThread") {
		t.Errorf("the thread was resolved for a fix that does not exist:\n%s", log)
	}
	if q, _ := st.QueuedComments("K-1"); len(q) != 1 {
		t.Errorf("queued = %d, want 1 - the batch must survive for a re-run", len(q))
	}
	rows, _ := st.Comments("K-1")
	if len(rows) == 1 && !rows[0].AddressedAt.IsZero() {
		t.Errorf("comment marked addressed at %s (sha %s) with no commit shipped",
			rows[0].AddressedAt, rows[0].AddressedSHA)
	}
	if strings.Contains(joined, "addressed 1 of 1") {
		t.Errorf("the log claims the comment was addressed:\n%s", joined)
	}
	if head := git("rev-parse", "HEAD"); head != preSHA {
		t.Fatalf("fixture broke: HEAD moved from %s to %s without a fix", preSHA, head)
	}
}

// The entry guard: worktree clean, origin current - the approved fixes exist
// nowhere, so the ship parks before spending a verify run.
func TestShipParksWhenTheFixesAreAlreadyGone(t *testing.T) {
	fx := newShipFixture(t, false, greenVerifyStub) // no edit at all

	var logs []string
	out := shipComments(t.Context(), fx.task, Hooks{
		Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) },
	})
	joined := strings.Join(logs, "\n")
	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want needs-you:\n%s", out.State, joined)
	}
	if strings.Contains(joined, "stage:verify") {
		t.Errorf("verify ran for a batch with nothing to ship:\n%s", joined)
	}
	log, _ := os.ReadFile(fx.ghLog)
	if strings.Contains(string(log), "addPullRequestReviewThreadReply") {
		t.Errorf("a reply reached GitHub with nothing to ship:\n%s", log)
	}
	if q, _ := fx.st.QueuedComments("K-1"); len(q) != 1 {
		t.Errorf("queued = %d, want 1 - the batch must survive for a re-run", len(q))
	}
}

// The happy path must stay a happy path: verify leaves the fixes alone, the
// ship commits them, and the reply cites the NEW commit - not the old head.
func TestShipHappyPathCommitsAndRepliesWithTheNewSHA(t *testing.T) {
	fx := newShipFixture(t, true, greenVerifyStub)

	var logs []string
	out := shipComments(t.Context(), fx.task, Hooks{
		Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) },
	})
	joined := strings.Join(logs, "\n")
	if out.State != store.StateReview {
		t.Fatalf("state = %q, want review:\n%s", out.State, joined)
	}
	newHead := fx.git("rev-parse", "HEAD")
	if newHead == fx.preSHA {
		t.Fatal("no new commit was created for the approved fix")
	}
	log, _ := os.ReadFile(fx.ghLog)
	if !strings.Contains(string(log), "addPullRequestReviewThreadReply") {
		t.Errorf("the reply never reached GitHub:\n%s", log)
	}
	rows, _ := fx.st.Comments("K-1")
	if len(rows) != 1 || rows[0].AddressedAt.IsZero() || rows[0].AddressedSHA != newHead {
		t.Errorf("comment = %+v, want addressed at the new commit %s", rows[0], newHead)
	}
}

// A fix already committed locally (but unpushed) is legitimate: nothing new to
// commit, but the push publishes it - the reply may cite that commit.
func TestShipAllowsAnAlreadyCommittedFix(t *testing.T) {
	fx := newShipFixture(t, true, greenVerifyStub)
	fx.git("add", ".")
	fx.git("commit", "-q", "-m", "the fix, committed by an earlier ship attempt")
	fixSHA := fx.git("rev-parse", "HEAD")

	var logs []string
	out := shipComments(t.Context(), fx.task, Hooks{
		Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) },
	})
	joined := strings.Join(logs, "\n")
	if out.State != store.StateReview {
		t.Fatalf("state = %q, want review:\n%s", out.State, joined)
	}
	log, _ := os.ReadFile(fx.ghLog)
	if !strings.Contains(string(log), "addPullRequestReviewThreadReply") {
		t.Errorf("the reply never reached GitHub for a committed fix:\n%s", log)
	}
	rows, _ := fx.st.Comments("K-1")
	if len(rows) != 1 || rows[0].AddressedSHA != fixSHA {
		t.Errorf("addressed sha = %q, want the committed fix %s", rows[0].AddressedSHA, fixSHA)
	}
}

// The three honesty predicates, by truth table.
func TestShipHonestyPredicates(t *testing.T) {
	someErr := fmt.Errorf("no origin")
	if !nothingToShip(false, 0, nil) {
		t.Error("clean + origin current must park")
	}
	if nothingToShip(true, 0, nil) {
		t.Error("uncommitted fixes are something to ship")
	}
	if nothingToShip(false, 1, nil) {
		t.Error("an unpushed local commit is something to ship")
	}
	if nothingToShip(false, 0, someErr) {
		t.Error("unknown divergence is not proof of nothing - the reply gate backstops it")
	}

	if !verifyRevertedFixes(true, false, "h1", "h1") {
		t.Error("fixes present, then gone, HEAD unmoved = a revert")
	}
	if verifyRevertedFixes(true, true, "h1", "h1") {
		t.Error("fixes still present is not a revert")
	}
	if verifyRevertedFixes(false, false, "h1", "h1") {
		t.Error("nothing at entry cannot be reverted")
	}
	if verifyRevertedFixes(true, false, "h1", "h2") {
		t.Error("HEAD moved = the work was committed, not erased")
	}

	if !nothingNewToCite("h1", "h1", 0, nil) {
		t.Error("no new commit and nothing ahead = nothing to cite")
	}
	if nothingNewToCite("h1", "h2", 0, nil) {
		t.Error("a new commit is citable")
	}
	if nothingNewToCite("h1", "h1", 2, nil) {
		t.Error("published local commits are citable")
	}
	if !nothingNewToCite("h1", "h1", 2, someErr) {
		t.Error("an errored divergence cannot vouch for ahead commits")
	}
}

// PLEX-60299: the verify agent rewrote report.json's prBody with its
// verification narrative, and a brand-new PR shipped that narrative as its
// description instead of the repo's filled template. The instruction says
// "preserve prBody"; this is the enforcement: whatever verify writes there,
// the pre-verify body wins.
func TestVerifyCannotRewriteThePRBody(t *testing.T) {
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := `{"status":"ready_for_build","verified":false,"prBody":"# [K-1] The template, filled in"}`
	if err := os.WriteFile(filepath.Join(wt, ".agent", "report.json"), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	// A claude that does what the PLEX verify agent did: clobbers prBody.
	dir := t.TempDir()
	script := "#!/bin/sh\ncat > '" + filepath.Join(wt, ".agent", "report.json") + "' <<'EOJ'\n" +
		`{"status":"ready_for_build","verified":true,"prBody":"## Verification Results\n\nBUILD SUCCESSFUL"}` +
		"\nEOJ\necho '{}'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	task := Task{Ticket: "K-1", Repo: &config.Repo{Path: wt}, Cfg: &config.Config{}}
	rep, verified, _, _, _ := verifyWithAgent(t.Context(), task, wt, "", false, true, "",
		func(string, ...interface{}) {}, func(string, int) {})
	if rep == nil || !verified {
		t.Fatalf("verify should have produced a verified report, got %+v", rep)
	}
	if rep.PRBody != "# [K-1] The template, filled in" {
		t.Fatalf("prBody = %q - verify's rewrite survived", rep.PRBody)
	}
}
