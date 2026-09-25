package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/jira"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/runner"
	"github.com/Apple-Pie-AI/pie-tui/internal/secrets"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/telemetry"
	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
)

// runOpts carries the `pie run` flags. These were eleven package-level vars,
// which made resolveSpecs and runOne read ambient state their signatures did not
// declare - and leaked between tests in the same binary, which is why the suite
// needed a resetRunFlags helper to paper over it.
type runOpts struct {
	repo, title, desc, base, branch, fromBranch       string
	dryRun, local, resume, ship, reviewPlan, fromPlan bool
	addressComments, shipComments                     bool
	commentFeedback                                   string
	// reviewPlanSet is whether --review-plan was explicitly passed (in either
	// direction: --review-plan or --review-plan=false), as opposed to left at
	// its zero value. Without this, an explicit "false" from the TUI wizard
	// (the user picking "No" on a ticket) is indistinguishable from the flag
	// never being passed at all, and reviewPlan's OR with the config default
	// (below) would silently override the user's own "No" back to "Yes".
	reviewPlanSet bool
	// The change-review gate (review-before-PR): reviewChange forces/suppresses
	// the gate for one run (reviewChangeSet distinguishes an explicit false
	// from never-passed, same reason as reviewPlanSet); shipChange is the
	// gate's approve run; rework resumes the session with the change screen's
	// notes (in commentFeedback's --feedback flag, shared).
	reviewChange, reviewChangeSet, shipChange, rework bool
	// discuss is the gate's Plan-mode round: resume the session under
	// --permission-mode plan with the change screen's queued message, reply,
	// and return to change-review - no edits, no re-verify.
	discuss bool
}

func init() {
	o := &runOpts{}
	c := &cobra.Command{
		Use:   "run <TICKET|FILE>...",
		Short: "Take one or more tickets (Jira keys or local .md files) end-to-end to open PRs",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.reviewPlanSet = cmd.Flags().Changed("review-plan")
			o.reviewChangeSet = cmd.Flags().Changed("review-change")
			return runE(args, *o)
		},
	}
	c.Flags().StringVar(&o.repo, "repo", "", "repo path (defaults to the first configured repo)")
	c.Flags().BoolVar(&o.dryRun, "dry-run", false, "do everything except git push and PR (Decision 16)")
	c.Flags().BoolVar(&o.local, "local", false, "skip Jira: take ticket text from --title/--desc (for testing)")
	c.Flags().StringVar(&o.title, "title", "", "ticket summary (requires --local)")
	c.Flags().StringVar(&o.desc, "desc", "", "ticket description (with --local)")
	c.Flags().BoolVar(&o.resume, "resume", false, "verify & ship: continue from the existing worktree (keep manual fixes), re-run the gate, then PR")
	c.Flags().BoolVar(&o.ship, "ship", false, "ship without verifying: trust your manual fix, commit + push + PR directly (use when verification itself is unreliable in the worktree)")
	c.Flags().StringVar(&o.base, "base", "", "base branch (or ticket id) to stack on; overrides config default")
	c.Flags().StringVar(&o.branch, "branch", "", "exact branch name for the PR; overrides the config branch pattern")
	c.Flags().StringVar(&o.fromBranch, "from-branch", "", "work ON this existing branch instead of creating one (address PR feedback, review a PR's code); never resets it")
	c.Flags().BoolVar(&o.reviewPlan, "review-plan", false, "pause after planning so you can review the plan before implementation (--review-plan=false explicitly overrides an enabled config default)")
	c.Flags().BoolVar(&o.fromPlan, "from-plan", false, "skip planning: implement from the existing plan.json in the worktree")
	c.Flags().BoolVar(&o.addressComments, "address-comments", false, "fix the PR review comments selected in the dashboard locally, then pause for your approval")
	c.Flags().StringVar(&o.commentFeedback, "feedback", "", "the human's correction: with --address-comments revises the local fixes; with --rework carries the change screen's notes")
	c.Flags().BoolVar(&o.shipComments, "ship-comments", false, "approve the local review fixes: verify, commit, push, and reply on the PR")
	c.Flags().BoolVar(&o.reviewChange, "review-change", false, "pause after a green verify to review the change before the PR is created (--review-change=false overrides an enabled config default)")
	c.Flags().BoolVar(&o.shipChange, "ship-change", false, "approve the reviewed change: apply reverts, re-verify if it moved, then commit + push + PR")
	c.Flags().BoolVar(&o.rework, "rework", false, "rework the reviewed change: apply reverts, resume the session with --feedback notes, re-verify, and park the next round")
	c.Flags().BoolVar(&o.discuss, "discuss", false, "Plan mode: resume the session read-only with the change screen's queued message, reply, and return to change-review - no edits, no re-verify")
	rootCmd.AddCommand(c)
}

func runE(args []string, o runOpts) error {
	// Interrupting `pie run` has to reach the claude subprocess, not just this
	// one: the TUI's Pause and "Stop & clean up" both SIGTERM this process, and
	// until it cancelled its children the agent kept editing a worktree that was
	// already being deleted. stop() restores the default disposition on the first
	// signal, so a second ctrl+c still kills immediately if the unwind hangs.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()

	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	repo, err := cfg.Resolve(o.repo)
	if err != nil {
		return err
	}

	specs, err := resolveSpecs(args, o)
	if err != nil {
		return err
	}

	tel := &telemetry.Client{}
	if cfg.TelemetryEnabled != nil && *cfg.TelemetryEnabled {
		tel.Configure(true, cfg.DeviceID, version)
	}
	defer tel.Flush()

	// One shared store across goroutines (its *sql.DB is concurrency-safe;
	// the daemon shares one the same way).
	st, err := store.Open(paths.StateDB())
	if err != nil {
		return err
	}
	defer st.Close()

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed int
	)
	for _, sp := range specs {
		wg.Add(1)
		go func(sp ticket.Spec) {
			defer wg.Done()
			out := runOne(ctx, sp, o, cfg, repo, st, tel)
			if out.Err != nil || out.State == store.StateFailed {
				mu.Lock()
				failed++
				mu.Unlock()
			}
		}(sp)
	}
	wg.Wait()

	// Cancellation is not a ticket failure - report it as itself rather than as
	// "N of M ticket(s) failed", which is what every in-flight run unwinding at
	// once would otherwise look like.
	if ctx.Err() != nil {
		return fmt.Errorf("cancelled")
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d ticket(s) failed", failed, len(specs))
	}
	return nil
}

// resolveSpecs turns CLI args into resolved, de-duplicated ticketSpecs, failing
// fast on validation before any work fans out. Per arg, in order:
//  1. legacy --local/--title (single arg only): take text from flags;
//  2. an existing file on disk: parse it as a local .md/text ticket;
//  3. otherwise: a Jira ticket key.
func resolveSpecs(args []string, o runOpts) ([]ticket.Spec, error) {
	multi := len(args) > 1
	if multi && (o.title != "" || o.desc != "") {
		return nil, fmt.Errorf("--title/--desc cannot be combined with multiple tickets; put the text in the .md files")
	}
	if multi && o.branch != "" {
		return nil, fmt.Errorf("--branch cannot be combined with multiple tickets; the branch name is per-ticket")
	}
	if multi && o.fromBranch != "" {
		return nil, fmt.Errorf("--from-branch cannot be combined with multiple tickets; the branch is per-ticket")
	}
	if o.fromBranch != "" && o.branch != "" {
		return nil, fmt.Errorf("--from-branch and --branch are mutually exclusive: the first works on an existing branch, the second names a new one")
	}

	specs := make([]ticket.Spec, 0, len(args))
	for _, arg := range args {
		// (1) Legacy flag-driven local mode, single arg only.
		if !multi && (o.local || o.title != "") {
			if o.title == "" {
				return nil, fmt.Errorf("--local requires --title")
			}
			sp := ticket.Spec{
				Ticket: ticket.Normalize(arg), Summary: o.title, Desc: o.desc, Local: true,
			}
			sp.Branch, sp.FromBranch = branchOpt(o)
			specs = append(specs, sp)
			continue
		}
		// (2) An existing file → local source.
		if fi, err := os.Stat(arg); err == nil && !fi.IsDir() {
			sp, err := ticket.LoadLocal(arg)
			if err != nil {
				return nil, err
			}
			sp.StackBase = o.base
			sp.Branch, sp.FromBranch = branchOpt(o)
			specs = append(specs, sp)
			continue
		}
		// (3) Jira ticket key.
		sp := ticket.Spec{Ticket: ticket.Normalize(arg), StackBase: o.base}
		sp.Branch, sp.FromBranch = branchOpt(o)
		specs = append(specs, sp)
	}

	// A ticket id becomes a path element under ~/.pie (worktree, log, plan,
	// report) and the worktree path is handed to os.RemoveAll, so reject anything
	// that could resolve outside the Apple Pie home before any work fans out.
	for _, sp := range specs {
		if !paths.ValidTicketID(sp.Ticket) {
			return nil, fmt.Errorf(
				"invalid ticket id %q: must be a single path segment with no %q, %q or %q",
				sp.Ticket, "/", `\`, "..")
		}
	}

	return dedupeSpecs(specs), nil
}

// branchOpt maps the two branch flags onto the spec's (Branch, FromBranch)
// pair: --from-branch carries the name AND flips the checkout-not-create mode.
// The two flags are rejected together before this runs.
func branchOpt(o runOpts) (string, bool) {
	if o.fromBranch != "" {
		return o.fromBranch, true
	}
	return o.branch, false
}

// effectiveReviewPlan decides whether this run pauses at the plan-review
// gate. An EXPLICIT --review-plan (true OR false - e.g. the TUI wizard's
// per-ticket answer) always wins; only when the flag was never passed at all
// does cfgDefault apply. A plain o.reviewPlan||cfgDefault OR - the previous
// logic - let an enabled config default silently override a user's explicit
// "No" back to "Yes", because "false" and "never passed" were indistinguishable.
func effectiveReviewPlan(o runOpts, cfgDefault bool) bool {
	if o.reviewPlanSet {
		return o.reviewPlan
	}
	return cfgDefault
}

// effectiveReviewChange resolves the change-review gate the same way: an
// explicit --review-change (true or false) wins; otherwise the config default
// (on unless review_before_pr = false) applies.
func effectiveReviewChange(o runOpts, cfgDefault bool) bool {
	if o.reviewChangeSet {
		return o.reviewChange
	}
	return cfgDefault
}

// dedupeSpecs guarantees unique ticket ids within one invocation so two files
// with the same basename (both derive e.g. "DUP") don't collide on
// worktree/branch/log path or the store's primary key. Later collisions get a
// -2/-3 suffix.
func dedupeSpecs(specs []ticket.Spec) []ticket.Spec {
	seen := map[string]int{}
	for i := range specs {
		id := specs[i].Ticket
		if n := seen[id]; n > 0 {
			newID := fmt.Sprintf("%s-%d", id, n+1)
			for seen[newID] > 0 {
				n++
				newID = fmt.Sprintf("%s-%d", id, n+1)
			}
			fmt.Printf("(warn) ticket id %q collides; using %q\n", id, newID)
			seen[id] = n + 1
			specs[i].Ticket = newID
			seen[newID] = 1
		} else {
			seen[id] = 1
		}
	}
	return specs
}

// runOne executes the full pipeline for a single resolved ticket. It never
// bubbles an error up the Go path: it returns an Outcome so sibling tickets in
// a parallel run keep going.
func runOne(ctx context.Context, sp ticket.Spec, o runOpts, cfg *config.Config, repo *config.Repo, st *store.Store, tel *telemetry.Client) runner.Outcome {
	logf, closeLog := newLogger(sp.Ticket)
	defer closeLog()
	start := time.Now()

	// Concurrency guard: refuse to start a second run on a ticket that is actively
	// in progress (deterministic via kill -0). Without this, tapping a dashboard
	// action (Resume / Open a PR) while the "Open in Claude Code" auto-chain is
	// also pending could fire two runs on the same worktree at once. The state
	// check is essential: in a terminal state (needs-you/failed/review/…) the
	// driver process has already exited, so its recorded PID is stale and may have
	// been recycled by an unrelated process - guarding on liveness alone would
	// wrongly report "already running".
	// priorState is the session as this launcher FOUND it, remembered before the
	// reset to `queued` below destroys it. The ship-comments gate reads it: its
	// question is "was this ticket parked at fix-review when the ship was
	// requested", and the row itself can no longer answer once we have stomped it.
	priorState, priorFlow := "", ""
	if existing, err := st.Get(sp.Ticket); err == nil && existing != nil {
		priorState, priorFlow = existing.State, existing.ParkedFlow
		if store.IsActive(existing.State) &&
			existing.PID != 0 && existing.PID != os.Getpid() && proc.Alive(existing.PID) {
			logf("[%s] already running (pid %d) - refusing to start a second run", sp.Ticket, existing.PID)
			return runner.Outcome{State: existing.State,
				Err: fmt.Errorf("%s already running (pid %d)", sp.Ticket, existing.PID)}
		}
	}

	tel.Track("run_started", map[string]any{"local": sp.Local})

	summary, desc := sp.Summary, sp.Desc
	var jc *jira.Client
	if sp.Local {
		logf("[%s] queued → working (local)", sp.Ticket)
	} else {
		jc = jira.New(cfg.JiraBaseURL, cfg.JiraEmail, secrets.Get(secrets.Jira))
		logf("[%s] queued → working", sp.Ticket)
		issue, err := jc.FetchIssue(sp.Ticket)
		if err != nil {
			logf("[%s] (error) fetch ticket: %v", sp.Ticket, err)
			return runner.Outcome{State: store.StateFailed, Err: fmt.Errorf("fetch %s: %w", sp.Ticket, err)}
		}
		summary, desc = issue.Summary, issue.Description
	}

	logf("[%s] %s", sp.Ticket, summary)

	// Effective plan-review, persisted so TUI re-spawns (answer/feedback) keep
	// the gate on.
	reviewPlan := effectiveReviewPlan(o, cfg.ReviewPlans)

	// State store so `pie status` reflects manual runs too.
	_, _ = st.Claim(sp.Ticket, repo.Path, summary)
	_ = st.SetPID(sp.Ticket, os.Getpid()) // for monitor liveness (kill -0)
	_ = st.SetReviewPlan(sp.Ticket, reviewPlan)
	if sp.SourcePath != "" {
		_ = st.SetSourcePath(sp.Ticket, sp.SourcePath)
	}
	// Reset state immediately so a re-run leaves a stale terminal state
	// (failed/needs-you/stopped/review) right away instead of lingering there
	// until the pipeline reaches planning (after the slow worktree setup).
	_ = st.SetState(sp.Ticket, store.StateQueued, 0)
	_ = st.SetDenials(sp.Ticket, "", false)  // last run's refusals are not this run's
	_ = st.SetParkedFlow(sp.Ticket, "")      // captured above as PriorFlow; the row starts clean
	_ = st.ExpirePendingApprovals(sp.Ticket) // last run's prompts are not this run's either

	// Short description for the dashboard third column — set immediately with a
	// programmatic fallback so the column is never empty, then upgraded async by an
	// LLM call so noisy or verbose summaries get normalised to <10 words.
	progDesc := ticket.TruncateWords(ticket.FirstNonEmpty(summary, ticket.FirstSentence(desc), sp.Ticket), 10)
	_ = st.SetShortDesc(sp.Ticket, progDesc)
	go func() {
		apiKey := secrets.Get(secrets.Anthropic)
		descSnip := desc
		if len(descSnip) > 1000 {
			descSnip = descSnip[:1000]
		}
		prompt := fmt.Sprintf(
			"In at most 9 words, plain text, no quotes or period, describe what this ticket asks for. Title: %s. Description: %s",
			summary, descSnip)
		if out := agent.Oneshot(ctx, prompt, apiKey); out != "" {
			llmDesc := ticket.TruncateWords(strings.TrimRight(out, "."), 10)
			if llmDesc != "" {
				_ = st.SetShortDesc(sp.Ticket, llmDesc)
			}
		}
	}()

	hooks := runner.Hooks{
		Logf:    logf,
		OnState: func(state string, retries int) { _ = st.SetState(sp.Ticket, state, retries) },
		OnField: func(branch, worktree, pr string) { _ = st.SetFields(sp.Ticket, branch, worktree, pr) },
	}
	if jc != nil {
		hooks.Comment = func(text string) {
			if err := jc.AddComment(sp.Ticket, text); err != nil {
				logf("[%s] (warn) jira comment failed: %v", sp.Ticket, err)
			}
		}
	} else {
		// No Jira: route the plan + any blocking questions to the CLI so they
		// don't vanish (the runner only emits them via Comment).
		hooks.Comment = localPlanComment(logf, sp.Ticket)
	}

	// Resolve the stack base: if the caller typed a ticket id, look up its branch;
	// otherwise treat the value as a bare branch name and pass it through.
	resolvedBase := sp.StackBase
	if resolvedBase != "" {
		if sess, err := st.Get(strings.ToUpper(resolvedBase)); err == nil &&
			sess != nil && sess.Branch != "" {
			resolvedBase = sess.Branch // ticket id → branch name
		}
		_ = st.SetBaseBranch(sp.Ticket, resolvedBase)
	}

	out := runner.Run(ctx, runner.Task{
		Ticket: sp.Ticket, Summary: summary, Description: desc,
		Repo: repo, Cfg: cfg, DryRun: o.dryRun, Resume: o.resume, Ship: o.ship,
		Store: st, Images: sp.Images, Base: resolvedBase, Branch: sp.Branch,
		FromBranch: sp.FromBranch, ReviewPlan: reviewPlan, FromPlan: o.fromPlan,
		AddressComments: o.addressComments, PriorState: priorState, PriorFlow: priorFlow,
		CommentFeedback: o.commentFeedback, ShipComments: o.shipComments,
		ReviewChange: effectiveReviewChange(o, cfg.ReviewChangesBeforePR()),
		ShipChange:   o.shipChange, Rework: o.rework, ReworkFeedback: o.commentFeedback,
		Discuss: o.discuss,
		// cmd owns the keychain, so the pipeline no longer reads it itself - the
		// same inversion that keeps Jira behind Hooks.Comment.
		AnthropicKey: secrets.Get(secrets.Anthropic),
	}, hooks)
	// Record the terminal outcome in the ticket log so the reason is visible in
	// the TUI/`pie logs` even when stdout/stderr is discarded (TUI-launched).
	// A cancelled run reports itself but writes no state - whoever signalled it
	// (Pause → NEEDS YOU, Stop → STOPPED) has already recorded the right one.
	switch {
	case ctx.Err() != nil:
		logf("[%s] ✗ cancelled", sp.Ticket)
	case out.Err != nil:
		logf("[%s] ✗ %s: %v", sp.Ticket, out.State, out.Err)
	case out.State == store.StateReview:
		logf("[%s] ✓ review - PR ready: %s", sp.Ticket, out.PRURL)
	case out.State == store.StateChangeReview:
		logf("[%s] ✓ change-review - the change is verified and waiting for your review; no PR yet", sp.Ticket)
	default:
		logf("[%s] ended in state: %s", sp.Ticket, out.State)
	}
	tel.Track("run_completed", map[string]any{
		"outcome":    out.State,
		"duration_s": int(time.Since(start).Seconds()),
		"has_error":  out.Err != nil,
		"local":      sp.Local,
	})
	return out
}

// localPlanComment routes runner Comment text (the rendered plan and blocking
// questions) to the CLI logger so it's visible without Jira. Emitted as a
// single logf call so the block doesn't interleave with other tickets' lines.
// Jira {code} markers are stripped: the same Comment text goes verbatim to
// Jira (where they render as code blocks) but they are pure noise in a
// terminal, and the remediation messages draw their own boxes instead.
func localPlanComment(logf func(string, ...interface{}), ticket string) func(string) {
	return func(text string) {
		var lines []string
		for _, ln := range strings.Split(text, "\n") {
			if strings.TrimSpace(ln) == "{code}" {
				continue
			}
			lines = append(lines, ln)
		}
		logf("[%s] ----- agent comment -----\n%s\n[%s] -------------------------", ticket, strings.Join(lines, "\n"), ticket)
	}
}

// newLogger returns a logf that writes to stdout and ~/.pie/logs/<ticket>.log.
func newLogger(ticket string) (func(string, ...interface{}), func()) {
	f, _ := os.OpenFile(paths.LogFor(ticket), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	logf := func(format string, a ...interface{}) {
		line := fmt.Sprintf(format, a...)
		fmt.Println(line)
		if f != nil {
			fmt.Fprintln(f, line)
		}
	}
	return logf, func() {
		if f != nil {
			f.Close()
		}
	}
}
