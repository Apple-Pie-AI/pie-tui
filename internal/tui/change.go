// The change screen: review a verified local change before its PR exists
// (session parked at change-review). Split view - file list + the amber
// Approve CTA on the left, the focused file's diff on the right, a feedback
// box always visible underneath - with a full-screen per-file mode and a
// line-cursor scroll mode inside it. Everything on ↑ ↓ ← → enter esc, nothing
// else: esc always goes one level up; ← does too, except inside the box,
// where it moves the caret.
//
// v1 has exactly two verbs: fix it (send feedback - the agent reworks and the
// screen returns for the next round) or ship it (approve - a two-item inline
// confirm, then verify-if-moved, commit, push, PR). No manual edits, no file
// selection, no revert - deliberately, per the redesign.
//
// change_data.go owns data loading; change_box.go the feedback box's
// textarea, @ file picker, spawns, and their rendering; change_view.go the
// split/full-screen rendering.
package tui

import (
	"strconv"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
)

// The screen's modes: which surface owns the keyboard.
const (
	chSplit  = iota // the split view's left list
	chFull          // one file, full screen
	chScroll        // a line cursor inside the full-screen file
	chBox           // the feedback box (and, inside it, the @ picker)
)

// changeFile is one row of the file list.
type changeFile struct {
	path       string
	status     string // "new" | "mod" | "del" | "binary"
	adds, dels int
}

// changeBoxOrigin is where Esc from the box returns to - the exact file row,
// scroll position, or bare split state the box was opened from.
type changeBoxOrigin struct {
	mode    int
	cursor  int
	fileIdx int
	lineCur int
	yOff    int
}

type changeState struct {
	ticket, worktree, branch, summary string
	baseBranch                        string // the PR base ("" = origin's default), the diff's baseline
	prURL                             string // "" until a PR exists for this ticket - the Approve CTA's label
	hasUnshipped                      bool   // once a PR exists: uncommitted edits or unpushed commits - see changeHasUnshippedContent
	round                             int
	roundTree                         string
	changeErr                         string // last approve failure, shown in the header
	report                            *agent.Report
	prBody                            string // the PR description (posted, or the intended text if not shipped yet) - see changePRBody
	files                             []changeFile
	diff                              map[string][]string
	diffLoaded, diffFetching          bool
	diffErr                           string
	showAll                           bool // round ≥ 2: whole change instead of changes-since-feedback

	mode        int
	cursor      int // split list: 0 Summary, files, then [Show all changes], then the CTA
	fileIdx     int // full-screen/scroll file index
	lineCur     int // scroll mode: highlighted row index into diff[path]
	yOff        int // full/scroll: viewport top row index into diff[path]
	approveOpen bool
	approveSel  int // 0 Create pull request, 1 Cancel

	// Plan mode: sending feedback opens a conversation instead of a rework -
	// the screen stays open, the agent replies read-only, and nothing is
	// implemented until the human explicitly switches to Auto and confirms.
	planMode          bool     // true = Plan (discuss), false = Auto (rework) - the box's send behavior
	transcript        []string // parsed change_transcript lines, refreshed every tick
	live              string   // the block currently streaming in (change_live) - "thinking: …" or a growing answer; "" when nothing is live
	transcriptScroll  int      // offset into renderChangeDiscussFull's line list; -1 = stick to the tail (default)
	discussing        bool     // a --discuss round is currently running (derived: IsActive && proc.Alive)
	reworkConfirmOpen bool     // Auto mode's explicit "start implementing?" confirm, armed by Enter in the box
	reworkConfirmSel  int      // 0 Start implementing, 1 Cancel
	chatCompact       bool     // box focused by entry, not by the reviewer: keep the split view until they send

	draft     *textarea.Model
	boxOrigin changeBoxOrigin
	picker    mentionPicker
	// The model backing each stage Enter in the box can reach, resolved once
	// when the box opens (see effectiveChangeBoxModels) - shift+tab picks
	// between these already-resolved strings rather than reloading config on
	// every toggle.
	boxModelDiscuss, boxModelImpl, boxModelVerify string

	repoFiles       []string // git ls-files, cached across screens - see monitorModel.repoFileCache
	repoFilesLower  []string // repoFiles' lowercase form, precomputed once (not per keystroke)
	repoFilesLoaded bool
}

func (c *changeState) reset() {
	*c = changeState{}
}

// Split-list geometry. Rows: Summary, the files, then optionally "Show all
// changes" (round ≥ 2), then the Approve CTA.
func (c *changeState) fileStart() int { return 1 }
func (c *changeState) fileEnd() int   { return c.fileStart() + len(c.files) }

// showAllAt is the "Show all changes" row's cursor index, or -1 when it
// doesn't exist yet (round 1 has nothing to compare feedback against).
func (c *changeState) showAllAt() int {
	if c.round < 2 {
		return -1
	}
	return c.fileEnd()
}

// hasPR reports whether a pull request already exists for this ticket - the
// Approve CTA still ships through the same gated run either way (finishShip
// pushes and no-ops on OpenPR when one already exists), but its label
// changes: "create" only makes sense before one does.
func (c *changeState) hasPR() bool { return c.prURL != "" }

// needsApprove reports whether the Approve CTA has a real action to take:
// no PR yet (creating one always has real content - verify never parks the
// gate on an empty change), or a PR exists but the worktree has moved since
// (an edit made from the screen, or a commit not yet pushed). A ticket whose
// worktree exactly matches its already-open PR has nothing to approve, and
// showing the button anyway would be a no-op dressed up as an action.
func (c *changeState) needsApprove() bool { return !c.hasPR() || c.hasUnshipped }

// ctaAt is the Approve CTA's cursor row, or -1 when there's nothing for it
// to do (needsApprove).
func (c *changeState) ctaAt() int {
	if !c.needsApprove() {
		return -1
	}
	if c.round >= 2 {
		return c.fileEnd() + 1
	}
	return c.fileEnd()
}

// lastRowAt is the split list's final selectable row: the CTA when there is
// one, else "Show all changes" (round ≥ 2), else the last file.
func (c *changeState) lastRowAt() int {
	if at := c.ctaAt(); at >= 0 {
		return at
	}
	if at := c.showAllAt(); at >= 0 {
		return at
	}
	return c.fileEnd() - 1
}

func (c *changeState) fileAt(i int) *changeFile {
	if i < c.fileStart() || i >= c.fileEnd() {
		return nil
	}
	return &c.files[i-c.fileStart()]
}

// changeScrollStep is lines per mouse-wheel tick - a single line per tick
// felt slow on real diffs; keyboard movement in scroll mode stays 1-at-a-time.
const changeScrollStep = 3

// changeBodyHeight is the change screen's usable vertical space. frame()
// (view.go) always reserves 2 rows below the body for its own rule + action-
// bar line - even when this screen leaves the outer action bar empty and
// draws its own footer inline, because it still owns its header (ownsHeader,
// view.go) and frame() has no per-screen opt-out of that reservation. Every
// layout calculation in this screen must budget against this, not m.height
// directly, or frame()'s own body-truncation clips the screen's LAST line -
// which for this screen is always its own footer.
func (m monitorModel) changeBodyHeight() int {
	h := m.height - 2
	if h < 1 {
		h = 1
	}
	return h
}

// currentDiffPath is the file full-screen/scroll are showing.
func (c *changeState) currentDiffPath() string {
	if c.fileIdx >= 0 && c.fileIdx < len(c.files) {
		return c.files[c.fileIdx].path
	}
	return ""
}

// updateChange routes to whichever surface owns the keyboard.
func (m monitorModel) updateChange(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.change.mode {
	case chBox:
		return m.updateChangeBox(msg)
	case chScroll:
		return m.updateChangeScroll(msg)
	case chFull:
		return m.updateChangeFull(msg)
	default:
		return m.updateChangeSplit(msg)
	}
}

// updateChangeSplit is the default view: the file list plus the CTA.
func (m monitorModel) updateChangeSplit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := &m.change
	if c.approveOpen {
		return m.updateChangeApprove(msg)
	}
	switch msg.String() {
	case "esc":
		// A live discuss round (started, then ↑ back out to the list while it
		// streams) takes Esc as cancel here too - "left" stays plain
		// navigation, an arrow key, so it keeps working regardless.
		if c.discussing {
			if s := m.sessionByTicket(c.ticket); s != nil {
				m.notice = "cancelling " + c.ticket + "…"
				return m, m.cancelDiscuss(*s)
			}
		}
		m.persistDraft()
		m.view = viewDashboard
	case "left":
		m.persistDraft()
		m.view = viewDashboard
	case "up":
		if c.cursor > 0 {
			c.cursor--
		}
	case "down":
		if c.cursor < c.lastRowAt() {
			c.cursor++
		} else {
			// Past the last row: general feedback, box opens empty.
			return m.openChangeBox("", changeBoxOrigin{mode: chSplit, cursor: c.lastRowAt()})
		}
	case "right":
		if f := c.fileAt(c.cursor); f != nil {
			c.fileIdx = c.cursor - c.fileStart()
			c.yOff, c.lineCur = 0, 0
			c.mode = chFull
			// The wheel scrolls the diff the moment it opens - see updateChangeFull.
			return m, tea.EnableMouseCellMotion
		}
	case "enter":
		switch {
		case c.cursor == 0:
			// Summary is informational; enter is a no-op.
		case c.fileAt(c.cursor) != nil:
			f := c.fileAt(c.cursor)
			return m.openChangeBox("@"+f.path, changeBoxOrigin{mode: chSplit, cursor: c.cursor})
		case c.cursor == c.showAllAt():
			return m.toggleShowAll()
		case c.cursor == c.ctaAt():
			c.approveOpen, c.approveSel = true, 0
		}
	}
	return m, nil
}

// updateChangeApprove is the inline 2-row confirm under the CTA.
func (m monitorModel) updateChangeApprove(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := &m.change
	switch msg.String() {
	case "up":
		if c.approveSel > 0 {
			c.approveSel--
		}
	case "down":
		if c.approveSel < 1 {
			c.approveSel++
		}
	case "esc", "left":
		c.approveOpen = false
	case "enter":
		c.approveOpen = false
		if c.approveSel == 0 {
			return m.confirmShipChange()
		}
	}
	return m, nil
}

// updateChangeFull is one file, full width; ↑↓ move between files.
func (m monitorModel) updateChangeFull(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := &m.change
	switch msg.String() {
	case "up":
		if c.fileIdx > 0 {
			c.fileIdx--
			c.yOff, c.lineCur = 0, 0
		}
	case "down":
		if c.fileIdx < len(c.files)-1 {
			c.fileIdx++
			c.yOff, c.lineCur = 0, 0
		}
	case "left", "esc":
		c.mode = chSplit
		c.cursor = c.fileStart() + c.fileIdx
		// Give the terminal its mouse back the moment the diff view closes -
		// everywhere else in the app, the mouse belongs to select-and-copy.
		return m, tea.DisableMouse
	case "enter":
		c.mode = chScroll
		c.lineCur = c.yOff
	}
	return m, nil
}

// updateChangeScroll is the line cursor inside a full-screen file; the
// viewport auto-follows it. Enter comments on the cursor's line.
func (m monitorModel) updateChangeScroll(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := &m.change
	n := len(c.diff[c.currentDiffPath()])
	switch msg.String() {
	case "up":
		if c.lineCur > 0 {
			c.lineCur--
		}
		c.keepLineInView(n, m.changeBodyHeight())
	case "down":
		if c.lineCur < n-1 {
			c.lineCur++
		}
		c.keepLineInView(n, m.changeBodyHeight())
	case "left", "esc":
		c.mode = chFull
	case "enter":
		return m.commentOnCurrentLine()
	}
	return m, nil
}

// clampYOff keeps the viewport top within [0, content length - one screen].
func (c *changeState) clampYOff(n, height int) {
	budget := height - 6
	if budget < 5 {
		budget = 5
	}
	max := n - budget
	if max < 0 {
		max = 0
	}
	if c.yOff > max {
		c.yOff = max
	}
	if c.yOff < 0 {
		c.yOff = 0
	}
}

// keepLineInView clamps lineCur to the content and slides yOff to keep it
// visible - the mockup's keepInView, ported.
func (c *changeState) keepLineInView(n, height int) {
	if c.lineCur < 0 {
		c.lineCur = 0
	}
	if n > 0 && c.lineCur > n-1 {
		c.lineCur = n - 1
	}
	budget := height - 6
	if budget < 5 {
		budget = 5
	}
	if c.lineCur < c.yOff {
		c.yOff = c.lineCur
	}
	if c.lineCur >= c.yOff+budget {
		c.yOff = c.lineCur - budget + 1
	}
	c.clampYOff(n, height)
}

// commentOnCurrentLine inserts @path:line (or @path if the row carries no
// line number - a hunk header) and opens the box.
func (m monitorModel) commentOnCurrentLine() (tea.Model, tea.Cmd) {
	c := &m.change
	f := c.files[c.fileIdx]
	nums := changeLineNumbers(c.diff[f.path])
	line := 0
	if c.lineCur < len(nums) {
		line = nums[c.lineCur]
	}
	mention := "@" + f.path
	if line > 0 {
		mention = "@" + f.path + ":" + strconv.Itoa(line)
	}
	return m.openChangeBox(mention, changeBoxOrigin{mode: chScroll, fileIdx: c.fileIdx, lineCur: c.lineCur, yOff: c.yOff})
}

// toggleShowAll flips the diff baseline (round ≥ 2 only) and re-fetches.
func (m monitorModel) toggleShowAll() (tea.Model, tea.Cmd) {
	c := &m.change
	c.showAll = !c.showAll
	c.diff, c.files, c.diffLoaded, c.diffErr = nil, nil, false, ""
	c.diffFetching = true
	return m, changeDiffCmd(c.worktree, c.ticket, c.roundTree, c.round, c.showAll, c.baseBranch)
}
