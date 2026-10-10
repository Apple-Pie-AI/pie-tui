// Package agent drives a headless Claude Code session (Decision 3) and reads the
// machine-readable completion contract it writes (Decision 6).
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// PlanTools is the read-only allowlist for the plan stage.
// Write is included only to create .agent/plan.json; source files must not be
// edited. Task (subagent exploration) and TodoWrite (Claude Code's own plan
// tracking) are allowed so the model can plan with its native tooling - both
// are read-only with respect to the source tree.
const PlanTools = "Write Read Glob Grep Task TodoWrite " +
	"Bash(ls:*) Bash(cat:*) Bash(head:*) Bash(tail:*) " +
	"Bash(grep:*) Bash(rg:*) Bash(find:*) Bash(git status:*) " +
	"Bash(git diff:*) Bash(git log:*) Bash(git show:*)"

// ReviewTools is the minimal allowlist for the self-review stage.
const ReviewTools = "Write Read Glob Grep " +
	"Bash(git diff:*) Bash(git show:*) Bash(git log:*) Bash(ls:*)"

// Options configure a single claude invocation.
type Options struct {
	WorktreeDir  string
	AllowedTools string
	MaxBudgetUSD float64
	AnthropicKey string
	Model        string // e.g. "claude-opus-4-8", "claude-sonnet-4-6"; empty = claude default
	ResumeID     string // set to resume the same session for retries (Decision 4)
	SettingsJSON string // inline --settings payload (e.g. sandbox posture); empty = omit
	// PermissionMode selects how the child claude gates tool use:
	// "" / "allowlist" - acceptEdits + the --allowed-tools prefix rules;
	// "auto"           - the model-graded classifier judges each action in
	//                    context (no static allowlist to maintain, and no
	//                    prefix-rule whack-a-mole on monorepo build commands);
	// "bypass"         - no checks at all. Explicit opt-in only.
	PermissionMode string
	// PermissionPromptConfig is the path to an --mcp-config file exposing
	// pie's approval server (WriteApprovalMCPConfig). When set, ask rules and
	// allowlist gaps route to `pie mcp-approve` instead of hard-denying -
	// callers set it for allowlist-mode stages only.
	PermissionPromptConfig string
	// AndroidSDKRoot, when set, puts that SDK's platform-tools and emulator
	// dirs on the subprocess PATH (and sets ANDROID_HOME) so `adb` works in
	// the agent's shell - see withAndroidSDK. Callers pass the same root the
	// emulator manager resolved (emulator.RootFor).
	AndroidSDKRoot string
	Logf           func(format string, args ...interface{})
	// Inbox, when set, opens a live channel into the session: the prompt is
	// sent as the first stdin message (--input-format stream-json) instead of
	// the -p argument, and the function is polled for follow-up messages
	// queued while the session is live, each forwarded as a new user turn. A
	// turn that ends with an empty inbox closes stdin, which ends the session
	// - so a run with an Inbox behaves exactly like one without until
	// somebody actually queues a message. Verified on CLI 2.1.247: under
	// --input-format stream-json the -p prompt argument is ignored, so the
	// prompt MUST travel over stdin instead (see buildArgs/feedStdin).
	Inbox func() []string
	// OnText, when set, receives the agent's own prose as it streams: each
	// assistant text block and the turn's final result text. Deliberately NOT
	// a full structured event feed (tool calls, durations, denials stay
	// Logf-only) - just enough for a caller building a plain conversation
	// transcript.
	OnText func(string)
	// StreamPartial requests --include-partial-messages: claude then
	// interleaves raw Anthropic Messages-API stream events (content block
	// start/delta/stop) between its usual whole-message "assistant"/"result"
	// lines - the only way to see a reply grow in before it's complete.
	// OnText's contract is unchanged either way (whole blocks, at the end);
	// this only feeds OnTextDelta/OnThinkingDelta. Only change_discuss.go
	// sets this today - every other stage leaves it false.
	StreamPartial bool
	// OnTextDelta, when StreamPartial is set, receives each streamed chunk
	// of the CURRENT in-progress text block - a live preview, not part of
	// the committed transcript OnText builds. Never fires without
	// StreamPartial.
	OnTextDelta func(string)
	// OnThinkingDelta is OnTextDelta's counterpart for extended-thinking
	// content. As of claude 2.1.x this is a documented event shape claude
	// emits but never fills in over the CLI: every thinking_delta observed
	// live carried delta.thinking == "" (the CLI reports thinking is
	// happening without exposing what it says) - kept and wired for when/if
	// that changes, but callers should treat "never fires with real text"
	// as the current normal outcome, not a bug. OnThinkingTokens below is
	// the signal that actually carries live data today.
	OnThinkingDelta func(string)
	// OnThinkingTokens, when StreamPartial is set, receives the running
	// estimated thinking-token count as it grows (from claude's own
	// "system"/"thinking_tokens" events) - the only genuinely live signal
	// the CLI exposes for an in-progress reasoning turn. Each call carries
	// the cumulative total, not a delta - callers replace, not append.
	OnThinkingTokens func(int)
}

// buildArgs assembles the `claude` argv for the given prompt and options. Split
// out from Run so the flag wiring (notably --settings) is unit-testable without
// spawning the CLI.
func buildArgs(prompt string, o Options) []string {
	args := []string{"-p"}
	if o.Inbox != nil {
		// Live session: the prompt goes over stdin (see Options.Inbox).
		args = append(args, "--input-format", "stream-json")
	} else {
		args = append(args, prompt)
	}
	args = append(args, "--output-format", "stream-json", "--verbose")
	if o.StreamPartial {
		args = append(args, "--include-partial-messages")
	}
	switch o.PermissionMode {
	case "auto":
		// The classifier replaces the allowlist entirely: passing
		// --allowed-tools alongside it would reintroduce the prefix rules the
		// mode exists to retire.
		args = append(args, "--permission-mode", "auto")
	case "bypass":
		args = append(args, "--permission-mode", "bypassPermissions")
	case "plan":
		// Read-only discussion: Claude can explore and reply but every edit
		// tool is refused by the CLI itself - no allowlist needed to enforce it.
		args = append(args, "--permission-mode", "plan")
	default:
		args = append(args, "--permission-mode", "acceptEdits",
			"--allowed-tools", o.AllowedTools)
	}
	if o.PermissionPromptConfig != "" {
		args = append(args, "--permission-prompt-tool", "mcp__pie__approve",
			"--mcp-config", o.PermissionPromptConfig)
	}
	if o.SettingsJSON != "" {
		args = append(args, "--settings", o.SettingsJSON)
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%g", o.MaxBudgetUSD))
	}
	if o.ResumeID != "" {
		args = append(args, "--resume", o.ResumeID)
	}
	return args
}

// Run invokes `claude -p` with stream-json, streaming progress through Logf.
// It returns a Result - the session id (for resuming on retry), any permission
// denials, and any API-level error text - plus the process error.
//
// Cancelling ctx stops the agent. That matters because the thing being cancelled
// is a process that is actively editing a worktree the canceller is about to
// delete: without it, "Stop & clean up" removed the directory out from under a
// claude session that kept right on writing to it.
func Run(ctx context.Context, prompt string, o Options) (Result, error) {
	// parseStream calls logf unconditionally, from a goroutine started *after* the
	// subprocess is spawned - so a nil Logf would abort the process rather than
	// return an error, leaving an orphaned claude child. Every caller happens to
	// set it today; default it so the zero value is not a trap.
	if o.Logf == nil {
		o.Logf = func(string, ...interface{}) {}
	}
	cmd := exec.CommandContext(ctx, "claude", buildArgs(prompt, o)...)
	// Always the worktree root. claude reads its cwd as the project - which
	// settings, hooks and MCP config it loads - so relocating one stage into
	// a subdirectory (tried for the verify stage, 2026-08-26) made it start in
	// a different world from every other stage and killed it at launch on
	// monorepo tickets (PLEX-57773). Monorepo build commands are handled by
	// the allowlist and the prompt instead, never by moving the session.
	cmd.Dir = o.WorktreeDir
	cmd.Env = withAndroidSDK(agentEnv(o.Logf), o.AndroidSDKRoot, o.Logf)
	if o.AnthropicKey != "" {
		cmd.Env = append(cmd.Env, "ANTHROPIC_API_KEY="+o.AnthropicKey)
	}
	// Give claude its own process group, and cancel by signalling the group rather
	// than the single pid. claude spawns real work of its own (test runners, build
	// tools); CommandContext's default cancel kills only the direct child and
	// leaves that subtree running against the worktree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative pid addresses the whole group. Fall back to the single
		// process if the group is already gone (ESRCH) or was never created.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	// Cancel only asks. Without a WaitDelay, a claude that declines to exit leaves
	// cmd.Wait() blocked forever - and its own process group now shields it from
	// the SIGKILL the TUI would otherwise land on the whole run. WaitDelay is what
	// escalates: the process is killed outright once it elapses.
	cmd.WaitDelay = 10 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	// The live channel (Options.Inbox): claude reads user turns from stdin
	// until it closes. Without an Inbox stdin stays /dev/null as before.
	var stdin io.WriteCloser
	turnDone := make(chan struct{}, 1)
	if o.Inbox != nil {
		if stdin, err = cmd.StdinPipe(); err != nil {
			return Result{}, err
		}
	}
	// claude's stderr used to go straight to pie's own stderr - invisible under
	// the TUI and the daemon. A CLI that dies at launch (bad flag, MCP server
	// it could not start, settings it refused) says why ONLY there, and with
	// no stream events to read, the stage looked like "no report" and was
	// reported as a red build. Keep the tail alongside the stream.
	var stderrTail tailBuffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrTail)
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	// Read the stream in a goroutine and wait on the process separately. The
	// agent can launch background processes during verify (e.g. `./gradlew … &`,
	// and Gradle in turn forks a long-lived daemon) that inherit claude's stdout
	// fd. When that happens the pipe never reaches EOF even after claude exits,
	// so reading to EOF *before* Wait() would hang forever - which left tickets
	// stuck in the build/running state after the agent had already finished.
	// cmd.Wait() returns when the claude process itself exits (it only reaps the
	// direct child, not the reparented daemon) and closes the read pipe, which
	// unblocks parseStream.
	resCh := make(chan Result, 1)
	go func() {
		resCh <- parseStream(stdout, o.Logf, o.OnText, o.OnTextDelta, o.OnThinkingDelta, o.OnThinkingTokens, func() {
			select {
			case turnDone <- struct{}{}:
			default:
			}
		})
	}()
	if stdin != nil {
		go feedStdin(ctx, stdin, prompt, o, turnDone)
	}
	waitErr := cmd.Wait()
	res := <-resCh
	res.Stderr = stderrTail.String()
	if waitErr != nil && res.SessionID == "" && res.ErrorText == "" {
		// Nothing streamed: the process never reached its init event, so the
		// real reason is whatever it printed on stderr - name it, so the
		// stage reports a launch failure instead of guessing about the build.
		res.ErrorText = LaunchFailurePrefix + " (" + waitErr.Error() + ")"
		if tail := strings.TrimSpace(res.Stderr); tail != "" {
			res.ErrorText += ": " + tail
		}
		o.Logf("  ✗ claude did not start: %s", oneLine(res.ErrorText, 300))
	} else if waitErr != nil {
		if tail := strings.TrimSpace(res.Stderr); tail != "" {
			o.Logf("  (warn) claude stderr: %s", oneLine(tail, 300))
		}
	}
	return res, waitErr
}

// LaunchFailurePrefix opens Result.ErrorText when claude exited without ever
// starting a session - callers use it to tell "the CLI never ran" from an
// API-level error the session itself reported.
const LaunchFailurePrefix = "claude exited before starting a session"

// NoReportPrefix opens the verify error text when the session ran to its end
// but left no fresh report - distinct from an API error, which carries the
// API's own words instead.
const NoReportPrefix = "verify produced no report"

// tailBuffer keeps the last tailBufferMax bytes written to it.
type tailBuffer struct {
	b []byte
}

const tailBufferMax = 4096

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > tailBufferMax {
		t.b = append([]byte(nil), t.b[len(t.b)-tailBufferMax:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }

// parseStream reads claude's stream-json to EOF. onText (nil-safe) fires with
// the agent's own prose as it streams; onResult (nil-safe) fires at each
// turn's result event so the stdin feeder can decide whether the session has
// more to do. onTextDelta/onThinkingDelta/onThinkingTokens (nil-safe) fire
// only when the caller requested Options.StreamPartial - see stream_event
// and the "system"/"thinking_tokens" case below.
func parseStream(r io.Reader, logf func(string, ...interface{}), onText, onTextDelta, onThinkingDelta func(string), onThinkingTokens func(int), onResult func()) Result {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	var res Result
	// Raw tool_result error text by tool_use_id: the result event's
	// permission_denials carry the same ids, and the joined text is the only
	// signal that tells an ask-rule refusal apart from an allowlist gap.
	errByID := map[string]string{}
	// Under stream-json input every turn re-emits system/init; the session
	// starts once, so the note (and its log line) is printed once.
	inited := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if id, ok := ev["session_id"].(string); ok && id != "" {
			res.SessionID = id
		}
		switch ev["type"] {
		case "system":
			switch ev["subtype"] {
			case "init":
				if !inited {
					inited = true
					model, _ := ev["model"].(string)
					if model != "" {
						logf("  • session %s started · model:%s", short(res.SessionID), model)
					} else {
						logf("  • session %s started", short(res.SessionID))
					}
				}
			case "thinking_tokens":
				// The live progress signal thinking_delta promises but never
				// delivers (see Options.OnThinkingTokens) - a cumulative
				// estimate, not a delta, sent repeatedly as a turn reasons.
				if n, ok := ev["estimated_tokens"].(float64); ok && onThinkingTokens != nil {
					onThinkingTokens(int(n))
				}
			}
		case "assistant":
			logAssistant(ev, logf, onText)
		case "stream_event":
			// Only present with --include-partial-messages (StreamPartial):
			// raw Anthropic Messages-API events wrapping content_block_delta,
			// interleaved between the whole-message "assistant"/"result" lines
			// above, which still arrive exactly as before and remain the
			// source of truth for the committed transcript - these deltas are
			// purely a live preview of the block currently in progress.
			logStreamEvent(ev, onTextDelta, onThinkingDelta)
		case "user":
			// tool_result errors used to be dropped entirely, which is how a
			// permission refusal stayed invisible in the ticket log.
			logUserToolErrors(ev, logf, errByID)
		case "result":
			if s, ok := ev["result"].(string); ok && strings.TrimSpace(s) != "" {
				logf("  • %s", oneLine(s, 200))
				if onText != nil {
					onText(strings.TrimSpace(s))
				}
				// An API-level failure (429 budget, 401 auth, 529 overload…)
				// surfaces here; keep it so the stage's needs-you can name the
				// real cause instead of guessing about the ticket.
				if isErr, _ := ev["is_error"].(bool); isErr ||
					strings.HasPrefix(strings.TrimSpace(s), "API Error") {
					res.ErrorText = strings.TrimSpace(s)
				}
			}
			noteResultMeta(ev, &res)
			if arr, ok := ev["permission_denials"].([]interface{}); ok {
				for _, it := range arr {
					if m, ok := it.(map[string]interface{}); ok {
						if d := denialFrom(m, errByID); d.Tool != "" {
							res.Denials = append(res.Denials, d)
						}
					}
				}
			}
			if onResult != nil {
				onResult()
			}
		}
	}
	res.Denials = MergeDenials(res.Denials)
	return res
}

// denialFrom converts one permission_denials entry into a Denial. The Bash
// command is kept VERBATIM - remediation shows it to the human and derives
// allowlist rules from it, so truncation would corrupt both. The raw error
// text is joined by tool_use_id from the stream's tool_result blocks; it is
// what Flavor() classifies.
func denialFrom(m map[string]interface{}, errByID map[string]string) Denial {
	name, _ := m["tool_name"].(string)
	input, _ := m["tool_input"].(map[string]interface{})
	cmd := ""
	if input != nil {
		if c, ok := input["command"].(string); ok && c != "" {
			cmd = c
		} else {
			cmd = toolDetail(name, input)
		}
	}
	id, _ := m["tool_use_id"].(string)
	return Denial{Tool: name, Command: cmd, ErrorText: errByID[id]}
}

// logUserToolErrors surfaces tool_result blocks with is_error into the ticket
// log as they happen, and records each error's text by tool_use_id so the
// result event's permission_denials can be classified. For claude CLIs that
// predate permission_denials the log line is the only trace a refusal leaves.
func logUserToolErrors(ev map[string]interface{}, logf func(string, ...interface{}), errByID map[string]string) {
	msg, _ := ev["message"].(map[string]interface{})
	if msg == nil {
		return
	}
	content, _ := msg["content"].([]interface{})
	for _, c := range content {
		blk, _ := c.(map[string]interface{})
		if blk == nil || blk["type"] != "tool_result" {
			continue
		}
		if isErr, _ := blk["is_error"].(bool); !isErr {
			continue
		}
		text := toolResultText(blk["content"])
		if text == "" {
			continue
		}
		logf("  ✗ tool error: %s", oneLine(text, 200))
		if id, _ := blk["tool_use_id"].(string); id != "" && errByID != nil {
			// Cap prefix-preserving: the classification fingerprints sit at
			// the front, and the store shouldn't carry page-length build logs.
			errByID[id] = oneLine(text, 500)
		}
	}
}

// toolResultText flattens a tool_result content field, which the stream may
// encode as a bare string or as a list of typed text blocks.
func toolResultText(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []interface{}:
		var b strings.Builder
		for _, it := range t {
			if m, ok := it.(map[string]interface{}); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	}
	return ""
}

func logAssistant(ev map[string]interface{}, logf func(string, ...interface{}), onText func(string)) {
	msg, _ := ev["message"].(map[string]interface{})
	if msg == nil {
		return
	}
	content, _ := msg["content"].([]interface{})
	for _, c := range content {
		blk, _ := c.(map[string]interface{})
		switch blk["type"] {
		case "text":
			if t, _ := blk["text"].(string); strings.TrimSpace(t) != "" {
				t = strings.TrimSpace(t)
				logf("  %s", t)
				if onText != nil {
					onText(t)
				}
			}
		case "tool_use":
			if name, _ := blk["name"].(string); name != "" {
				input, _ := blk["input"].(map[string]interface{})
				detail := toolDetail(name, input)
				if detail != "" {
					logf("  ⚙ %s(%s)", name, detail)
				} else {
					logf("  ⚙ %s", name)
				}
			}
		}
	}
}

// logStreamEvent handles one --include-partial-messages "stream_event" line:
// only content_block_delta carries anything worth surfacing (text_delta /
// thinking_delta text chunks) - content_block_start/stop and message_*
// events exist in the wrapped stream but this build has no use for them yet,
// so they're silently skipped, same treatment logAssistant gives tool_use
// blocks it doesn't otherwise act on.
func logStreamEvent(ev map[string]interface{}, onTextDelta, onThinkingDelta func(string)) {
	event, _ := ev["event"].(map[string]interface{})
	if event == nil || event["type"] != "content_block_delta" {
		return
	}
	delta, _ := event["delta"].(map[string]interface{})
	if delta == nil {
		return
	}
	switch delta["type"] {
	case "text_delta":
		if t, _ := delta["text"].(string); t != "" && onTextDelta != nil {
			onTextDelta(t)
		}
	case "thinking_delta":
		if t, _ := delta["thinking"].(string); t != "" && onThinkingDelta != nil {
			onThinkingDelta(t)
		}
	}
}

// Oneshot runs `claude -p <prompt>` with a 30 s timeout and returns the first
// non-blank output line. It uses the haiku model for speed and low cost.
// Returns "" on any error — callers keep their programmatic fallback. The 30 s
// budget is capped by ctx, so cancelling the run also abandons this side call.
func Oneshot(ctx context.Context, prompt, anthropicKey string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude",
		"-p", prompt,
		"--model", "claude-haiku-4-5-20251001",
		"--output-format", "text",
	)
	cmd.Env = os.Environ()
	if anthropicKey != "" {
		cmd.Env = append(cmd.Env, "ANTHROPIC_API_KEY="+anthropicKey)
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// toolDetail extracts the most useful display string from a tool's input map.
func toolDetail(name string, input map[string]interface{}) string {
	if input == nil {
		return ""
	}
	switch name {
	case "Read":
		if p, _ := input["file_path"].(string); p != "" {
			return p
		}
	case "Write":
		if p, _ := input["file_path"].(string); p != "" {
			return p
		}
	case "Edit", "MultiEdit":
		if p, _ := input["file_path"].(string); p != "" {
			return p
		}
	case "Bash":
		if c, _ := input["command"].(string); c != "" {
			return oneLine(c, 80)
		}
	case "Glob":
		if p, _ := input["pattern"].(string); p != "" {
			return p
		}
	case "Grep":
		if p, _ := input["pattern"].(string); p != "" {
			return p
		}
	case "TodoWrite":
		return ""
	}
	// Fallback: first string value found.
	for _, v := range input {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return oneLine(s, 60)
		}
	}
	return ""
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func oneLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
