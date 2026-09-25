// The @ file-mention picker: a small, screen-agnostic widget for pointing a
// free-text box at a file in the repo. Built first for the change screen
// (change_box.go), factored out here so any other textarea-backed input can
// reuse it - filtering, key handling, and rendering all take their inputs as
// plain parameters, never a specific screen's state.
//
// This targets *textarea.Model directly rather than an interface: today it
// has exactly one concrete caller. The PR-comments screens (comments_cards.go,
// comments_menu.go) are the next obvious adopters, but their free-text fields
// are atomInput (input.go), not textarea.Model - wiring them in means either
// growing atomInput the few methods this file needs (Value, an insert, a
// cursor-to-end) or moving them onto textarea.Model, both real decisions left
// for that follow-up rather than guessed at here.
package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// mentionPickerCap is the picker's row budget: priority paths first, then
// substring matches over the whole repo.
const mentionPickerCap = 6

// mentionScanCap bounds how many repoFiles entries a single filter pass
// examines, independent of mentionPickerCap (which only bounds the RESULT
// count) - so a query that matches little or nothing (a typo, an empty
// picker just opened on a huge repo) still returns in bounded time instead of
// walking the whole file list on every keystroke.
const mentionScanCap = 20000

// mentionPicker is the @ picker's state while open.
type mentionPicker struct {
	open  bool
	query string
	items []string // filtered paths, capped, priority entries first
	sel   int
}

// repoFileList is a worktree's cached `git ls-files` listing plus its
// lowercase form, computed once per worktree per session instead of on
// every keystroke a picker filters against it.
type repoFileList struct {
	files []string
	lower []string
}

// newRepoFileList lowercases files once, up front.
func newRepoFileList(files []string) repoFileList {
	lower := make([]string, len(files))
	for i, f := range files {
		lower[i] = strings.ToLower(f)
	}
	return repoFileList{files: files, lower: lower}
}

// insertMention appends "token " to the draft, adding a separating space
// first if the draft has content that doesn't already end in one.
func insertMention(ta *textarea.Model, token string) {
	ta.CursorEnd()
	if v := ta.Value(); v != "" && !strings.HasSuffix(v, " ") {
		ta.InsertString(" ")
	}
	ta.InsertString(token + " ")
}

// removePickerQuery strips the trailing "@query" the user typed so far, so
// selecting a picker entry replaces the partial mention rather than
// appending after it.
func removePickerQuery(ta *textarea.Model, query string) {
	n := len(query) + 1 // +1 for "@"
	v := ta.Value()
	if n > len(v) {
		n = len(v)
	}
	ta.SetValue(v[:len(v)-n])
	ta.CursorEnd()
}

// filterMentions ranks repoFiles against query, with priority entries (e.g.
// a change screen's already-reviewed files) always listed first. repoFilesLower
// is repoFiles' precomputed lowercase form (see newRepoFileList) - passing it
// in means this function never re-lowercases the whole list per keystroke; a
// short or missing entry falls back to lowercasing that one path on the spot.
// Scans at most mentionScanCap repoFiles candidates and returns at most
// limit results, a basename match ranked ahead of a directory-segment-only
// match, a shorter path breaking further ties.
func filterMentions(query string, priority, repoFiles, repoFilesLower []string, limit int) []string {
	q := strings.ToLower(query)
	seen := make(map[string]bool, len(priority))
	var items []string
	for _, p := range priority {
		seen[p] = true
		if len(items) >= limit {
			continue
		}
		if strings.Contains(strings.ToLower(p), q) {
			items = append(items, p)
		}
	}

	type candidate struct {
		path        string
		idx         int
		basenameHit bool
	}
	var pool []candidate
	for i := 0; i < len(repoFiles) && i < mentionScanCap; i++ {
		p := repoFiles[i]
		if seen[p] {
			continue
		}
		lower := p
		if i < len(repoFilesLower) {
			lower = repoFilesLower[i]
		} else {
			lower = strings.ToLower(p)
		}
		if !strings.Contains(lower, q) {
			continue
		}
		pool = append(pool, candidate{path: p, idx: i, basenameHit: strings.Contains(mentionBasename(lower), q)})
	}
	sort.SliceStable(pool, func(a, b int) bool {
		if pool[a].basenameHit != pool[b].basenameHit {
			return pool[a].basenameHit
		}
		if len(pool[a].path) != len(pool[b].path) {
			return len(pool[a].path) < len(pool[b].path)
		}
		return pool[a].idx < pool[b].idx
	})
	for _, c := range pool {
		if len(items) >= limit {
			break
		}
		items = append(items, c.path)
	}
	return items
}

// mentionBasename returns the part of a (lowercased) path after its last "/".
func mentionBasename(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// updateMentionPicker owns the keyboard while a picker is open, layered over
// a textarea: ↑↓ choose, Enter inserts the selection (replacing the typed
// "@query"), Esc closes the picker only. refilter is called with the new
// query whenever it changes, so callers supply their own priority/repoFiles
// via a closure and this stays generic. stop reports whether the caller
// should skip forwarding msg to the textarea (the picker fully consumed it -
// ↑↓/Esc/Enter); changed reports whether the draft's value moved, so a
// store-backed caller knows a persist is worth doing.
func updateMentionPicker(p *mentionPicker, ta *textarea.Model, msg tea.KeyMsg, refilter func(query string) []string) (stop, changed bool) {
	switch msg.Type {
	case tea.KeyUp:
		if p.sel > 0 {
			p.sel--
		}
		return true, false
	case tea.KeyDown:
		if p.sel < len(p.items)-1 {
			p.sel++
		}
		return true, false
	case tea.KeyEsc:
		*p = mentionPicker{}
		return true, false
	case tea.KeyEnter:
		if len(p.items) > 0 {
			path := p.items[p.sel]
			removePickerQuery(ta, p.query)
			ta.InsertString(path + " ")
		}
		*p = mentionPicker{}
		return true, true
	case tea.KeyLeft, tea.KeyRight, tea.KeySpace:
		// Caret movement or a space means the mention is done being typed -
		// close the picker, but the key itself still lands in the textarea.
		*p = mentionPicker{}
	case tea.KeyBackspace:
		if len(p.query) == 0 {
			*p = mentionPicker{} // backspacing the "@" itself
		} else {
			p.query = p.query[:len(p.query)-1]
			p.items = refilter(p.query)
			if p.sel >= len(p.items) {
				p.sel = 0
			}
		}
	case tea.KeyRunes:
		p.query += string(msg.Runes)
		p.items = refilter(p.query)
		if p.sel >= len(p.items) {
			p.sel = 0
		}
	}
	return false, false
}

// renderMentionPicker draws the ≤mentionPickerCap-row bordered list under a
// mention box. items are already ranked and capped by the caller
// (filterMentions); tag labels each row (e.g. "changed") - a caller with no
// such notion can pass a function that always returns "".
func renderMentionPicker(items []string, sel int, query string, tag func(path string) string, cw int) []string {
	inner := min(cw-6, 60)
	if inner < 24 {
		inner = 24
	}
	var out []string
	out = append(out, "  "+stChrome.Render("┌"+strings.Repeat("─", inner+2)+"┐"))
	if len(items) == 0 {
		msg := fmt.Sprintf("no files match %q", query)
		out = append(out, "  "+stChrome.Render("│ ")+padRight(cmtDimSty.Render(truncate(msg, inner)), inner)+stChrome.Render(" │"))
	}
	for i, p := range items {
		label := shortPath(p, inner-10)
		if t := tag(p); t != "" {
			label += "  " + t
		}
		if i == sel {
			out = append(out, "  "+stChrome.Render("│ ")+selStyle.Render(padRight("▸ "+label, inner))+stChrome.Render(" │"))
		} else {
			out = append(out, "  "+stChrome.Render("│ ")+padRight("  "+label, inner)+stChrome.Render(" │"))
		}
	}
	out = append(out, "  "+stChrome.Render("└"+strings.Repeat("─", inner+2)+"┘"))
	return out
}
