package tui

import (
	"context"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// The demo's opening frame: one PR up, three agents live, and every fixture
// worktree "ready" - otherwise the detail pane says the worktree was removed.
func TestDemoSeedOpeningFrame(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(paths.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := &demoDriver{st: st, ctx: context.Background(), stage: map[string]string{},
		cursor: map[string]int{}, tickets: map[string]demoTicket{}}
	if err := d.seed(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		demoCrash.id: store.StateReview, demoDark.id: store.StateWorking,
		demoOffline.id: store.StatePlanning, demoSpanish.id: store.StateQueued,
	}
	for id, state := range want {
		s, err := st.Get(id)
		if err != nil || s == nil {
			t.Fatalf("%s: not seeded (%v)", id, err)
		}
		if s.State != state {
			t.Errorf("%s: state %q, want %q", id, s.State, state)
		}
		if isStopped(*s) {
			t.Errorf("%s: filed as stopped - the hub would show a dead driver", id)
		}
		if !worktreeExists(s.Repo, id) {
			t.Errorf("%s: worktree not ready", id)
		}
	}

	// The NEEDS YOU beat: the question is what the detail pane shows.
	d.askOffline()
	s, _ := st.Get(demoOffline.id)
	if s.State != store.StateAwaiting {
		t.Fatalf("AND-219 state %q, want %q", s.State, store.StateAwaiting)
	}
	plan, err := agent.ReadPlan(paths.WorktreeFor(demoRepo, demoOffline.id))
	if err != nil || len(plan.Questions) != 1 {
		t.Fatalf("plan.json questions = %v (%v)", plan, err)
	}
}
