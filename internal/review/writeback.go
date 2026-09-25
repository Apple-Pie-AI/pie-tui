// Closing the loop on GitHub after the agent's fix is pushed: telling each
// reviewer what happened, and marking the thread done.
//
// Both mutations key off the same thread node id the fetch already returned, so
// nothing extra needs storing. Neither is ever fatal to a run - the code is
// already committed and pushed by the time these are called, and a PR whose
// threads went un-replied is a cosmetic problem, not a lost fix.
package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const replyMutation = `mutation($threadId: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $threadId, body: $body}) {
    comment { id }
  }
}`

const resolveMutation = `mutation($threadId: ID!) {
  resolveReviewThread(input: {threadId: $threadId}) {
    thread { id isResolved }
  }
}`

// Reply posts body as a new comment on an existing review thread and returns
// the new comment's node id.
//
// The id is not a nicety: the caller must advance the thread's stored
// last_comment to it, or the next poll sees "the last comment changed" - the
// exact pivot that means "the reviewer came back" - and reopens a thread that
// only we spoke on, resurrecting every comment the run just addressed.
//
// Only inline threads (KindThread) can be replied to; a review's summary body
// is not a thread and has no reply target.
func Reply(repoDir, threadID, body string) (commentID string, err error) {
	if threadID == "" || strings.TrimSpace(body) == "" {
		return "", errors.New("reply: empty thread id or body")
	}
	out, err := mutate(repoDir, replyMutation, "threadId="+threadID, "body="+body)
	if err != nil {
		return "", err
	}
	return replyCommentID(out), nil
}

// Resolve marks a review thread resolved.
func Resolve(repoDir, threadID string) error {
	if threadID == "" {
		return errors.New("resolve: empty thread id")
	}
	_, err := mutate(repoDir, resolveMutation, "threadId="+threadID)
	return err
}

// mutate runs a mutation, reports the first GraphQL error it comes back with,
// and otherwise hands back the response body for the caller to read.
func mutate(repoDir, query string, fields ...string) ([]byte, error) {
	out, stderr, runErr := ghGraphQL(repoDir, query, fields...)
	// Same ordering as Fetch: a GraphQL error is reported in the body even when
	// the process exits non-zero, and it is the far more useful message.
	if msg := graphQLError(out); msg != "" {
		return nil, errors.New("graphql: " + msg)
	}
	if runErr != nil {
		if isAuthError(stderr) {
			return nil, ErrNoAuth
		}
		return nil, fmt.Errorf("gh api graphql: %w\n%s", runErr, strings.TrimSpace(stderr))
	}
	return out, nil
}

// replyCommentID pulls the created comment's node id out of a reply response.
// "" when the shape is unexpected - the reply still happened, so the caller
// must treat a missing id as cosmetic, never as a failed reply.
func replyCommentID(raw []byte) string {
	var resp struct {
		Data struct {
			Add struct {
				Comment struct {
					ID string `json:"id"`
				} `json:"comment"`
			} `json:"addPullRequestReviewThreadReply"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ""
	}
	return resp.Data.Add.Comment.ID
}

// graphQLError returns the first error message in a response, or "" when the
// body carries none (including when it is not JSON at all).
//
// Deliberately not parse(): that one also rejects a body for carrying no
// `resource`, which every mutation payload legitimately does.
func graphQLError(raw []byte) string {
	var resp struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || len(resp.Errors) == 0 {
		return ""
	}
	return resp.Errors[0].Message
}
