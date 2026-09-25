package review

import (
	"errors"
	"strings"
	"testing"
)

// fullResponse mirrors a real `gh api graphql` payload: one CHANGES_REQUESTED
// review with a body, one approving review (must be dropped), a two-comment
// thread, a bot thread, and a thread whose diff has moved.
const fullResponse = `{"data":{"resource":{
  "number": 128,
  "state": "OPEN",
  "reviewDecision": "CHANGES_REQUESTED",
  "headRefOid": "abc123",
  "reviews": {"nodes": [
    {"id":"PRR_1","state":"CHANGES_REQUESTED","url":"https://x/1","body":"Use the repository pattern here.","author":{"login":"alice","__typename":"User"}},
    {"id":"PRR_2","state":"APPROVED","url":"https://x/2","body":"LGTM","author":{"login":"bob","__typename":"User"}},
    {"id":"PRR_3","state":"COMMENTED","url":"https://x/3","body":"   ","author":{"login":"carol","__typename":"User"}}
  ]},
  "reviewThreads": {
    "pageInfo": {"hasNextPage": false},
    "nodes": [
      {"id":"PRRT_1","isResolved":false,"isOutdated":false,"path":"app/Login.kt","line":42,"originalLine":40,
       "comments":{"totalCount":2,"nodes":[
         {"id":"PRRC_1","body":"Don't swallow the exception.","url":"https://x/c1","diffHunk":"@@ -38,7 +38,11 @@","author":{"login":"alice","__typename":"User"}},
         {"id":"PRRC_2","body":"Agreed.","url":"https://x/c2","diffHunk":"@@ -38,7 +38,11 @@","author":{"login":"dave","__typename":"User"}}
       ]}},
      {"id":"PRRT_2","isResolved":true,"isOutdated":true,"path":"build.gradle.kts","line":null,"originalLine":12,
       "comments":{"totalCount":1,"nodes":[
         {"id":"PRRC_3","body":"Bump the plugin.","url":"https://x/c3","diffHunk":"@@ -10,3 +10,3 @@","author":{"login":"coderabbitai[bot]","__typename":"User"}}
       ]}}
    ]
  }
}}}`

func TestParseFullResponse(t *testing.T) {
	pr, err := parse([]byte(fullResponse))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pr.Number != 128 || pr.State != "OPEN" || pr.Decision != "CHANGES_REQUESTED" || pr.HeadSHA != "abc123" {
		t.Fatalf("PR metadata wrong: %+v", pr)
	}
	if pr.Truncated {
		t.Error("Truncated should be false when hasNextPage is false")
	}
	// alice's review body + two inline threads. The APPROVED "LGTM" and the
	// whitespace-only COMMENTED body must both be dropped.
	if len(pr.Threads) != 3 {
		t.Fatalf("want 3 threads, got %d: %+v", len(pr.Threads), pr.Threads)
	}

	// Review summaries come first so the frame precedes the details.
	rev := pr.Threads[0]
	if rev.Kind != KindReview || rev.ID != "PRR_1" || rev.Author != "alice" {
		t.Errorf("review summary wrong: %+v", rev)
	}
	if rev.Path != "" || rev.Location() != "—" {
		t.Errorf("review summary must have no file anchor, got path=%q loc=%q", rev.Path, rev.Location())
	}
	// A review body cannot gain replies, so its own id is its pivot - that is
	// what keeps it from spuriously reopening after it is addressed.
	if rev.LastCommentID != "PRR_1" {
		t.Errorf("review pivot = %q, want its own id", rev.LastCommentID)
	}

	th := pr.Threads[1]
	if th.Kind != KindThread || th.Author != "alice" || th.Line != 42 {
		t.Errorf("inline thread wrong: %+v", th)
	}
	if th.Location() != "app/Login.kt:42" {
		t.Errorf("Location() = %q", th.Location())
	}
	if th.CommentCount != 2 || th.LastCommentID != "PRRC_2" {
		t.Errorf("pivot must be the NEWEST comment: count=%d last=%q", th.CommentCount, th.LastCommentID)
	}
	// A conversation is attributed per turn; a lone comment is not (see below).
	if !strings.Contains(th.Body, "@alice: Don't swallow") || !strings.Contains(th.Body, "@dave: Agreed.") {
		t.Errorf("multi-comment body should attribute each turn, got %q", th.Body)
	}
	if th.DiffHunk == "" {
		t.Error("diff hunk must survive - it is how the agent locates an outdated comment")
	}
	if th.Bot {
		t.Error("alice is not a bot")
	}
}

// A comment whose diff later commits moved past keeps its original anchor,
// which is the only line number left to show.
func TestParseOutdatedThreadFallsBackToOriginalLine(t *testing.T) {
	pr, err := parse([]byte(fullResponse))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	th := pr.Threads[2]
	if th.Line != 12 {
		t.Errorf("Line = %d, want the originalLine 12 when line is null", th.Line)
	}
	if !th.Outdated || !th.Resolved {
		t.Errorf("Outdated=%v Resolved=%v, want both true", th.Outdated, th.Resolved)
	}
	// Single comment: no "@author:" prefix, since Thread.Author already says who.
	if th.Body != "Bump the plugin." {
		t.Errorf("lone comment body = %q, want it unprefixed", th.Body)
	}
}

// Bot detection has to catch integrations posting through a plain user account
// whose login merely ends in "[bot]" - checking __typename alone lets a
// 40-comment bot review straight through the filter.
func TestParseBotDetection(t *testing.T) {
	pr, err := parse([]byte(fullResponse))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !pr.Threads[2].Bot {
		t.Error(`login "coderabbitai[bot]" with __typename "User" must still be flagged a bot`)
	}

	typenameOnly := threadWith(`{"login":"copilot-pull-request-reviewer","__typename":"Bot"}`)
	pr, err = parse([]byte(typenameOnly))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !pr.Threads[0].Bot {
		t.Error(`__typename "Bot" must be flagged even when the login looks human`)
	}
}

// A deleted account arrives as a null author. Dereferencing it would panic the
// 1Hz TUI render loop, which is a hard crash, not a blank name.
func TestParseNullAuthor(t *testing.T) {
	pr, err := parse([]byte(threadWith("null")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := pr.Threads[0].Author; got != "(deleted)" {
		t.Errorf("Author = %q, want %q", got, "(deleted)")
	}
	if pr.Threads[0].Bot {
		t.Error("a deleted account is not a bot")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, raw, wantSubstr string
		wantErr               error
	}{
		{
			name:       "graphql error is surfaced, not swallowed",
			raw:        `{"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a node"}]}`,
			wantSubstr: "Could not resolve to a node",
		},
		{
			name:    "rate limiting is typed so the caller can back off",
			raw:     `{"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`,
			wantErr: ErrRateLimited,
		},
		{
			name:       "a url that is not a PR",
			raw:        `{"data":{"resource":null}}`,
			wantSubstr: "did not resolve to a pull request",
		},
		{
			name:       "garbage is a decode error, not a zero-value PR",
			raw:        `not json at all`,
			wantSubstr: "decode graphql response",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, err := parse([]byte(tt.raw))
			if err == nil {
				t.Fatalf("want an error, got PR %+v", pr)
			}
			if pr != nil {
				t.Errorf("PR must be nil on error, got %+v", pr)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantSubstr != "" && !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantSubstr)
			}
		})
	}
}

// A truncated page must be flagged, because the caller uses it to suppress the
// "delete threads I didn't see" step - otherwise page 2 would erase page 1.
func TestParseTruncated(t *testing.T) {
	raw := strings.Replace(fullResponse, `"hasNextPage": false`, `"hasNextPage": true`, 1)
	pr, err := parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !pr.Truncated {
		t.Error("hasNextPage true must set Truncated")
	}
}

// A thread whose comments were all deleted has nothing to show or fix.
func TestParseSkipsEmptyThread(t *testing.T) {
	raw := `{"data":{"resource":{"number":1,"state":"OPEN","reviews":{"nodes":[]},
	  "reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[
	    {"id":"PRRT_x","path":"a.kt","line":1,"comments":{"totalCount":0,"nodes":[]}}
	  ]}}}}`
	pr, err := parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(pr.Threads) != 0 {
		t.Errorf("want no threads, got %+v", pr.Threads)
	}
}

func TestGist(t *testing.T) {
	tests := []struct{ body, want string }{
		{"Don't swallow the exception.", "Don't swallow the exception."},
		{"\n\n  > quoted\nreal text", "quoted"},
		{"### Heading\nbody", "Heading"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := (Thread{Body: tt.body}).Gist(); got != tt.want {
			t.Errorf("Gist(%q) = %q, want %q", tt.body, got, tt.want)
		}
	}
}

// threadWith builds a minimal one-thread response around an author literal.
func threadWith(author string) string {
	return `{"data":{"resource":{"number":1,"state":"OPEN","reviews":{"nodes":[]},
	  "reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[
	    {"id":"PRRT_x","isResolved":false,"isOutdated":false,"path":"a.kt","line":1,"originalLine":1,
	     "comments":{"totalCount":1,"nodes":[
	       {"id":"PRRC_x","body":"hi","url":"https://x","diffHunk":"@@","author":` + author + `}
	     ]}}
	  ]}}}}`
}

// The reviewer's own review type is the marker: "Comment" is advisory,
// "Request changes" wants action. Nothing is inferred from the wording -
// deciding weight from prose would misstate what a reviewer meant.
func TestReviewThreadAdvisoryFollowsTheReviewType(t *testing.T) {
	commented, ok := reviewThread(ghReview{
		ID: "R1", State: "COMMENTED", Body: "Update the PR description, it is vague.",
	})
	if !ok || !commented.Advisory {
		t.Errorf("a COMMENTED review must be advisory: ok=%v advisory=%v", ok, commented.Advisory)
	}
	blocking, ok := reviewThread(ghReview{
		ID: "R2", State: "CHANGES_REQUESTED", Body: "The retry policy is wrong.",
	})
	if !ok || blocking.Advisory {
		t.Errorf("a CHANGES_REQUESTED review must want attention: ok=%v advisory=%v", ok, blocking.Advisory)
	}
}
