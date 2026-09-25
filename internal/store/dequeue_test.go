package store

import (
	"path/filepath"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
)

// Ticket 2, PR #68: the human resolved a queued thread on GitHub while the
// fix was parked at fix-review. Pie kept demanding a decision on it - the
// batch is "every queued row", and a poll marks the row resolved without
// touching queued. A thread resolved on GitHub has nothing left to ship:
// DequeueResolved drops it from the batch and says how many it dropped.
func TestDequeueResolvedDropsQueuedThreadsResolvedOnGitHub(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	threads := []review.Thread{
		{ID: "T1", Kind: review.KindThread, Author: "a", Path: "a.kt", Line: 1, Body: "q", LastCommentID: "c1"},
		{ID: "T2", Kind: review.KindThread, Author: "a", Path: "a.kt", Line: 2, Body: "r", LastCommentID: "c2"},
	}
	if err := st.UpsertThreads("K-2", "https://pr/68", threads, true); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueComments("K-2", []string{"T1", "T2"}); err != nil {
		t.Fatal(err)
	}
	// Nothing resolved yet: nothing dropped.
	if n, err := st.DequeueResolved("K-2"); err != nil || n != 0 {
		t.Fatalf("DequeueResolved before any resolution = %d, %v; want 0", n, err)
	}
	// The next poll sees T1 resolved by the human.
	threads[0].Resolved = true
	if err := st.UpsertThreads("K-2", "https://pr/68", threads, true); err != nil {
		t.Fatal(err)
	}
	n, err := st.DequeueResolved("K-2")
	if err != nil || n != 1 {
		t.Fatalf("DequeueResolved = %d, %v; want 1", n, err)
	}
	q, _ := st.QueuedComments("K-2")
	if len(q) != 1 || q[0].ID != "T2" {
		t.Fatalf("queued after dequeue = %+v, want only T2", q)
	}
}
