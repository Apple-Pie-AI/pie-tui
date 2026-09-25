package runner

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// PLEX-56803: the human wrote an instruction for a comment, the agent ignored
// it, and the run log could not say whether the instruction ever reached the
// prompt - it printed only the thread's author and location. The instruction
// is logged under its thread, so "did pie pass it on" is answerable.
func TestAddressCommentsLogsTheOwnerInstruction(t *testing.T) {
	fakeGH(t)
	deadClaude(t, "boom") // the log lines under test are written before the spawn
	t.Setenv("PIE_HOME", t.TempDir())
	readyWorktree(t, "/repo", "K-9")
	wt := paths.WorktreeFor("", "K-9")

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("K-9", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFields("K-9", "b", wt, "https://github.com/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("K-9", "https://github.com/o/r/pull/9", []review.Thread{{
		ID: "T1", Kind: review.KindThread, Author: "greptile-apps", Path: "a.kt", Line: 278, Body: "args discarded", LastCommentID: "c1",
	}}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCommentNote("T1", "Only pass the contact id through; do not touch the nav graphs."); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueComments("K-9", []string{"T1"}); err != nil {
		t.Fatal(err)
	}

	var logs []string
	task := Task{Ticket: "K-9", Repo: &config.Repo{Path: "/repo"}, Cfg: &config.Config{}, Store: st, AddressComments: true}
	addressComments(t.Context(), task, Hooks{Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) }})
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "do not touch the nav graphs") {
		t.Fatalf("the owner's instruction never reached the log:\n%s", joined)
	}
}
