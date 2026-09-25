//go:build live

// Pins Fetch against the real GitHub API. Excluded from the normal suite (it
// needs network and a logged-in gh); the parse tests cover the same shapes from
// fixtures. Run it when the query text changes:
//
//	go test -tags live ./internal/review -run TestLiveFetch -v \
//	  -pr https://github.com/cli/cli/pull/9000
package review

import (
	"flag"
	"testing"
)

var prFlag = flag.String("pr", "https://github.com/cli/cli/pull/9000", "PR url to fetch")

func TestLiveFetch(t *testing.T) {
	pr, err := Fetch(".", *prFlag)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if pr.Number == 0 {
		t.Fatalf("no PR number came back: %+v", pr)
	}
	t.Logf("PR #%d state=%s decision=%q head=%s threads=%d truncated=%v",
		pr.Number, pr.State, pr.Decision, pr.HeadSHA, len(pr.Threads), pr.Truncated)
	for i, th := range pr.Threads {
		t.Logf("  [%d] %s %-14s %-28s bot=%v outdated=%v resolved=%v n=%d %q",
			i, th.Kind, th.Author, th.Location(), th.Bot, th.Outdated, th.Resolved,
			th.CommentCount, th.Gist())
	}
}
