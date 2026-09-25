package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// A ticket parked at fix-review still has an open PR, and the human may
// resolve threads on GitHub while deciding. Ticket 2 sat blocked because
// only `review` was polled: neither the background tick nor "Check for PR
// updates" ever asked GitHub again.
func TestFixReviewIsPollable(t *testing.T) {
	s := store.Session{State: store.StateFixReview, PRURL: "https://github.com/o/r/pull/68", Repo: "/repo"}
	if !pollable(s) {
		t.Fatal("a fix-review session with an open PR must be pollable")
	}
}

// When a fetch drops resolved threads from the batch, the hub says so; when
// that empties the batch it also says the ticket went back to review - a row
// silently leaving the approve screen is a mystery.
func TestFetchAnnouncesDequeuedThreads(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Claim("K-2", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	m := baseModel()
	m.store = st

	got, _ := m.Update(commentsFetchedMsg{ticket: "K-2", n: 3, dequeued: 1})
	if notice := got.(monitorModel).notice; !strings.Contains(notice, "resolved on GitHub") {
		t.Fatalf("notice = %q, want it to name the thread resolved on GitHub", notice)
	}
	got, _ = m.Update(commentsFetchedMsg{ticket: "K-2", n: 3, dequeued: 1, unparked: true})
	if notice := got.(monitorModel).notice; !strings.Contains(notice, "nothing left to approve") {
		t.Fatalf("notice = %q, want it to say the batch emptied and the ticket unparked", notice)
	}
}

// A fix-review row must offer "Check for PR updates" too: that is how the
// human asks pie to notice a thread they resolved on GitHub while deciding,
// instead of waiting for the background poll.
func TestFixReviewRowOffersCheckForPRUpdates(t *testing.T) {
	s := store.Session{Ticket: "K-2", State: store.StateFixReview, PRURL: "https://github.com/o/r/pull/68", Repo: "/repo"}
	for _, it := range agentActions(s) {
		if it.key == actFetchComments {
			return
		}
	}
	t.Fatal("fix-review row does not offer Check for PR updates")
}
