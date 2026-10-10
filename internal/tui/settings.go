// The config/setup form screens and the doctor output pane, plus the daemon
// start/stop controls the palette drives.
package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/secrets"
	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
)

func (m monitorModel) runDoctor() tea.Cmd {
	self := m.selfPath
	return func() tea.Msg {
		out, _ := exec.Command(self, "doctor").CombinedOutput()
		return doctorDoneMsg{out: string(out)}
	}
}

func (m monitorModel) updateOutput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "enter":
		m.view = viewDashboard
	}
	return m, nil
}

func (m monitorModel) renderOutput(w int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("  "+m.outputTitle) + "\n\n")
	for _, ln := range strings.Split(strings.TrimRight(m.outputText, "\n"), "\n") {
		for _, seg := range wrapLine(ln, w) {
			b.WriteString(seg + "\n")
		}
	}
	b.WriteString("\n  " + dimStyle.Render("esc/enter close"))
	return b.String()
}

// ---- daemon control --------------------------------------------------------

// daemonAlive reports the daemon pid and whether it's running (reuses start.go).
func daemonAlive() (int, bool) {
	return proc.DaemonAlive()
}

func (m monitorModel) startDaemon() tea.Cmd {
	self := m.selfPath
	return func() tea.Msg {
		c := exec.Command(self, "start")
		return daemonMsg{action: "start", err: startDetached(c)}
	}
}

func (m monitorModel) stopDaemon() tea.Cmd {
	return func() tea.Msg {
		pid, ok := daemonAlive()
		if !ok {
			return daemonMsg{action: "stop", err: fmt.Errorf("daemon not running")}
		}
		return daemonMsg{action: "stop", err: syscall.Kill(pid, syscall.SIGTERM)}
	}
}

// ---- config / setup forms --------------------------------------------------

func (m monitorModel) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A form with no fields has nothing to focus, and both the paste path and the
	// editing path below index fields[focus] - so bail here rather than let a
	// keystroke panic the hub.
	if m.form == nil || len(m.form.fields) == 0 {
		m.view = viewDashboard
		m.form = nil
		return m, nil
	}
	// Pasted text: drop it in at the caret as a single line (config fields are
	// single-line, so collapse any newlines).
	if msg.Paste {
		m.form.typeRunes(strings.ReplaceAll(ticket.NormalizeNewlines(string(msg.Runes)), "\n", " "))
		return m, nil
	}
	// Form-level keys first - ↑/↓ move between fields here rather than editing.
	switch msg.Type {
	case tea.KeyEsc:
		m.view = viewDashboard
		m.form = nil
		return m, nil
	case tea.KeyEnter:
		submit, fields := m.form.submit, m.form.fields
		m.view = viewDashboard
		m.form = nil
		if submit != nil {
			return m, func() tea.Msg { return submit(fields) }
		}
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		m.form.next()
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		m.form.prev()
		return m, nil
	}
	// Everything else is caret editing, which formField already implements. This
	// switch used to restate all eight of editKey's cases by hand - a sixth copy
	// of the same single-line editor, and the one nobody remembered to update.
	m.form.focused().editKey(msg)
	return m, nil
}

// configFields builds the editable (non-secret) field set from a config: the
// general fields plus the per-stage models. The setup wizard and the deep-link
// form edit everything in one list; the "Edit config" screen shows the general
// half inline and links into the models screen (editmodels.go) for the rest.
func configFields(cfg *config.Config) []formField {
	return append(generalConfigFields(cfg), modelFields(cfg)...)
}

// generalConfigFields is the repo/branch/plan-review half of the form.
func generalConfigFields(cfg *config.Config) []formField {
	repo := config.Repo{}
	if len(cfg.Repos) > 0 {
		repo = cfg.Repos[0]
	}
	return []formField{
		{key: fldRepoPath, label: "Repository path for the Android app", value: repo.Path},
		{key: fldRepoBranch, label: "Branch name pattern", value: repo.Branch},
		{key: fldRepoBase, label: "Base branch to open PRs against (blank = repo default)", value: repo.Base},
		{key: fldReviewPlans, label: "Pause to review each plan before coding? (y/n)", value: boolToYN(cfg.ReviewPlans)},
		{key: fldReviewChange, label: "Review each change before its PR is created? (y/n)", value: boolToYN(cfg.ReviewChangesBeforePR())},
	}
}

// modelFields is the per-stage model half.
//
// Models are free-text on purpose: the set of usable model identifiers depends
// on the account (personal vs enterprise) and the org's policy, and Claude Code
// fetches that list at runtime - there's no offline list Apple Pie could show
// without it going stale or offering models a company forbids. Leave a model
// blank to inherit Claude Code's configured default; or type the exact id your
// account allows (e.g. "sonnet", "claude-opus-4-8", or a Bedrock/Vertex id).
func modelFields(cfg *config.Config) []formField {
	return []formField{
		{key: fldModelPlan, label: "Model for the planning stage (blank = Claude Code default)", value: cfg.ModelPlan},
		{key: fldModelImpl, label: "Model for the implementation stage (blank = default)", value: cfg.ModelImpl},
		{key: fldModelVerify, label: "Model for the verify stage (blank = the implementation model)", value: cfg.ModelVerify},
		{key: fldModelReview, label: "Model for the self-review stage (blank = default)", value: cfg.ModelReview},
		{key: fldModelCommentFix, label: "Model for review-comment fixes (blank = the implementation model)", value: cfg.ModelCommentFix},
	}
}

// The config form's field keys. These are the contract between configFields and
// applyConfigFields; nothing outside this file needs them.
const (
	fldRepoPath        = "repo.path"
	fldRepoBranch      = "repo.branch"
	fldRepoBase        = "repo.base"
	fldModelPlan       = "model.plan"
	fldModelImpl       = "model.impl"
	fldModelVerify     = "model.verify"
	fldModelReview     = "model.review"
	fldModelCommentFix = "model.commentfix"
	fldReviewPlans     = "review_plans"
	fldReviewChange    = "review_before_pr"
	fldAnthropicKey    = "secret.anthropic" // setup wizard only; goes to the keychain
)

// fieldValue returns the value of the field with the given key, or "".
func fieldValue(f []formField, key string) string {
	for _, fld := range f {
		if fld.key == key {
			return fld.value
		}
	}
	return ""
}

// boolToYN / parseYN render and parse the loose y/n form field for booleans.
func boolToYN(v bool) string {
	if v {
		return "y"
	}
	return "n"
}

func parseYN(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "true", "1":
		return true
	}
	return false
}

// applyConfigFields writes the (non-secret) form values back onto a config.
// Jira settings are intentionally not shown/edited here; any values already in
// the config file are preserved untouched.
//
// Fields are matched by key, never by position, so a caller may pass extra rows
// (the setup wizard appends the keychain-bound API key) or reorder them without
// silently writing values into the wrong settings.
func applyConfigFields(cfg *config.Config, f []formField) {
	repo := config.Repo{}
	if len(cfg.Repos) > 0 {
		repo = cfg.Repos[0]
	}
	for _, fld := range f {
		switch fld.key {
		case fldRepoPath:
			repo.Path = fld.value
		case fldRepoBranch:
			repo.Branch = fld.value
		case fldRepoBase:
			repo.Base = fld.value
		case fldModelPlan:
			cfg.ModelPlan = fld.value
		case fldModelImpl:
			cfg.ModelImpl = fld.value
		case fldModelVerify:
			cfg.ModelVerify = fld.value
		case fldModelReview:
			cfg.ModelReview = fld.value
		case fldModelCommentFix:
			cfg.ModelCommentFix = fld.value
		case fldReviewPlans:
			cfg.ReviewPlans = parseYN(fld.value)
		case fldReviewChange:
			v := parseYN(fld.value)
			cfg.ReviewBeforePR = &v
		default:
			applyBudgetField(cfg, fld.key, fld.value)
		}
	}
	cfg.Repos = []config.Repo{repo}
}

// openConfigForm opens the plain field form directly - no permissions
// section, no chooser. Used only for the repo-fix preflight's deep link
// (openConfigFormFocused below), which needs to land the user on exactly one
// field; the merged Edit Permissions + fields screen (permissions.go) is
// everyone else's path to config editing.
func (m *monitorModel) openConfigForm() {
	cfg, err := config.Load()
	if err != nil {
		// No config yet → fall back to the setup wizard.
		m.openSetupForm()
		return
	}
	m.form = &formModel{
		title:  "Edit config",
		note:   "~/.pie/config.toml · first repo",
		fields: configFields(cfg),
		submit: func(f []formField) tea.Msg {
			cfg, err := config.Load()
			if err != nil {
				cfg = &config.Config{}
			}
			applyConfigFields(cfg, f)
			if err := config.Save(cfg); err != nil {
				return formDoneMsg{err: err}
			}
			return formDoneMsg{notice: "config saved"}
		},
	}
	m.form.focusEnd()
	m.view = viewForm
}

// openConfigFormFocused opens the config form with the given field pre-focused,
// so a preflight can drop the user straight onto the setting it flagged. Falls
// back to whatever openConfigForm does (setup wizard) when there's no config.
func (m *monitorModel) openConfigFormFocused(key string) {
	m.openConfigForm()
	if m.form != nil {
		m.form.focusField(key)
	}
}

// preflightRepo validates the repository a new run would use, before dispatching
// one, so a bad repo path surfaces as an inline, fixable message instead of a
// run that dies at worktree creation. It checks the first configured repo - the
// same one the run wizard and pickers default to (run_pickers.go). A missing or
// empty config is not this check's problem: the setup wizard is the right nudge
// there, so it passes.
func (m monitorModel) preflightRepo() error {
	cfg, err := config.Load()
	if err != nil || len(cfg.Repos) == 0 {
		return nil
	}
	return git.ValidateRepo(config.Expand(cfg.Repos[0].Path))
}

func (m *monitorModel) openSetupForm() {
	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{
			Concurrency: 3, PollInterval: 30, MaxBudgetUSD: 5,
			Repos: []config.Repo{{
				Path: "~/src/android-app", Branch: "{ticket}-{slug}",
			}},
		}
	}
	fields := append(configFields(cfg),
		formField{key: fldAnthropicKey, label: "Anthropic API key (blank = skip, saved to keychain)", secret: true},
	)
	m.form = &formModel{
		title:  "Setup wizard",
		note:   "writes ~/.pie/config.toml; tokens go to the OS keychain",
		fields: fields,
		submit: func(f []formField) tea.Msg {
			cfg, err := config.Load()
			if err != nil {
				cfg = &config.Config{PollInterval: 30, Concurrency: 3, MaxBudgetUSD: 5}
			}
			// The secret row has no config key of its own, so applyConfigFields
			// ignores it - no "everything but the last field" slicing needed.
			applyConfigFields(cfg, f)
			if err := config.Save(cfg); err != nil {
				return formDoneMsg{err: err}
			}
			if key := strings.TrimSpace(fieldValue(f, fldAnthropicKey)); key != "" {
				// The config above is already saved, so a keychain failure is not
				// fatal - but it must not be reported as success, or the user
				// leaves setup believing the key is stored.
				if err := secrets.Set(secrets.Anthropic, key); err != nil {
					return formDoneMsg{notice: "setup saved, but the API key was not stored: " + err.Error()}
				}
			}
			return formDoneMsg{notice: "setup saved - pick Doctor to verify"}
		},
	}
	m.form.focusEnd()
	m.view = viewForm
}
