// The review-comment triage screen: master-detail.
//
// The top pane lists every unresolved comment; the bottom pane shows the code
// and full text of whichever row the cursor is on. Every comment starts
// selected, so the work is deselecting exceptions rather than assembling a
// batch - the common answer to a review is "yes, all of these".
//
// One list, one cursor, one primary action. Up and down move, enter acts on the
// row under the cursor, left and right reach that row's two secondary actions,
// esc backs out. There is no second menu and there are no letter shortcuts:
// every affordance on this screen is a row in the one list, including "all
// comments" (bulk check) and the run bar (the primary action).
//
// Nothing is ever hidden. A reviewer's comment that the screen filtered away is
// a comment that ships unanswered, so bots are tinted rather than suppressed.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// codeRadius is how many lines of the real file to show around a comment's
// anchor - about the enclosing block, which is what makes a request judgeable
// without leaving the screen.
const codeRadius = 4

// commentsState is the triage screen.
//
// cursor indexes the comments and then the run bar: cursor == len(visible) is
// the run bar. Keeping it in one index is what lets up/down walk the whole
// screen without the run bar needing a key of its own.
type commentsState struct {
	ticket   string
	prLabel  string // "PR #482", for the header
	worktree string // where the reviewed code is, for the code pane

	rows    []store.Comment
	cursor  int             // cursorAll..len(visible) - the last position is the run bar
	sel     map[string]bool // comment id → Apple Pie will fix it
	visited map[string]bool // comment id → the cursor has landed on it

	// mode is which of the three key routers is live: the list, an open comment
	// menu, or the instruction field. Checked at the top of the router and
	// nowhere else - a key reaching the list while a menu is open is the whole
	// class of bug this field exists to make impossible.
	mode     commentsMode
	menuItem int // which entry of the open menu is picked

	nb atomInput

	// posting: thread id → a hand-written reply is on its way to GitHub. One
	// request per thread; the row reads "posting…" meanwhile.
	posting map[string]bool

	// The card screen (the approve phase): which card, which action row, and
	// every decision made so far. redoSent/reworked track fixes out with the
	// agent; redoInFlight/redoWasActive detect the rework's round trip.
	cardIdx                     int
	cardSel                     int
	cardMode                    int
	diffScroll                  int
	decisions                   map[string]string
	redoNotes                   map[string]string
	redoSent                    map[string]bool
	reworked                    map[string]bool
	redoInFlight, redoWasActive bool

	// The approve phase (session parked at fix-review): the worktree diff the
	// cards show.
	diff         map[string][]string
	diffErr      string
	diffLoaded   bool
	diffFetching bool

	code   []codeLine // source around the focused comment's anchor
	lines  []string   // rendered body of the focused comment
	scroll int

	// ran is the batch this screen submitted, and it is what keeps the finished
	// comments on screen: once fixed they are no longer "open", so without it
	// the result of the run would vanish the moment it succeeded.
	ran       map[string]bool
	startedAt time.Time

	fetching   bool
	syncFailed bool // the last GitHub fetch failed; the header says so
	lastFetch  time.Time
}

// reset prepares the screen for a ticket; loadComments then selects every
// comment it finds (see applySelectionDefaults).
func (c *commentsState) reset(ticket, prLabel, worktree string) {
	*c = commentsState{
		ticket:    ticket,
		prLabel:   prLabel,
		worktree:  worktree,
		sel:       map[string]bool{},
		visited:   map[string]bool{},
		posting:   map[string]bool{},
		ran:       map[string]bool{},
		decisions: map[string]string{},
		redoNotes: map[string]string{},
		redoSent:  map[string]bool{},
		reworked:  map[string]bool{},
	}
}

// visible is the comments the screen lists: every unresolved thread, and the
// batch this screen ran.
//
// There is no filter and no hiding. A bot's comment is listed exactly like a
// human's, tinted and nothing more - the one time hiding them by default was
// tried, a blocking bot comment and a CVE went with them, which is the whole
// argument. The list is the review, not a view of it.
func (m monitorModel) visible() []store.Comment {
	out := make([]store.Comment, 0, len(m.comments.rows))
	for _, c := range m.comments.rows {
		if c.Open() || m.comments.ran[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// cursorAll is the aggregate row: the first row of the list, above the first
// comment. It is a row like any other - same box, same cursor, same enter - so
// the one bulk control on the screen teaches the user nothing new.
const cursorAll = -1

// hasAllRow reports whether the aggregate row is drawn. It is a control for
// assembling a batch, so it is gone the moment the batch is with the agent.
func (m monitorModel) hasAllRow() bool {
	return m.phase() == phaseIdle && len(m.visible()) > 0
}

// onAllRow reports whether the cursor is on the aggregate row.
func (m monitorModel) onAllRow() bool {
	return m.comments.cursor == cursorAll && m.hasAllRow()
}

// allChecked reports whether every listed comment is selected.
func (m monitorModel) allChecked() bool {
	vis := m.visible()
	if len(vis) == 0 {
		return false
	}
	for _, c := range vis {
		if !m.comments.sel[c.ID] {
			return false
		}
	}
	return true
}

// toggleAll checks everything, or clears it when everything is already checked.
func (m *monitorModel) toggleAll() {
	on := !m.allChecked()
	for _, c := range m.visible() {
		m.comments.sel[c.ID] = on
	}
}

// applySelectionDefaults answers for every comment the screen has not been told
// about yet. The default is "fix it" - that is the common answer to a review,
// and it makes the work deselecting exceptions rather than assembling a batch.
func (m *monitorModel) applySelectionDefaults() {
	for _, c := range m.visible() {
		if _, known := m.comments.sel[c.ID]; !known {
			m.comments.sel[c.ID] = true
		}
	}
}

// focused returns the comment under the cursor, or nil when the cursor is on
// the run bar (or there is nothing to show).
func (m monitorModel) focused() *store.Comment {
	vis := m.visible()
	if m.comments.cursor < 0 || m.comments.cursor >= len(vis) {
		return nil
	}
	return &vis[m.comments.cursor]
}

// onRunBar reports whether the cursor is on the run bar.
func (m monitorModel) onRunBar() bool {
	return m.comments.cursor == len(m.visible()) &&
		len(m.visible()) > 0 && m.phase() != phaseRunning
}

// selectedIDs returns the comments Apple Pie will fix, in display order.
func (m monitorModel) selectedIDs() []string {
	var out []string
	for _, c := range m.visible() {
		if m.comments.sel[c.ID] {
			out = append(out, c.ID)
		}
	}
	return out
}

// openCommentsView opens the screen for a session.
func (m monitorModel) openCommentsView(s store.Session) (tea.Model, tea.Cmd) {
	if s.PRURL == "" {
		m.notice = "no PR recorded for " + s.Ticket + " yet"
		return m, nil
	}
	m.comments.reset(s.Ticket, prLabel(s.PRURL), paths.WorktreeFor(s.Repo, s.Ticket))
	m.loadComments() // selects every listed comment; see applySelectionDefaults
	m.view = viewComments
	m.notice = ""
	m.renderFocused()
	// Opening straight onto a fix-review session wants the worktree diff now,
	// not at the next tick.
	if cmd := m.approveDiffIfNeeded(); cmd != nil {
		return m, tea.Batch(m.fetchComments(s, false), cmd)
	}
	return m, m.fetchComments(s, false)
}

// prLabel turns a PR URL into "PR #482" for the header, falling back to "PR"
// when the URL is not the shape we expect.
func prLabel(prURL string) string {
	if i := strings.LastIndex(prURL, "/"); i >= 0 && i+1 < len(prURL) {
		if n := prURL[i+1:]; n != "" && strings.IndexFunc(n, notDigit) < 0 {
			return "PR #" + n
		}
	}
	return "PR"
}

func notDigit(r rune) bool { return r < '0' || r > '9' }

// plural renders "1 comment" / "3 comments" so counts read like English.
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// renderFocused re-renders the focused comment's body and the code it points
// at. An outdated comment's line refers to code later commits moved, so the
// worktree is not read at that line - the diff it was written against is shown
// instead.
func (m *monitorModel) renderFocused() {
	c := m.focused()
	if c == nil {
		m.comments.lines, m.comments.code, m.comments.scroll = nil, nil, 0
		return
	}
	m.comments.visited[c.ID] = true
	// The severity marker is lifted into the quote header, so leaving "nit:" in
	// the prose would state it twice.
	body := c.Body
	if tag, rest := severity(body); tag != "" {
		body = rest
	}
	m.comments.lines = trimBlankEdges(renderMarkdownLines(body, m.quoteWidth()))
	m.comments.scroll = 0
	if c.Outdated {
		m.comments.code = nil
		return
	}
	m.comments.code = codeContext(m.comments.worktree, c.Path, c.Line, codeRadius)
}

// loadComments re-reads the ticket's rows from the store, keeping the cursor in
// range as comments arrive or resolve.
func (m *monitorModel) loadComments() {
	if m.store == nil || m.comments.ticket == "" {
		return
	}
	rows, err := m.store.Comments(m.comments.ticket)
	if err != nil {
		m.notice = "reading review comments: " + err.Error()
		return
	}
	m.comments.rows = rows
	if n := len(m.visible()); m.comments.cursor > n {
		m.comments.cursor = n
	}
	// The floor is the aggregate row when it is drawn. Clamping to 0
	// unconditionally would knock the cursor off it on every 1Hz reload.
	if min := m.cursorFloor(); m.comments.cursor < min {
		m.comments.cursor = min
	}
	// A comment that arrived since the screen opened gets an answer too.
	m.applySelectionDefaults()
}

// cursorFloor is the topmost cursor position: the aggregate row when it is
// drawn, otherwise the first comment.
func (m monitorModel) cursorFloor() int {
	if m.hasAllRow() {
		return cursorAll
	}
	return 0
}

// trimBlankEdges drops the leading and trailing blank rows glamour pads its
// output with: behind the quote spine they render as "│" followed by nothing,
// which reads as a hole in the reviewer's comment.
func trimBlankEdges(lines []string) []string {
	blank := func(s string) bool { return strings.TrimSpace(stripAnsi(s)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// stripAnsi removes escape sequences so a styled-but-empty line still counts as
// blank.
//
// OSC sequences (the hyperlink around a comment's URL) are handled separately
// from CSI: they carry a payload of ordinary printable text and run until BEL or
// a string terminator, so the CSI rule would stop at the "]" and leave the URL
// behind as content.
func stripAnsi(s string) string {
	var b strings.Builder
	const (
		plain = iota
		esc
		csi
		osc
		oscEsc // inside an OSC, having just seen ESC - the terminator is ESC \
	)
	state := plain
	for _, r := range s {
		switch state {
		case plain:
			if r == 0x1b {
				state = esc
				continue
			}
			b.WriteRune(r)
		case esc:
			switch r {
			case '[':
				state = csi
			case ']':
				state = osc
			default:
				state = plain
			}
		case csi:
			if r >= 0x40 && r <= 0x7e {
				state = plain
			}
		case osc:
			switch r {
			case 0x07: // BEL
				state = plain
			case 0x1b:
				state = oscEsc
			}
		case oscEsc:
			state = plain // the "\" of ESC \, or anything else: the OSC is over
		}
	}
	return b.String()
}

// commentWhyLines is the dashboard's "what is this waiting on" block.
func commentWhyLines(s store.Session) []string {
	return []string{
		plural(s.OpenComments, "review comment") + " on the PR - press ↵ enter to read and fix them.",
		"  " + s.PRURL,
	}
}
