// Package config loads ~/.pie/config.toml (Decision 15: global config keyed by repo path).
package config

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/BurntSushi/toml"
)

// Repo is one registered Android repository. Apple Pie no longer stores
// build/test commands here - the agent discovers and runs verification itself
// during the verify stage.
type Repo struct {
	Path   string `toml:"path"`
	Branch string `toml:"branch"`
	Base   string `toml:"base"`
}

// Config is the whole ~/.pie/config.toml.
type Config struct {
	JiraBaseURL  string `toml:"jira_base_url"`
	JiraEmail    string `toml:"jira_email"`
	PollInterval int    `toml:"poll_interval"`
	Concurrency  int    `toml:"concurrency"`
	// AllowedTools is an EXPERT knob: when set it replaces Apple Pie's additive
	// baseline (DefaultAllowedTools) - and only that. It never restricts the
	// user's own Claude Code settings, which are the base permission layer the
	// agent always keeps (see DefaultAllowedTools for the three-layer model).
	// Empty = track the built-in default across upgrades. Prefer
	// extra_allowed_tools for adding rules.
	AllowedTools string `toml:"allowed_tools"`
	// ExtraAllowedTools is APPENDED to the effective allowlist (the default, or
	// allowed_tools when set) rather than replacing it, so a team that needs one
	// more command doesn't fork the default list and stop receiving its upgrades.
	// This is also the key the permission-denial remediation tells users to set.
	ExtraAllowedTools string `toml:"extra_allowed_tools"`
	// Permissions selects how agents are gated: "" resolves per model -
	// "auto" (the classifier judges each action; no allowlist to maintain)
	// when the stage's model supports it, "allowlist" otherwise. Explicit
	// "allowlist", "auto", or "bypass" (no checks at all - a deliberate,
	// logged opt-out) override the resolution.
	Permissions string `toml:"permissions"`
	// ApprovalPolicy controls the permission callback's auto-answer: "" or
	// "auto" approves commands that already match the effective allowlist
	// without prompting (the human pre-approved those shapes by allowlisting
	// them); "always-ask" routes every callback to the dashboard.
	ApprovalPolicy string `toml:"approval_policy"`
	// MaxBudgetUSD is the default --max-budget-usd for every stage; the
	// per-stage keys below override it (0 = use this). See BudgetFor.
	MaxBudgetUSD           float64 `toml:"max_budget_usd"`
	MaxBudgetPlanUSD       float64 `toml:"max_budget_plan_usd"`
	MaxBudgetImplUSD       float64 `toml:"max_budget_impl_usd"`
	MaxBudgetReviewUSD     float64 `toml:"max_budget_review_usd"`
	MaxBudgetVerifyUSD     float64 `toml:"max_budget_verify_usd"`
	MaxBudgetCommentFixUSD float64 `toml:"max_budget_comment_fix_usd"`
	ModelPlan              string  `toml:"model_plan"`
	ModelImpl              string  `toml:"model_impl"`
	ModelReview            string  `toml:"model_review"`
	// ModelVerify, when set, runs the verify stage on its own model instead
	// of ModelImpl. Certification honesty is where model judgment is
	// cheapest to buy: a field session on a small model certified
	// verified:true off one test's logcat while the suite it launched was
	// still running (and later red).
	ModelVerify string `toml:"model_verify"`
	// ModelCommentFix, when set, runs the review-comment fix (and its revise
	// round, which resumes the same session) on its own model instead of
	// ModelImpl.
	ModelCommentFix string `toml:"model_comment_fix"`
	// SavedModels are model names the user added from the models screen,
	// offered in every stage's picker - passed to `claude --model` verbatim.
	SavedModels         []string `toml:"saved_models"`
	AVDName             string   `toml:"avd_name"`
	AndroidSDKPath      string   `toml:"android_sdk_path"`
	EmulatorIdleTimeout int      `toml:"emulator_idle_timeout"`
	TelemetryEnabled    *bool    `toml:"telemetry_enabled"`
	DeviceID            string   `toml:"device_id"`
	ReviewPlans         bool     `toml:"review_plans"` // default per-ticket answer for the plan-review gate
	// ReviewBeforePR pauses every ticket after a green verify so the human
	// reviews the local change before a PR is created. A pointer so "absent"
	// defaults to ON - stopping before anything leaves the machine is the
	// posture, opting back into auto-PR is the choice.
	ReviewBeforePR *bool `toml:"review_before_pr"`
	// What Apple Pie writes back to a PR after it addresses review comments.
	// Pointers so that "absent" is distinguishable from "false" and can default
	// to on, the same reason telemetry_enabled is a pointer.
	ReviewReply   *bool   `toml:"review_reply"`   // reply "fixed in <sha>" on each thread; default true
	ReviewResolve *bool   `toml:"review_resolve"` // also mark the thread resolved; default true
	Sandbox       Sandbox `toml:"sandbox"`
	Repos         []Repo  `toml:"repo"`
}

// Sandbox controls Claude Code's OS sandbox for Apple Pie's child `claude` runs.
// The default (Enabled=false) disables the sandbox for Apple Pie's autonomous
// runs so Gradle gets the network and `~/.gradle` writes it needs - Apple Pie's
// guardrail is the scoped --allowed-tools allowlist, not the OS sandbox. Set
// Enabled=true (e.g. on shared/CI machines) to keep the sandbox on; the
// Gradle-friendly defaults below are then merged with these extra lists.
type Sandbox struct {
	Enabled        bool     `toml:"enabled"`
	AllowedDomains []string `toml:"allowed_domains"` // extra network domains when Enabled
	AllowWrite     []string `toml:"allow_write"`     // extra writable paths when Enabled
}

// defaultSandboxDomains / defaultSandboxWrites are the network + filesystem
// allowances Gradle needs, baked in so an Enabled sandbox can still build a
// typical Android project. Users extend these via [sandbox] in config.
var (
	defaultSandboxDomains = []string{
		"repo.maven.apache.org", // Maven Central
		"dl.google.com",         // Google Maven (AndroidX, build tools)
		"*.gradle.org",          // plugins.gradle.org, services.gradle.org, …
	}
	defaultSandboxWrites = []string{"~/.gradle", "~/.konan", "~/.android"}
)

// SandboxSettingsJSON returns the `--settings` payload Apple Pie passes to its
// child `claude`, overriding the machine's global sandbox setting for that one
// process. Default (Enabled=false) → the sandbox is turned off for Apple Pie's
// runs. Enabled=true → the sandbox stays on with Gradle-friendly allowlists.
func (c *Config) SandboxSettingsJSON() string {
	type network struct {
		AllowedDomains []string `json:"allowedDomains,omitempty"`
	}
	type filesystem struct {
		AllowWrite []string `json:"allowWrite,omitempty"`
	}
	type sandbox struct {
		Enabled    bool        `json:"enabled"`
		Network    *network    `json:"network,omitempty"`
		Filesystem *filesystem `json:"filesystem,omitempty"`
	}
	s := sandbox{Enabled: c.Sandbox.Enabled}
	if c.Sandbox.Enabled {
		domains := append(append([]string{}, defaultSandboxDomains...), c.Sandbox.AllowedDomains...)
		writes := append(append([]string{}, defaultSandboxWrites...), c.Sandbox.AllowWrite...)
		if c.AndroidSDKPath != "" {
			writes = append(writes, c.AndroidSDKPath)
		}
		for i, w := range writes {
			writes[i] = expand(w)
		}
		s.Network = &network{AllowedDomains: domains}
		s.Filesystem = &filesystem{AllowWrite: writes}
	}
	b, err := json.Marshal(struct {
		Sandbox sandbox `json:"sandbox"`
	}{s})
	if err != nil {
		// Static shapes; marshal can't realistically fail. Fall back to the
		// disabled posture rather than emitting nothing.
		return `{"sandbox":{"enabled":false}}`
	}
	return string(b)
}

// DefaultAllowedTools is Apple Pie's recommended baseline allowlist for the
// implement and verify stages (Decision 16): file edits within the worktree
// plus the build/test commands an Android verify actually needs, and read-only
// inspection - no arbitrary Bash.
//
// Agent permissions layer ADDITIVELY, in three layers Apple Pie can widen but
// never narrow:
//  1. The user's/org's own Claude Code settings (managed, user, project
//     .claude/settings.json, and personal .claude/settings.local.json - which
//     worktree seeding deliberately carries over) always apply; org deny rules
//     always win. `--allowed-tools` cannot subtract from them.
//  2. This baseline (or allowed_tools when a user replaces it).
//  3. extra_allowed_tools, appended on top.
//
// Empirically (claude 2.1.220): compound commands are checked per segment, so
// `cd App && ./gradlew test` passes; but `gradle`, `chmod +x gradlew`, `adb`
// and `java` were all hard-denied under the old list, which is how a monorepo
// verify ended in a misleading needs-you. The allowlist is not a security
// boundary once ./gradlew is on it (build scripts are arbitrary code) - its
// job is preventing accidental damage, so build-adjacent commands belong here.
// (Note: read commands like `cat` can still reach paths outside the worktree;
// full FS confinement needs a sandbox.)
const DefaultAllowedTools = "Edit Write Read Glob Grep MultiEdit TodoWrite Skill " +
	"Bash(./gradlew:*) Bash(gradle:*) Bash(cd:*) Bash(chmod +x:*) " +
	"Bash(adb:*) Bash(java:*) Bash(mkdir:*) Bash(which:*) Bash(pwd:*) Bash(echo:*) " +
	"Bash(ls:*) Bash(cat:*) Bash(head:*) Bash(tail:*) Bash(tee:*) " +
	"Bash(grep:*) Bash(rg:*) Bash(find:*) Bash(git status:*) " +
	"Bash(git diff:*) Bash(git log:*) Bash(git show:*)"

// prevDefaultAllowedTools is the default before Skill and tee joined it, byte
// for byte - same upgrade treatment as legacyAllowedTools below.
const prevDefaultAllowedTools = "Edit Write Read Glob Grep MultiEdit TodoWrite " +
	"Bash(./gradlew:*) Bash(gradle:*) Bash(cd:*) Bash(chmod +x:*) " +
	"Bash(adb:*) Bash(java:*) Bash(mkdir:*) Bash(which:*) Bash(pwd:*) Bash(echo:*) " +
	"Bash(ls:*) Bash(cat:*) Bash(head:*) Bash(tail:*) " +
	"Bash(grep:*) Bash(rg:*) Bash(find:*) Bash(git status:*) " +
	"Bash(git diff:*) Bash(git log:*) Bash(git show:*)"

// legacyAllowedTools is the pre-broadening default, byte for byte. Configs
// that carry it in allowed_tools were baked by an old TUI save, not authored -
// applyDefaults resets them to "" so they track DefaultAllowedTools again.
const legacyAllowedTools = "Edit Write Read Glob Grep MultiEdit TodoWrite " +
	"Bash(./gradlew:*) Bash(ls:*) Bash(cat:*) Bash(head:*) Bash(tail:*) " +
	"Bash(grep:*) Bash(rg:*) Bash(find:*) Bash(git status:*) " +
	"Bash(git diff:*) Bash(git log:*) Bash(git show:*)"

// NormalizeAllowedTools returns the canonical stored form of an allowed_tools
// value: "" when it is blank or is (modulo whitespace) the CURRENT default -
// so a config keeps tracking default upgrades instead of freezing today's
// list (the baked-default bug legacyAllowedTools exists to undo). A genuinely
// hand-tuned list passes through trimmed.
func NormalizeAllowedTools(s string) string {
	if strings.Join(strings.Fields(s), " ") == DefaultAllowedTools {
		return ""
	}
	return strings.TrimSpace(s)
}

// EffectiveModelVerify is the model the verify stage runs on: model_verify
// when set, else the implementation model. Every verify spawn site goes
// through this - reading ModelVerify directly would silently skip the
// fallback.
func (c *Config) EffectiveModelVerify() string {
	if c.ModelVerify != "" {
		return c.ModelVerify
	}
	return c.ModelImpl
}

// EffectiveModelCommentFix is the model the review-comment fix stage runs on:
// model_comment_fix when set, else the implementation model. Every comment-fix
// spawn site goes through this - reading ModelCommentFix directly would
// silently skip the fallback.
func (c *Config) EffectiveModelCommentFix() string {
	if c.ModelCommentFix != "" {
		return c.ModelCommentFix
	}
	return c.ModelImpl
}

// EffectiveAllowedTools is the allowlist the implement/verify/comment-fix stages must pass
// to the agent: allowed_tools (or the default when unset) plus the append-only
// extras. It is Apple Pie's ADDITIVE contribution only - the agent's session
// merges it on top of the user's own Claude Code settings (see the three-layer
// model on DefaultAllowedTools). Every spawn site goes through this - reading
// AllowedTools directly would silently drop a user's extra_allowed_tools.
func (c *Config) EffectiveAllowedTools() string {
	base := c.AllowedTools
	if base == "" {
		base = DefaultAllowedTools
	}
	if extra := strings.TrimSpace(c.ExtraAllowedTools); extra != "" {
		return base + " " + extra
	}
	return base
}

// validRuleToken reports whether one allowlist token is something Claude Code
// can act on: a bare tool name (letters only), Bash(<head>:*) with a
// command-shaped head, or an exact-command rule Bash(<verbatim command>) -
// the shape "Allow & remember" writes when the generalized head rules are
// already present yet the CLI refused anyway. The denial suggester once
// emitted prose-derived garbage like Bash(Unable:*) and bare unknown words; a
// malformed token in --allowed-tools helps nobody and may degrade matching of
// the valid ones. Without the exact-rule arm here, a remembered exact rule
// would be silently stripped by SanitizeExtraTools on the very next Load.
func validRuleToken(tok string) bool {
	if bashRule.MatchString(tok) {
		inner := tok[len("Bash(") : len(tok)-1]
		if prefix, ok := strings.CutSuffix(inner, ":*"); ok {
			// Prefix rule: validate the head (up to any further colon,
			// matching the granularity the defaults use). Prose is not a
			// head: every word must look like a path/flag/binary.
			head := strings.SplitN(prefix, ":", 2)[0]
			if head == "" {
				return false
			}
			for _, w := range strings.Fields(head) {
				if !headWord.MatchString(w) {
					return false
				}
			}
			return true
		}
		return exactCommandShaped(inner)
	}
	return toolName.MatchString(tok)
}

// exactCommandShaped is the config-side test for an exact-command rule's
// inner text: one line, sane length, and a first token (after VAR= prefixes)
// that looks like a binary rather than an English sentence opener. Mirrors
// the agent package's command-shape checks without importing it (wrong
// dependency direction).
func exactCommandShaped(cmd string) bool {
	if cmd == "" || len([]rune(cmd)) > 200 || strings.ContainsAny(cmd, "\n\r") {
		return false
	}
	fields := strings.Fields(cmd)
	i := 0
	for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "=") {
		i++
	}
	return i < len(fields) && headWord.MatchString(fields[i])
}

var (
	bashRule = regexp.MustCompile(`^Bash\([^)]+\)$`)
	headWord = regexp.MustCompile(`^[a-z0-9_./~+-][A-Za-z0-9_./~+=-]*$`)
	toolName = regexp.MustCompile(`^[A-Z][A-Za-z]+$`)
)

// SanitizeExtraTools drops malformed tokens from an extras string, returning
// the clean string and what was dropped. Load() applies it, which is what
// repairs a config the old suggester polluted.
func SanitizeExtraTools(extras string) (clean string, dropped []string) {
	// Bash(...) rules may contain spaces ("Bash(git diff:*)"): tokenize on the
	// rule shape first, then bare words.
	var kept []string
	for _, tok := range SplitRules(extras) {
		if validRuleToken(tok) {
			kept = append(kept, tok)
		} else {
			dropped = append(dropped, tok)
		}
	}
	return strings.Join(kept, " "), dropped
}

var ruleToken = regexp.MustCompile(`Bash\([^)]*\)|\S+`)

// SplitRules splits an allowlist/extras string into whole rules - a
// Bash(...) rule is one token even when its head has spaces. Byte-identical
// regex to agent.SplitRules, kept as two copies deliberately: config must
// not import agent (config is the lower-level package the agent layer
// depends on, not the reverse).
func SplitRules(s string) []string { return ruleToken.FindAllString(s, -1) }

// ValidRule reports whether one allowlist token is something Claude Code can
// act on: a bare tool name, a Bash(<head>:*) prefix rule, or an exact
// Bash(<verbatim command>) rule. This is the public contract an editor (the
// TUI's permissions screen, or any future one) validates a hand-typed rule
// against before adding it; validRuleToken stays unexported so this wrapper
// is the one entry point outside the package.
func ValidRule(tok string) bool { return validRuleToken(tok) }

// AutoCapable reports whether a model can run under --permission-mode auto,
// which is gated to the most capable tiers. Empty means "claude's default
// model", which on a current install is such a tier - but pie cannot know the
// user's default, so empty resolves to allowlist and users opt in explicitly.
func AutoCapable(model string) bool {
	m := strings.ToLower(model)
	for _, p := range []string{"claude-fable", "fable", "claude-opus-5", "opus-5"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

// PermissionModeFor resolves the gate for one stage's model: the explicit
// config setting wins; empty picks auto for models that support it and the
// allowlist for everything else.
func (c *Config) PermissionModeFor(model string) string {
	switch c.Permissions {
	case "auto", "bypass", "allowlist":
		return c.Permissions
	}
	if AutoCapable(model) {
		return "auto"
	}
	return "allowlist"
}

// GenerateDeviceID returns a random UUID v4 string. Call once at consent time.
func GenerateDeviceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Save writes the config to ~/.pie/config.toml (0600), creating/truncating it.
// Same encoding the init wizard uses; reusable by the TUI config/setup forms.
func Save(c *Config) error {
	f, err := os.OpenFile(paths.Config(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// Load reads and validates the config, applying defaults.
func Load() (*Config, error) {
	b, err := os.ReadFile(paths.Config())
	if err != nil {
		return nil, fmt.Errorf("read config (%s): %w - run `pie init`", paths.Config(), err)
	}
	var c Config
	if err := toml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	return &c, nil
}

// ReviewChangesBeforePR reports whether the pipeline parks after a green
// verify for the human to review the change before any PR exists. Default on.
func (c *Config) ReviewChangesBeforePR() bool {
	return c.ReviewBeforePR == nil || *c.ReviewBeforePR
}

// ReplyOnReview reports whether to post "fixed in <sha>" on each review thread
// Apple Pie addressed. Default on: a PR whose comments were silently acted on
// leaves the reviewer to work out what changed from the diff alone.
func (c *Config) ReplyOnReview() bool {
	return c.ReviewReply == nil || *c.ReviewReply
}

// ResolveReviewThreads reports whether to mark an addressed thread resolved.
// Default on, but separately switchable from ReplyOnReview: on some teams
// resolving is the reviewer's call, and replying is welcome while resolving is
// presumptuous.
func (c *Config) ResolveReviewThreads() bool {
	return c.ReviewResolve == nil || *c.ReviewResolve
}

func (c *Config) applyDefaults() {
	if clean, dropped := SanitizeExtraTools(c.ExtraAllowedTools); len(dropped) > 0 {
		// Repair configs the old suggester polluted with prose-derived rules.
		// Silent beyond this: the tokens were never matchable, so dropping
		// them changes no behavior except un-breaking the flag they rode in.
		c.ExtraAllowedTools = clean
	}
	// Model identifiers reach `claude --model` verbatim, and the Effective*
	// fallbacks treat any non-empty string as a configured override - so a
	// stray space ("opus ", " ") would both defeat the fallback and break the
	// spawn hours later, headless. Normalize once here; every reader goes
	// through Load.
	for _, m := range []*string{&c.ModelPlan, &c.ModelImpl, &c.ModelReview, &c.ModelVerify, &c.ModelCommentFix} {
		*m = strings.TrimSpace(*m)
	}
	if c.PollInterval == 0 {
		c.PollInterval = 30
	}
	if c.Concurrency == 0 {
		c.Concurrency = 3
	}
	if c.MaxBudgetUSD == 0 {
		c.MaxBudgetUSD = DefaultMaxBudgetUSD
	}
	// Models are intentionally left empty by default: an empty value makes the
	// agent omit `--model`, so Claude Code uses whatever default the user/org has
	// configured (via `claude`'s /model, respecting enterprise policy). Users can
	// set per-stage overrides to any identifier their account allows (including
	// non-Claude models on Bedrock/Vertex). Nothing is hardcoded here.
	if c.AndroidSDKPath == "" {
		if v := os.Getenv("ANDROID_HOME"); v != "" {
			c.AndroidSDKPath = v
		} else {
			c.AndroidSDKPath = os.Getenv("ANDROID_SDK_ROOT")
		}
	}
	if c.EmulatorIdleTimeout == 0 {
		c.EmulatorIdleTimeout = 30
	}
	// The TUI's config form round-trips Load→Save, which used to bake the
	// then-current default allowlist into allowed_tools as if the user had typed
	// it. Treat that exact literal as "unset" so those users keep receiving
	// default upgrades; a hand-edited list is respected untouched.
	if c.AllowedTools == prevDefaultAllowedTools {
		c.AllowedTools = ""
	}
	if c.AllowedTools == legacyAllowedTools {
		c.AllowedTools = ""
	}
	// And the generic form of the same guard: any stored value equal to the
	// CURRENT default is a bake, not an authored list. (The legacy literal
	// above must stay - it no longer equals the current default.)
	c.AllowedTools = NormalizeAllowedTools(c.AllowedTools)
	for i := range c.Repos {
		r := &c.Repos[i]
		r.Path = expand(r.Path)
		if r.Branch == "" {
			r.Branch = "{ticket}-{slug}"
		}
	}
}

func expand(p string) string {
	if strings.HasPrefix(p, "~") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// Expand resolves a leading ~ to the user's home directory.
func Expand(p string) string { return expand(p) }

// Resolve picks the repo to act on. Empty repoPath returns the first configured repo.
func (c *Config) Resolve(repoPath string) (*Repo, error) {
	if len(c.Repos) == 0 {
		return nil, fmt.Errorf("no [[repo]] configured in %s", paths.Config())
	}
	if repoPath == "" {
		return &c.Repos[0], nil
	}
	rp := expand(repoPath)
	for i := range c.Repos {
		if c.Repos[i].Path == rp {
			return &c.Repos[i], nil
		}
	}
	return nil, fmt.Errorf("repo %q not found in config", repoPath)
}
