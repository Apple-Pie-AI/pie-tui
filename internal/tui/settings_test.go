package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// preflightRepo flags a configured-but-missing repo path, but stays out of the
// way when there's no config at all (that's the setup wizard's job).
func TestPreflightRepo(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// No config saved → Load fails → preflight must pass (nil).
	if err := (monitorModel{}).preflightRepo(); err != nil {
		t.Errorf("no config should pass preflight, got %v", err)
	}
	// Configured but missing path → preflight must flag it.
	if err := config.Save(&config.Config{Repos: []config.Repo{{Path: filepath.Join(t.TempDir(), "nope")}}}); err != nil {
		t.Fatal(err)
	}
	if err := (monitorModel{}).preflightRepo(); err == nil {
		t.Error("missing repo path should fail preflight")
	}
}

// Regression: "Start new" must open the ticket-creation flow even when the repo
// path is broken. The repo has nothing to do with composing a ticket, so the
// preflight belongs at dispatch, not here - a bad repo must never block entry.
func TestActRunEntersFlowDespiteBadRepo(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{
		Concurrency: 3,
		Repos:       []config.Repo{{Path: filepath.Join(t.TempDir(), "does-not-exist"), Branch: "{ticket}-{slug}"}},
	}); err != nil {
		t.Fatal(err)
	}

	mm, _ := monitorModel{run: newRunState()}.doAction(actRun)
	hub := mm.(monitorModel)

	if hub.view == viewForm {
		t.Fatalf("Start new must not open the config form; a bad repo can't block ticket entry (view=%d)", hub.view)
	}
	if hub.view != viewRunInput {
		t.Errorf("Start new should enter the run input flow, got view=%d", hub.view)
	}
}

// At dispatch (launchOrClose) a broken repo path bounces the user to the repo-fix
// screen with the reason inline - and leaves the composed tickets intact so
// nothing typed is lost.
func TestLaunchPreflightBlocksOnBadRepo(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{
		Concurrency: 3,
		Repos:       []config.Repo{{Path: filepath.Join(t.TempDir(), "does-not-exist"), Branch: "{ticket}-{slug}"}},
	}); err != nil {
		t.Fatal(err)
	}

	m := monitorModel{run: newRunState()}
	m.run.tickets = []pendingTicket{{kind: "jira", id: "KAN-1", title: "KAN-1"}}
	mm, cmd := m.launchOrClose()
	hub := mm.(monitorModel)

	if cmd != nil {
		t.Error("a blocked launch must not dispatch a run")
	}
	if hub.view != viewRepoFix {
		t.Fatalf("bad repo should open the repo-fix screen; view=%d", hub.view)
	}
	if !strings.Contains(hub.repofix.reason, "does not exist") {
		t.Errorf("repo-fix should carry the reason, got %q", hub.repofix.reason)
	}
	if len(hub.run.tickets) != 1 {
		t.Errorf("composed tickets must be preserved on a blocked launch, got %d", len(hub.run.tickets))
	}
}

// The config form round-trips the plan-review toggle as a loose y/n value.
func TestConfigFieldsReviewPlansYN(t *testing.T) {
	// ReviewPlans=true renders "y" and parses back to true.
	// Look the field up by key, not position - fields are matched by key in
	// applyConfigFields for exactly this reason, and a positional index here
	// broke the last time a field was added.
	rpIdx := func(f []formField) int {
		for i := range f {
			if f[i].key == fldReviewPlans {
				return i
			}
		}
		t.Fatal("no review_plans field in the form")
		return -1
	}
	in := &config.Config{ReviewPlans: true, Repos: []config.Repo{{Path: "/r", Branch: "b"}}}
	fields := configFields(in)
	if fields[rpIdx(fields)].value != "y" {
		t.Errorf("review-plans field should render 'y', got %q", fields[rpIdx(fields)].value)
	}
	out := &config.Config{}
	applyConfigFields(out, fields)
	if !out.ReviewPlans {
		t.Error("y should parse back to ReviewPlans=true")
	}

	// ReviewPlans=false renders "n" and parses back to false; loose parsing.
	in.ReviewPlans = false
	fields = configFields(in)
	if fields[rpIdx(fields)].value != "n" {
		t.Errorf("review-plans field should render 'n', got %q", fields[rpIdx(fields)].value)
	}
	fields[rpIdx(fields)].value = "yes" // loose truthy
	applyConfigFields(out, fields)
	if !out.ReviewPlans {
		t.Error("'yes' should parse to true")
	}
	fields[rpIdx(fields)].value = "nope"
	applyConfigFields(out, fields)
	if out.ReviewPlans {
		t.Error("'nope' should parse to false")
	}
}

func TestConfigFieldsRoundTrip(t *testing.T) {
	in := &config.Config{
		Concurrency:     5,
		ModelPlan:       "claude-opus-4-8",
		ModelImpl:       "claude-sonnet-4-6",
		ModelReview:     "claude-haiku-4-5",
		ModelVerify:     "claude-sonnet-5",
		ModelCommentFix: "claude-opus-5",
		Repos:           []config.Repo{{Path: "/r", Branch: "b", Base: "main"}},
	}
	out := &config.Config{}
	applyConfigFields(out, configFields(in))

	if out.ModelPlan != "claude-opus-4-8" || out.ModelImpl != "claude-sonnet-4-6" || out.ModelReview != "claude-haiku-4-5" {
		t.Errorf("model round-trip failed: plan=%q impl=%q review=%q", out.ModelPlan, out.ModelImpl, out.ModelReview)
	}
	if out.ModelVerify != "claude-sonnet-5" {
		t.Errorf("verify model round-trip failed: got %q", out.ModelVerify)
	}
	if out.ModelCommentFix != "claude-opus-5" {
		t.Errorf("comment-fix model round-trip failed: got %q", out.ModelCommentFix)
	}
	if len(out.Repos) != 1 || out.Repos[0].Path != "/r" || out.Repos[0].Branch != "b" || out.Repos[0].Base != "main" {
		t.Errorf("repo round-trip failed: %+v", out.Repos)
	}
}

// Jira fields are no longer editable in the form, but any existing config value
// must be preserved (not wiped) when the form is saved.
func TestApplyConfigFieldsPreservesJira(t *testing.T) {
	cfg := &config.Config{
		JiraBaseURL: "https://x.atlassian.net",
		JiraEmail:   "me@x.com",
		Repos:       []config.Repo{{Path: "/r", Branch: "b"}},
	}
	applyConfigFields(cfg, configFields(cfg))
	if cfg.JiraBaseURL != "https://x.atlassian.net" || cfg.JiraEmail != "me@x.com" {
		t.Errorf("existing Jira config must be preserved, got base=%q email=%q", cfg.JiraBaseURL, cfg.JiraEmail)
	}
}

// actConfig's wiring (opens viewEditConfig with the allowlist link + 7
// fields, and the link opens the nested allowlist screen) is pinned in
// editconfig_test.go, next to the screen it wires.

// configFields and applyConfigFields used to be coupled by list position:
// inserting a row in the middle of one wrote every later value into the wrong
// config key, and config.Save persisted the damage. They agree by key now, so
// shuffling the form must round-trip a config unchanged.
func TestApplyConfigFieldsIsPositionIndependent(t *testing.T) {
	src := &config.Config{
		ModelPlan: "opus", ModelImpl: "sonnet", ModelReview: "haiku",
		ReviewPlans: true,
		Repos:       []config.Repo{{Path: "/src/app", Branch: "pie/{ticket}", Base: "develop"}},
	}
	fields := configFields(src)

	// Reverse the form, and stick an unrecognised row in the middle - the setup
	// wizard appends exactly such a row for the keychain-bound API key.
	shuffled := make([]formField, 0, len(fields)+1)
	for i := len(fields) - 1; i >= 0; i-- {
		shuffled = append(shuffled, fields[i])
		if i == 3 {
			shuffled = append(shuffled, formField{key: fldAnthropicKey, value: "sk-secret", secret: true})
		}
	}

	got := &config.Config{}
	applyConfigFields(got, shuffled)

	if len(got.Repos) != 1 {
		t.Fatalf("Repos = %+v, want exactly one", got.Repos)
	}
	if got.Repos[0] != src.Repos[0] {
		t.Errorf("repo round-trip = %+v, want %+v", got.Repos[0], src.Repos[0])
	}
	if got.ModelPlan != "opus" || got.ModelImpl != "sonnet" || got.ModelReview != "haiku" {
		t.Errorf("models round-trip = %q/%q/%q, want opus/sonnet/haiku",
			got.ModelPlan, got.ModelImpl, got.ModelReview)
	}
	if !got.ReviewPlans {
		t.Error("ReviewPlans should round-trip true")
	}
	// The secret must never reach the config file - it belongs in the keychain.
	if got.ModelPlan == "sk-secret" || got.Repos[0].Path == "sk-secret" {
		t.Error("the keychain-bound API key leaked into a config field")
	}
}

// Every field the form offers must have a key, or applyConfigFields silently
// drops the user's edit.
func TestEveryConfigFieldHasAKey(t *testing.T) {
	for i, f := range configFields(&config.Config{}) {
		if f.key == "" {
			t.Errorf("configFields[%d] (%q) has no key - its value would be discarded on save", i, f.label)
		}
	}
}

// fieldValue is how the setup wizard finds the secret row now that it no longer
// slices "everything but the last field".
func TestFieldValueByKey(t *testing.T) {
	f := []formField{{key: "a", value: "1"}, {key: "b", value: "2"}}
	if got := fieldValue(f, "b"); got != "2" {
		t.Errorf("fieldValue(b) = %q, want 2", got)
	}
	if got := fieldValue(f, "missing"); got != "" {
		t.Errorf("fieldValue(missing) = %q, want empty", got)
	}
}
