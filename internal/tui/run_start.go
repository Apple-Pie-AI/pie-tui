// The dashboard's "Start from existing branch" entry: pick the branch first
// (before any ticket exists), then open the normal content editor so what the
// user writes next is understood as instructions for that branch (PR
// feedback, code review). The runner checks the branch out as-is
// (git.CheckoutWorktree) instead of creating one off the base.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// openInitialBranchPick is the dashboard's branch-first entry point: loads
// the branch list and enters the picker before any ticket chip exists. The
// list shown is whatever's already known locally (instant), then
// loadStackBranchesCmd's fetch refreshes it - see its own doc comment for why
// this fetch is what fixes a just-pushed branch not appearing.
func (m monitorModel) openInitialBranchPick() (tea.Model, tea.Cmd) {
	repoPath := ""
	if cfg, err := config.Load(); err == nil && len(cfg.Repos) > 0 {
		repoPath = cfg.Repos[0].Path
	}
	m.run.stackBranches, _ = git.Branches(repoPath) // best-effort; empty list = picker is empty until the fetch resolves
	m.run.stackFilter = ""
	m.run.stackCursor = 0
	m.run.branchFirstPick = true
	m.run.stackLoading = true
	return m, loadStackBranchesCmd(repoPath, true, -1)
}

// pickList is the existing-branch picker's list: unlike the stack picker there
// is no "default" row - an existing branch is the whole point of this step.
func (m monitorModel) pickList() []git.Branch {
	if m.run.stackFilter == "" {
		return m.run.stackBranches
	}
	f := strings.ToLower(m.run.stackFilter)
	var out []git.Branch
	for _, b := range m.run.stackBranches {
		if strings.Contains(strings.ToLower(b.Name), f) {
			out = append(out, b)
		}
	}
	return out
}

// updateRunBranchPick records the chosen branch, then opens the normal
// checkout: no editor, no chip, no agent - the branch lands on the dashboard
// as a row (review when it has a PR, NO PULL REQUEST YET when it does not) and everything
// else happens from that row's menu.
func (m monitorModel) updateRunBranchPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A branch held by one of the user's own worktrees needs their say-so
	// before anything proceeds - the prompt owns the keys while it is up, so
	// enter cannot fall through to the picker underneath it.
	if m.run.holderPath != "" {
		switch msg.Type {
		case tea.KeyEnter: // authorize: detach the holder, then continue as normal
			if err := git.DetachHolder(m.run.holderPath); err != nil {
				m.notice = "could not detach " + m.run.holderPath + ": " + err.Error()
				m.run.holderPath = ""
				return m, nil
			}
			branch := m.run.existingBranch
			m.run.holderPath = ""
			repoPath := ""
			if cfg, err := config.Load(); err == nil && len(cfg.Repos) > 0 {
				repoPath = cfg.Repos[0].Path
			}
			m = m.cancelRunInput()
			m.notice = "freed " + branch + " - checking it out…"
			return m, checkoutBranchCmd(m.store, repoPath, branch)
		case tea.KeyEsc: // pick a different branch instead; the holder is untouched
			m.run.holderPath, m.run.existingBranch = "", ""
			return m, nil
		}
		return m, nil
	}
	list := m.pickList()
	switch msg.Type {
	case tea.KeyEnter:
		if m.run.stackCursor < len(list) {
			branch := list[m.run.stackCursor].Name
			m.run.existingBranch = branch
			// Held by the user's own checkout? Ask now, at the moment of choice,
			// not as a failed checkout seconds later. Pie-owned worktrees are
			// reconciled automatically by the checkout itself and never prompt.
			repoPath := ""
			if cfg, err := config.Load(); err == nil && len(cfg.Repos) > 0 {
				repoPath = cfg.Repos[0].Path
			}
			if holder := git.BranchHolder(repoPath, branch); holder != "" && !paths.UnderWorktrees(holder) {
				m.run.holderPath = holder
				return m, nil
			}
			m = m.cancelRunInput() // back to the dashboard; the cmd reports in
			m.notice = "checking out " + branch + "…"
			return m, checkoutBranchCmd(m.store, repoPath, branch)
		}
	case tea.KeyEsc:
		return m.cancelRunInput(), nil
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

func (m monitorModel) renderRunBranchPick(w int) string {
	var b strings.Builder
	if m.run.holderPath != "" {
		b.WriteString(headerStyle.Render("  This branch is checked out somewhere else") + "\n\n")
		b.WriteString("  " + stTitle.Render(m.run.existingBranch) + "\n")
		b.WriteString("  " + dimStyle.Render("is currently checked out at") + "\n")
		b.WriteString("  " + stTitle.Render(truncFront(m.run.holderPath, w-4)) + "\n\n")
		b.WriteString(dimStyle.Render("  git allows a branch in only one worktree at a time. Apple Pie can free it by") + "\n")
		b.WriteString(dimStyle.Render("  detaching that checkout at its current commit - same files, same commit,") + "\n")
		b.WriteString(dimStyle.Render("  uncommitted work untouched; it just stops naming the branch.") + "\n\n")
		b.WriteString("  " + stAccent.Render("enter") + dimStyle.Render("  detach it and continue      ") +
			stAccent.Render("esc") + dimStyle.Render("  pick a different branch") + "\n")
		return b.String()
	}
	b.WriteString(headerStyle.Render("  Checkout a branch") + "\n")
	b.WriteString(dimStyle.Render("  checked out into a pie worktree - nothing is reset, no agent runs") + "\n\n")
	b.WriteString(headerStyle.Render("  Search branches") + "\n")
	placeholder := ""
	if m.run.stackFilter == "" {
		placeholder = dimStyle.Render("type to search…")
	}
	b.WriteString("  🔍 " + m.run.stackFilter + "▌ " + placeholder + "\n\n")
	list := m.pickList()
	maxShow := 12
	start := 0
	if m.run.stackCursor >= maxShow {
		start = m.run.stackCursor - maxShow + 1
	}
	for idx := start; idx < len(list) && idx < start+maxShow; idx++ {
		br := list[idx]
		tag := dimStyle.Render(" (local)")
		if br.Remote {
			tag = dimStyle.Render(" (remote)")
		}
		line := "   " + br.Name + tag
		if idx == m.run.stackCursor {
			line = selStyle.Render(" "+br.Name) + tag
		}
		b.WriteString(truncate(line, w) + "\n")
	}
	if len(list) == 0 {
		msg := "(no branches match your search)"
		if m.run.stackLoading {
			msg = "fetching latest branches…"
		}
		b.WriteString("  " + dimStyle.Render(msg) + "\n")
	}
	b.WriteString("\n  " + dimStyle.Render("type to search · ↑↓ move · enter select · esc cancel"))
	return b.String()
}
