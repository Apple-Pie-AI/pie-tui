package runner

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// Run's entire observable contract is the ORDERED sequence of Hooks callbacks it
// fires - the store row, the dashboard and the Jira thread are all downstream of
// that order, and nothing else in the suite asserts it. These tests record the
// full transcript for the paths that terminate before `claude` is ever spawned,
// so the pipeline's guard clauses cannot be reordered or dropped silently.

// transcript records every hook callback in the order Run fires them.
type transcript struct {
	states   []string
	comments int
	fields   int
}

func (tr *transcript) hooks() Hooks {
	return Hooks{
		Logf:    func(string, ...interface{}) {},
		OnState: func(state string, retries int) { tr.states = append(tr.states, state) },
		OnField: func(_, _, _ string) { tr.fields++ },
		Comment: func(string) { tr.comments++ },
	}
}

func TestRunStateTranscripts(t *testing.T) {
	tests := []struct {
		name       string
		task       func(t *testing.T) Task
		wantStates []string
		wantErr    bool
		// wantComment: the human must be told why the pipeline stopped.
		wantComment bool
	}{
		{
			// --ship with no worktree: fails before any state is recorded, because
			// there is nothing to ship. No comment - this is a caller error, not a
			// ticket outcome.
			name: "ship without a worktree fails before any transition",
			task: func(t *testing.T) Task {
				return Task{
					Ticket: "T-1", Summary: "x", Ship: true,
					Repo: &config.Repo{Branch: "ai/{ticket}"}, Cfg: &config.Config{},
				}
			},
			wantStates: []string{store.StateFailed},
			wantErr:    true,
		},
		{
			// --resume with no worktree: same shape. The guard must fire before the
			// verify stage, or the agent would run against a directory that is not there.
			name: "resume without a worktree fails before any transition",
			task: func(t *testing.T) Task {
				return Task{
					Ticket: "T-2", Summary: "x", Resume: true,
					Repo: &config.Repo{Branch: "ai/{ticket}"}, Cfg: &config.Config{},
				}
			},
			wantStates: []string{store.StateFailed},
			wantErr:    true,
		},
		{
			// --from-plan with no reviewed worktree: planning is entered (the pipeline
			// commits to the ticket) and then the guard stops it at needs-you. The
			// planning->needs-you pair is the assertion: a bare needs-you would mean
			// the guard moved above the state write and the dashboard would never
			// show the ticket as started.
			name: "from-plan without the reviewed worktree stops at needs-you",
			task: func(t *testing.T) Task {
				return Task{
					Ticket: "T-3", Summary: "x", FromPlan: true,
					Repo: &config.Repo{
						Path:   filepath.Join(t.TempDir(), "no-such-repo"),
						Branch: "ai/{ticket}",
					},
					Cfg: &config.Config{},
				}
			},
			wantStates:  []string{store.StatePlanning, store.StateNeedsYou},
			wantComment: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PIE_HOME", t.TempDir())
			var tr transcript
			out := Run(context.Background(), tc.task(t), tr.hooks())

			if got := strings.Join(tr.states, " → "); got != strings.Join(tc.wantStates, " → ") {
				t.Errorf("state transcript = %q, want %q", got, strings.Join(tc.wantStates, " → "))
			}
			if last := tc.wantStates[len(tc.wantStates)-1]; out.State != last {
				t.Errorf("Outcome.State = %q, want %q (it must agree with the last transition)", out.State, last)
			}
			if (out.Err != nil) != tc.wantErr {
				t.Errorf("Outcome.Err = %v, want error: %v", out.Err, tc.wantErr)
			}
			if (tr.comments > 0) != tc.wantComment {
				t.Errorf("comments posted = %d, want any: %v", tr.comments, tc.wantComment)
			}
		})
	}
}

// TestRunToleratesZeroHooks pins the documented contract that "any field may be
// nil" - the daemon and several tests pass a bare Hooks{}. A missing nil guard
// panics the whole run, not just the callback.
func TestRunToleratesZeroHooks(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Run panicked on a zero Hooks{}: %v", r)
		}
	}()
	for _, task := range []Task{
		{Ticket: "Z-1", Summary: "x", Ship: true},
		{Ticket: "Z-2", Summary: "x", Resume: true},
		{Ticket: "Z-3", Summary: "x", FromPlan: true},
	} {
		task.Repo = &config.Repo{Path: filepath.Join(t.TempDir(), "nope"), Branch: "ai/{ticket}"}
		task.Cfg = &config.Config{}
		if out := Run(context.Background(), task, Hooks{}); out.State == "" {
			t.Errorf("%s: Run returned an empty state", task.Ticket)
		}
	}
}
