package store

import (
	"path/filepath"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
)

// Issue #16: the human replies on a thread and resolves it from the hub. The
// row must read as answered AND closed locally, leave any batch it was queued
// in, advance last_comment to our reply so the next poll keeps it closed, and
// - when only the reply went through - stay unresolved.
func TestMarkResolvedByHand(t *testing.T) {
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
	if err := st.SetCommentSkipped("T1", true); err != nil {
		t.Fatal(err)
	}

	if err := st.MarkResolvedByHand("T1", "PRRC_9", true); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.Comments("K-2")
	var t1 Comment
	for _, c := range rows {
		if c.ID == "T1" {
			t1 = c
		}
	}
	if !t1.Replied || !t1.Resolved || t1.Queued || t1.Skipped || t1.LastCommentID != "PRRC_9" {
		t.Fatalf("T1 after resolve-by-hand = replied:%v resolved:%v queued:%v skipped:%v last:%q",
			t1.Replied, t1.Resolved, t1.Queued, t1.Skipped, t1.LastCommentID)
	}
	if open, _ := st.OpenComments("K-2"); len(open) != 1 || open[0].ID != "T2" {
		t.Fatalf("open = %+v, want only T2", open)
	}
	// The next poll reports our own reply as the last comment: stays closed.
	threads[0].LastCommentID, threads[0].Resolved = "PRRC_9", true
	if err := st.UpsertThreads("K-2", "https://pr/68", threads, true); err != nil {
		t.Fatal(err)
	}
	if open, _ := st.OpenComments("K-2"); len(open) != 1 {
		t.Fatalf("our own reply reopened the thread: %d open", len(open))
	}

	// Reply went through, resolve did not: answered, still open, no pivot lie.
	if err := st.MarkResolvedByHand("T2", "", false); err != nil {
		t.Fatal(err)
	}
	rows, _ = st.Comments("K-2")
	for _, c := range rows {
		if c.ID == "T2" && (!c.Replied || c.Resolved || c.Queued || c.LastCommentID != "c2") {
			t.Fatalf("T2 after reply-only = %+v", c)
		}
	}
}
