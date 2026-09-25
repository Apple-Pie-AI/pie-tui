// Data for the change screen (change.go): opening it from a parked session,
// and reading the diff off the render path. The store is the only channel to
// the detached --rework/--ship-change runs, exactly like the comments flow.
package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/vcs"
)

// openChangeView enters the screen for a session parked at change-review.
func (m *monitorModel) openChangeView(s store.Session) (tea.Model, tea.Cmd) {
	m.change.reset()
	c := &m.change
	c.ticket, c.worktree, c.branch, c.summary = s.Ticket, s.Worktree, s.Branch, s.Summary
	if c.worktree == "" {
		c.worktree = paths.WorktreeFor(s.Repo, s.Ticket)
	}
	c.round, c.roundTree, c.changeErr = s.ChangeRound, s.ChangeRoundTree, s.ChangeError
	c.baseBranch = s.BaseBranch
	c.prURL = s.PRURL
	if c.hasPR() {
		c.hasUnshipped = changeHasUnshippedContent(c.worktree)
	}
	c.planMode = true // Plan (discuss) is the default; Auto is an explicit switch
	c.transcript = parseChangeTranscript(s.ChangeTranscript)
	c.live = s.ChangeLive
	c.transcriptScroll = -1 // stick to the tail until the human scrolls up
	c.discussing = store.IsActive(s.State) && s.PID != 0 && proc.Alive(s.PID)
	c.draft = newChangeBox(s.ChangeNotes)
	m.sizeChangeBox()
	if b, err := os.ReadFile(paths.ReportFor(s.Ticket)); err == nil {
		var r agent.Report
		if json.Unmarshal(b, &r) == nil {
			c.report = &r
		}
	}
	c.prBody = changePRBody(c.worktree, c.report, c.ticket, c.summary, c.branch, c.baseBranch)
	m.view = viewChange
	c.diffFetching = true
	cmds := []tea.Cmd{changeDiffCmd(c.worktree, c.ticket, c.roundTree, c.round, c.showAll, c.baseBranch)}
	if cached, ok := m.repoFileCache[c.worktree]; ok {
		c.repoFiles, c.repoFilesLower, c.repoFilesLoaded = cached.files, cached.lower, true
	} else {
		cmds = append(cmds, loadRepoFilesCmd(c.worktree, c.ticket))
	}
	return *m, tea.Batch(cmds...)
}

// changeDiffMsg carries the change's files and hunks.
type changeDiffMsg struct {
	ticket string
	files  []changeFile
	diff   map[string][]string
	err    error
}

// changeDiffCmd reads the change off the render path. The baseline is, in
// order: the round tree (round ≥ 2, "changes since your feedback") unless
// showAll; else the PR BASE branch (merge-base with HEAD) - the review is
// "everything this branch would put in a PR, plus local edits", which stays
// right after the change is committed and pushed to the branch, where an
// upstream diff reads empty; else @{upstream}'s merge-base (an unpushed
// branch with no known base); else HEAD. The round ≥ 2 check matters even
// when roundTree is non-empty: a ticket that already shipped a PR once, then
// re-enters the gate fresh for an unrelated new change, can still carry a
// stale roundTree left over from its earlier cycle - trusting it unconditionally
// showed a tiny, unrelated diff instead of the real one.
func changeDiffCmd(worktree, ticket, roundTree string, round int, showAll bool, baseBranch string) tea.Cmd {
	return func() tea.Msg {
		base := "HEAD"
		if roundTree != "" && round >= 2 && !showAll {
			base = roundTree
		} else if mb := changeMergeBase(worktree, baseBranch); mb != "" {
			base = mb
		}
		raw, err := exec.Command("git", "-C", worktree, "diff", base).CombinedOutput()
		if err != nil {
			return changeDiffMsg{ticket: ticket, err: fmt.Errorf("git diff %s: %v: %s", base, err, strings.TrimSpace(string(raw)))}
		}
		diff := splitDiffByFile(string(raw))
		for path, lines := range untrackedAsHunks(worktree) {
			diff[path] = lines
		}
		return changeDiffMsg{ticket: ticket, files: changeFileList(worktree, base, diff), diff: diff}
	}
}

// changeMergeBase resolves the review baseline commit: merge-base of HEAD
// with the PR base branch (given, or origin's default), falling back to the
// branch's own upstream when no base resolves (an unpushed local base, say).
// "" means no baseline could be resolved - the caller diffs against HEAD.
func changeMergeBase(worktree, baseBranch string) string {
	tryBase := func(ref string) string {
		if ref == "" {
			return ""
		}
		if _, err := exec.Command("git", "-C", worktree, "rev-parse", "--verify", "--quiet", ref).Output(); err != nil {
			return ""
		}
		mb, err := exec.Command("git", "-C", worktree, "merge-base", ref, "HEAD").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(mb))
	}
	if mb := tryBase("origin/" + baseBranch); baseBranch != "" && mb != "" {
		return mb
	}
	// The repo's default branch, as origin reports it.
	if out, err := exec.Command("git", "-C", worktree, "rev-parse", "--abbrev-ref", "origin/HEAD").Output(); err == nil {
		if def := strings.TrimSpace(string(out)); def != "" && def != "origin/HEAD" {
			if mb := tryBase(def); mb != "" {
				return mb
			}
		}
	}
	if out, err := exec.Command("git", "-C", worktree,
		"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}").Output(); err == nil {
		if mb := tryBase(strings.TrimSpace(string(out))); mb != "" {
			return mb
		}
	}
	return ""
}

// changeFileList derives the ordered file rows: status from git's
// name-status, +/− counts from numstat ("-" pairs mean binary), untracked
// additions counted from their synthesized hunks.
func changeFileList(worktree, base string, diff map[string][]string) []changeFile {
	status := map[string]string{}
	if out, err := exec.Command("git", "-C", worktree, "diff", "--name-status", base).Output(); err == nil {
		for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.Fields(ln)
			if len(parts) < 2 {
				continue
			}
			st := "mod"
			switch parts[0][0] {
			case 'A':
				st = "new"
			case 'D':
				st = "del"
			}
			status[parts[len(parts)-1]] = st
		}
	}
	adds, dels := map[string]int{}, map[string]int{}
	if out, err := exec.Command("git", "-C", worktree, "diff", "--numstat", base).Output(); err == nil {
		for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.Fields(ln)
			if len(parts) < 3 {
				continue
			}
			p := parts[len(parts)-1]
			if parts[0] == "-" { // binary
				status[p] = "binary"
				continue
			}
			adds[p], _ = strconv.Atoi(parts[0])
			dels[p], _ = strconv.Atoi(parts[1])
		}
	}
	var files []changeFile
	for path, hunks := range diff {
		f := changeFile{path: path, status: status[path], adds: adds[path], dels: dels[path]}
		if f.status == "" { // not in the tracked diff: an untracked addition
			f.status = "new"
			for _, ln := range hunks {
				if strings.HasPrefix(ln, "+") {
					f.adds++
				}
			}
			if strings.Contains(strings.Join(hunks, ""), "binary or too large") {
				f.status = "binary"
			}
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files
}

// changeLineNumbers returns, for each row of a file's raw diff lines, the
// line number "comment on this line" should name: the new-file number for
// context/added rows, the old-file number for removed rows (renderHunk never
// shows this side, but a comment still needs SOME number to point at), 0 for
// a hunk header. Index-aligned with renderHunk's own output (code.go) since
// both walk the same rows 1:1.
func changeLineNumbers(lines []string) []int {
	out := make([]int, 0, len(lines))
	newNo, oldNo := 0, 0
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "@@"):
			oldNo, newNo = hunkBounds(ln)
			out = append(out, 0)
		case strings.HasPrefix(ln, "+"):
			out = append(out, newNo)
			newNo++
		case strings.HasPrefix(ln, "-"):
			out = append(out, oldNo)
			oldNo++
		default:
			out = append(out, newNo)
			newNo++
			oldNo++
		}
	}
	return out
}

// hunkBounds reads both starting line numbers off a "@@ -a,b +c,d @@" header.
func hunkBounds(header string) (oldStart, newStart int) {
	for _, f := range strings.Fields(header) {
		switch {
		case strings.HasPrefix(f, "-"):
			oldStart = atoiBeforeComma(f[1:])
		case strings.HasPrefix(f, "+"):
			newStart = atoiBeforeComma(f[1:])
		}
	}
	return
}

func atoiBeforeComma(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}

// changeHasUnshippedContent reports whether the worktree has anything a PR
// already open for it doesn't have: uncommitted or untracked edits, or local
// commits not yet pushed to the branch's upstream. Only meaningful once a PR
// exists - before that the Approve CTA always has real content to create one
// from (verify never parks the gate on an empty change). Any command failing
// (an unreadable worktree, a branch with no upstream) errs toward "yes,
// there's something to ship" rather than hiding a real action behind a guess.
func changeHasUnshippedContent(worktree string) bool {
	out, err := exec.Command("git", "-C", worktree, "status", "--porcelain").Output()
	if err != nil {
		return true
	}
	if strings.TrimSpace(string(out)) != "" {
		return true
	}
	out, err = exec.Command("git", "-C", worktree, "rev-list", "--count", "@{upstream}..HEAD").Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != "0"
}

// changePRBody resolves the same PR-description text finishShip
// (internal/runner/ship.go) uses: the agent's own repo-template fill-in from
// verify (report.PRBody), repaired against the template for any section it
// dropped; Apple Pie's default template as a fallback when the agent
// produced none. Same text whether a PR already exists (this IS what got
// posted) or doesn't yet (this is what will be) - computed once when the
// screen opens, not on every render.
func changePRBody(worktree string, report *agent.Report, ticket, summary, branch, base string) string {
	if report != nil && strings.TrimSpace(report.PRBody) != "" {
		body := report.PRBody
		if tmpl := vcs.ReadRepoTemplate(worktree); tmpl != "" {
			if missing := vcs.MissingSections(tmpl, body); len(missing) > 0 {
				body = vcs.RepairSections(tmpl, body, missing)
			}
		}
		return body
	}
	var files []string
	if report != nil {
		files = report.FilesChanged
	}
	body, err := vcs.RenderPRBody(vcs.PRData{
		Ticket: ticket, Summary: summary, Branch: branch, Base: base,
		FilesChanged: files, Tests: previewTestsLine(report), Attempts: 1,
	})
	if err != nil {
		return ""
	}
	return body
}

// previewTestsLine mirrors runner/ship.go's unexported testsLine for this
// screen's preview: pie never claims tests the agent didn't itself report
// running.
func previewTestsLine(report *agent.Report) string {
	if report == nil || strings.TrimSpace(report.VerifyLog) == "" {
		return "✅ agent-verified"
	}
	first := strings.TrimSpace(strings.SplitN(report.VerifyLog, "\n", 2)[0])
	return "✅ agent-verified: " + truncate(first, 100)
}

// syncChangeFromSession refreshes the parts of the change screen a live
// --discuss round updates in place - called on every tick while the screen
// is open, mirroring internal/tui/comments.go's own tick-driven refresh.
func (m *monitorModel) syncChangeFromSession() {
	s := m.sessionByTicket(m.change.ticket)
	if s == nil {
		return
	}
	m.change.changeErr = s.ChangeError
	m.change.transcript = parseChangeTranscript(s.ChangeTranscript)
	m.change.live = s.ChangeLive
	m.change.discussing = store.IsActive(s.State) && s.PID != 0 && proc.Alive(s.PID)
}

// parseChangeTranscript splits the store's plain-text, newline-delimited
// Plan-mode conversation back into lines for rendering. "" (never started,
// or cleared) is zero lines, not one empty one.
func parseChangeTranscript(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}
