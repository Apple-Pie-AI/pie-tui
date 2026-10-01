package tui

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/jira"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/secrets"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/telemetry"
)

// Run opens the TUI hub and blocks until the user quits. It is the package's
// only entrypoint: cmd registers the cobra commands (bare `pie` and
// `pie monitor`/`top`) and calls this.
//
// version is passed in rather than read from a package-level ldflags var,
// because the -X target lives in package cmd and must stay there - both the
// Makefile and .goreleaser.yaml name it.
func Run(version string) error {
	if err := paths.EnsureDirs(); err != nil {
		return fmt.Errorf("cannot create ~/.pie directory: %w\nRun `pie init` to configure your setup", err)
	}
	st, err := store.Open(paths.StateDB())
	if err != nil {
		return err
	}
	defer st.Close()

	// Best-effort Jira client for answer-and-resume; nil if not configured.
	var jc *jira.Client
	cfg, cfgErr := config.Load()
	noResolve := cfgErr == nil && !cfg.ResolveReviewThreads()
	if cfgErr == nil && cfg.JiraBaseURL != "" {
		jc = jira.New(cfg.JiraBaseURL, cfg.JiraEmail, secrets.Get(secrets.Jira))
	}

	tel := &telemetry.Client{}
	defer tel.Flush()

	initialView := viewDashboard
	if cfgErr == nil && cfg.TelemetryEnabled != nil {
		if *cfg.TelemetryEnabled {
			tel.Configure(true, cfg.DeviceID, version)
			tel.Track("tui_opened", nil)
		}
	} else if cfgErr == nil {
		// Consent not yet given - show the consent screen first.
		initialView = viewConsent
	}

	self, _ := os.Executable()
	if self == "" {
		self = "pie"
	}

	m := monitorModel{
		store: st, jira: jc, tel: tel, selfPath: self, version: version, view: initialView,
		run: newRunState(), noResolve: noResolve,
	}
	m.reload()
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// ---- liveness & triage -----------------------------------------------------

// The hub's state, grouped by the screen that owns it.
//
// It was one flat struct of 53 fields, and the cost was not the count: every
// file in the package could reach every field, and the four hand-written "reset
// the run wizard" sites had each drifted to a different subset (doAction's
// forgot runStackPrompt, updateRunMethod's forgot three more). Each screen's
// state is now one value with one reset, so there is a single place to be wrong.
type monitorModel struct {
	store       *store.Store
	jira        *jira.Client
	tel         *telemetry.Client
	selfPath    string
	version     string // the running binary's version, for telemetry
	groups      []*group
	flat        []store.Session // ALL sessions in group order, hidden ones included
	rows        []listRow       // the list rows actually shown (headers + visible tickets)
	expanded    map[string]bool // collapsible groups open by label; zero value = all folded
	cursor      int
	width       int
	height      int
	lastRefresh time.Time
	err         error
	notice      string // transient status/error line under the footer

	// hub views (palette / run-input / doctor output / form). dashboard = zero value.
	view             hubView
	paletteCursor    int
	paletteAgentOnly bool // palette scoped to the selected agent (no global commands)
	outputTitle      string
	outputText       string
	form             *formModel // active form (config/setup), nil otherwise

	answer   answerState
	confirm  confirmState
	run      runState
	plan     planState
	repofix  repoFixState
	comments commentsState // defined in comments.go, to keep this file within budget
	// noResolve mirrors review_resolve=false: a hand-written reply is posted
	// but the thread is left open, and the action is labeled plain "Reply".
	noResolve bool

	// Pending permission approvals (the mcp-approve callback's bus) and the
	// overlay answering them; both defined in approvals.go.
	pendingApprovals []store.Approval
	approval         approvalOverlay
	// The "Edit config" screen's state (editconfig.go) and the nested command
	// allowlist review screen it links to (permissions.go).
	editConfig  editConfigState
	editModels  editModelsState
	change      changeState
	permissions permissionsState

	// repoFileCache is the @ mention picker's `git ls-files` listing, keyed by
	// worktree so a screen reopened - or a second screen against the same
	// worktree - reuses the load instead of re-shelling out (mention_picker.go).
	repoFileCache map[string]repoFileList
}

// answerState is the answer-and-resume overlay, which pre-empts whatever view is
// active. The answer is an atom list so a paste shows inline as "[N lines
// added]" right where the cursor was, exactly like Claude Code.
type answerState struct {
	open   bool
	ticket string
	in     atomInput
}

func (a *answerState) reset() { *a = answerState{} }

// confirmState is the stop/cleanup confirmation. A non-empty ticket means the
// dialog is up and awaiting a choice.
type confirmState struct {
	ticket string
	cursor int // 0 = "Yes, stop" / 1 = "No, keep running"
}

func (c *confirmState) reset() { *c = confirmState{} }

// runState is the "start new ticket(s)" wizard: the input method, the queued
// chips, and whichever per-chip prompt is currently open.
type runState struct {
	mode            string          // "" | content | jira (active run-input method)
	methodCursor    int             // cursor in the run-method picker
	text            string          // typed buffer (a key/path/id being typed)
	tickets         []pendingTicket // accumulated ticket chips awaiting launch
	idPrompt        int             // index of a content chip confirming its ticket id; -1 = none
	branchPrompt    int             // index of a content chip confirming its branch name; -1 = none
	imgPrompt       int             // index of a content chip attaching images; -1 = none
	stackPrompt     int             // index of a chip choosing its stack base; -1 = none
	reviewPrompt    int             // index of a chip choosing its plan-review toggle; -1 = none
	reviewCursor    int             // cursor in the plan-review mini palette (0=Yes 1=No)
	ticketLines     []string        // glamour-rendered ticket markdown (prompts + editor preview)
	ticketScroll    int             // top visible row of ticketLines
	promptCursor    int             // caret rune-index in the active id/branch prompt field
	content         *textarea.Model // multi-line markdown editor for content mode; nil when inactive
	preview         bool            // content mode is showing the glamour preview instead of the editor
	bar             bool            // focus is on the editor's action bar (Esc steps out to it)
	btn             int             // selected action-bar button (0=Continue 1=Preview 2=Discard)
	stackBranches   []git.Branch    // loaded when the stack/checkout picker opens, refreshed by loadStackBranchesCmd's fetch
	stackLoading    bool            // a fetch is in flight - render "fetching…" instead of "no branches match" on an empty list
	stackDefault    string          // repo's default branch name (main/master/…), for the default row
	stackFilter     string          // search filter in the stack picker
	stackCursor     int             // cursor row in the filtered branch list
	branchFirstPick bool            // dashboard flow: choose branch before a ticket exists
	holderPath      string          // worktree holding the picked branch, awaiting the user's authorization
	existingBranch  string          // the picked branch, carried across the holder prompt
}

// newRunState is the one definition of a fresh run wizard. The five prompt
// indices are -1 sentinels ("no chip is prompting"); their zero value 0 means
// "chip 0 is prompting", so a plain runState{} silently opens the id prompt.
// That trap is exactly why every reset site has to come through here.
func newRunState() runState {
	return runState{idPrompt: -1,
		branchPrompt: -1, imgPrompt: -1, stackPrompt: -1, reviewPrompt: -1}
}

func (r *runState) reset() { *r = newRunState() }

// planState is the full-screen plan viewer behind the plan-review gate. lines is
// the glamour-rendered markdown, pre-split into rows and re-rendered on resize;
// scroll is the top visible row; menu is the action selection (0=feedback
// 1=approve). When feedback is on, an inline input lets the user type
// corrections while the plan stays visible above.
type planState struct {
	ticket   string
	scroll   int
	lines    []string
	menu     int
	feedback bool
	fb       atomInput
}

// repoFixAction is one of the always-present (non-suggestion) options on the
// repo-fix screen. fixCreate is offered only when a `git init` at the target
// would actually produce a new repo (the path is absent or a plain folder).
type repoFixAction int

const (
	fixCreate repoFixAction = iota // git init a new (local) repo at the target
	fixClone                       // clone from a URL into the target
	fixEdit                        // drop into the config form to edit the path
)

// repoFixState is the fix-a-broken-repo-path screen the run preflight bounces to
// (repofix.go). The pickable rows are the suggested local repos first, then the
// actions in `actions` order. urlMode swaps the picker for the clone-URL input;
// cloning marks a clone in flight so the render can say so and keys are ignored
// until it returns.
type repoFixState struct {
	reason      string // the ValidateRepo error that triggered the bounce
	target      string // the configured (invalid) repo path being fixed
	suggestions []git.RepoSuggestion
	actions     []repoFixAction // always-present rows after the suggestions
	cursor      int             // selected row in the picker
	urlMode     bool            // editing the clone URL instead of the picker
	url         formField       // clone-URL input (reuses the single-line caret editor)
	cloning     bool            // a clone is running
}

func (s *repoFixState) reset() { *s = repoFixState{} }

// rowOf returns the picker row index of an action, or -1 if it isn't offered.
func (s repoFixState) rowOf(a repoFixAction) int {
	for i, act := range s.actions {
		if act == a {
			return len(s.suggestions) + i
		}
	}
	return -1
}

func (s repoFixState) createRow() int { return s.rowOf(fixCreate) }
func (s repoFixState) cloneRow() int  { return s.rowOf(fixClone) }
func (s repoFixState) editRow() int   { return s.rowOf(fixEdit) }
func (s repoFixState) lastRow() int   { return len(s.suggestions) + len(s.actions) - 1 }

// answerDoneMsg is returned by submitAnswer once the answer is written and the
// agent relaunched (or on failure).
type answerDoneMsg struct {
	ticket string
	err    error
}

// cloneDoneMsg reports the outcome of a git clone kicked off from the repo-fix
// screen. On success dir is the freshly cloned checkout, now saved as the repo.
type cloneDoneMsg struct {
	dir string
	err error
}

// cancelDoneMsg is returned once an agent has been stopped + cleaned up.
type cancelDoneMsg struct{ ticket string }

// pauseDoneMsg is returned once a live run has been paused into NEEDS YOU.
type pauseDoneMsg struct{ ticket string }

// discussCancelledMsg is returned once a live Plan-mode round has been
// interrupted - Esc mid-turn, the same cancel Claude Code's own CLI offers.
// Auto mode has no equivalent message: sending there hands off to the
// dashboard immediately (sendReworkMessage), so nothing is ever "working" on
// this screen for Esc to interrupt.
type discussCancelledMsg struct{ ticket string }

// consentDoneMsg carries the result of the telemetry consent save.
type consentDoneMsg struct {
	enabled  bool
	deviceID string
}

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Init arms both loops: the 1Hz local store refresh, and the much slower
// GitHub review-comment poll. They are separate on purpose - the fast one must
// never make a network call, and the slow one is a gh subprocess per open PR.
func (m monitorModel) Init() tea.Cmd { return tea.Batch(tick(), reviewTick()) }

func (m *monitorModel) reload() {
	sessions, err := m.store.List()
	if err != nil {
		m.err = err
		return
	}
	m.err = nil
	m.lastRefresh = time.Now()
	// Pending approvals ride the same 1 Hz reload as sessions; an agent is
	// actively blocked on each one, so staleness here is felt immediately.
	// Ghosts are reaped before display: a prompt is only shown while a live
	// driver is waiting behind it (see reapGhostApprovals).
	pending, _ := m.store.PendingApprovals("")
	m.pendingApprovals = reapGhostApprovals(m.store, sessions, pending)

	groups := newGroups()
	for _, s := range sessions {
		for _, g := range groups {
			if g.match(s) {
				g.sessions = append(g.sessions, s)
				break
			}
		}
	}
	var flat []store.Session
	for _, g := range groups {
		flat = append(flat, g.sessions...)
	}
	m.groups = groups
	m.flat = flat
	m.rows = buildRows(groups, m.expanded)
	if m.cursor > len(m.rows)+2 { // 0=Start-new 1=Start-from-branch 2=Edit-config list=3..len(rows)+2
		m.cursor = len(m.rows) + 2
	}
}

// sessionByTicket returns the session with the given ticket id, or nil.
func (m *monitorModel) sessionByTicket(ticket string) *store.Session {
	for i := range m.flat {
		if m.flat[i].Ticket == ticket {
			return &m.flat[i]
		}
	}
	return nil
}

// ---- views & messages ------------------------------------------------------
//
// hubView names the screens; each has an update handler in a tui_*.go file and
// a matching arm in both dispatchKey (tui_update.go) and View (tui_view.go).

// hubView is the top-level view of the hub. The zero value is the dashboard.
type hubView int

const (
	viewDashboard hubView = iota
	viewPalette
	viewRunMethod
	viewRunInput
	viewOutput
	viewForm
	viewConsent
	viewPlan        // full-screen markdown plan viewer (plan-review gate)
	viewRepoFix     // fix-a-broken-repo-path screen (suggest local repos / clone)
	viewComments    // PR review comments: read, select, hand to the agent
	viewEditConfig  // "Edit config": allowlist link, repo/branch fields
	viewPermissions // command allowlist review: baseline + extra rules, add/remove
	viewEditModels  // per-stage model pickers, the dashboard's "Edit models" row
	viewChange      // review the local change before its PR exists (change-review gate)
)

// ---- messages --------------------------------------------------------------

type doctorDoneMsg struct{ out string }

type formDoneMsg struct {
	notice string
	err    error
}

type runLaunchedMsg struct {
	text string
	err  error
}

// ticketEditedMsg returns from the external-editor session opened with ctrl+e
// on the branch prompt: the pasted ticket body was (maybe) rewritten at path.
type ticketEditedMsg struct {
	i    int    // index of the chip being edited
	path string // temp .md file the editor was opened on
	err  error
}

// pendingTicket is one queued ticket in the "Start new ticket(s)" input, shown
// as a chip. kind is content|jira.
type pendingTicket struct {
	id            string // detected ticket id (content kind: auto-inferred, not user-edited)
	title         string // parsed title (or the key itself, for a jira chip)
	lines         int
	kind          string   // "content" | "jira"
	body          string   // raw pasted content (content kind)
	images        []string // attached image files (content kind)
	base          string   // chosen base branch name (bare); "" = default
	branch        string   // final branch name (pre-filled from the pattern; also the edit buffer while prompting)
	reviewPlan    bool     // pause after the plan stage for human approval
	reviewPlanSet bool     // the review picker was actually answered (vs. a chip that never asked, e.g. jira) - lets an explicit "No" override an enabled config default instead of silently falling back to it
}

type daemonMsg struct {
	action string // "start" | "stop"
	err    error
}
