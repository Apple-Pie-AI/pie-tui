package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// stubDiscussClaude puts a claude on PATH that speaks real stream-json (a
// session start, one assistant text reply, and a result) - the shape
// discussChange's OnText callback actually parses, unlike stubClaude's plain
// `echo '{}'` (which suffices for the verify-report-reading tests but never
// fires an assistant/result event).
func stubDiscussClaude(t *testing.T, sessionID, reply string) {
	t.Helper()
	dir := t.TempDir()
	stream := fmt.Sprintf(
		`{"type":"system","subtype":"init","session_id":%q,"model":"m"}
{"type":"assistant","message":{"content":[{"type":"text","text":%q}]}}
{"type":"result","subtype":"success","result":%q,"session_id":%q}
`, sessionID, reply, reply, sessionID)
	script := "#!/bin/sh\ncat <<'EOJ'\n" + stream + "EOJ\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A discuss round drains the queued message (ChangeNotes), appends both
// sides of the exchange to the transcript, resumes the session id, and
// returns to the gate untouched - no round bump, no fingerprint change.
func TestDiscussChangeAppendsTranscriptAndReturnsToGate(t *testing.T) {
	fx := newChangeFixture(t)
	stubDiscussClaude(t, "disc-1", "It renders the home screen.")
	if err := fx.st.SetChangeNotes("C-1", "@a.kt what does this do?"); err != nil {
		t.Fatal(err)
	}
	fx.task.Discuss = true

	out := discussChange(t.Context(), fx.task, fx.hook)
	joined := strings.Join(*fx.logs, "\n")
	if out.State != store.StateChangeReview {
		t.Fatalf("state = %q, want change-review:\n%s", out.State, joined)
	}

	sess, err := fx.st.Get("C-1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ChangeNotes != "" {
		t.Errorf("the queued message must be drained, got %q", sess.ChangeNotes)
	}
	if !strings.Contains(sess.ChangeTranscript, "you: @a.kt what does this do?") {
		t.Errorf("transcript missing the human's turn: %q", sess.ChangeTranscript)
	}
	if !strings.Contains(sess.ChangeTranscript, "claude: It renders the home screen.") {
		t.Errorf("transcript missing the agent's reply: %q", sess.ChangeTranscript)
	}
	if sess.SessionID != "disc-1" {
		t.Errorf("SessionID = %q, want disc-1 (so a rework afterward resumes this same conversation)", sess.SessionID)
	}
	if sess.ChangeRound != 1 {
		t.Errorf("round = %d, want unchanged at 1 - a discuss turn is not a rework round", sess.ChangeRound)
	}
	if !strings.Contains(joined, "stage:discuss") {
		t.Errorf("no discuss stage logged:\n%s", joined)
	}
}

// agent.Options.OnText fires once for the assistant's text block and again
// for the turn's final result text - normally the same string for a
// single-block reply (stubDiscussClaude's canned stream mirrors this real
// shape exactly). Both firing must not double-post the reply into the
// committed transcript.
func TestDiscussChangeDoesNotDuplicateTheReplyLine(t *testing.T) {
	fx := newChangeFixture(t)
	stubDiscussClaude(t, "disc-1", "It renders the home screen.")
	if err := fx.st.SetChangeNotes("C-1", "what does this do?"); err != nil {
		t.Fatal(err)
	}
	fx.task.Discuss = true

	discussChange(t.Context(), fx.task, fx.hook)
	sess, err := fx.st.Get("C-1")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(sess.ChangeTranscript, "claude: It renders the home screen."); n != 1 {
		t.Fatalf("reply committed %d time(s), want exactly 1:\n%s", n, sess.ChangeTranscript)
	}
}

// discuss runs on ModelPlan, not ModelImpl - it is a read-only conversation
// (permissions:plan), the same character as the pipeline's own upfront
// planning stage, even when it resumes the implementation session: Claude
// Code's CLI takes --model alongside --resume fine, so this is a genuine
// model switch for one turn, not a silent no-op.
func TestDiscussChangeRunsOnModelPlan(t *testing.T) {
	fx := newChangeFixture(t)
	stubDiscussClaude(t, "disc-1", "It renders the home screen.")
	if err := fx.st.SetChangeNotes("C-1", "what does this do?"); err != nil {
		t.Fatal(err)
	}
	fx.task.Discuss = true
	fx.task.Cfg.ModelPlan = "fable"
	fx.task.Cfg.ModelImpl = "haiku"

	discussChange(t.Context(), fx.task, fx.hook)
	joined := strings.Join(*fx.logs, "\n")
	if !strings.Contains(joined, "stage:discuss model:fable") {
		t.Errorf("discuss must log ModelPlan's value (fable), not ModelImpl's (haiku):\n%s", joined)
	}
}

// A discuss run with nothing queued is a no-op named at the gate - and,
// deliberately, no claude stub is on PATH: a spawn here would fail loudly.
func TestDiscussChangeWithNothingQueuedIsANoOp(t *testing.T) {
	fx := newChangeFixture(t)
	fx.task.Discuss = true
	out := discussChange(t.Context(), fx.task, fx.hook)
	if out.State != store.StateChangeReview {
		t.Fatalf("state = %q, want change-review", out.State)
	}
	sess, _ := fx.st.Get("C-1")
	if !strings.Contains(sess.ChangeError, "nothing to do") {
		t.Errorf("ChangeError = %q, want the no-op named", sess.ChangeError)
	}
}

// discussInbox is what lets a second message typed while the turn is still
// running reach the SAME live process (via agent.Options.Inbox/feedStdin)
// instead of waiting for a fresh --discuss spawn: it drains ChangeNotes,
// clears it so a later poll finds nothing, and records the human's side in
// the transcript - same drain-once shape as the initial message.
func TestDiscussInboxDrainsAndRecordsTheHumanTurn(t *testing.T) {
	fx := newChangeFixture(t)
	inbox := discussInbox(fx.task)

	if got := inbox(); got != nil {
		t.Fatalf("inbox() with nothing queued = %v, want nil", got)
	}

	if err := fx.st.SetChangeNotes("C-1", "one more thing"); err != nil {
		t.Fatal(err)
	}
	got := inbox()
	if len(got) != 1 || got[0] != "one more thing" {
		t.Fatalf("inbox() = %v, want the queued message", got)
	}
	sess, _ := fx.st.Get("C-1")
	if sess.ChangeNotes != "" {
		t.Errorf("ChangeNotes not drained: %q", sess.ChangeNotes)
	}
	if !strings.Contains(sess.ChangeTranscript, "you: one more thing") {
		t.Errorf("transcript missing the forwarded turn: %q", sess.ChangeTranscript)
	}

	// Drained: a second poll with nothing new typed finds nothing.
	if got := inbox(); got != nil {
		t.Fatalf("inbox() after draining = %v, want nil", got)
	}
}

// stubFailingResume puts a claude on PATH that fails immediately - no
// session_id, no assistant text, nothing on stdout - mimicking a `--resume`
// of a session the CLI's local store no longer has (stale, pruned, or from a
// different machine/session store): real stderr, a non-zero exit.
func stubFailingResume(t *testing.T, stderrMsg string) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\necho '%s' >&2\nexit 1\n", stderrMsg)
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A resume that fails (the CLI can't find the session - stale, pruned, or
// created under a different session store) must surface as a visible error,
// never as a silent "success" with no reply: the round produced NOTHING, and
// reporting it as change-review's normal "waiting for your review" outcome
// left the box's "thinking…" indicator hanging with no explanation at all.
func TestDiscussChangeSurfacesAFailedResume(t *testing.T) {
	fx := newChangeFixture(t)
	stubFailingResume(t, "No conversation found with session ID: abc123")
	if err := fx.st.SetSessionID("C-1", "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := fx.st.SetChangeNotes("C-1", "what does this do?"); err != nil {
		t.Fatal(err)
	}
	fx.task.Discuss = true

	out := discussChange(t.Context(), fx.task, fx.hook)
	if out.State != store.StateChangeReview {
		t.Fatalf("state = %q, want change-review", out.State)
	}
	sess, _ := fx.st.Get("C-1")
	if out.Err == nil {
		t.Fatal("a failed resume must be a visible Outcome.Err, not a silent success - cmd/run.go's completion banner reads THIS field")
	}
	if sess.ChangeError == "" {
		t.Fatal("a failed resume must leave a visible ChangeError for the screen to show")
	}
	if !strings.Contains(sess.ChangeTranscript, "you: what does this do?") {
		t.Errorf("the human's turn must still be recorded even though the reply failed: %q", sess.ChangeTranscript)
	}
}

// stubStreamingDiscussClaude puts a claude on PATH that speaks real
// --include-partial-messages output, paced with small sleeps between groups
// of lines so a concurrently polling test can actually observe the
// in-progress state instead of the round finishing before the first poll -
// the exact event shapes (system/thinking_tokens with a cumulative
// estimated_tokens, stream_event/content_block_delta/text_delta) are
// captured live from claude 2.1.x (see the plan): thinking_delta's own
// "thinking" field is always "" over the real CLI, so thinking_tokens is
// the only live reasoning signal that exists to stub here.
func stubStreamingDiscussClaude(t *testing.T, sessionID, reply string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"cat <<EOJ\n" +
		fmt.Sprintf(`{"type":"system","subtype":"init","session_id":%q,"model":"m"}`, sessionID) + "\n" +
		"EOJ\n" +
		"sleep 0.05\n" +
		"cat <<EOJ\n" +
		fmt.Sprintf(`{"type":"system","subtype":"thinking_tokens","estimated_tokens":50,"estimated_tokens_delta":50,"session_id":%q}`, sessionID) + "\n" +
		"EOJ\n" +
		"sleep 0.15\n" +
		"cat <<'EOJ'\n" +
		fmt.Sprintf(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}},"session_id":%q}`, reply[:1], sessionID) + "\n" +
		"EOJ\n" +
		"sleep 0.1\n" +
		"cat <<EOJ\n" +
		fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"text","text":%q}]}}`, reply) + "\n" +
		fmt.Sprintf(`{"type":"result","subtype":"success","result":%q,"session_id":%q}`, reply, sessionID) + "\n" +
		"EOJ\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// End to end: a discuss round run with StreamPartial must actually populate
// ChangeLive while it's in progress (both the thinking-tokens preview and
// the growing answer text - not just the committed transcript at the end),
// and must always leave it cleared back to "" once the round is done,
// whatever the committed transcript's own final content is. This is the
// live preview's real contract; TestDiscussChangeAppendsTranscriptAndReturnsToGate
// already covers the committed transcript in isolation.
func TestDiscussChangeStreamsLiveTextAndThinkingTokens(t *testing.T) {
	fx := newChangeFixture(t)
	stubStreamingDiscussClaude(t, "disc-stream-1", "It renders the home screen.")
	if err := fx.st.SetChangeNotes("C-1", "what does this do?"); err != nil {
		t.Fatal(err)
	}
	fx.task.Discuss = true

	done := make(chan Outcome, 1)
	go func() { done <- discussChange(t.Context(), fx.task, fx.hook) }()

	var sawThinking, sawGrowingText bool
	deadline := time.After(5 * time.Second)
poll:
	for {
		select {
		case <-deadline:
			t.Fatal("discussChange never finished")
		case <-time.After(10 * time.Millisecond):
			sess, err := fx.st.Get("C-1")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(sess.ChangeLive, "thinking:") && strings.Contains(sess.ChangeLive, "tokens so far") {
				sawThinking = true
			}
			// The growing answer text carries no "thinking: " prefix - it's
			// styled/read as a plain in-progress reply (see change_plan.go).
			if sess.ChangeLive != "" && !strings.HasPrefix(sess.ChangeLive, "thinking: ") {
				sawGrowingText = true
			}
		case out := <-done:
			if out.State != store.StateChangeReview {
				t.Fatalf("state = %q, want change-review", out.State)
			}
			break poll
		}
	}

	if !sawThinking {
		t.Error("ChangeLive never showed a live thinking-tokens preview during the round")
	}
	if !sawGrowingText {
		t.Error("ChangeLive never showed the growing answer text during the round")
	}

	sess, err := fx.st.Get("C-1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ChangeLive != "" {
		t.Errorf("ChangeLive must be cleared once the round ends, got %q", sess.ChangeLive)
	}
	if !strings.Contains(sess.ChangeTranscript, "claude: It renders the home screen.") {
		t.Errorf("committed transcript missing the final reply: %q", sess.ChangeTranscript)
	}
	// The streamed fragments must never leak into the COMMITTED transcript as
	// their own lines - only the whole, final "claude: ..." line from OnText.
	if strings.Count(sess.ChangeTranscript, "claude: ") != 1 {
		t.Errorf("committed transcript must have exactly one claude: line, got %q", sess.ChangeTranscript)
	}
}

// The discuss round shares the gate: a run with no reviewable row behind it
// bounces to needs-you, same as ship/rework.
func TestDiscussChangeGateBounces(t *testing.T) {
	fx := newChangeFixture(t)
	fx.task.Discuss = true
	fx.task.PriorState = store.StateQueued
	out := discussChange(t.Context(), fx.task, fx.hook)
	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want needs-you (gate refusal)", out.State)
	}
}
