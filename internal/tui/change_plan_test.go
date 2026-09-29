package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// longTranscript builds n alternating you:/claude: lines - enough to exceed
// the full-screen chat's viewport (changeModel's 120x40) so PgUp/PgDown
// actually have something to scroll through.
func longTranscript(n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			out[i] = fmt.Sprintf("you: question number %d", i)
		} else {
			out[i] = fmt.Sprintf("claude: answer number %d", i)
		}
	}
	return out
}

// openChangeDiscussFull opens the box in Plan mode (the default) with a
// conversation already under way, so discussFullScreen() is true and the
// screen is showing renderChangeDiscussFull - the state every test in this
// file starts from.
func openChangeDiscussFull(t *testing.T, transcriptLines int) monitorModel {
	t.Helper()
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, empty, Plan mode (the default)
	m.change.transcript = longTranscript(transcriptLines)
	if m.change.mode != chBox || !m.change.discussFullScreen() {
		t.Fatalf("setup: mode=%v discussFullScreen=%v, want chBox + full-screen chat", m.change.mode, m.change.discussFullScreen())
	}
	return m
}

// The screen must actually be showing the full-screen chat (not the split
// file list) once a conversation is under way - the diff/file list's own
// markers ("Changed files", a tracked path) must be absent.
func TestChangeDiscussFullScreenHidesTheFileList(t *testing.T) {
	m := openChangeDiscussFull(t, 4)
	out := stripAnsiStr(m.View())
	if strings.Contains(out, "Changed files") {
		t.Fatalf("full-screen chat must hide the split file list:\n%s", out)
	}
	if !strings.Contains(out, "question number 2") {
		t.Fatalf("full-screen chat must show the transcript:\n%s", out)
	}
}

// PgUp/PgDown scroll the transcript while the box is focused - free keys
// today (see updateChangeBox), only meaningful once the full-screen chat is
// showing. Starting at the tail (-1), one PgUp must move away from it and
// reveal earlier lines with the "earlier line(s)" hint; paging back down
// enough must return to stick-to-tail mode.
func TestChangeDiscussFullScreenScrollsWithPgUpPgDown(t *testing.T) {
	m := openChangeDiscussFull(t, 60)
	if m.change.transcriptScroll != -1 {
		t.Fatalf("transcriptScroll = %d, want -1 (stick to tail) on open", m.change.transcriptScroll)
	}
	// 60 lines outgrow one screen, so even at the tail some are hidden above
	// - the hint is about "is everything visible", not "are we scrolled";
	// what matters here is that the newest line is on screen and the
	// oldest ("question number 0") is not.
	before := stripAnsiStr(m.View())
	if !strings.Contains(before, "answer number 59") {
		t.Fatalf("at the tail, the newest line must be visible:\n%s", before)
	}
	if strings.Contains(before, "question number 0") {
		t.Fatalf("at the tail, the very first line should be scrolled off:\n%s", before)
	}

	got, _ := m.updateChange(tea.KeyMsg{Type: tea.KeyPgUp})
	m = got.(monitorModel)
	if m.change.transcriptScroll < 0 {
		t.Fatal("PgUp must move transcriptScroll off the -1 sentinel")
	}
	after := stripAnsiStr(m.View())
	if !strings.Contains(after, "earlier line") {
		t.Fatalf("scrolled up, the earlier-lines hint must show:\n%s", after)
	}
	if strings.Contains(after, "answer number 59") {
		t.Fatalf("scrolled up, the newest line must no longer be visible:\n%s", after)
	}

	// Page all the way to the top: the very first line must eventually show,
	// and paging further up must not go negative or panic.
	for i := 0; i < 20; i++ {
		got, _ = m.updateChange(tea.KeyMsg{Type: tea.KeyPgUp})
		m = got.(monitorModel)
	}
	top := stripAnsiStr(m.View())
	if !strings.Contains(top, "question number 0") {
		t.Fatalf("paging all the way up must reach the first line:\n%s", top)
	}

	// Page back down enough times to reach the tail again.
	for i := 0; i < 20 && m.change.transcriptScroll >= 0; i++ {
		got, _ = m.updateChange(tea.KeyMsg{Type: tea.KeyPgDown})
		m = got.(monitorModel)
	}
	if m.change.transcriptScroll != -1 {
		t.Fatalf("transcriptScroll = %d after paging down, want back to -1 (stick to tail)", m.change.transcriptScroll)
	}
	final := stripAnsiStr(m.View())
	if !strings.Contains(final, "answer number 59") {
		t.Fatalf("back at the tail, the newest line must be visible again:\n%s", final)
	}
}

// Ctrl+B/Ctrl+F are the PRIMARY scroll binding - real-terminal testing found
// PageUp/PageDown getting captured by the terminal's own scrollback before
// ever reaching pie (a plain control byte like ctrl+b doesn't have that
// problem, and it's the same "page back" binding less/vim use). They must
// drive the exact same scroll as PgUp/PgDown, not a parallel, possibly
// inconsistent mechanism.
func TestChangeDiscussFullScreenScrollsWithCtrlBCtrlF(t *testing.T) {
	m := openChangeDiscussFull(t, 60)
	got, _ := m.updateChange(tea.KeyMsg{Type: tea.KeyCtrlB})
	m = got.(monitorModel)
	if m.change.transcriptScroll < 0 {
		t.Fatal("ctrl+b must move transcriptScroll off the -1 sentinel")
	}
	afterUp := stripAnsiStr(m.View())
	if strings.Contains(afterUp, "answer number 59") {
		t.Fatalf("ctrl+b scrolled up, the newest line must no longer be visible:\n%s", afterUp)
	}
	if !strings.Contains(afterUp, "ctrl+b") {
		t.Fatalf("the earlier-lines hint must name the real, reliable binding:\n%s", afterUp)
	}

	got, _ = m.updateChange(tea.KeyMsg{Type: tea.KeyCtrlF})
	m = got.(monitorModel)
	if m.change.transcriptScroll != -1 {
		t.Fatalf("ctrl+f one page down from the first PgUp must return to the tail (-1), got %d", m.change.transcriptScroll)
	}
	afterDown := stripAnsiStr(m.View())
	if !strings.Contains(afterDown, "answer number 59") {
		t.Fatalf("ctrl+f back at the tail, the newest line must be visible:\n%s", afterDown)
	}
}

// PgUp/PgDown are no-ops outside the full-screen chat (composing the very
// first message, before any conversation exists) - they fall through to the
// textarea, which doesn't bind them either, so nothing should change.
func TestChangePgUpPgDownAreNoOpsBeforeTheChatIsFullScreen(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, empty - no transcript yet
	if m.change.discussFullScreen() {
		t.Fatal("setup: an empty box with no transcript must not be full-screen yet")
	}
	before := m.change.transcriptScroll
	got, _ := m.updateChange(tea.KeyMsg{Type: tea.KeyPgUp})
	m = got.(monitorModel)
	if m.change.transcriptScroll != before {
		t.Errorf("PgUp before any conversation changed transcriptScroll: %d -> %d", before, m.change.transcriptScroll)
	}
}

// The mouse wheel scrolls the full-screen chat, the same "notch-sized step"
// convention the diff viewer already uses (changeScrollStep) - the fix for
// the actual root cause of "PgUp doesn't scroll": a lot of terminals
// (including the one this was found in) intercept PageUp/PageDown for their
// OWN scrollback before the key ever reaches pie, but once a program enables
// mouse reporting (openChangeBox's tea.EnableMouseCellMotion), wheel events
// route to the program instead - the same mechanism Claude Code's own CLI
// relies on for its scroll-with-the-wheel behavior.
func TestChangeDiscussFullScreenScrollsWithMouseWheel(t *testing.T) {
	m := openChangeDiscussFull(t, 60)
	got, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m = got.(monitorModel)
	if m.change.transcriptScroll < 0 {
		t.Fatal("wheel up must move transcriptScroll off the -1 sentinel")
	}
	// One notch is a small step (changeScrollStep), not a full page - the
	// page-sized jump is ctrl+b/ctrl+f's job (TestChangeDiscussFullScreenScrollsWithCtrlBCtrlF).
	_, cw := layout(m.width)
	budget := m.changeDiscussBudget(cw)
	if want := 60 - budget - changeScrollStep; m.change.transcriptScroll != want {
		t.Fatalf("one wheel-up notch = %d, want %d (a %d-line step from the tail)", m.change.transcriptScroll, want, changeScrollStep)
	}
	afterOneNotch := m.change.transcriptScroll

	got, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m = got.(monitorModel)
	if m.change.transcriptScroll >= afterOneNotch {
		t.Fatalf("a second wheel-up notch must move further up: %d -> %d", afterOneNotch, m.change.transcriptScroll)
	}

	// Enough wheel-down notches must return to the tail.
	for i := 0; i < 30 && m.change.transcriptScroll >= 0; i++ {
		got, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
		m = got.(monitorModel)
	}
	if m.change.transcriptScroll != -1 {
		t.Fatalf("transcriptScroll = %d after wheeling down, want back to -1 (stick to tail)", m.change.transcriptScroll)
	}
}

// The wheel must not touch the transcript before there's a conversation to
// scroll (composing the very first message - split view, or the box not yet
// full-screen) - it's a no-op there, not an error, mirroring
// TestChangePgUpPgDownAreNoOpsBeforeTheChatIsFullScreen.
func TestChangeWheelIsANoOpBeforeTheChatIsFullScreen(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, empty - no transcript yet
	before := m.change.transcriptScroll
	got, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m = got.(monitorModel)
	if m.change.transcriptScroll != before {
		t.Errorf("wheel before any conversation changed transcriptScroll: %d -> %d", before, m.change.transcriptScroll)
	}
}

// funcPointer identifies a func value by its entry point - tea.Cmd values
// aren't otherwise comparable, and bubbletea's own enable/disable-mouse
// messages are unexported types this package can't type-assert against, so
// this is the only way to confirm which specific tea.Cmd came back.
func funcPointer(f interface{}) uintptr {
	return reflect.ValueOf(f).Pointer()
}

// Opening the box enables mouse capture (so the wheel can scroll a
// conversation the instant one exists); leaving it gives the terminal its
// mouse back, UNLESS we're returning to the full-screen file view, which
// owns the wheel for its own diff scroll and must not have it cut out from
// under it by the box closing.
func TestChangeBoxMouseCaptureEnabledOnOpenDisabledOnClose(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	got, cmd := m.updateChange(keyOf("down")) // open the box
	m = got.(monitorModel)
	if cmd == nil {
		t.Fatal("opening the box must return a command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		batch = tea.BatchMsg{cmd}
	}
	var sawEnableMouse bool
	for _, c := range batch {
		if c != nil && funcPointer(c) == funcPointer(tea.EnableMouseCellMotion) {
			sawEnableMouse = true
		}
	}
	if !sawEnableMouse {
		t.Fatal("opening the box must enable mouse capture (tea.EnableMouseCellMotion)")
	}

	got, cmd = m.updateChange(tea.KeyMsg{Type: tea.KeyEsc})
	m = got.(monitorModel)
	if m.change.mode != chSplit {
		t.Fatalf("esc on an empty box must return to the split list, mode=%v", m.change.mode)
	}
	if cmd == nil || funcPointer(cmd) != funcPointer(tea.DisableMouse) {
		t.Fatal("closing the box back to the split list must give the mouse back (tea.DisableMouse)")
	}
}

// Closing the box back into the full-screen FILE view (a line comment's
// origin) must NOT give the mouse back - chFull/chScroll still needs it for
// their own diff-scroll wheel handling, enabled on their own entry.
func TestChangeBoxClosingToFullScreenFileViewKeepsMouseCapture(t *testing.T) {
	m := changeModel(t)
	m.change.mode = chScroll
	m.change.fileIdx = 0
	got, _ := m.commentOnCurrentLine()
	m = got.(monitorModel)
	if m.change.mode != chBox || m.change.boxOrigin.mode != chScroll {
		t.Fatalf("setup: commentOnCurrentLine must open the box from chScroll, mode=%v origin=%v", m.change.mode, m.change.boxOrigin.mode)
	}

	got, cmd := m.updateChange(tea.KeyMsg{Type: tea.KeyEsc})
	m = got.(monitorModel)
	if m.change.mode != chScroll {
		t.Fatalf("esc must return to chScroll (the box's origin), got mode=%v", m.change.mode)
	}
	if cmd != nil && funcPointer(cmd) == funcPointer(tea.DisableMouse) {
		t.Fatal("closing the box back into the full-screen file view must not disable mouse capture - chScroll still needs it")
	}
}

// A live thinking-tokens preview (change_live, "thinking: " prefixed)
// replaces the static "claude is thinking…" placeholder and renders
// distinctly from a real committed line - it must never be confused with
// actual reasoning prose (the CLI's thinking_delta text is always empty in
// practice - see internal/agent's stream_partial_test.go - so this token
// count is the only live signal there is).
func TestChangeLiveThinkingTokensReplaceTheStaticPlaceholder(t *testing.T) {
	m := openChangeDiscussFull(t, 2)
	m.change.discussing = true
	m.change.live = "thinking: (~1,850 tokens so far)"

	out := stripAnsiStr(m.View())
	if !strings.Contains(out, "1,850 tokens so far") {
		t.Fatalf("live thinking-tokens preview not rendered:\n%s", out)
	}
	if strings.Contains(out, "claude is thinking…") {
		t.Fatalf("the static placeholder must not show once a live preview exists:\n%s", out)
	}
}

// Without any live content, a running round still falls back to the static
// placeholder - the graceful-degradation path for an older claude build, or
// a turn that streamed nothing yet.
func TestChangeDiscussingWithNoLiveContentShowsStaticPlaceholder(t *testing.T) {
	m := openChangeDiscussFull(t, 2)
	m.change.discussing = true
	m.change.live = ""

	out := stripAnsiStr(m.View())
	if !strings.Contains(out, "claude is thinking…") {
		t.Fatalf("must fall back to the static placeholder with no live content:\n%s", out)
	}
}

// A live growing answer (no "thinking: " prefix) renders as plain streamed
// text, not as a thinking line - and once it's showing, the static
// placeholder must not also appear (only one "is this turn done yet"
// indicator at a time).
func TestChangeLiveAnswerTextRendersWithoutThinkingPrefix(t *testing.T) {
	m := openChangeDiscussFull(t, 2)
	m.change.discussing = true
	m.change.live = "It renders the home"

	out := stripAnsiStr(m.View())
	if !strings.Contains(out, "It renders the home") {
		t.Fatalf("live answer text not rendered:\n%s", out)
	}
	if strings.Contains(out, "claude is thinking…") {
		t.Fatalf("a growing answer must replace the placeholder, not sit beside it:\n%s", out)
	}
}
