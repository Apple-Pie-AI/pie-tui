// Reading a pull request's review feedback, so a ticket's reviewers can be
// answered from the hub instead of from a browser. One `gh api graphql` call
// per PR returns every review thread with the file and diff hunk it is anchored
// to; the caller persists them and decides which ones the agent should fix.
//
// GraphQL rather than REST because a thread's isResolved flag exists nowhere
// else - without it, comments the reviewer already resolved would be re-offered
// forever - and because the same node id that identifies a thread here is the
// one the reply and resolve mutations take (see writeback.go).
package review

import (
	"strconv"
	"strings"
)

// Kind values for Thread.
const (
	// KindThread is an inline review thread, anchored to a file and line.
	KindThread = "thread"
	// KindReview is a review's own summary body. It has no file anchor, and it
	// is frequently the most load-bearing text on the PR ("this whole approach
	// is wrong, use the repository pattern"), so it is carried alongside the
	// inline threads rather than dropped.
	KindReview = "review"
)

// Thread is one review conversation: an inline thread, or a review's summary
// body (Kind == KindReview, empty Path). Body carries every comment in the
// thread, so a back-and-forth reads in order without a second lookup.
//
// One struct rather than a thread/comment pair: a thread is GitHub's own unit
// of review, it is what "addressed" attaches to, and it is what the hub renders
// as a row - a normalized model would need a join and a group-by to produce
// exactly this.
type Thread struct {
	ID       string // GraphQL node id; stable across polls, and the key both mutations take
	Kind     string // KindThread | KindReview
	Author   string // who opened the thread ("(deleted)" when the account is gone)
	Path     string // repo-root-relative file path; empty for KindReview
	Body     string // every comment, in order
	URL      string // permalink to the comment on GitHub
	DiffHunk string // the diff the thread is anchored to; empty for KindReview

	Line     int  // best-known line; falls back to originalLine once the diff moves on
	Bot      bool // posted by an integration (CodeRabbit, Copilot, Sonar, …)
	Outdated bool // anchored to a diff that later commits have moved past
	Resolved bool // resolved on GitHub

	CommentCount  int    // total comments in the thread
	LastCommentID string // newest comment's node id - the pivot that reopens an addressed thread
	// Advisory: the reviewer chose "Comment", not "Request changes". GitHub has
	// no resolve mechanism for a review body at all, so counting one as
	// "wanting attention" pins the ticket in NEEDS YOU forever - and the
	// reviewer already said, with the review type itself, that they are not
	// blocking on it. Advisory threads are listed and fixable, never counted.
	Advisory bool
}

// Location renders the thread's anchor for a list row: "path:line", "path" when
// the line is unknown, or "—" for a review summary.
func (t Thread) Location() string {
	if t.Path == "" {
		return "—"
	}
	if t.Line <= 0 {
		return t.Path
	}
	return t.Path + ":" + strconv.Itoa(t.Line)
}

// Gist is the thread's first non-empty line, for a one-line list row.
func (t Thread) Gist() string {
	for _, ln := range strings.Split(t.Body, "\n") {
		if s := strings.TrimSpace(strings.TrimLeft(ln, ">#*- ")); s != "" {
			return s
		}
	}
	return ""
}

// PR is the review state of one pull request.
type PR struct {
	Number   int
	State    string // OPEN | MERGED | CLOSED
	Decision string // CHANGES_REQUESTED | APPROVED | REVIEW_REQUIRED | ""
	HeadSHA  string
	Threads  []Thread

	// Truncated reports that the PR has more threads than one page returns. The
	// caller must not delete stored threads that are missing from a truncated
	// result - they may simply be on the next page.
	Truncated bool
}
