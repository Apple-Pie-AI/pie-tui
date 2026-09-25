// Checking out an existing branch into a pie worktree, from the hub, with no
// agent run: the flow behind the "Checkout a branch" command. The whole thing
// runs in a tea.Cmd - CheckoutWorktree fetches origin, which can take tens of
// seconds - and reports back with one message.
//
// A branch WITH a pull request lands in state `review`, which wires it into
// everything review rows already get (the daemon's merge detection here; the
// comments machinery in the branches stacked above). A branch WITHOUT one gets
// the new `checked-out` state and files under NO PULL REQUEST YET.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
	"github.com/Apple-Pie-AI/pie-tui/internal/vcs"
)

// checkoutDoneMsg is the checkout's one report back to the hub.
type checkoutDoneMsg struct {
	ticket, branch, prURL string
	threads               int // review threads fetched with the PR; -1 = fetch failed
	err                   error
}

// prDetectedMsg answers the menu's "Check for a pull request".
type prDetectedMsg struct {
	ticket, branch, prURL string
	err                   error
}

// checkoutBranchCmd checks branch out into a pie worktree and records the
// session. No agent runs; acting on the branch happens from the row's menu.
func checkoutBranchCmd(st *store.Store, repoPath, branch string) tea.Cmd {
	return func() tea.Msg {
		// The stub .md is written FIRST and the session id derived from its
		// path, not from the branch: IDFromBranch lowercases while IDFromPath
		// (which every later `pie run` on this stub applies) uppercases, and a
		// session claimed under the wrong case would make the first agent run
		// create a second row and orphan whatever pointed at the first.
		stub := "# " + branch + "\n\nManual checkout of branch `" + branch +
			"`. Instructions for agent runs on this branch are given per-run.\n"
		mdPath, err := ticket.WritePasted(ticket.IDFromBranch(branch), stub, nil)
		if err != nil {
			return checkoutDoneMsg{branch: branch, err: fmt.Errorf("writing the ticket stub: %w", err)}
		}
		id := ticket.IDFromPath(mdPath)

		// A branch that already has a session - the run that opened its PR, or
		// an earlier checkout - is ADOPTED under its existing ticket id, never
		// twinned: pr_comments rows belong to whichever ticket fetched them
		// first, so a second session for the same branch splits one PR's
		// threads into two partial counts (the PLEX/tutorial "1 of 3
		// comments" bug).
		adopted := false
		if prev, err := st.ByBranch(branch); err == nil && prev != nil {
			id = prev.Ticket
			adopted = true
		}

		// The same guard `pie run` applies: never stomp a session whose driver
		// process is alive.
		if existing, err := st.Get(id); err == nil && existing != nil &&
			store.IsActive(existing.State) && existing.PID != 0 && proc.Alive(existing.PID) {
			return checkoutDoneMsg{ticket: id, branch: branch,
				err: fmt.Errorf("%s is already running (pid %d)", id, existing.PID)}
		}

		worktree := paths.WorktreeFor(repoPath, id)
		if err := git.CheckoutWorktree(repoPath, worktree, branch); err != nil {
			// No store writes on failure: a checkout that did not happen must
			// not leave a phantom row on the dashboard.
			return checkoutDoneMsg{ticket: id, branch: branch, err: err}
		}
		prURL := vcs.BranchPR(worktree)

		// The row. Claim is ON CONFLICT DO NOTHING, so checking out a branch
		// that already has a (non-live) session re-opens that session rather
		// than duplicating it; the hygiene writes then reset what a stale row
		// may still carry. State goes last, once everything it implies is true.
		_, _ = st.Claim(id, repoPath, branch)
		_ = st.SetPID(id, 0)
		_ = st.SetDenials(id, "", false)
		// An adopted session keeps its own source ticket - the stub is only for
		// sessions born from the checkout itself.
		if !adopted {
			_ = st.SetSourcePath(id, mdPath)
		}
		_ = st.SetFields(id, branch, worktree, prURL)
		_ = st.SetShortDesc(id, "checked out "+branch)
		state := store.StateCheckedOut
		if prURL != "" {
			state = store.StateReview
		}
		_ = st.SetState(id, state, 0)

		// The PR's review threads, fetched NOW rather than on the next 2-minute
		// poll: the whole point of checking a branch out is to see where its
		// review stands, and a badge that appears two minutes later reads as a
		// badge that is broken. A failed fetch is non-fatal - the row is in
		// review, so the poll retries it.
		threads := 0
		if prURL != "" {
			if pr, ferr := review.Fetch(repoPath, prURL); ferr != nil {
				threads = -1
			} else if uerr := st.UpsertThreads(id, prURL, pr.Threads, !pr.Truncated); uerr == nil {
				threads = len(pr.Threads)
			}
		}
		return checkoutDoneMsg{ticket: id, branch: branch, prURL: prURL, threads: threads}
	}
}

// detectPRCmd is the menu's re-probe for a no-PR row: a PR opened after the
// checkout flips the session into review, where the rest of the machinery
// takes over.
func detectPRCmd(st *store.Store, ticketID, repoPath, branch, worktree string) tea.Cmd {
	return func() tea.Msg {
		prURL := vcs.BranchPR(worktree)
		if prURL == "" {
			return prDetectedMsg{ticket: ticketID, branch: branch}
		}
		if sess, err := st.Get(ticketID); err == nil && sess != nil {
			_ = st.SetFields(ticketID, sess.Branch, sess.Worktree, prURL)
		}
		_ = st.SetState(ticketID, store.StateReview, 0)
		// Same immediacy as the checkout itself: the flip to review comes with
		// the threads already on the row.
		if repoPath != "" {
			if pr, ferr := review.Fetch(repoPath, prURL); ferr == nil {
				_, _ = len(pr.Threads), st.UpsertThreads(ticketID, prURL, pr.Threads, !pr.Truncated)
			}
		}
		return prDetectedMsg{ticket: ticketID, branch: branch, prURL: prURL}
	}
}
