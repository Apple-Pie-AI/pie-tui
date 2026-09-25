// Turning GitHub's GraphQL response into Threads. Kept free of exec and of any
// file access so the whole shape - null authors, moved lines, bot logins,
// truncated pages - is testable from a fixture string.
package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Every field carries an explicit json tag. Decision 18 obfuscates releases
// with `garble -literals`, which is only safe because deserialization never
// relies on Go field names.

type ghAuthor struct {
	Login    string `json:"login"`
	TypeName string `json:"__typename"` // "User" | "Bot" | "Organization" | "Mannequin"
}

type ghComment struct {
	ID       string    `json:"id"`
	Body     string    `json:"body"`
	URL      string    `json:"url"`
	DiffHunk string    `json:"diffHunk"`
	Author   *ghAuthor `json:"author"` // null once the account is deleted
}

type ghThread struct {
	ID           string `json:"id"`
	IsResolved   bool   `json:"isResolved"`
	IsOutdated   bool   `json:"isOutdated"`
	Path         string `json:"path"`
	Line         *int   `json:"line"`         // null once later commits move the diff
	OriginalLine *int   `json:"originalLine"` // the anchor the comment was written against
	Comments     struct {
		TotalCount int         `json:"totalCount"`
		Nodes      []ghComment `json:"nodes"`
	} `json:"comments"`
}

type ghReview struct {
	ID     string    `json:"id"`
	State  string    `json:"state"`
	URL    string    `json:"url"`
	Body   string    `json:"body"`
	Author *ghAuthor `json:"author"`
}

type ghPR struct {
	Number         int    `json:"number"`
	State          string `json:"state"`
	ReviewDecision string `json:"reviewDecision"`
	HeadRefOid     string `json:"headRefOid"`
	Reviews        struct {
		Nodes []ghReview `json:"nodes"`
	} `json:"reviews"`
	ReviewThreads struct {
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
		Nodes []ghThread `json:"nodes"`
	} `json:"reviewThreads"`
}

type ghResponse struct {
	Data struct {
		Resource *ghPR `json:"resource"`
	} `json:"data"`
	Errors []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"errors"`
}

// ErrRateLimited reports that GitHub refused the query for quota reasons, so
// the caller can back off rather than hammer it on the next tick.
var ErrRateLimited = errors.New("github api rate limited")

// parse converts one GraphQL response document into a PR.
func parse(raw []byte) (*PR, error) {
	var resp ghResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode graphql response: %w", err)
	}
	if len(resp.Errors) > 0 {
		for _, e := range resp.Errors {
			if e.Type == "RATE_LIMITED" {
				return nil, ErrRateLimited
			}
		}
		return nil, fmt.Errorf("graphql: %s", resp.Errors[0].Message)
	}
	// A null resource means the URL didn't resolve to a pull request this token
	// can see - a deleted PR, a typo, or a repo the user lost access to.
	if resp.Data.Resource == nil {
		return nil, errors.New("graphql: url did not resolve to a pull request")
	}
	src := resp.Data.Resource

	pr := &PR{
		Number:    src.Number,
		State:     src.State,
		Decision:  src.ReviewDecision,
		HeadSHA:   src.HeadRefOid,
		Truncated: src.ReviewThreads.PageInfo.HasNextPage,
	}
	// Review summaries first: they set the frame the inline comments sit in.
	for _, r := range src.Reviews.Nodes {
		if t, ok := reviewThread(r); ok {
			pr.Threads = append(pr.Threads, t)
		}
	}
	for _, th := range src.ReviewThreads.Nodes {
		if t, ok := inlineThread(th); ok {
			pr.Threads = append(pr.Threads, t)
		}
	}
	return pr, nil
}

// reviewThread converts a review's summary body into a Thread, reporting false
// for the ones that carry no request.
//
// Only CHANGES_REQUESTED and COMMENTED are kept. An APPROVED review's body is
// almost always "LGTM" or an emoji, and surfacing it would badge an approved PR
// as needing work - the badge would be lying. PENDING is someone's unsubmitted
// draft, and DISMISSED has been explicitly retracted.
func reviewThread(r ghReview) (Thread, bool) {
	body := strings.TrimSpace(r.Body)
	if body == "" {
		return Thread{}, false
	}
	if r.State != "CHANGES_REQUESTED" && r.State != "COMMENTED" {
		return Thread{}, false
	}
	login, bot := authorOf(r.Author)
	return Thread{
		ID:     r.ID,
		Kind:   KindReview,
		Author: login,
		Body:   body,
		URL:    r.URL,
		Bot:    bot,
		// The reviewer's own choice of review type is the marker - nothing is
		// inferred from the wording.
		Advisory: r.State == "COMMENTED",
		// A review body cannot gain replies, so its own id is a stable pivot: it
		// never spuriously reopens once addressed.
		CommentCount:  1,
		LastCommentID: r.ID,
	}, true
}

// inlineThread converts one review thread into a Thread, reporting false for a
// thread whose comments all vanished (deleted mid-review).
func inlineThread(th ghThread) (Thread, bool) {
	if len(th.Comments.Nodes) == 0 {
		return Thread{}, false
	}
	first := th.Comments.Nodes[0]
	login, bot := authorOf(first.Author)

	count := th.Comments.TotalCount
	if count < len(th.Comments.Nodes) {
		count = len(th.Comments.Nodes)
	}
	return Thread{
		ID:            th.ID,
		Kind:          KindThread,
		Author:        login,
		Path:          th.Path,
		Body:          flattenComments(th.Comments.Nodes),
		URL:           first.URL,
		DiffHunk:      first.DiffHunk,
		Line:          lineOf(th),
		Bot:           bot,
		Outdated:      th.IsOutdated,
		Resolved:      th.IsResolved,
		CommentCount:  count,
		LastCommentID: th.Comments.Nodes[len(th.Comments.Nodes)-1].ID,
	}, true
}

// lineOf prefers the thread's current line and falls back to the line it was
// originally written against, which is all that survives once later commits
// move the diff out from under it.
func lineOf(th ghThread) int {
	if th.Line != nil {
		return *th.Line
	}
	if th.OriginalLine != nil {
		return *th.OriginalLine
	}
	return 0
}

// flattenComments renders a thread as readable text. A lone comment is its own
// body; a conversation is attributed per turn so the reader can tell who
// conceded and who did not.
func flattenComments(cs []ghComment) string {
	if len(cs) == 1 {
		return strings.TrimSpace(cs[0].Body)
	}
	var b strings.Builder
	for i, c := range cs {
		body := strings.TrimSpace(c.Body)
		if body == "" {
			continue
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		login, _ := authorOf(c.Author)
		b.WriteString("@" + login + ": " + body)
	}
	return b.String()
}

// authorOf returns a display login and whether the author is an integration.
//
// Both signals are checked: GitHub Apps report __typename "Bot", but plenty of
// integrations post through a regular user account whose login merely ends in
// "[bot]". Missing either one lets a 40-comment bot review through the filter.
func authorOf(a *ghAuthor) (login string, bot bool) {
	if a == nil {
		return "(deleted)", false
	}
	if a.Login == "" {
		return "(unknown)", a.TypeName == "Bot"
	}
	return a.Login, a.TypeName == "Bot" || strings.HasSuffix(a.Login, "[bot]")
}
