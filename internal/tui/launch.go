// Handing a ticket to something outside the hub: the plan .md to whatever opens
// markdown, the worktree to an interactive Claude Code session in a new
// terminal.
//
// Everything here ends in a process the hub does not own and cannot watch, so
// each one reports back through its own message type rather than a return
// value. Kept apart from actions.go because that file decides *which* action to
// run; this one knows how to leave the program.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// shellQuote single-quotes a string for safe embedding in a shell command line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// worktreeExists reports whether the ticket still has a usable git worktree.
// "Stop & clean up" removes it, so Resume / Open Studio / Open Claude only make
// sense when this is true. The check itself is shared with the runner, which
// refuses those same operations when it returns false.
func worktreeExists(repo, ticket string) bool {
	return paths.WorktreeReady(paths.WorktreeFor(repo, ticket))
}

// planAvailable reports whether there is a plan to open: the durable archive, or
// a worktree that still has the plan.json it can be generated from.
func planAvailable(s store.Session) bool {
	if _, err := os.Stat(paths.PlanMDFor(s.Ticket)); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(paths.WorktreeFor(s.Repo, s.Ticket), ".agent", "plan.json"))
	return err == nil
}

// planOpenedMsg is returned after handing the plan .md to the OS opener.
type planOpenedMsg struct {
	path string
	err  error
}

// openPlanMD opens the ticket's plan .md externally (macOS `open` / linux
// `xdg-open`). The durable archive is preferred; when only the worktree's
// plan.json exists (runs from before the archive existed) the .md is generated
// into ~/.pie/plans/ first.
func openPlanMD(s store.Session) tea.Cmd {
	return func() tea.Msg {
		path := paths.PlanMDFor(s.Ticket)
		if _, err := os.Stat(path); err != nil {
			plan, rerr := agent.ReadPlan(paths.WorktreeFor(s.Repo, s.Ticket))
			if rerr != nil || plan == nil {
				return planOpenedMsg{err: fmt.Errorf("no plan found for %s", s.Ticket)}
			}
			title := s.Ticket
			if s.Summary != "" {
				title += " · " + s.Summary
			}
			if err := os.MkdirAll(paths.Plans(), 0o755); err != nil {
				return planOpenedMsg{err: err}
			}
			if err := os.WriteFile(path, []byte(agent.PlanMarkdown(title, plan)), 0o644); err != nil {
				return planOpenedMsg{err: err}
			}
		}
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if err := exec.Command(opener, path).Start(); err != nil {
			return planOpenedMsg{path: path, err: err}
		}
		return planOpenedMsg{path: path}
	}
}

// claudeClosedMsg is returned after launching a Claude Code terminal.
type claudeClosedMsg struct{ err error }

// openClaude opens an interactive Claude Code session in the ticket's worktree in
// a new Terminal window. When the session has a stored ID the history is resumed
// so the user can review what the agent did. The dashboard keeps running.
func (m monitorModel) openClaude(s store.Session) tea.Cmd {
	worktree := paths.WorktreeFor(s.Repo, s.Ticket)
	cmdline := "cd " + shellQuote(worktree) + " && claude"
	if s.SessionID != "" {
		// Resumed sessions keep the model they were saved with (per Claude Code
		// docs); "--model default" reverts to the user's/org's default so the
		// interactive window isn't stuck on the headless stage's model. Do NOT add
		// --append-system-prompt here: alongside --resume it drops the restored
		// conversation history, defeating the point of resuming.
		cmdline += " --resume " + shellQuote(s.SessionID) + " --model default"
	}
	// When the ticket is stuck (needs-you/failed/stopped) the user opens this
	// session to fix it by hand. Chain Apple Pie's own gate+PR after the
	// interactive session exits, so finishing the fix automatically re-runs
	// verification and opens the PR - no need to come back and tap "Create PR
	// from my fix". `run --resume` writes to the store, so the dashboard reflects
	// the outcome. (If the user closes the window instead of exiting claude the
	// chained command can't run - an inherent limit of doing this in-terminal.)
	stuck := s.State == "needs-you" || s.State == "failed" || s.State == store.StateStopped || isStopped(s)
	if self, err := os.Executable(); err == nil && stuck {
		cmdline += "; " + shellQuote(self) + " run " + shellQuote(runArg(s)) + " --resume"
	}
	// Window title: "TICKET-1 · ai/ticket-1-branch" (or just the ticket when no branch yet).
	title := s.Ticket
	if s.Branch != "" {
		title += " · " + s.Branch
	}
	// `do script` with no target always opens a NEW Terminal window and runs the
	// command there. Simple and reliable.
	safeCmd := asEscapeAS(cmdline)
	safeTitle := asEscapeAS(title)
	script := "tell application \"Terminal\"\n" +
		"\tactivate\n" +
		"\tset t to do script \"" + safeCmd + "\"\n" +
		"\ttry\n" +
		"\t\tset custom title of t to \"" + safeTitle + "\"\n" +
		"\tend try\n" +
		"end tell"
	return func() tea.Msg {
		// Run (not Start) so an AppleScript failure actually surfaces instead of
		// silently "succeeding". osascript returns as soon as the window is open;
		// it does not wait for the claude session.
		out, err := exec.Command("osascript", "-e", script).CombinedOutput()
		if err != nil {
			return claudeClosedMsg{err: fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))}
		}
		return claudeClosedMsg{}
	}
}

// asEscapeAS escapes a string for embedding inside an AppleScript double-quoted literal.
func asEscapeAS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
