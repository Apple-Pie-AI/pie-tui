// The answer-and-resume overlay. The answer is an ordered list of atoms - typed
// runes and pasted blobs interleaved - so a paste shows inline as "[N lines
// added]" right where the cursor was, exactly like Claude Code.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
	tea "github.com/charmbracelet/bubbletea"
)

// pasteInlineMaxRunes is the cutoff below which a single-line paste is dropped
// in as literal typed text instead of a collapsed "[1 line added]" chip.
const pasteInlineMaxRunes = 100

// inputAtom is one unit of the answer input. A typed character carries a rune;
// a paste carries its whole body in blob (and r is zero). Each atom is a single
// cursor stop, so backspace over a paste removes the entire "[N lines added]"
// chunk at once - matching Claude Code's paste-as-one-token behaviour.
type inputAtom struct {
	r    rune   // typed character (when blob == "")
	blob string // pasted body (when non-empty)
}

// atomInput is a single-line atom-based input with a cursor. Two screens need
// exactly this - the answer overlay and the plan-review feedback box - and each
// carried its own atoms/cursor field pair plus its own copy of the same paste,
// backspace and insert logic. One of them is always the stale one.
//
// It deliberately handles only the editing keys. Screens that overload the
// arrow keys for something else (the feedback box scrolls the plan with ↑/↓)
// keep those bindings by handling them before delegating here.
type atomInput struct {
	atoms  []inputAtom
	cursor int
}

// text reassembles the atoms into the string sent to the agent: typed runes
// verbatim, pastes expanded to their full body in place.
func (in atomInput) text() string {
	var b strings.Builder
	for _, a := range in.atoms {
		if a.blob != "" {
			b.WriteString(a.blob)
		} else {
			b.WriteRune(a.r)
		}
	}
	return b.String()
}

// clear empties the buffer and parks the cursor at the start.
func (in *atomInput) clear() { in.atoms, in.cursor = nil, 0 }

// insert places one atom at the cursor and advances past it.
func (in *atomInput) insert(a inputAtom) {
	out := make([]inputAtom, 0, len(in.atoms)+1)
	out = append(out, in.atoms[:in.cursor]...)
	out = append(out, a)
	out = append(out, in.atoms[in.cursor:]...)
	in.atoms, in.cursor = out, in.cursor+1
}

// paste drops bracketed-paste content in at the cursor. A short single-line
// paste (a URL, an identifier, a phrase) reads better as literal text than
// hidden behind an "[1 line added]" chip; anything multi-line or long stays a
// single collapsed atom, so backspace removes it in one stroke.
func (in *atomInput) paste(raw string) {
	blob := ticket.NormalizeNewlines(raw)
	if blob == "" {
		return
	}
	if oneLine := strings.TrimRight(blob, "\n"); !strings.Contains(oneLine, "\n") &&
		len([]rune(oneLine)) <= pasteInlineMaxRunes {
		for _, ch := range oneLine {
			in.insert(inputAtom{r: ch})
		}
		return
	}
	in.insert(inputAtom{blob: blob})
}

// setText fills the input with s as individually editable runes - the prefill
// path for editing existing text. paste() would collapse it into an
// "[1 line added]" chip, which is right for dropping foreign text into an
// empty field and exactly wrong when the text IS the thing being edited.
// Newlines flatten to spaces: the input is one line and its consumers re-wrap.
func (in *atomInput) setText(s string) {
	in.clear()
	for _, ch := range strings.ReplaceAll(ticket.NormalizeNewlines(s), "\n", " ") {
		in.insert(inputAtom{r: ch})
	}
}

// key applies one editing keystroke, reporting whether it consumed it so
// callers can fall through to their own bindings.
func (in *atomInput) key(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyLeft:
		if in.cursor > 0 {
			in.cursor--
		}
	case tea.KeyRight:
		if in.cursor < len(in.atoms) {
			in.cursor++
		}
	case tea.KeyBackspace:
		if in.cursor > 0 {
			in.atoms = append(in.atoms[:in.cursor-1], in.atoms[in.cursor:]...)
			in.cursor--
		}
	case tea.KeySpace:
		in.insert(inputAtom{r: ' '})
	case tea.KeyRunes:
		for _, ch := range msg.Runes {
			in.insert(inputAtom{r: ch})
		}
	default:
		return false
	}
	return true
}

// render walks the atoms into one display line: typed runes verbatim, each
// paste shown inline as "[N lines added]", and "▌" where the cursor sits.
// Callers wrap the result to their own width.
func (in atomInput) render() string {
	var line strings.Builder
	for i, a := range in.atoms {
		if i == in.cursor {
			line.WriteString("▌")
		}
		if a.blob != "" {
			n := ticket.LineCount(a.blob)
			line.WriteString(fmt.Sprintf("[%d line%s added]", n, ticket.Plural(n)))
		} else {
			line.WriteRune(a.r)
		}
	}
	if in.cursor >= len(in.atoms) {
		line.WriteString("▌")
	}
	return line.String()
}

// updateAnswering handles keystrokes while the answer input box is open.
func (m monitorModel) updateAnswering(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Bracketed paste arrives as a KeyMsg with Paste=true regardless of Type.
	if msg.Paste {
		m.answer.in.paste(string(msg.Runes))
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEnter:
		full := strings.TrimSpace(m.answer.in.text())
		if full == "" {
			m.answer.reset()
			return m, nil
		}
		key := m.answer.ticket
		m.answer.reset()
		m.notice = "sending answer to " + key + "…"
		return m, m.submitAnswer(key, full)
	case tea.KeyEsc:
		m.answer.reset()
	default:
		m.answer.in.key(msg)
	}
	return m, nil
}

// submitAnswer appends the answer to the .md source file and relaunches. This
// only works for local tickets (SourcePath set). For Jira tickets the action
// is not offered - use "Open in Claude Code" to answer in the session directly.
func (m monitorModel) submitAnswer(key, answer string) tea.Cmd {
	if m.demo != nil {
		return m.demo.answered(key)
	}
	self, st := m.selfPath, m.store
	return func() tea.Msg {
		var sourcePath string
		reviewPlan := false
		if st != nil {
			if sess, err := st.Get(key); err == nil && sess != nil {
				sourcePath = sess.SourcePath
				reviewPlan = sess.ReviewPlan
			}
		}
		if sourcePath == "" {
			return answerDoneMsg{ticket: key, err: fmt.Errorf("answer via TUI only works for local .md tickets - use \"Open in Claude Code\" to answer in the session")}
		}
		raw, err := os.ReadFile(sourcePath)
		if err != nil {
			return answerDoneMsg{ticket: key, err: fmt.Errorf("read %s: %w", sourcePath, err)}
		}
		updated := strings.TrimSpace(string(raw)) + "\n\n---\nAnswers:\n" + answer
		if err := os.WriteFile(sourcePath, []byte(updated+"\n"), 0o644); err != nil {
			return answerDoneMsg{ticket: key, err: fmt.Errorf("write %s: %w", sourcePath, err)}
		}
		// Keep the plan-review gate across the re-run: a ticket that paused for the
		// plan should pause again after re-planning on the answered version.
		args := []string{"run", sourcePath}
		if reviewPlan {
			args = append(args, "--review-plan")
		}
		c := exec.Command(self, args...)
		if err := startDetached(c); err != nil {
			return answerDoneMsg{ticket: key, err: err}
		}
		return answerDoneMsg{ticket: key}
	}
}
