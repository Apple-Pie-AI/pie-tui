// Rendering for the command allowlist review screen. Split from
// permissions.go (the comments.go/comments_view.go split already in this
// package).
package tui

import (
	"fmt"
	"strings"
)

func (m monitorModel) renderPermissions(w int) string {
	p := m.permissions
	if p.loadErr != nil {
		return headerStyle.Render("  Command allowlist") + "\n" +
			dimStyle.Render("  could not load config: "+p.loadErr.Error()) + "\n"
	}
	// The remove dialog pre-empts the list, exactly like the stop
	// confirmation pre-empts the dashboard.
	if p.confirm >= 0 && p.confirm < len(p.rows) {
		return m.renderPermissionsConfirm(w)
	}

	// Build the whole screen as lines, remembering which one the cursor is
	// on, then window it to the terminal height - the baseline alone can be
	// 30+ rules, and rows past the bottom edge used to be simply unreachable
	// by eye (the cursor still reached them; the feedback didn't).
	var lines []string
	cursorLine := -1

	lines = append(lines, headerStyle.Render("  Command allowlist"), "")
	title := fmt.Sprintf("Default allowlist (%d rules)", len(p.baseline))
	hint := "the base set every agent can use - to change it, edit allowed_tools in ~/.pie/config.toml"
	if p.baselineSrc == "custom" {
		title = fmt.Sprintf("Custom allowlist (%d rules)", len(p.baseline))
		hint = "replaces the default - set via allowed_tools in ~/.pie/config.toml; delete that line to restore the default"
	}
	lines = append(lines,
		stAccent.Render("  "+title),
		dimStyle.Render("  "+truncate(hint, w-4)))
	for _, r := range p.baseline {
		lines = append(lines, dimStyle.Render("    "+truncate(r, w-6)))
	}
	lines = append(lines, "",
		stAccent.Render(fmt.Sprintf("  Your extra rules (%d)", len(p.rows))),
		dimStyle.Render("  added when you pick \"Allow & remember\" on an approval, or by hand below"))
	if len(p.rows) == 0 {
		lines = append(lines, dimStyle.Render("    (you haven't added any rules yet)"))
	}
	for i, r := range p.rows {
		if i == p.cursor && !p.adding {
			cursorLine = len(lines)
			lines = append(lines, selStyle.Render(" "+truncate(r, w-2)))
		} else {
			lines = append(lines, "  "+truncate(r, w-2))
		}
	}
	lines = append(lines, "")

	addLabel := "+ Add rule"
	if p.adding {
		addLabel = "+ Add rule: " + p.addField.caretView()
	}
	if p.cursor == p.addIdx() || p.adding {
		cursorLine = len(lines)
		lines = append(lines, selStyle.Render(" "+truncate(addLabel, w-2)))
	} else {
		lines = append(lines, "  "+truncate(addLabel, w-2))
	}
	if p.err != "" {
		lines = append(lines, cmdApprovalStyle.Render("    "+p.err))
	}

	footer := "  ↑↓ move   enter remove a rule / add a new one   esc close"
	if p.adding {
		footer = "  type a rule (e.g. Bash(python3:*))   enter add   esc cancel"
	}

	body := windowLines(lines, cursorLine, m.height-4)
	return strings.Join(body, "\n") + "\n\n" + dimStyle.Render(footer)
}

// renderPermissionsConfirm is the remove/keep dialog for one rule.
func (m monitorModel) renderPermissionsConfirm(w int) string {
	p := m.permissions
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("  Remove this rule?") + "\n\n")
	b.WriteString("    " + truncate(p.rows[p.confirm], w-6) + "\n\n")
	b.WriteString(dimStyle.Render("  agents lose this grant as soon as it is removed") + "\n\n")
	items := []string{"Remove it", "Keep it"}
	for i, item := range items {
		if i == p.confirmCursor {
			b.WriteString(selStyle.Render(" "+item) + "\n")
		} else {
			b.WriteString("  " + item + "\n")
		}
	}
	b.WriteString("\n" + dimStyle.Render("  ↑↓ move   enter select   esc keep"))
	return b.String()
}

// windowLines returns at most max lines of the list, keeping cursorLine in
// view, with "… N more" markers standing in for whatever is cut off. max <= 0
// (an unmeasured terminal) returns everything - never hide rows on a guess.
func windowLines(lines []string, cursorLine, max int) []string {
	if max <= 0 || len(lines) <= max {
		return lines
	}
	if max < 3 {
		max = 3 // room for both markers and at least one real line
	}
	// Slide a window that keeps the cursor inside it, biased so the cursor
	// sits near the bottom while scrolling down (reading order).
	start := 0
	if cursorLine >= 0 && cursorLine >= max-1 {
		start = cursorLine - (max - 2)
	}
	if start > len(lines)-max {
		start = len(lines) - max
	}
	end := start + max
	out := append([]string{}, lines[start:end]...)
	if start > 0 {
		// The marker replaces the first visible line, so it hides start+1.
		out[0] = dimStyle.Render(fmt.Sprintf("  … %d more above", start+1))
	}
	if end < len(lines) {
		out[len(out)-1] = dimStyle.Render(fmt.Sprintf("  … %d more below", len(lines)-end+1))
	}
	return out
}
