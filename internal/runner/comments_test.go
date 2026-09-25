package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// syncPlan guards the sharpest edge in this path: finishShip pushes with
// --force-with-lease, whose lease is the remote-tracking ref. Getting this
// wrong means the push is rejected *after* the agent has already done the work.
func TestSyncPlan(t *testing.T) {
	tests := []struct {
		name          string
		behind, ahead int
		dirty         bool
		want          syncAction
	}{
		{"nothing moved", 0, 0, false, syncNone},
		{"our own commits, origin unchanged", 0, 3, false, syncNone},
		{"uncommitted work, origin unchanged", 0, 0, true, syncNone},
		{"reviewer pushed, we are clean", 2, 0, false, syncFastForward},
		{"reviewer pushed and we have local commits", 2, 1, false, syncBlocked},
		{"reviewer pushed and we have uncommitted work", 2, 0, true, syncBlocked},
		{"diverged and dirty", 4, 2, true, syncBlocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why := syncPlan(tt.behind, tt.ahead, tt.dirty)
			if got != tt.want {
				t.Fatalf("syncPlan(%d, %d, %v) = %v, want %v", tt.behind, tt.ahead, tt.dirty, got, tt.want)
			}
			// A block must always explain itself: this message is the only thing
			// the user gets to act on.
			if got == syncBlocked && strings.TrimSpace(why) == "" {
				t.Error("a blocked sync must come with a reason")
			}
			if got != syncBlocked && why != "" {
				t.Errorf("non-blocked sync should have no reason, got %q", why)
			}
		})
	}
}

// Rebasing is deliberately absent: a conflict in a headless run has nobody to
// resolve it. Any behind+diverged combination must hand back to the human.
func TestSyncPlanNeverRebases(t *testing.T) {
	for ahead := 1; ahead <= 3; ahead++ {
		if got, _ := syncPlan(1, ahead, false); got != syncBlocked {
			t.Fatalf("behind=1 ahead=%d = %v, want syncBlocked - divergence must go back to the human", ahead, got)
		}
	}
}

func TestToReviewComments(t *testing.T) {
	in := []store.Comment{{
		ID: "PRRT_1", Author: "alice", Path: "a.kt", Line: 4,
		Body: "fix", DiffHunk: "@@", UserNote: "and test it", Outdated: true,
		// Fields the prompt has no business seeing must not leak into it.
		Resolved: true, AddressedSHA: "deadbeef",
	}}
	got := toReviewComments(in)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	want := agent.ReviewComment{
		ID: "PRRT_1", Author: "alice", Path: "a.kt", Line: 4,
		Body: "fix", DiffHunk: "@@", UserNote: "and test it", Outdated: true,
	}
	if got[0] != want {
		t.Errorf("toReviewComments = %+v, want %+v", got[0], want)
	}
}

// The second commit on a branch must not reuse the first one's subject, or the
// PR reads as though the same change was committed twice.
func TestDefaultCommitMsg(t *testing.T) {
	got := defaultCommitMsg(Task{Ticket: "KAN-12", Summary: "Add login"})
	if got != "[KAN-12] Add login" {
		t.Errorf("defaultCommitMsg = %q", got)
	}
}

// The comment-fix spawn must route its model through EffectiveModelCommentFix
// everywhere the model matters: the --model flag handed to claude, the
// stage:comment-fix log line, and the permission mode (which is resolved FOR
// that model - an auto-capable comment-fix model must get "auto" even when the
// impl model would not, and vice versa). A stub claude on PATH records its
// argv, the same seam TestBlockedFixAgentParksAsDenial uses.
func TestCommentFixUsesEffectiveModel(t *testing.T) {
	binDir := t.TempDir()
	argsFile := filepath.Join(binDir, "args.txt")
	stub := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + argsFile + "\n" +
		`echo '{"type":"system","subtype":"init","session_id":"cf1","model":"stub"}'` + "\n" +
		`echo '{"type":"result","subtype":"success","result":"done","session_id":"cf1"}'` + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PIE_HOME", t.TempDir())

	cases := []struct {
		name      string
		cfg       *config.Config
		wantModel string
		wantMode  string
	}{
		// claude-opus-5 is auto-capable, claude-haiku-4-5 is not: if the code
		// resolved the permission mode from ModelImpl instead of the effective
		// comment-fix model, the modes below would come out swapped.
		{"override wins over the impl model",
			&config.Config{ModelImpl: "claude-haiku-4-5", ModelCommentFix: "claude-opus-5"},
			"claude-opus-5", "auto"},
		{"blank falls back to the impl model",
			&config.Config{ModelImpl: "claude-haiku-4-5"},
			"claude-haiku-4-5", "allowlist"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var logs []string
			logf := func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) }
			task := Task{Ticket: "M-1", Cfg: c.cfg}
			_, err := runCommentFix(t.Context(), task, t.TempDir(), "main", nil, "", logf,
				func(string, int) {})
			if err != nil {
				t.Fatalf("runCommentFix: %v\n%s", err, strings.Join(logs, "\n"))
			}
			wantLine := fmt.Sprintf("stage:comment-fix model:%s permissions:%s", c.wantModel, c.wantMode)
			if joined := strings.Join(logs, "\n"); !strings.Contains(joined, wantLine) {
				t.Errorf("log line missing %q:\n%s", wantLine, joined)
			}
			argv, rerr := os.ReadFile(argsFile)
			if rerr != nil {
				t.Fatalf("stub never ran: %v", rerr)
			}
			if !strings.Contains(string(argv), "--model\n"+c.wantModel+"\n") {
				t.Errorf("claude argv missing --model %s:\n%s", c.wantModel, argv)
			}
		})
	}
}
