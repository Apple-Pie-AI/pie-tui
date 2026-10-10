// The run recipe: per repo, what pie learned about running the app. Mostly a
// cache - the Sync result and the user's last choices - plus whatever a repair
// agent had to teach it for projects Studio's logic alone can't run (a
// different Gradle root, an extra flag, a JDK, an override for one variant).
// Repairs are replayed deterministically on every later run.
package runapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// Recipe is ~/.pie/run/<repo>-<hash>.json.
type Recipe struct {
	Version  int    `json:"version"`
	SyncHash string `json:"sync_hash,omitempty"`
	Model    Model  `json:"model"`

	// Learned by repair (validated before every save and every run).
	GradleDir  string              `json:"gradle_dir,omitempty"` // relative to the project dir
	GradleArgs []string            `json:"gradle_args,omitempty"`
	Env        map[string]string   `json:"env,omitempty"` // JAVA_HOME, GRADLE_OPTS only
	Overrides  map[string]Override `json:"overrides,omitempty"`
	Notes      string              `json:"notes,omitempty"`
	History    []Change            `json:"history,omitempty"`

	// The user's last choices.
	LastModule  string    `json:"last_module,omitempty"`
	LastVariant string    `json:"last_variant,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Override replaces what Studio's logic would derive for one variant.
type Override struct {
	AssembleTask   string `json:"assemble_task,omitempty"`
	ApplicationID  string `json:"application_id,omitempty"`
	LaunchActivity string `json:"launch_activity,omitempty"`
}

// Change is one entry of what was learned, newest last.
type Change struct {
	At      time.Time `json:"at"`
	Step    string    `json:"step"`
	Summary string    `json:"summary"`
}

const historyCap = 10

// RecipePath is where repo's recipe lives.
func RecipePath(repo string) string { return paths.RunRecipeFor(repo) }

// LoadRecipe reads repo's recipe; a missing file is an empty recipe.
func LoadRecipe(repo string) (*Recipe, error) {
	b, err := os.ReadFile(RecipePath(repo))
	if errors.Is(err, os.ErrNotExist) {
		return &Recipe{Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var r Recipe
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("run recipe %s is unreadable (%v) - delete it to start over", RecipePath(repo), err)
	}
	return &r, nil
}

// SaveRecipe writes repo's recipe atomically, after validating it.
func SaveRecipe(repo string, r *Recipe) error {
	if err := r.Validate(); err != nil {
		return err
	}
	r.Version, r.UpdatedAt = 1, time.Now()
	if len(r.History) > historyCap {
		r.History = r.History[len(r.History)-historyCap:]
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	p := RecipePath(repo)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

var (
	reTask     = regexp.MustCompile(`^(:[A-Za-z0-9_\-]+)*:?assemble[A-Z][A-Za-z0-9_]*$`)
	reAppID    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
	reActivity = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]*/[A-Za-z0-9_.$]+$`)
	reArg      = regexp.MustCompile(`^-[A-Za-z0-9_.:=/,+@-]*$`)
)

// envKeys are the only variables a recipe may set.
var envKeys = map[string]bool{"JAVA_HOME": true, "GRADLE_OPTS": true}

// Validate is the safety gate on anything learned: every value that reaches
// an argv or the environment must match a narrow shape. Commands never run
// through a shell, but a value must not even look like more than one argument.
func (r *Recipe) Validate() error {
	if d := r.GradleDir; d != "" {
		if filepath.IsAbs(d) || strings.Contains(filepath.ToSlash(d), "..") {
			return fmt.Errorf("gradle_dir %q must stay inside the project", d)
		}
	}
	for _, a := range r.GradleArgs {
		if !reArg.MatchString(a) {
			return fmt.Errorf("gradle arg %q is not a plain flag", a)
		}
	}
	for k, v := range r.Env {
		if !envKeys[k] {
			return fmt.Errorf("env %q is not allowed (only JAVA_HOME, GRADLE_OPTS)", k)
		}
		if strings.ContainsAny(v, "\n\x00") {
			return fmt.Errorf("env %s has a newline", k)
		}
	}
	for v, o := range r.Overrides {
		if o.AssembleTask != "" && !reTask.MatchString(o.AssembleTask) {
			return fmt.Errorf("variant %s: assemble task %q is not an assemble task", v, o.AssembleTask)
		}
		if o.ApplicationID != "" && !reAppID.MatchString(o.ApplicationID) {
			return fmt.Errorf("variant %s: %q is not an applicationId", v, o.ApplicationID)
		}
		if o.LaunchActivity != "" && !reActivity.MatchString(o.LaunchActivity) {
			return fmt.Errorf("variant %s: %q is not a package/activity", v, o.LaunchActivity)
		}
	}
	return nil
}

// envLines renders the recipe env as KEY=value lines for a step.
func (r *Recipe) envLines(serial string) string {
	var b strings.Builder
	for k, v := range r.Env {
		b.WriteString(k + "=" + v + "\n")
	}
	if serial != "" {
		b.WriteString("ANDROID_SERIAL=" + serial + "\n")
	}
	return b.String()
}

// note records a learned change.
func (r *Recipe) note(step, summary string) {
	r.History = append(r.History, Change{At: time.Now(), Step: step, Summary: summary})
}
