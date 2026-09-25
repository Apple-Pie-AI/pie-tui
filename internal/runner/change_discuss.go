// Plan mode's half of the change-review gate: the human wants to talk
// through the change, not send it back for a rework yet. This resumes the
// ticket's existing session under --permission-mode plan (read-only - the
// CLI itself refuses every edit tool), appends both sides of the exchange to
// the screen's running transcript as it streams, and returns to
// change-review when the turn ends. No re-verify, no round bump, nothing
// shipped - exactly one round of conversation.
package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func discussChange(ctx context.Context, t Task, h Hooks) Outcome {
	logf, setState := h.logf(), h.setState()

	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	if t.Store != nil {
		// Whatever's mid-stream when this round ends - success, error, or
		// cancelled - is either already committed (OnText's own clear, below)
		// or was never meant to outlive the round (a thinking buffer with no
		// answer yet). Either way nothing should linger as "still typing"
		// once discussChange returns.
		defer func() { _ = t.Store.SetChangeLive(t.Ticket, "") }()
	}
	sess, out, ok := changePreflight(t, h)
	if !ok {
		return out
	}
	worktree := paths.WorktreeFor(t.Repo.Path, t.Ticket)
	if h.OnField != nil {
		h.OnField(branchFor(t), worktree, "")
	}
	ensureProjectFiles(t, worktree, logf)

	// The queued message: ChangeNotes doubles as the Plan-mode inbox (Enter on
	// the box, in Plan mode, persists the sent text there instead of clearing
	// it the way a rework's send does) - drained here exactly once so a later
	// poll of the same field, with nothing new typed, finds it empty.
	message := sess.ChangeNotes
	if message == "" {
		// Not a failure - the TUI already refuses to send an empty box; this
		// is a defensive backstop, so no Outcome.Err.
		return reparkAfterDiscuss(t, h, "discuss requested with no message - nothing to do")
	}
	if t.Store != nil {
		_ = t.Store.SetChangeNotes(t.Ticket, "")
		_ = t.Store.AppendChangeTranscript(t.Ticket, "you: "+message)
	}

	setState(store.StateWorking, 0)
	// ModelPlan, not ModelImpl: this is a read-only conversation (permissions:
	// plan below), the same character as the pipeline's own upfront planning
	// stage (runner.go), and Claude Code's CLI takes --model alongside
	// --resume fine - switching models mid-session for one turn is exactly
	// what it's for, the same as /model in an interactive session. The
	// resumed session still has the full implementation history; only which
	// model answers THIS turn changes.
	model := t.Cfg.ModelPlan
	logf("[%s] stage:discuss model:%s permissions:plan", t.Ticket, model)

	// Live preview of the block currently streaming in, flushed to the store
	// on a throttle rather than per delta - a turn can emit dozens of deltas
	// a second, and every OnTextDelta/OnThinkingDelta call runs synchronously
	// off parseStream's one reader goroutine, so no locking is needed here.
	// Thinking and text never stream at the same time for one turn (thinking
	// always precedes the answer), so one buffer tagged by kind covers both -
	// switching kind mid-stream (thinking finishing, the answer starting)
	// resets it, which is also what makes the thinking preview ephemeral:
	// once real answer text starts flushing, the last "thinking: ..." value
	// is simply overwritten, never having touched ChangeTranscript at all.
	var (
		liveKind    string // "text" | "thinking"
		liveBuf     strings.Builder
		liveFlushed time.Time
		// lastCommittedText dedupes OnText's own double-fire (assistant block,
		// then the identical final result text) - see OnText below.
		lastCommittedText string
	)
	const liveFlushEvery = 200 * time.Millisecond
	flushLive := func(force bool) {
		if t.Store == nil || (!force && time.Since(liveFlushed) < liveFlushEvery) {
			return
		}
		text := liveBuf.String()
		if liveKind == "thinking" {
			text = "thinking: " + text
		}
		_ = t.Store.SetChangeLive(t.Ticket, text)
		liveFlushed = time.Now()
	}
	onDelta := func(kind, chunk string) {
		// Force the flush the instant the block kind changes (thinking
		// finishing, the answer starting) rather than letting the throttle
		// possibly sit on it for up to 200ms - that transition is the one
		// moment users most want to see land immediately, not smoothed.
		force := liveKind != kind
		if force {
			liveKind = kind
			liveBuf.Reset()
		}
		liveBuf.WriteString(chunk)
		flushLive(force)
	}
	// setLive replaces the buffer outright rather than appending - for
	// signals that arrive as a running total, not a stream of chunks (see
	// OnThinkingTokens: the CLI's thinking_delta itself never carries real
	// text - every one observed live has delta.thinking == "" - so the
	// token count is the only genuinely live reasoning signal there is).
	setLive := func(kind, text string) {
		force := liveKind != kind
		liveKind = kind
		liveBuf.Reset()
		liveBuf.WriteString(text)
		flushLive(force)
	}

	opts := agent.Options{
		WorktreeDir:  worktree,
		Model:        model,
		MaxBudgetUSD: t.Cfg.MaxBudgetUSD,
		AnthropicKey: t.AnthropicKey,
		SettingsJSON: t.Cfg.SandboxSettingsJSON(),
		// Plan mode: the CLI refuses every edit tool itself, so no allowlist or
		// approval-prompt config is needed the way rework's normal mode needs.
		PermissionMode: "plan",
		Logf:           logf,
		OnText: func(text string) {
			// OnText fires once per assistant text block AND again for the
			// turn's final result text (agent.Options.OnText's own contract) -
			// for the common single-block reply those are the SAME string, so
			// committing both unconditionally double-posted every reply into
			// the transcript ("claude: X" twice in a row). Only the FIRST of
			// two consecutive identical calls is real content; the second is
			// just the result event echoing what was already committed.
			if t.Store != nil && text != lastCommittedText {
				_ = t.Store.AppendChangeTranscript(t.Ticket, "claude: "+text)
				lastCommittedText = text
			}
			if t.Store != nil {
				_ = t.Store.SetChangeLive(t.Ticket, "")
			}
		},
		// StreamPartial/OnTextDelta/OnThinkingDelta/OnThinkingTokens are purely
		// additive: OnText above still fires exactly as it always has (whole
		// committed blocks), these just feed the live preview - a claude build
		// without partial messages, or a turn with nothing to stream, leaves
		// ChangeLive at "" and the screen falls back to today's static
		// "thinking…" placeholder.
		StreamPartial:   true,
		OnTextDelta:     func(s string) { onDelta("text", s) },
		OnThinkingDelta: func(s string) { onDelta("thinking", s) },
		// The actual live reasoning signal (see setLive's comment): a running
		// token estimate, not text - shown as "thinking: (~N tokens so far)",
		// which the screen renders dim and drops the instant real answer text
		// starts flushing (setLive/onDelta share one buffer, keyed by kind).
		OnThinkingTokens: func(n int) {
			setLive("thinking", fmt.Sprintf("(~%d tokens so far)", n))
		},
		// A second (or third...) message typed while this turn is still
		// running - or in the brief window before stdin closes - is forwarded
		// into this SAME live process instead of waiting for it to exit and
		// spawning a fresh --discuss: one continuous conversation instead of
		// one process per keystroke-timed message.
		Inbox: discussInbox(t),
	}
	const freshPreamble = "Run \"git diff HEAD\" and \"git status\" first to read the local change under review.\n\n"
	prompt := agent.BuildChangeDiscussPrompt(message)
	if sess.SessionID != "" {
		opts.ResumeID = sess.SessionID
	} else {
		prompt = freshPreamble + prompt
	}
	res, err := agent.Run(ctx, prompt, opts)
	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	if err != nil && opts.ResumeID != "" && staleResume(res) {
		// The implementation session --resume pointed at is gone from the
		// CLI's own local history (rotated out, or from a worktree/machine
		// this session store outlives) - claude exits 1 immediately, before
		// any stream event, so nothing above already handled it as "no
		// reply". Retry once as a brand-new session, the same path a ticket
		// with no SessionID at all already takes, rather than failing a
		// Plan-mode turn the human just typed over a session id they never
		// chose and can't fix.
		logf("[%s] discuss: resume session %s not found locally, retrying fresh", t.Ticket, opts.ResumeID)
		opts.ResumeID = ""
		prompt = freshPreamble + agent.BuildChangeDiscussPrompt(message)
		res, err = agent.Run(ctx, prompt, opts)
		if out, done := stopped(ctx, t, logf); done {
			return out
		}
	}
	if res.SessionID != "" && t.Store != nil {
		_ = t.Store.SetSessionID(t.Ticket, res.SessionID)
	}
	// Any of these alone means the round produced nothing worth calling a
	// reply - checked as a plain sequence (not the original's single "err !=
	// nil && no session" test) so a claude that exits 0 having genuinely done
	// nothing - e.g. `--resume` of a session the CLI's local store no longer
	// has, which does not always exit non-zero - is caught too, instead of
	// silently reporting the round as a success with no reply and no error.
	reason := ""
	switch {
	case err != nil:
		reason = fmt.Sprintf("the discussion agent exited with an error: %v", err)
		if tail := strings.TrimSpace(res.Stderr); tail != "" {
			reason += ": " + oneLine(tail, 300)
		}
	case res.ErrorText != "":
		reason = "discuss agent: " + oneLine(res.ErrorText, 300)
	case res.SessionID == "":
		reason = "the discussion agent produced no reply"
		if tail := strings.TrimSpace(res.Stderr); tail != "" {
			reason += ": " + oneLine(tail, 300)
		}
	}
	if reason != "" {
		return reparkAfterDiscussErr(t, h, reason)
	}
	return reparkAfterDiscuss(t, h, "")
}

// staleResume reports whether a failed run's own message says --resume
// couldn't find the session locally - the CLI's exact wording as of
// 2026-09 is "No conversation found with session ID: <uuid>", surfaced on
// agent.Result.ErrorText (agent.Run's own launch-failure framing, since a
// resume that fails this way exits before any stream event) or, as a
// backstop, the raw Stderr tail either was built from.
func staleResume(res agent.Result) bool {
	return strings.Contains(res.ErrorText, "No conversation found") ||
		strings.Contains(res.Stderr, "No conversation found")
}

// discussInbox drains ChangeNotes for agent.Options.Inbox, exactly the way
// the initial message was drained above: read, clear, and record the human's
// side of the turn in the transcript, so the feeder (internal/agent/inbox.go)
// only ever sees plain follow-up text - no re-wrapping in the discuss prompt,
// matching how a live session's later turns already work.
func discussInbox(t Task) func() []string {
	return func() []string {
		if t.Store == nil {
			return nil
		}
		sess, err := t.Store.Get(t.Ticket)
		if err != nil || sess == nil || sess.ChangeNotes == "" {
			return nil
		}
		msg := sess.ChangeNotes
		_ = t.Store.SetChangeNotes(t.Ticket, "")
		_ = t.Store.AppendChangeTranscript(t.Ticket, "you: "+msg)
		return []string{msg}
	}
}

// reparkAfterDiscuss returns to change-review, keeping the round/fingerprint
// exactly as they were - a discuss turn changes nothing about the reviewed
// change itself, only the conversation about it. Used for the success path
// (errMsg "") and the benign "nothing was queued" backstop - neither sets
// Outcome.Err, matching how a real no-op is never reported as a failure.
func reparkAfterDiscuss(t Task, h Hooks, errMsg string) Outcome {
	logf, setState := h.logf(), h.setState()
	if errMsg != "" {
		logf("[%s] discuss: %s", t.Ticket, errMsg)
	}
	if t.Store != nil {
		_ = t.Store.SetChangeError(t.Ticket, errMsg)
	}
	setState(store.StateChangeReview, 0)
	return Outcome{State: store.StateChangeReview}
}

// reparkAfterDiscussErr is reparkAfterDiscuss for an actual failure: it also
// sets Outcome.Err, which is what cmd/run.go's own completion banner reads.
// Without it, a round that produced no reply at all still printed the exact
// same "✓ change-review - waiting for your review" line as a real reply
// would have - the store's ChangeError was right, but `pie logs` and the
// process's own summary line both lied about what happened.
func reparkAfterDiscussErr(t Task, h Hooks, reason string) Outcome {
	out := reparkAfterDiscuss(t, h, reason)
	out.Err = errors.New(reason)
	return out
}
