package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// A comment fetch that found the PR resolved announces the move and reloads -
// the row leaves PR READY FOR REVIEW for CLOSED without a daemon in sight.
// (The state write itself happens in fetchThreadsCmd via SetStateIf, which
// store's tests pin; this guards the TUI half.)
func TestResolvedFetchAnnouncesTheMove(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Claim("K-2312", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("K-2312", store.StateClosed, 0); err != nil {
		t.Fatal(err) // what fetchThreadsCmd's CAS has already done by msg time
	}

	m := baseModel()
	m.store = st
	got, _ := m.Update(commentsFetchedMsg{ticket: "K-2312", resolved: store.StateClosed})
	hub := got.(monitorModel)

	if !strings.Contains(hub.notice, "moved to CLOSED") {
		t.Fatalf("notice = %q, want the moved-to-CLOSED announcement", hub.notice)
	}
	// The reload picked the row up into the CLOSED group.
	for _, g := range hub.groups {
		if g.label == "CLOSED" && len(g.sessions) == 1 {
			return
		}
	}
	t.Fatal("the closed row should have reloaded into the CLOSED group")
}
