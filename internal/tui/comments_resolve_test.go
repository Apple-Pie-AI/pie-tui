package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// A gh on PATH that answers the two review mutations and logs every call, so a
// test can assert what reached GitHub and in what order.
func fakeGHForTUI(t *testing.T) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> '" + logPath + "'\n" +
		"case \"$*\" in\n" +
		"  *addPullRequestReviewThreadReply*) echo '{\"data\":{\"addPullRequestReviewThreadReply\":{\"comment\":{\"id\":\"PRRC_FAKE1\"}}}}' ;;\n" +
		"  *resolveReviewThread*) echo '{\"data\":{\"resolveReviewThread\":{\"thread\":{\"id\":\"T1\",\"isResolved\":true}}}}' ;;\n" +
		"  *) echo '{}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// Issue #16 AC1: the review-screen menu leads with Reply & resolve on a
// thread, and a review body (no thread to reply to) does not offer it.
func TestMenuLeadsWithReplyAndResolve(t *testing.T) {
	m := commentsModel()
	items := m.commentMenu()
	if len(items) == 0 || items[0].action != menuReply || items[0].label != "Reply & resolve" {
		t.Fatalf("first menu entry = %+v, want Reply & resolve", items)
	}
	m.noResolve = true
	if items := m.commentMenu(); items[0].label != "Reply" {
		t.Errorf("with review_resolve=false the label must be plain Reply, got %q", items[0].label)
	}

	m = commentsModel()
	m.comments.rows[0].Kind = review.KindReview
	m.comments.rows[0].Path = ""
	for _, it := range m.commentMenu() {
		if it.action == menuReply {
			t.Fatal("a review body offers Reply & resolve, but GitHub cannot resolve it")
		}
	}
}

// AC2: on the approve card the action sits directly under the primary button,
// and the cursor can reach every row.
func TestCardOffersReplyAndResolveUnderApprove(t *testing.T) {
	m := commentsModel()
	c := m.comments.rows[0]
	out := ansiRe.ReplaceAllString(m.renderCardActions(c, "", false, 80), "")
	ri, ai := strings.Index(out, "Reply & resolve"), strings.Index(out, "Ask for a different fix")
	if ri < 0 || ai < 0 || ri > ai {
		t.Fatalf("Reply & resolve must be the first row under the primary:\n%s", out)
	}
	m.comments.rows[0].Queued = true
	m.comments.cardIdx = 0
	if n := m.cardActionCount(); n != 5 {
		t.Errorf("cardActionCount = %d, want 5 (primary + 4 rows)", n)
	}
}

// AC3/4/5/6, end to end from the menu: Enter on Reply & resolve, type, Enter.
// gh receives the reply then the resolve (only the resolve on empty text; no
// resolve when review_resolve is off); the row is answered and closed; a
// parked fix-review whose batch just emptied returns to review.
func TestReplyAndResolveEndToEnd(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	logPath := fakeGHForTUI(t)
	st := openTestStore(t)
	if _, err := st.Claim("A-1", t.TempDir(), "x"); err != nil { // gh runs in the repo dir
		t.Fatal(err)
	}
	if err := st.SetFields("A-1", "b", "", "https://github.com/o/r/pull/482"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("A-1", store.StateFixReview, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("A-1", "https://github.com/o/r/pull/482", []review.Thread{
		{ID: "T1", Kind: review.KindThread, Author: "bob", Path: "a.kt", Line: 12, Body: "x", LastCommentID: "c1"},
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueComments("A-1", []string{"T1"}); err != nil {
		t.Fatal(err)
	}

	m := commentsModel()
	m.store = st
	m.reload()
	m.loadComments()
	m.comments.rows = m.comments.rows[:1]

	// The session is parked at fix-review, so the screen is the approve card:
	// row 1 under the primary is Reply & resolve. Pick it, type, Enter.
	if m.phase() != phaseApprove {
		t.Fatalf("phase = %v, want the approve card", m.phase())
	}
	m.comments.cardIdx, m.comments.cardSel = 0, 1
	m = press(m, "enter")
	if m.comments.cardMode != cardResolve {
		t.Fatalf("cardMode = %v, want the reply field", m.comments.cardMode)
	}
	m = press(m, "o", "k")
	got, cmd := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if cmd == nil {
		t.Fatal("Enter produced no command - nothing would reach GitHub")
	}
	msg := cmd()
	res, ok := msg.(threadResolvedMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want threadResolvedMsg", msg)
	}
	if res.err != nil || !res.replied || !res.resolved {
		t.Fatalf("result = replied:%v resolved:%v err:%v", res.replied, res.resolved, res.err)
	}
	log, _ := os.ReadFile(logPath)
	ri, si := strings.Index(string(log), "addPullRequestReviewThreadReply"), strings.Index(string(log), "resolveReviewThread")
	if ri < 0 || si < 0 || ri > si {
		t.Fatalf("gh must get the reply then the resolve:\n%s", log)
	}
	if !strings.Contains(string(log), "ok") {
		t.Errorf("the typed reply never reached gh:\n%s", log)
	}
	rows, _ := st.Comments("A-1")
	if !rows[0].Replied || !rows[0].Resolved || rows[0].Queued || rows[0].LastCommentID != "PRRC_FAKE1" {
		t.Fatalf("row after resolve = %+v", rows[0])
	}
	if open, _ := st.OpenComments("A-1"); len(open) != 0 {
		t.Errorf("thread still open locally: %+v", open)
	}
	// The batch emptied: the park is over.
	if s, _ := st.Get("A-1"); s.State != store.StateReview {
		t.Errorf("session = %q, want review (nothing left to approve)", s.State)
	}
	if !res.unparked {
		t.Error("msg must say the ticket left fix-review")
	}
	got, _ = m.Update(res)
	if n := got.(monitorModel).notice; !strings.Contains(n, "resolved") || !strings.Contains(n, "nothing left to approve") {
		t.Errorf("notice = %q", n)
	}

	// Empty text: resolve only.
	os.Remove(logPath)
	if err := st.UpsertThreads("A-1", "https://github.com/o/r/pull/482", []review.Thread{
		{ID: "T2", Kind: review.KindThread, Author: "bob", Path: "b.kt", Line: 3, Body: "y", LastCommentID: "c2"},
	}, false); err != nil {
		t.Fatal(err)
	}
	s, _ := st.Get("A-1")
	msg = replyResolveCmd(st, *s, store.Comment{ID: "T2", Kind: review.KindThread, Path: "b.kt", Line: 3}, "", true)()
	log, _ = os.ReadFile(logPath)
	if strings.Contains(string(log), "addPullRequestReviewThreadReply") || !strings.Contains(string(log), "resolveReviewThread") {
		t.Errorf("empty text must resolve without replying:\n%s", log)
	}
	if r := msg.(threadResolvedMsg); r.replied || !r.resolved {
		t.Errorf("resolve-only result = %+v", r)
	}

	// review_resolve=false: reply, never resolve.
	os.Remove(logPath)
	msg = replyResolveCmd(st, *s, store.Comment{ID: "T2", Kind: review.KindThread, Path: "b.kt", Line: 3}, "thanks", false)()
	log, _ = os.ReadFile(logPath)
	if strings.Contains(string(log), "resolveReviewThread") || !strings.Contains(string(log), "addPullRequestReviewThreadReply") {
		t.Errorf("with resolve off gh must only get the reply:\n%s", log)
	}
}

// AC7: gh fails - nothing is recorded, the notice carries the error, and the
// typed text comes back into the field so Enter retries.
func TestReplyAndResolveFailureKeepsTheText(t *testing.T) {
	m := commentsModel()
	m.comments.mode = modeBrowsing
	got, _ := m.Update(threadResolvedMsg{ticket: "A-1", id: "T1", text: "my answer",
		err: os.ErrPermission})
	hub := got.(monitorModel)
	if !strings.Contains(hub.notice, "permission") {
		t.Errorf("notice = %q, want the gh error", hub.notice)
	}
	if hub.comments.mode != modeReplying || hub.comments.nb.text() != "my answer" {
		t.Errorf("the typed reply must come back for a retry: mode=%v text=%q", hub.comments.mode, hub.comments.nb.text())
	}
}

// A declined thread's primary is not a dead key: it opens the reply field
// pre-filled with the agent's answer, so one review + Enter posts it.
func TestDeclinedPrimaryOpensPrefilledReply(t *testing.T) {
	m := approveModel(t)
	m.width, m.height = 120, 40
	// Card 0 is the declined T2 (declined fixes lead the batch).
	m.comments.cardIdx, m.comments.cardSel = 0, 0
	got, _ := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if m.comments.cardMode != cardResolve {
		t.Fatalf("cardMode = %v, want the reply field", m.comments.cardMode)
	}
	if m.comments.nb.text() != "version bumps are out of scope for this PR" {
		t.Fatalf("the field must carry the agent's answer, got %q", m.comments.nb.text())
	}
	// The pre-filled reply is visible - typing was blind here once.
	out := ansiRe.ReplaceAllString(m.View(), "")
	if !strings.Contains(out, "version bumps are out of scope") ||
		!strings.Contains(out, letterSpace("YOUR REPLY - IT POSTS NOW, NO AGENT")) {
		t.Errorf("the reply field and its content must render:\n%s", out)
	}
	// Esc backs out without a decision - the card stays undecided.
	got, _ = m.updateComments(keyOf("esc"))
	m = got.(monitorModel)
	if m.comments.cardMode != cardView || m.comments.decisions["T2"] != "" {
		t.Errorf("esc must cancel cleanly: mode=%v decision=%q", m.comments.cardMode, m.comments.decisions["T2"])
	}
}

// A declined review body has no reply target on GitHub: there the primary
// stays inert and the old ask-again-or-skip meta stands.
func TestDeclinedReviewBodyPrimaryStaysInert(t *testing.T) {
	m := approveModel(t)
	m.width, m.height = 120, 40
	for i := range m.comments.rows {
		if m.comments.rows[i].ID == "T2" {
			m.comments.rows[i].Kind = review.KindReview
		}
	}
	m.comments.cardIdx, m.comments.cardSel = 0, 0
	got, _ := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if m.comments.cardMode != cardView || m.comments.decisions["T2"] != "" {
		t.Fatalf("a review body's primary must stay inert: mode=%v decision=%q",
			m.comments.cardMode, m.comments.decisions["T2"])
	}
	out := ansiRe.ReplaceAllString(m.View(), "")
	if !strings.Contains(out, "nothing was fixed - ask again or skip") {
		t.Errorf("the inert meta must say why:\n%s", out)
	}
}

// The card's Reply & resolve field renders what is typed - it was invisible
// once (cardResolve had no render branch), which made the reply a blind type.
func TestCardResolveInputIsVisible(t *testing.T) {
	m := approveModel(t)
	m.width, m.height = 120, 40
	m.comments.cardIdx, m.comments.cardSel = 0, 1 // Reply & resolve on card 0
	got, _ := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if m.comments.cardMode != cardResolve {
		t.Fatalf("cardMode = %v, want the reply field", m.comments.cardMode)
	}
	m = press(m, "h", "i")
	out := ansiRe.ReplaceAllString(m.View(), "")
	if !strings.Contains(out, "hi") {
		t.Errorf("typed reply must be visible on the card:\n%s", out)
	}
}

// Edit the reply on an answer card edits the agent's note in place: the field
// opens pre-filled and visible, and saving keeps it an answer card - writing
// it to DraftReply would silently turn it into a fix card and ship the answer
// with a bogus "Fixed in <sha>" suffix.
func TestEditReplyOnAnswerCardEditsTheNote(t *testing.T) {
	m := approveModel(t)
	m.width, m.height = 120, 40
	m.comments.cardIdx, m.comments.cardSel = 0, 3 // Edit the reply on declined T2
	got, _ := m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	if m.comments.cardMode != cardReply {
		t.Fatalf("cardMode = %v, want the edit field", m.comments.cardMode)
	}
	if m.comments.nb.text() != "version bumps are out of scope for this PR" {
		t.Fatalf("the field must open on the agent's answer, got %q", m.comments.nb.text())
	}
	out := ansiRe.ReplaceAllString(m.View(), "")
	if !strings.Contains(out, "version bumps are out of scope") ||
		!strings.Contains(out, "enter save") {
		t.Errorf("the editor must render on the declined card:\n%s", out)
	}
	m.comments.nb.setText("Deliberate: version bumps ship separately.")
	got, _ = m.updateComments(keyOf("enter"))
	m = got.(monitorModel)
	rows, err := m.store.Comments("A-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID != "T2" {
			continue
		}
		if r.AgentNote != "Deliberate: version bumps ship separately." {
			t.Errorf("AgentNote = %q, want the edited answer", r.AgentNote)
		}
		if r.DraftReply != "" {
			t.Errorf("DraftReply = %q - the answer card must not become a fix card", r.DraftReply)
		}
	}
}
