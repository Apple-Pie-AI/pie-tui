// Rendering the approve screen: the verdict list, the approve bar, and the
// focused comment's diff next to the reply that will be posted for it.
//
// Split out of comments_approve.go, which owns that screen's state and keys.
package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// approveDiffRows is the focused comment's evidence: every change the fix
// made, always. The anchored file leads, with the ▶ marker on the commented
// line; every other changed file follows under its own header.
//
// An earlier version showed only the anchored file's slice, with a hint that
// other files had changed too. Twice that hid the half of a fix the human was
// looking for - a "use a localized string" fix IS the strings.xml line, and
// when every comment anchors the code file, no row on the screen could ever
// show it. What ships is the whole diff, so the preview is the whole diff.
func (m monitorModel) approveDiffRows(c *store.Comment, w int) []string {
	if m.comments.diffErr != "" {
		return []string{"  " + cmtRedSty.Render(truncate("reading the diff: "+m.comments.diffErr, w-4))}
	}
	if !m.comments.diffLoaded {
		return []string{"  " + cmtDimSty.Render("reading the worktree diff…")}
	}
	// Cap the diff to what the card can afford: the terminal, less every fixed
	// line the card draws - header, quote, captions, reply, button, action rows,
	// footer. The old formula budgeted for the list screen this replaced, and a
	// long diff pushed the actions off the bottom - including "See the whole
	// diff", the one row that would have shown the rest.
	quote := len(wrapWords(c.Body, w-2))
	reply := c.DraftReply
	if reply == "" {
		reply = c.AgentNote
	}
	replyLines := len(wrapWords(reply, w-2))
	max := m.height - quote - replyLines - 24
	if max < 5 {
		max = 5
	}

	var rows []string
	anchored := c.Path != "" && len(m.comments.diff[c.Path]) > 0
	// More than one file on screen means every section gets a header; a single
	// unlabeled diff is fine because the pane head above already names it.
	multi := len(m.comments.diff) > 1
	switch {
	case anchored:
		if multi {
			rows = append(rows, "  "+cmtMidSty.Render(truncate(c.Path, w-4)))
		}
		rows = append(rows, renderHunk(strings.Join(m.comments.diff[c.Path], "\n"), c.Path, c.Line, w)...)
	case c.Path == "":
		rows = append(rows, "  "+cmtDimSty.Render(truncate("review summary - every change the fix made:", w-4)))
	default:
		rows = append(rows, "  "+cmtDimSty.Render(truncate(
			"no changes in "+c.Path+" itself - every change the fix made:", w-4)))
	}

	paths := make([]string, 0, len(m.comments.diff))
	for p := range m.comments.diff {
		if p != c.Path || !anchored { // the anchor already led
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		if len(m.comments.diff[p]) == 0 {
			continue
		}
		// A blank row before each header: two diffs butted together read as one
		// file with a stray path in the middle of it.
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, "  "+cmtMidSty.Render(truncate(p, w-4)))
		rows = append(rows, renderHunk(strings.Join(m.comments.diff[p], "\n"), p, 0, w)...)
	}

	if len(rows) <= 1 && !anchored {
		return []string{"  " + cmtDimSty.Render("the worktree has no uncommitted changes - the fix may already be committed")}
	}
	return capDiffRows(rows, max)
}

// capDiffRows cuts a diff to the pane's budget, saying how much it held back.
func capDiffRows(rows []string, max int) []string {
	if len(rows) <= max {
		return rows
	}
	return append(rows[:max], "  "+cmtDimSty.Render(fmt.Sprintf(
		"… %d more line(s) - \"See the whole diff\" below shows the rest", len(rows)-max)))
}

// firstLine is the first non-empty line of a note, for a one-line row label.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}
