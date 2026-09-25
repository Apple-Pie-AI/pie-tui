package review

import "testing"

// The reply response carries the new comment's node id, and the caller advances
// the thread's last_comment to it - "" on any unexpected shape must mean "reply
// happened, id unknown", never an error.
func TestReplyCommentID(t *testing.T) {
	good := []byte(`{"data":{"addPullRequestReviewThreadReply":{"comment":{"id":"PRRC_kwDONew123"}}}}`)
	if got := replyCommentID(good); got != "PRRC_kwDONew123" {
		t.Errorf("replyCommentID = %q, want PRRC_kwDONew123", got)
	}
	for name, raw := range map[string][]byte{
		"empty":     []byte(``),
		"not json":  []byte(`gh: connection reset`),
		"no data":   []byte(`{}`),
		"null body": []byte(`{"data":{"addPullRequestReviewThreadReply":null}}`),
	} {
		if got := replyCommentID(raw); got != "" {
			t.Errorf("%s: replyCommentID = %q, want \"\"", name, got)
		}
	}
}
