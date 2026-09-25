// The wizard's two list pickers: which branch to stack this ticket on, and
// whether to pause at the plan-review gate.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
)

// defaultBaseSentinel marks the picker's first row - "use the repo's default
// base, don't stack". It's rendered as the actual default branch name (via
// baseLabel); the leading NUL keeps it from ever colliding with a real branch.
const defaultBaseSentinel = "\x00default"

// baseLabel is the display text for a picker row. The sentinel row shows the
// repo's default branch (e.g. "main (default)") instead of a cryptic "none".
func (m monitorModel) baseLabel(b git.Branch) string {
	if b.Name == defaultBaseSentinel {
		if m.run.stackDefault != "" {
			return m.run.stackDefault + " (default)"
		}
		return "default base"
	}
	return b.Name
}

// openStackPicker loads branch list and enters the stack sub-step for chip i.
// The list shown is whatever's already known locally (instant), then
// loadStackBranchesCmd's fetch refreshes it - so a branch pushed after the
// user's last fetch still appears without them leaving this screen.
func (m monitorModel) openStackPicker(i int) (tea.Model, tea.Cmd) {
	repoPath := ""
	if cfg, err := config.Load(); err == nil && len(cfg.Repos) > 0 {
		repoPath = cfg.Repos[0].Path
	}
	m.run.stackBranches, _ = git.Branches(repoPath) // best-effort; empty list = picker is empty until the fetch resolves
	m.run.stackDefault = git.DefaultBranch(repoPath)
	m.run.stackFilter = ""
	m.run.stackCursor = 0
	m.run.stackPrompt = i
	m.run.stackLoading = true
	return m, loadStackBranchesCmd(repoPath, false, i)
}

// stackBranchesMsg carries a freshly-fetched branch list for either branch
// picker (openInitialBranchPick, openStackPicker). branchFirst/chipIndex say
// which picker this refresh is for, so a result that lands after the user
// has already moved on (closed the picker, opened a different chip's) is
// discarded rather than clobbering the wrong screen's state.
type stackBranchesMsg struct {
	branchFirst   bool
	chipIndex     int
	repoPath      string
	branches      []git.Branch
	defaultBranch string
}

// loadStackBranchesCmd fetches origin (git.Fetch - best-effort; a fetch
// failure, e.g. offline, still falls through to whatever Branches finds
// locally rather than emptying the picker) and THEN lists branches, off the
// render path. This is the fix for "a branch I just pushed doesn't show up
// in Checkout a branch" - Branches alone makes no network call (see its own
// doc comment), so without a fetch here the list is only ever as fresh as
// whatever last happened to fetch this repo (which, before this, was only
// ever AFTER a branch was already picked - CreateWorktree/CheckoutWorktree).
func loadStackBranchesCmd(repoPath string, branchFirst bool, chipIndex int) tea.Cmd {
	return func() tea.Msg {
		_ = git.Fetch(repoPath)
		branches, _ := git.Branches(repoPath)
		return stackBranchesMsg{
			branchFirst: branchFirst, chipIndex: chipIndex, repoPath: repoPath,
			branches: branches, defaultBranch: git.DefaultBranch(repoPath),
		}
	}
}

// filteredBranches returns the picker list: the default-base row followed by
// branches whose name contains the current filter (case-insensitive).
func (m monitorModel) filteredBranches() []git.Branch {
	none := git.Branch{Name: defaultBaseSentinel}
	if m.run.stackFilter == "" {
		return append([]git.Branch{none}, m.run.stackBranches...)
	}
	f := strings.ToLower(m.run.stackFilter)
	out := []git.Branch{none}
	for _, b := range m.run.stackBranches {
		if strings.Contains(strings.ToLower(b.Name), f) {
			out = append(out, b)
		}
	}
	return out
}

func (m monitorModel) updateRunStack(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	i := m.run.stackPrompt
	if i < 0 || i >= len(m.run.tickets) {
		m.run.stackPrompt = -1
		return m, nil
	}
	isContentTicket := i < len(m.run.tickets) && m.run.tickets[i].kind == "content"
	list := m.filteredBranches()
	switch msg.Type {
	case tea.KeyEnter:
		if m.run.stackCursor < len(list) {
			b := list[m.run.stackCursor]
			if b.Name != defaultBaseSentinel {
				m.run.tickets[i].base = b.Name
			}
		}
		m.run.stackPrompt, m.run.stackFilter, m.run.stackCursor = -1, "", 0
		// Content tickets: advance to the final plan-review toggle step (the last
		// in the finalize chain), which launches on its Enter.
		if isContentTicket {
			return m.openReviewPicker(i)
		}
	case tea.KeyEsc:
		// Esc means "cancel the creation", not "launch with the default base".
		// For content (the stack step is the last in a linear finalize chain)
		// abort the whole creation. For jira/file the picker was opened via
		// ctrl+s on an existing chip list, so just close it and keep the chips.
		m.run.stackPrompt, m.run.stackFilter, m.run.stackCursor = -1, "", 0
		if isContentTicket {
			return m.cancelRunInput(), nil
		}
	case tea.KeyUp:
		if m.run.stackCursor > 0 {
			m.run.stackCursor--
		}
	case tea.KeyDown:
		if m.run.stackCursor < len(list)-1 {
			m.run.stackCursor++
		}
	case tea.KeyBackspace:
		if r := []rune(m.run.stackFilter); len(r) > 0 {
			m.run.stackFilter = string(r[:len(r)-1])
			m.run.stackCursor = 0
		}
	case tea.KeyRunes:
		m.run.stackFilter += string(msg.Runes)
		m.run.stackCursor = 0
	}
	return m, nil
}

func (m monitorModel) renderRunStack(w int) string {
	var b strings.Builder

	// Show existing chips first so the context is always visible.
	for _, t := range m.run.tickets {
		b.WriteString("  " + chipLabel(t) + "\n")
	}
	if len(m.run.tickets) > 0 {
		b.WriteString("\n")
	}

	b.WriteString(headerStyle.Render("  Choose the base branch if this ticket depends on another") + "\n")
	b.WriteString(dimStyle.Render("  the PR will target this branch - pick the default to not stack, esc cancels") + "\n\n")

	// Search box: the user types to filter the branch list below.
	b.WriteString(headerStyle.Render("  Search branches") + "\n")
	placeholder := ""
	if m.run.stackFilter == "" {
		placeholder = dimStyle.Render("type to search…")
	}
	b.WriteString("  🔍 " + m.run.stackFilter + "▌ " + placeholder + "\n\n")
	list := m.filteredBranches()
	maxShow := 12
	start := 0
	if m.run.stackCursor >= maxShow {
		start = m.run.stackCursor - maxShow + 1
	}
	for idx := start; idx < len(list) && idx < start+maxShow; idx++ {
		br := list[idx]
		name := m.baseLabel(br)
		tag := ""
		if br.Remote {
			tag = dimStyle.Render(" (remote)")
		} else if br.Name != defaultBaseSentinel {
			tag = dimStyle.Render(" (local)")
		}
		line := "   " + name + tag
		if idx == m.run.stackCursor {
			line = selStyle.Render(" "+name) + tag
		}
		b.WriteString(truncate(line, w) + "\n")
	}
	if len(list) == 1 { // only the "none" row survived the filter
		msg := "(no branches match your search)"
		if m.run.stackLoading {
			msg = "fetching latest branches…"
		}
		b.WriteString("  " + dimStyle.Render(msg) + "\n")
	}
	b.WriteString("\n  " + dimStyle.Render("type to search · ↑↓ move · enter select · esc cancel"))
	return b.String()
}

// openReviewPicker enters the final plan-review toggle step for chip i, with the
// cursor pre-selected from the config default (0 = Yes/pause, 1 = No/run through).
func (m monitorModel) openReviewPicker(i int) (tea.Model, tea.Cmd) {
	m.run.reviewPrompt = i
	m.run.reviewCursor = 1 // default: No (run straight through)
	if cfg, err := config.Load(); err == nil && cfg.ReviewPlans {
		m.run.reviewCursor = 0 // config opts in → pre-select Yes
	}
	return m, nil
}

// updateRunReview handles the two-item "Review the plan before implementing?"
// mini palette. ↑↓ moves; Enter records the choice on the chip and launches; Esc
// cancels the whole creation (consistent with the stack step for content tickets).
func (m monitorModel) updateRunReview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	i := m.run.reviewPrompt
	if i < 0 || i >= len(m.run.tickets) {
		m.run.reviewPrompt = -1
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEnter:
		m.run.tickets[i].reviewPlan = m.run.reviewCursor == 0
		m.run.tickets[i].reviewPlanSet = true
		m.run.reviewPrompt, m.run.reviewCursor = -1, 0
		return m.launchOrClose()
	case tea.KeyEsc:
		return m.cancelRunInput(), nil
	case tea.KeyUp:
		m.run.reviewCursor = 0
	case tea.KeyDown:
		m.run.reviewCursor = 1
	}
	return m, nil
}

func (m monitorModel) renderRunReview(w int) string {
	var b strings.Builder

	// Show existing chips first so the context stays visible.
	for _, t := range m.run.tickets {
		b.WriteString("  " + chipLabel(t) + "\n")
	}
	if len(m.run.tickets) > 0 {
		b.WriteString("\n")
	}

	b.WriteString(headerStyle.Render("  Review the plan before implementing?") + "\n\n")
	items := []string{
		"Yes — pause so I can approve or give feedback",
		"No — run straight through",
	}
	for i, item := range items {
		if i == m.run.reviewCursor {
			b.WriteString(selStyle.Render(" "+item) + "\n")
		} else {
			b.WriteString("  " + item + "\n")
		}
	}
	b.WriteString("\n  " + dimStyle.Render("↑↓ move · enter launch · esc cancel"))
	return b.String()
}
