package store

import (
	"path/filepath"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
)

func commentStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Claim("KAN-1", "/repo", "Add login"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := st.SetState("KAN-1", StateReview, 0); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	return st
}

func thread(id, lastComment string) review.Thread {
	return review.Thread{
		ID:            id,
		Kind:          review.KindThread,
		Author:        "alice",
		Path:          "app/Login.kt",
		Line:          42,
		Body:          "don't swallow the exception",
		DiffHunk:      "@@ -38,7 +38,11 @@",
		CommentCount:  1,
		LastCommentID: lastComment,
	}
}

func TestUpsertThreadsIsIdempotent(t *testing.T) {
	st := commentStore(t)
	ths := []review.Thread{thread("T1", "C1"), thread("T2", "C2")}

	for i := 0; i < 3; i++ {
		if err := st.UpsertThreads("KAN-1", "https://pr/1", ths, true); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	got, err := st.Comments("KAN-1")
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("re-polling must not duplicate rows: got %d", len(got))
	}
	first := got[0].FirstSeenAt
	if err := st.UpsertThreads("KAN-1", "https://pr/1", ths, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, _ = st.Comments("KAN-1")
	if !got[0].FirstSeenAt.Equal(first) {
		t.Error("first_seen_at must survive re-polling")
	}
}

// The session badge is derived, so it has to track the table exactly.
func TestSessionOpenCommentsIsDerived(t *testing.T) {
	st := commentStore(t)
	sess, _ := st.Get("KAN-1")
	if sess.OpenComments != 0 {
		t.Fatalf("fresh session should have 0 open comments, got %d", sess.OpenComments)
	}

	if err := st.UpsertThreads("KAN-1", "https://pr/1",
		[]review.Thread{thread("T1", "C1"), thread("T2", "C2")}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sess, _ = st.Get("KAN-1"); sess.OpenComments != 2 {
		t.Fatalf("OpenComments = %d, want 2", sess.OpenComments)
	}
	// List() must agree with Get(): the dashboard reads List.
	list, _ := st.List()
	if len(list) != 1 || list[0].OpenComments != 2 {
		t.Fatalf("List OpenComments = %+v, want 2", list)
	}

	if err := st.MarkAddressed([]string{"T1"}, "abc123"); err != nil {
		t.Fatalf("MarkAddressed: %v", err)
	}
	if sess, _ = st.Get("KAN-1"); sess.OpenComments != 1 {
		t.Fatalf("after addressing one, OpenComments = %d, want 1", sess.OpenComments)
	}
	if err := st.SetCommentSkipped("T2", true); err != nil {
		t.Fatalf("SetCommentSkipped: %v", err)
	}
	if sess, _ = st.Get("KAN-1"); sess.OpenComments != 0 {
		t.Fatalf("after skipping the last, OpenComments = %d, want 0", sess.OpenComments)
	}
}

// A resolved-on-GitHub thread is not ours to re-offer.
func TestResolvedThreadIsNotOpen(t *testing.T) {
	st := commentStore(t)
	th := thread("T1", "C1")
	th.Resolved = true
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{th}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	sess, _ := st.Get("KAN-1")
	if sess.OpenComments != 0 {
		t.Errorf("a resolved thread must not count as open, got %d", sess.OpenComments)
	}
}

// The multi-round test: this is the behaviour most likely to be subtly wrong.
// A poll that changes nothing must preserve every decision; a poll that brings
// a NEW reply must reopen the thread, because the reviewer came back.
func TestUpsertPreservesDecisionsUntilANewReplyArrives(t *testing.T) {
	st := commentStore(t)
	if err := st.UpsertThreads("KAN-1", "https://pr/1",
		[]review.Thread{thread("T1", "C1"), thread("T2", "C2")}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.SetCommentNote("T1", "also update the unit test"); err != nil {
		t.Fatalf("SetCommentNote: %v", err)
	}
	if err := st.MarkAddressed([]string{"T1"}, "abc123"); err != nil {
		t.Fatalf("MarkAddressed: %v", err)
	}
	if err := st.SetCommentSkipped("T2", true); err != nil {
		t.Fatalf("SetCommentSkipped: %v", err)
	}

	// Poll again with nothing changed - same pivots.
	if err := st.UpsertThreads("KAN-1", "https://pr/1",
		[]review.Thread{thread("T1", "C1"), thread("T2", "C2")}, true); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	byID := indexByID(t, st)
	if byID["T1"].AddressedAt.IsZero() {
		t.Error("an unchanged poll must not reopen an addressed thread")
	}
	if byID["T1"].AddressedSHA != "abc123" {
		t.Errorf("addressed_sha = %q, want it preserved", byID["T1"].AddressedSHA)
	}
	if !byID["T2"].Skipped {
		t.Error("an unchanged poll must not un-skip a dismissed thread")
	}
	if byID["T1"].UserNote != "also update the unit test" {
		t.Errorf("user_note = %q, a poll must never overwrite it", byID["T1"].UserNote)
	}

	// Now the reviewers reply to both: the pivot moves.
	if err := st.UpsertThreads("KAN-1", "https://pr/1",
		[]review.Thread{thread("T1", "C9"), thread("T2", "C8")}, true); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	byID = indexByID(t, st)
	if !byID["T1"].AddressedAt.IsZero() {
		t.Error("a new reply must reopen an addressed thread")
	}
	if byID["T1"].AddressedSHA != "" {
		t.Errorf("addressed_sha = %q, want it cleared on reopen", byID["T1"].AddressedSHA)
	}
	if byID["T2"].Skipped {
		t.Error("a new reply must un-skip a dismissed thread")
	}
	if byID["T1"].UserNote != "also update the unit test" {
		t.Error("user_note must survive a reopen too - it is the human's own words")
	}
	sess, _ := st.Get("KAN-1")
	if sess.OpenComments != 2 {
		t.Errorf("both threads should be open again, got %d", sess.OpenComments)
	}
}

// A truncated fetch must never be treated as authoritative about what exists.
func TestUpsertDeletesOnlyOnACompleteFetch(t *testing.T) {
	st := commentStore(t)
	if err := st.UpsertThreads("KAN-1", "https://pr/1",
		[]review.Thread{thread("T1", "C1"), thread("T2", "C2")}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Partial page: T2 is missing but must be kept.
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{thread("T1", "C1")}, false); err != nil {
		t.Fatalf("partial upsert: %v", err)
	}
	if got, _ := st.Comments("KAN-1"); len(got) != 2 {
		t.Fatalf("a truncated fetch must not delete unseen threads: got %d", len(got))
	}

	// Complete page: T2 really is gone.
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{thread("T1", "C1")}, true); err != nil {
		t.Fatalf("complete upsert: %v", err)
	}
	if got, _ := st.Comments("KAN-1"); len(got) != 1 {
		t.Fatalf("a complete fetch must delete vanished threads: got %d", len(got))
	}
}

// An empty complete fetch means every thread was deleted on GitHub.
func TestUpsertEmptyCompleteFetchClearsAll(t *testing.T) {
	st := commentStore(t)
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{thread("T1", "C1")}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.UpsertThreads("KAN-1", "https://pr/1", nil, true); err != nil {
		t.Fatalf("empty upsert: %v", err)
	}
	if got, _ := st.Comments("KAN-1"); len(got) != 0 {
		t.Fatalf("want no rows, got %d", len(got))
	}
}

func TestQueueComments(t *testing.T) {
	st := commentStore(t)
	if err := st.UpsertThreads("KAN-1", "https://pr/1",
		[]review.Thread{thread("T1", "C1"), thread("T2", "C2"), thread("T3", "C3")}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := st.QueueComments("KAN-1", []string{"T1", "T3"}); err != nil {
		t.Fatalf("QueueComments: %v", err)
	}
	q, _ := st.QueuedComments("KAN-1")
	if len(q) != 2 || q[0].ID != "T1" || q[1].ID != "T3" {
		t.Fatalf("queued = %v, want T1 and T3", ids(q))
	}

	// A second selection must not inherit the first one's leftovers.
	if err := st.QueueComments("KAN-1", []string{"T2"}); err != nil {
		t.Fatalf("QueueComments: %v", err)
	}
	q, _ = st.QueuedComments("KAN-1")
	if len(q) != 1 || q[0].ID != "T2" {
		t.Fatalf("queued = %v, want only T2", ids(q))
	}

	if err := st.ClearQueued("KAN-1"); err != nil {
		t.Fatalf("ClearQueued: %v", err)
	}
	if q, _ = st.QueuedComments("KAN-1"); len(q) != 0 {
		t.Fatalf("queued = %v, want empty", ids(q))
	}
}

// Selecting nothing is a legal state and must not be a SQL syntax error.
func TestQueueCommentsEmpty(t *testing.T) {
	st := commentStore(t)
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{thread("T1", "C1")}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.QueueComments("KAN-1", nil); err != nil {
		t.Fatalf("QueueComments(nil): %v", err)
	}
	if q, _ := st.QueuedComments("KAN-1"); len(q) != 0 {
		t.Fatalf("queued = %v, want empty", ids(q))
	}
}

func TestMarkRepliedAndAgentNote(t *testing.T) {
	st := commentStore(t)
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{thread("T1", "C1")}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.MarkReplied(map[string]string{"T1": ""}); err != nil {
		t.Fatalf("MarkReplied: %v", err)
	}
	if err := st.SetCommentAgentNote("T1", "the suggested API does not exist"); err != nil {
		t.Fatalf("SetCommentAgentNote: %v", err)
	}
	c := indexByID(t, st)["T1"]
	if !c.Replied {
		t.Error("Replied should be true")
	}
	if c.AgentNote != "the suggested API does not exist" {
		t.Errorf("AgentNote = %q", c.AgentNote)
	}
	// The agent's excuse must not clobber the human's own guidance.
	if c.UserNote != "" {
		t.Errorf("UserNote = %q, want it untouched", c.UserNote)
	}
}

func indexByID(t *testing.T, st *Store) map[string]Comment {
	t.Helper()
	all, err := st.Comments("KAN-1")
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	m := make(map[string]Comment, len(all))
	for _, c := range all {
		m[c.ID] = c
	}
	return m
}

func ids(cs []Comment) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// The regression the user found in manual QA: fix a comment, and within two
// minutes the ticket was back in NEEDS YOU. Our own reply was the thread's new
// last comment, so the next poll read it as "the reviewer came back" and reset
// addressed_at. MarkReplied now advances last_comment to our reply, and this
// walks the whole loop to pin it.
func TestOwnReplyDoesNotReopenTheThread(t *testing.T) {
	st := commentStore(t)
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{thread("T1", "C1")}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// The ship path: the fix lands, we reply, the reply's id comes back.
	if err := st.MarkAddressed([]string{"T1"}, "abc123"); err != nil {
		t.Fatalf("MarkAddressed: %v", err)
	}
	if err := st.MarkReplied(map[string]string{"T1": "OURREPLY"}); err != nil {
		t.Fatalf("MarkReplied: %v", err)
	}

	// The next poll: GitHub now reports our own reply as the last comment.
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{thread("T1", "OURREPLY")}, true); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	open, err := st.OpenComments("KAN-1")
	if err != nil {
		t.Fatalf("OpenComments: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("our own reply reopened the thread: open = %v", ids(open))
	}
	if c := indexByID(t, st)["T1"]; c.AddressedAt.IsZero() || !c.Replied {
		t.Errorf("addressed/replied lost across the poll: addressed=%v replied=%v", c.AddressedAt, c.Replied)
	}

	// The reviewer actually coming back must still reopen it - the pivot exists
	// for them, we just stopped tripping it ourselves.
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{thread("T1", "REVIEWER-AGAIN")}, true); err != nil {
		t.Fatalf("reviewer upsert: %v", err)
	}
	if open, _ := st.OpenComments("KAN-1"); len(open) != 1 {
		t.Fatalf("a real reviewer reply must reopen the thread: open = %v", ids(open))
	}
}

// A reply whose id could not be read out of the response records the reply but
// leaves last_comment alone: the thread will reopen on the next poll, which is
// the safe failure - silently staying closed on a real follow-up is not.
func TestMarkRepliedWithoutIDLeavesThePivotAlone(t *testing.T) {
	st := commentStore(t)
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{thread("T1", "C1")}, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.MarkReplied(map[string]string{"T1": ""}); err != nil {
		t.Fatalf("MarkReplied: %v", err)
	}
	c := indexByID(t, st)["T1"]
	if !c.Replied {
		t.Error("Replied should be true")
	}
	if c.LastCommentID != "C1" {
		t.Errorf("last_comment = %q, want C1 untouched", c.LastCommentID)
	}
}

// A reviewer's "Comment"-type review has no resolve mechanism on GitHub and
// the reviewer chose not to block on it - so it must never hold the ticket in
// NEEDS YOU. It stays listed (and fixable) but is not counted; the dashboard's
// grouping runs off this count, so advisory-only means READY FOR REVIEW.
func TestAdvisoryReviewsDoNotCountAsOpen(t *testing.T) {
	st := commentStore(t)
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{
		{ID: "RV1", Kind: review.KindReview, Author: "gerardo", Body: "Update the PR description.",
			LastCommentID: "RV1", Advisory: true},
	}, true); err != nil {
		t.Fatal(err)
	}

	// Listed - the triage screen still shows it, and the user may still queue it.
	all, err := st.Comments("KAN-1")
	if err != nil || len(all) != 1 || !all[0].Advisory {
		t.Fatalf("Comments = %d rows (err %v), want the advisory row listed", len(all), err)
	}
	// Not counted - neither by OpenComments nor by the session's derived count.
	if open, _ := st.OpenComments("KAN-1"); len(open) != 0 {
		t.Fatalf("advisory review counted as open: %v", ids(open))
	}
	sess, err := st.Get("KAN-1")
	if err != nil || sess == nil {
		t.Fatal(err)
	}
	if sess.OpenComments != 0 {
		t.Fatalf("session OpenComments = %d, want 0 - this is what pins READY FOR REVIEW", sess.OpenComments)
	}

	// A blocking review still counts.
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{
		{ID: "RV1", Kind: review.KindReview, Author: "gerardo", Body: "Update the PR description.",
			LastCommentID: "RV1", Advisory: true},
		{ID: "RV2", Kind: review.KindReview, Author: "gerardo", Body: "The retry policy is wrong.",
			LastCommentID: "RV2"},
	}, true); err != nil {
		t.Fatal(err)
	}
	sess, _ = st.Get("KAN-1")
	if sess.OpenComments != 1 {
		t.Fatalf("OpenComments = %d, want 1 - only the blocking review", sess.OpenComments)
	}

	// Existing rows from before the column existed backfill on the next poll:
	// the upsert writes advisory unconditionally.
	if _, err := st.db.Exec(`UPDATE pr_comments SET advisory=0 WHERE id='RV1'`); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("KAN-1", "https://pr/1", []review.Thread{
		{ID: "RV1", Kind: review.KindReview, Author: "gerardo", Body: "Update the PR description.",
			LastCommentID: "RV1", Advisory: true},
		{ID: "RV2", Kind: review.KindReview, Author: "gerardo", Body: "The retry policy is wrong.",
			LastCommentID: "RV2"},
	}, true); err != nil {
		t.Fatal(err)
	}
	sess, _ = st.Get("KAN-1")
	if sess.OpenComments != 1 {
		t.Fatalf("OpenComments = %d after re-poll, want 1 - advisory did not backfill", sess.OpenComments)
	}
}
