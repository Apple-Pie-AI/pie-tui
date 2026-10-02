package tui

import (
	"context"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// seedDemo seeds a scenario into a fresh PIE_HOME and returns its driver.
func seedDemo(t *testing.T, sc demoScenario) *demoDriver {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(paths.StateDB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := newDemoDriver(context.Background(), sc, st)
	if err := sc.seed(d); err != nil {
		t.Fatal(err)
	}
	return d
}

// assertOpening checks each ticket's state, and that none is filed as a dead
// driver or a removed worktree - both of which the dashboard would say aloud.
func assertOpening(t *testing.T, st *store.Store, want map[string]string) {
	t.Helper()
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
}

// The pipeline demo's opening frame, then its NEEDS YOU beat: the question is
// what the detail pane shows.
func TestDemoPipelineSeed(t *testing.T) {
	d := seedDemo(t, pipelineDemo)
	assertOpening(t, d.st, map[string]string{
		demoCrash.id: store.StateReview, demoDark.id: store.StateWorking,
		demoOffline.id: store.StatePlanning, demoSpanish.id: store.StateQueued,
	})
	askOffline(d)
	s, _ := d.st.Get(demoOffline.id)
	if s.State != store.StateAwaiting {
		t.Fatalf("AND-219 state %q, want %q", s.State, store.StateAwaiting)
	}
	plan, err := agent.ReadPlan(paths.WorktreeFor(demoRepo, demoOffline.id))
	if err != nil || len(plan.Questions) != 1 {
		t.Fatalf("plan.json questions = %v (%v)", plan, err)
	}
}

// The review demo opens with AND-207's PR filed under NEEDS YOU by its open
// threads, each anchored to a line the comments screen can show code for.
func TestDemoReviewSeed(t *testing.T) {
	d := seedDemo(t, reviewDemo)
	assertOpening(t, d.st, map[string]string{
		reviewTicket.id: store.StateReview, demoOffline.id: store.StateReview,
		demoDark.id: store.StateWorking, demoSpanish.id: store.StatePlanning,
	})
	s, _ := d.st.Get(reviewTicket.id)
	if s.OpenComments != len(reviewThreads) {
		t.Fatalf("AND-207 open comments = %d, want %d", s.OpenComments, len(reviewThreads))
	}
	if g := newGroups()[0]; !g.match(*s) {
		t.Errorf("AND-207 is not filed under %s", g.label)
	}
	wt := paths.WorktreeFor(demoRepo, reviewTicket.id)
	for _, r := range reviewThreads {
		if len(codeContext(wt, r.th.Path, r.th.Line, codeRadius)) == 0 {
			t.Errorf("%s: no code at %s:%d", r.th.ID, r.th.Path, r.th.Line)
		}
	}
}
