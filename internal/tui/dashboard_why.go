// Why an agent is blocked, in the words the detail pane shows.
//
// Split out of dashboard.go, which renders these lines but does not decide
// them - and together the two overran the file-size guideline.
//
// Nothing here names a key. The copy used to say "press o to inspect the
// worktree" and "press R to gate & open the PR", teaching shortcuts this UI has
// never had; every one of those is a row in the actions menu, so the copy points
// at the menu instead.
package tui

import (
	"fmt"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
)

// whyLines explains, for a needs-you/stopped/failed agent, what it's blocked on.
func whyLines(s store.Session) []string {
	if isStopped(s) {
		reason := fmt.Sprintf("no activity for %s", Age(freshest(s)))
		if s.PID > 0 {
			reason = fmt.Sprintf("process %d is gone", s.PID)
		}
		return []string{
			fmt.Sprintf("Stopped - %s.", reason),
			"Use the actions menu to run it again or inspect the worktree.",
		}
	}
	switch s.State {
	case "awaiting-answer":
		if plan, err := agent.ReadPlan(paths.WorktreeFor(s.Repo, s.Ticket)); err == nil && len(plan.Questions) > 0 {
			out := []string{fmt.Sprintf("Needs you - enter opens the menu to answer %d question(s):", len(plan.Questions))}
			// Cap count and length so many/verbose questions can't flood the pane.
			const maxQuestions = 4
			for i, q := range plan.Questions {
				if i == maxQuestions {
					out = append(out, fmt.Sprintf("… +%d more - the menu shows them all", len(plan.Questions)-maxQuestions))
					break
				}
				out = append(out, fmt.Sprintf("  Q%d %s", i+1, ticket.TruncateWords(q, 24)))
			}
			return out
		}
		return []string{"Needs you - enter opens the menu to respond (see the log below)."}
	case store.StatePlanReview:
		// The full plan (free-form markdown, possibly long) lives in the dedicated
		// full-screen viewer (press enter); the detail pane shows a one-line teaser.
		out := []string{"Plan ready - enter opens the menu to read the full plan, then approve or give feedback"}
		if plan, err := agent.ReadPlan(paths.WorktreeFor(s.Repo, s.Ticket)); err == nil && plan != nil {
			if teaser := planTeaser(plan.Plan); teaser != "" {
				out = append(out, teaser)
			}
			meta := fmt.Sprintf("Confidence: %s · Type: %s", plan.Confidence, plan.Type)
			if n := len(plan.Steps); n > 0 { // legacy plans still carry a steps list
				meta = fmt.Sprintf("%d step(s) · ", n) + meta
			}
			out = append(out, meta)
		}
		return out
	case store.StateChangeReview:
		out := []string{
			"The change is built and verified locally - enter opens the review screen.",
			"  Nothing is committed, pushed, or opened as a PR until you approve.",
		}
		if s.ChangeRound >= 2 {
			out[0] = fmt.Sprintf("Round %d of the change is ready - enter opens the review screen.", s.ChangeRound)
		}
		if s.ChangeError != "" {
			out = append(out, "✗ the last attempt failed: "+truncate(s.ChangeError, 90))
		}
		return out
	case store.StateFixReview:
		out := []string{
			"Review fixes ready - enter opens the menu to read the diff and the draft replies.",
			"  Nothing is committed, pushed, or posted until you approve.",
		}
		// A fix drafted under denials may never have been compiled - the human
		// must know that BEFORE approving it, not at the ship verify.
		if denials := agent.UnmarshalDenials(s.Denials); len(denials) > 0 {
			out = append(out, "⚠ some commands were denied while drafting these fixes - they may be unverified:")
			for i, d := range denials {
				if i == 3 {
					out = append(out, fmt.Sprintf("  … and %d more (see the log below)", len(denials)-3))
					break
				}
				out = append(out, "  "+d.Command)
			}
		}
		return out
	case "failed":
		return []string{
			"Failed - " + failReason(s.Ticket),
			"Use the actions menu to open the project or run it again.",
		}
	case "needs-you":
		// A permission-blocked run is diagnosed, not shrugged at - say what was
		// refused and point at the action that actually exists.
		if denials := agent.UnmarshalDenials(s.Denials); len(denials) > 0 {
			out := []string{"Needs you - verification was BLOCKED by permissions:"}
			for i, d := range denials {
				if i == 3 {
					out = append(out, fmt.Sprintf("  … and %d more (see the log below)", len(denials)-3))
					break
				}
				out = append(out, "  "+d.Command)
			}
			// DenialFixable is the runner's park-time verdict, computed with the
			// real allowlist - the same answer the menu's allowRerunAvailable
			// re-derives, so the pane can never promise an action the menu
			// hides (which it did, when this call passed an empty allowlist).
			if s.DenialFixable {
				return append(out, "The actions menu offers \"Allow denied commands & re-run\".")
			}
			switch agent.DominantFlavor(denials) {
			case agent.FlavorHumanDenied:
				return append(out, "You denied these from the dashboard - Resume re-runs and asks again.")
			case agent.FlavorAskRule:
				return append(out, "Your Claude settings want a human to approve these - re-run and answer the prompt here when it appears.")
			case agent.FlavorDenyRule:
				return append(out, "A deny rule in your Claude settings blocks these - pie will not work around it.")
			}
			return append(out, "A config change will not fix this - the log below shows what the agent hit.")
		}
		return []string{
			"Needs you - the agent got stuck (see the log below).",
			"Use the actions menu to open the project or run it again.",
		}
	case store.StateReview:
		if s.OpenComments > 0 {
			return commentWhyLines(s)
		}
		line := "Under review - the PR is open and waiting for a human."
		if s.PRURL != "" {
			line = "Under review - " + s.PRURL
		}
		actions := "The actions menu opens the project, or the PR."
		if !worktreeExists(s.Repo, s.Ticket) {
			actions = "Worktree removed - the menu can run it again fresh."
		}
		return []string{line, actions}
	}
	return nil
}

// whyRowsFor wraps a session's whyLines to the width and caps the total rows,
// so a verbose state note can never flood the detail pane and push the ticket
// list or log off screen - the full content lives in the dedicated viewers.
func whyRowsFor(s store.Session, w int) []string {
	const maxWhyRows = 8
	var rows []string
	for _, ln := range whyLines(s) {
		rows = append(rows, wrapWords(ln, w)...)
	}
	if len(rows) > maxWhyRows {
		rows = append(rows[:maxWhyRows], "…")
	}
	return rows
}

// planTeaser reduces a free-form markdown plan to a single display line for
// the dashboard detail pane: the first non-empty line, stripped of markdown
// decoration and capped in length.
func planTeaser(plan string) string {
	for _, ln := range strings.Split(plan, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "```") {
			continue // headings and fences make useless teasers - find real prose
		}
		if ln = strings.TrimSpace(strings.TrimLeft(ln, "*-> ")); ln != "" {
			return ticket.TruncateWords(ln, 24)
		}
	}
	return ""
}

// failReason scrapes the most recent failure line from the log tail.
func failReason(ticket string) string {
	lines := tailLines(paths.LogFor(ticket), 50)
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.ToLower(lines[i])
		if strings.Contains(l, "fail") || strings.Contains(l, "error") || strings.Contains(l, "still red") {
			return truncate(strings.TrimSpace(stripPrefix(lines[i], ticket)), 100)
		}
	}
	return "see log below"
}
