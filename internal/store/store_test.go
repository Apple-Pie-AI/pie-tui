package store

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestClaimIsAtomic(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 10 concurrent workers race to claim the same ticket; exactly one wins.
	const n = 10
	var wg sync.WaitGroup
	wins := make([]bool, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			ok, err := s.Claim("PROJ-1", "/repo", "summary")
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			wins[i] = ok
		}(i)
	}
	wg.Wait()

	count := 0
	for _, w := range wins {
		if w {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 winning claim, got %d", count)
	}
}

func TestStateAndList(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.Claim("PROJ-2", "/repo", "do thing"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState("PROJ-2", StateReview, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFields("PROJ-2", "ai/proj-2", "/wt", "http://pr/1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("PROJ-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateReview || got.Retries != 2 || got.Branch != "ai/proj-2" || got.PRURL != "http://pr/1" {
		t.Fatalf("unexpected session: %+v", got)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v (err %v), want 1", list, err)
	}
}

// SetStateIf must swap only when the row is still in the state the caller
// read - it is the daemon's guard against stomping a run the user started
// between the daemon's List and its write.
func TestSetStateIf(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.Claim("PROJ-9", "/repo", "do thing"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState("PROJ-9", StateNeedsYou, 1); err != nil {
		t.Fatal(err)
	}

	ok, err := s.SetStateIf("PROJ-9", StateNeedsYou, StateMerged, 1)
	if err != nil || !ok {
		t.Fatalf("SetStateIf from the read state = (%v, %v), want a swap", ok, err)
	}
	got, _ := s.Get("PROJ-9")
	if got.State != StateMerged {
		t.Fatalf("state = %q, want %q", got.State, StateMerged)
	}

	// The row moved on (a re-run flipped it to queued): the CAS must lose.
	if err := s.SetState("PROJ-9", StateQueued, 0); err != nil {
		t.Fatal(err)
	}
	ok, err = s.SetStateIf("PROJ-9", StateNeedsYou, StateClosed, 0)
	if err != nil || ok {
		t.Fatalf("SetStateIf on a moved row = (%v, %v), want no swap", ok, err)
	}
	got, _ = s.Get("PROJ-9")
	if got.State != StateQueued {
		t.Fatalf("state = %q - the live run's state must be untouched", got.State)
	}
}

func TestSetPIDRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.Claim("PROJ-9", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPID("PROJ-9", 4242); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("PROJ-9")
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 4242 {
		t.Errorf("PID = %d, want 4242", got.PID)
	}
}

func TestSetSessionIDRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Claim("PROJ-7", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionID("PROJ-7", "sess-abc-123"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("PROJ-7")
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "sess-abc-123" {
		t.Errorf("SessionID = %q, want sess-abc-123", got.SessionID)
	}
}

func TestSetShortDescRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.Claim("PROJ-20", "/repo", "summary"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetShortDesc("PROJ-20", "upload paper PDF to storage"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("PROJ-20")
	if err != nil {
		t.Fatal(err)
	}
	if got.ShortDesc != "upload paper PDF to storage" {
		t.Errorf("ShortDesc = %q, want %q", got.ShortDesc, "upload paper PDF to storage")
	}
}

func TestSetReviewPlanRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.Claim("PROJ-30", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	// Default is false.
	got, err := s.Get("PROJ-30")
	if err != nil {
		t.Fatal(err)
	}
	if got.ReviewPlan {
		t.Error("ReviewPlan should default to false")
	}
	// Set true, read back.
	if err := s.SetReviewPlan("PROJ-30", true); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get("PROJ-30")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ReviewPlan {
		t.Error("ReviewPlan should be true after SetReviewPlan(true)")
	}
	// Clear back to false.
	if err := s.SetReviewPlan("PROJ-30", false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get("PROJ-30")
	if got.ReviewPlan {
		t.Error("ReviewPlan should be false after SetReviewPlan(false)")
	}
}

// StatePlanReview is an idle state: it must not count as active, so the driver
// process exits and a re-run (approve/feedback) is allowed.
// IsActive gates the "already running, refuse a second run" check (cmd/run.go)
// and the dashboard's action menu. A single negative case would still pass if
// IsActive were gutted to `return false`, so pin every state in both directions.
func TestIsActiveCoversEveryState(t *testing.T) {
	active := []string{
		StateQueued, StatePlanning, StateWorking,
		StateReviewing, StateBuilding, StateTesting,
	}
	inactive := []string{
		StateAwaiting, StateReview, StateNeedsYou, StateFailed,
		StatePlanReview, StateMerged, StateClosed, StateStopped,
		"", "unknown-state",
	}
	for _, s := range active {
		if !IsActive(s) {
			t.Errorf("IsActive(%q) = false, want true", s)
		}
	}
	for _, s := range inactive {
		if IsActive(s) {
			t.Errorf("IsActive(%q) = true, want false", s)
		}
	}
	if len(active) == 0 {
		t.Fatal("no active states asserted - IsActive could be gutted to return false")
	}
}

func TestPlanReviewNotActive(t *testing.T) {
	if IsActive(StatePlanReview) {
		t.Error("StatePlanReview must not be an active state")
	}
}

// The review_plan migration is idempotent (a second Open of the same DB must not
// fail, and round-trips still work).
func TestReviewPlanMigrationIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open failed: %v", err)
	}
	defer s2.Close()

	if _, err := s2.Claim("PROJ-31", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s2.SetReviewPlan("PROJ-31", true); err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get("PROJ-31")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ReviewPlan {
		t.Error("ReviewPlan after reopen should be true")
	}
}

func TestShortDescMigrationIdempotent(t *testing.T) {
	// Opening the same DB twice should not fail (ALTER TABLE short_desc is idempotent
	// because the error is silently ignored on the second call).
	path := filepath.Join(t.TempDir(), "state.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open failed: %v", err)
	}
	defer s2.Close()

	// Basic operations work on the re-opened DB.
	if _, err := s2.Claim("PROJ-21", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s2.SetShortDesc("PROJ-21", "short gist"); err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get("PROJ-21")
	if err != nil {
		t.Fatal(err)
	}
	if got.ShortDesc != "short gist" {
		t.Errorf("ShortDesc after reopen = %q", got.ShortDesc)
	}
}

// The denials column: set, read back through Get/List, clear, and survive the
// ALTER-based migration on a pre-existing DB (Open runs it idempotently).
func TestDenialsRoundTripAndClear(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Claim("D-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDenials("D-1", `[{"tool":"Bash","command":"gradle help"}]`, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("D-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Denials != `[{"tool":"Bash","command":"gradle help"}]` {
		t.Errorf("Denials = %q", got.Denials)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].Denials == "" {
		t.Errorf("List lost the denials: %v %v", list, err)
	}
	if err := s.SetDenials("D-1", "", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("D-1"); got.Denials != "" {
		t.Errorf("clear failed: %q", got.Denials)
	}
}

// ClearWorktree records that a reclaimed row's worktree and branch are gone -
// it is what makes the daemon's CLOSED sweep idempotent instead of re-running
// git against the same row every poll.
func TestClearWorktree(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.Claim("PROJ-11", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFields("PROJ-11", "ai/proj-11", "/wt/proj-11", "http://pr/1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearWorktree("PROJ-11"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("PROJ-11")
	if err != nil {
		t.Fatal(err)
	}
	if got.Worktree != "" || got.Branch != "" {
		t.Fatalf("worktree/branch = %q/%q, want both cleared", got.Worktree, got.Branch)
	}
	if got.PRURL != "http://pr/1" {
		t.Fatal("PRURL must survive the clear - the CLOSED row still links to its PR")
	}
}

// AppendChangeTranscript grows the Plan-mode conversation line by line
// rather than requiring the caller to read-modify-write the whole thing.
func TestAppendChangeTranscriptGrowsLineByLine(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Claim("T-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}

	if err := s.AppendChangeTranscript("T-1", "you: what does this file do?"); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendChangeTranscript("T-1", "claude: it renders the home screen."); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("T-1")
	if err != nil {
		t.Fatal(err)
	}
	want := "you: what does this file do?\nclaude: it renders the home screen."
	if got.ChangeTranscript != want {
		t.Fatalf("ChangeTranscript = %q, want %q", got.ChangeTranscript, want)
	}

	if err := s.SetChangeTranscript("T-1", ""); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get("T-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ChangeTranscript != "" {
		t.Fatalf("SetChangeTranscript must be able to clear it, got %q", got.ChangeTranscript)
	}
}
