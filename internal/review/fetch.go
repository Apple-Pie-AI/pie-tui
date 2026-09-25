// Talking to GitHub through the gh CLI, the way the rest of the tree does -
// no SDK, no token handling of our own.
package review

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// threadsQuery reads a PR by URL. `resource` resolves any GitHub URL to its
// node, which spares us parsing owner/repo/number out of the stored PR URL and
// works unchanged on GitHub Enterprise, where that parse would need the host.
//
// first: 100 is deliberately the whole budget - a PR with more review threads
// than that is pathological, and paginating would mean either several round
// trips per poll or `gh --paginate`, which emits one JSON document per page and
// so cannot be unmarshalled as a single response. PR.Truncated reports when the
// bound actually bit, and the caller must then not treat missing threads as
// deleted.
const threadsQuery = `query($url: URI!) {
  resource(url: $url) {
    ... on PullRequest {
      number
      state
      reviewDecision
      headRefOid
      reviews(last: 20) {
        nodes { id state url body author { login __typename } }
      }
      reviewThreads(first: 100) {
        pageInfo { hasNextPage }
        nodes {
          id
          isResolved
          isOutdated
          path
          line
          originalLine
          comments(first: 20) {
            totalCount
            nodes { id body url diffHunk author { login __typename } }
          }
        }
      }
    }
  }
}`

// ErrNoAuth reports that gh has no usable credentials, which is a setup problem
// the user must fix once - not a transient failure worth retrying every poll.
var ErrNoAuth = errors.New("gh is not authenticated - run: gh auth login")

// Fetch returns the review state of the PR at prURL.
//
// repoDir only supplies gh's repository context, which is how it resolves the
// host - the same reason vcs.PRState takes one. It is not read from.
func Fetch(repoDir, prURL string) (*PR, error) {
	if strings.TrimSpace(prURL) == "" {
		return nil, errors.New("no PR url")
	}
	out, stderr, runErr := ghGraphQL(repoDir, threadsQuery, "url="+prURL)

	// gh exits non-zero on a GraphQL-level error but still prints the errors
	// array to stdout, and that array says far more than "exit status 1". Parse
	// first; fall back to the exit status only when there was nothing to parse.
	pr, err := parse(out)
	if err == nil {
		return pr, nil
	}
	if runErr != nil {
		if isAuthError(stderr) {
			return nil, ErrNoAuth
		}
		return nil, fmt.Errorf("gh api graphql: %w\n%s", runErr, strings.TrimSpace(stderr))
	}
	return nil, err
}

// ghGraphQL runs one query and returns stdout, stderr, and the exit error.
//
// Output() with a separate stderr buffer, never CombinedOutput(): gh writes
// upgrade notices and auth warnings to stderr, and folding those into stdout
// makes the JSON unparseable. vcs.PRState carries the same scar.
func ghGraphQL(repoDir, query string, fields ...string) (stdout []byte, stderrText string, err error) {
	args := []string{"api", "graphql", "-f", "query=" + query}
	for _, f := range fields {
		args = append(args, "-F", f)
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = repoDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	return out, stderr.String(), runErr
}

// isAuthError recognizes gh's several ways of saying "log in first".
func isAuthError(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "gh auth login") ||
		strings.Contains(s, "authentication required") ||
		strings.Contains(s, "not logged into") ||
		strings.Contains(s, "bad credentials")
}
