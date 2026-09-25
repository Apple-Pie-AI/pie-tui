package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
)

// checkoutFixture is a repo with a bare origin and one feature branch, plus a
// fake gh on PATH whose `pr view` behavior the test controls.
func checkoutFixture(t *testing.T, prURL string) (repo string, st *store.Store) {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	origin, repo := t.TempDir(), t.TempDir()
	gitc := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	gitc(repo, "init", "-q")
	gitc(repo, "remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(repo, "add", ".")
	gitc(repo, "commit", "-q", "-m", "base")
	gitc(repo, "push", "-q", "-u", "origin", "HEAD")
	gitc(repo, "branch", "feedback/pr-42")
	gitc(repo, "push", "-q", "origin", "feedback/pr-42")

	ghDir := t.TempDir()
	script := "#!/bin/sh\n"
	if prURL == "" {
		script += "exit 1\n"
	} else {
		script += "echo '" + prURL + "'\n"
	}
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return repo, st
}

// A branch with no PR files under TRACKING: state checked-out, worktree on the
// branch, and a stub the later agent runs resolve through runArg.
func TestCheckoutWithoutPRLandsInTracking(t *testing.T) {
	repo, st := checkoutFixture(t, "")
	msg := checkoutBranchCmd(st, repo, "feedback/pr-42")().(checkoutDoneMsg)
	if msg.err != nil {
		t.Fatalf("checkout: %v", msg.err)
	}
	sess, err := st.Get(msg.ticket)
	if err != nil || sess == nil {
		t.Fatalf("no session row: %v", err)
	}
	if sess.State != store.StateCheckedOut || sess.PRURL != "" {
		t.Fatalf("state=%q pr=%q, want checked-out with no PR", sess.State, sess.PRURL)
	}
	out, _ := exec.Command("git", "-C", sess.Worktree, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if got := strings.TrimSpace(string(out)); got != "feedback/pr-42" {
		t.Fatalf("worktree on %q, want the branch", got)
	}
	// The id round-trip: the session ticket must equal what a later `pie run`
	// derives from the stub path, and runArg must resolve to that stub.
	if want := ticket.IDFromPath(sess.SourcePath); sess.Ticket != want {
		t.Fatalf("ticket %q != IDFromPath %q - a later run would fork a second session", sess.Ticket, want)
	}
	if runArg(*sess) != sess.SourcePath {
		t.Fatalf("runArg = %q, want the stub path", runArg(*sess))
	}
	if _, err := os.Stat(sess.SourcePath); err != nil {
		t.Fatalf("stub missing: %v", err)
	}
	// Grouping: exactly one group claims it, and it is TRACKING.
	matched := ""
	for _, g := range newGroups() {
		if g.match(*sess) {
			if matched != "" {
				t.Fatalf("both %q and %q claim the row", matched, g.label)
			}
			matched = g.label
		}
	}
	if matched != "NO PULL REQUEST YET" {
		t.Fatalf("row filed under %q, want TRACKING", matched)
	}
}

// A branch with a PR goes straight into review with the URL recorded - the
// state that inherits merge detection (and, upstack, comment polling).
func TestCheckoutWithPRLandsInReview(t *testing.T) {
	repo, st := checkoutFixture(t, "https://github.com/o/r/pull/42")
	msg := checkoutBranchCmd(st, repo, "feedback/pr-42")().(checkoutDoneMsg)
	if msg.err != nil {
		t.Fatalf("checkout: %v", msg.err)
	}
	sess, _ := st.Get(msg.ticket)
	if sess.State != store.StateReview || sess.PRURL != "https://github.com/o/r/pull/42" {
		t.Fatalf("state=%q pr=%q, want review with the PR", sess.State, sess.PRURL)
	}
}

// A live run on the same derived id refuses; a dead one is reused, once.
func TestCheckoutGuardsAndReuses(t *testing.T) {
	repo, st := checkoutFixture(t, "")
	if _, err := st.Claim("FEEDBACK-PR-42", repo, "x"); err != nil {
		t.Fatal(err)
	}
	_ = st.SetState("FEEDBACK-PR-42", store.StateWorking, 0)
	_ = st.SetPID("FEEDBACK-PR-42", os.Getpid())
	msg := checkoutBranchCmd(st, repo, "feedback/pr-42")().(checkoutDoneMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "already running") {
		t.Fatalf("live run not refused: %v", msg.err)
	}
	// Kill the driver record; re-checkout reuses the same row.
	_ = st.SetState("FEEDBACK-PR-42", store.StateStopped, 0)
	_ = st.SetPID("FEEDBACK-PR-42", 0)
	if msg := checkoutBranchCmd(st, repo, "feedback/pr-42")().(checkoutDoneMsg); msg.err != nil {
		t.Fatalf("re-checkout: %v", msg.err)
	}
	rows, _ := st.List()
	if len(rows) != 1 || rows[0].State != store.StateCheckedOut {
		t.Fatalf("rows=%d state=%q, want the one row re-opened as checked-out", len(rows), rows[0].State)
	}
}

// A checkout that fails leaves no phantom row.
func TestCheckoutErrorLeavesNoRow(t *testing.T) {
	repo, st := checkoutFixture(t, "")
	msg := checkoutBranchCmd(st, repo, "no/such-branch")().(checkoutDoneMsg)
	if msg.err == nil {
		t.Fatal("nonexistent branch must fail")
	}
	if rows, _ := st.List(); len(rows) != 0 {
		t.Fatalf("%d phantom row(s) after a failed checkout", len(rows))
	}
}

// The menu's re-probe flips a TRACKING row to review once a PR exists.
func TestDetectPRFlipsToReview(t *testing.T) {
	repo, st := checkoutFixture(t, "")
	msg := checkoutBranchCmd(st, repo, "feedback/pr-42")().(checkoutDoneMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	sess, _ := st.Get(msg.ticket)
	// A PR appears: swap the fake gh for one that answers.
	ghDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(ghDir, "gh"),
		[]byte("#!/bin/sh\necho 'https://github.com/o/r/pull/9'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	dmsg := detectPRCmd(st, sess.Ticket, repo, sess.Branch, sess.Worktree)().(prDetectedMsg)
	if dmsg.prURL == "" {
		t.Fatal("PR not detected")
	}
	sess, _ = st.Get(msg.ticket)
	if sess.State != store.StateReview || sess.PRURL == "" {
		t.Fatalf("state=%q pr=%q, want review with the PR", sess.State, sess.PRURL)
	}
}

// A checked-out row's menu leads with the change screen (review the branch's
// local changes, rework, ship) and offers the PR probe second; agent-recovery
// verbs stay hidden - nothing ran, so there is nothing to resume or rerun.
func TestCheckedOutMenu(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	items := agentActions(store.Session{Ticket: "K-1", Repo: "/r",
		State: store.StateCheckedOut, Branch: "b", Worktree: "/wt"})
	if len(items) < 2 || items[0].key != actViewChange || items[1].key != actDetectPR {
		t.Fatalf("menu should lead with Review and make changes, then the PR probe, got %+v", items)
	}
	for _, it := range items {
		switch it.key {
		case actResume, actRerun, actShip:
			t.Fatalf("agent-recovery verb %q offered on a row no agent ever ran", it.key)
		}
	}
}

// The decision this stack exists for: a checkout that finds a PR carries its
// review threads immediately - the badge is live from second one, not from the
// next 2-minute poll. The fake gh answers `pr view` with a URL and the
// GraphQL thread query with one unresolved thread.
func TestCheckoutAutoFetchesReviewThreads(t *testing.T) {
	repo, st := checkoutFixture(t, "") // its gh is replaced below
	ghDir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *graphql*) echo '{"data":{"resource":{"__typename":"PullRequest","number":42,"state":"OPEN","reviews":{"pageInfo":{"hasNextPage":false},"nodes":[]},"reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"T1","isResolved":false,"isOutdated":false,"path":"a.kt","line":3,"comments":{"totalCount":1,"nodes":[{"id":"C1","author":{"login":"alice"},"body":"fix this","url":"https://x/1","diffHunk":""}]}}]}}}}' ;;
  *) echo 'https://github.com/o/r/pull/42' ;;
esac`
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	msg := checkoutBranchCmd(st, repo, "feedback/pr-42")().(checkoutDoneMsg)
	if msg.err != nil {
		t.Fatalf("checkout: %v", msg.err)
	}
	if msg.threads != 1 {
		t.Fatalf("threads = %d, want the fetched thread reported", msg.threads)
	}
	sess, _ := st.Get(msg.ticket)
	if sess.State != store.StateReview || sess.OpenComments != 1 {
		t.Fatalf("state=%q open=%d, want review with the badge already live", sess.State, sess.OpenComments)
	}
}

// The tutorial-1 bug: a branch whose PR-opening run already has a session
// (under a DIFFERENT ticket id) must be adopted, not twinned - twin sessions
// split one PR's threads into two partial comment counts.
func TestCheckoutAdoptsTheBranchsExistingSession(t *testing.T) {
	repo, st := checkoutFixture(t, "")
	// The original run's session: ticket id unrelated to the branch's name.
	if _, err := st.Claim("TUTORIAL-1", repo, "the original run"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFields("TUTORIAL-1", "feedback/pr-42", "", "https://github.com/o/r/pull/65"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("TUTORIAL-1", store.StateReview, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSourcePath("TUTORIAL-1", "/orig/ticket.md"); err != nil {
		t.Fatal(err)
	}

	msg := checkoutBranchCmd(st, repo, "feedback/pr-42")().(checkoutDoneMsg)
	if msg.err != nil {
		t.Fatalf("checkout: %v", msg.err)
	}
	if msg.ticket != "TUTORIAL-1" {
		t.Fatalf("ticket = %q, want the branch's existing session adopted", msg.ticket)
	}
	rows, _ := st.List()
	if len(rows) != 1 {
		t.Fatalf("%d sessions for one branch - the twin bug is back", len(rows))
	}
	if rows[0].SourcePath != "/orig/ticket.md" {
		t.Fatalf("SourcePath = %q - adoption must keep the original ticket", rows[0].SourcePath)
	}
}
