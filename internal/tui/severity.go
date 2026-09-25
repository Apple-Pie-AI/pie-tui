// Reading a reviewer's own severity marker off the front of their comment.
//
// Reviewers already say how much a comment weighs - "nit:", "blocking:",
// "chore:" - and burying that in the body made a crash report and a rename
// request render identically. Lifting it into its own column is the whole
// point: the tag is what decides which comments you read first.
//
// Nothing is inferred. A comment with no marker gets no tag, because guessing
// severity from wording would be a confident lie about a reviewer's intent.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The severity tags, in the vocabulary the column renders.
const (
	sevBlock = "block"
	sevNit   = "nit"
	sevChore = "chore"
)

// sevAliases maps what reviewers actually type to the tag shown. Longest forms
// first so "nitpick" is not matched as "nit" with "pick" left behind.
var sevAliases = []struct{ word, tag string }{
	{"blocking", sevBlock},
	{"blocker", sevBlock},
	{"block", sevBlock},
	{"nitpick", sevNit},
	{"nit", sevNit},
	{"chore", sevChore},
	{"style", sevChore},
}

// severity splits a leading severity marker off a comment body.
//
// It only fires on a marker followed by a separator - "nit: rename this" - so
// a sentence that merely opens with the word ("blocking the release on this")
// keeps its first word and gets no tag.
func severity(body string) (tag, rest string) {
	trimmed := strings.TrimLeft(body, " \t>#*-")
	lower := strings.ToLower(trimmed)
	for _, a := range sevAliases {
		if !strings.HasPrefix(lower, a.word) {
			continue
		}
		after := trimmed[len(a.word):]
		sep := strings.TrimLeft(after, " ")
		// The marker has to be punctuated off from the sentence it labels.
		if !strings.HasPrefix(sep, ":") && !strings.HasPrefix(sep, "-") &&
			!strings.HasPrefix(sep, "—") && !strings.HasPrefix(sep, "!") {
			continue
		}
		sep = strings.TrimLeft(sep, ":-—! ")
		if sep == "" {
			continue // the marker was the entire comment; keep the body as-is
		}
		return a.tag, sep
	}
	return "", body
}

// sevStyle paints a tag: red blocks, amber nits, dim chores. An unchecked row
// mutes all three, because the tag is a reason to look and a skipped comment is
// one you have already decided not to.
func sevStyle(tag string, checked bool) lipgloss.Style {
	if !checked {
		return cmtOffSty
	}
	switch tag {
	case sevBlock:
		return cmtRedSty
	case sevNit:
		return cmtAmberSty
	default:
		return cmtDimSty
	}
}
