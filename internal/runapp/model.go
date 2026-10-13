// Package runapp runs an Android app on the active device the way Android
// Studio's Run button does, deterministically - and calls an agent only to
// repair a step that failed.
//
// Studio needs no exploration to run a project: Gradle sync gives it the
// app modules and their build variants, Run builds the variant, installs the
// APK and starts the launcher activity. Every one of those has a terminal
// equivalent:
//
//	app modules + variants  ./gradlew tasks --all   (only app modules have install<Variant>)
//	build                   ./gradlew :app:assemble<Variant>
//	applicationId + APK     build/outputs/apk/.../output-metadata.json (written by AGP)
//	launcher activity       adb shell cmd package resolve-activity -c LAUNCHER <appId>
//
// so a normal project runs with no agent at all, first run included.
package runapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Model is what Studio's Gradle sync would know: the app modules and their
// installable variants.
type Model struct {
	Apps []AppModule `json:"apps"`
}

// AppModule is one com.android.application module.
type AppModule struct {
	Path     string   `json:"path"`     // ":app", ":feature:app"
	Variants []string `json:"variants"` // "debug", "stagingDebug", ...
}

// Module returns the app module at path.
func (m Model) Module(path string) (AppModule, bool) {
	for _, a := range m.Apps {
		if a.Path == path {
			return a, true
		}
	}
	return AppModule{}, false
}

// installTask matches `tasks --all` lines naming an install task:
// "app:installStagingDebug - Installs the StagingDebug build." (a subproject)
// or "installDebug - ..." (an app module at the Gradle root).
var installTask = regexp.MustCompile(`^(?:([A-Za-z0-9_\-:]+):)?install([A-Z][A-Za-z0-9_]*)\s+-\s`)

// ParseTasks reads `gradlew tasks --all` output into a Model. Test-APK
// installs (…AndroidTest) are not runnable apps and are skipped.
func ParseTasks(out string) Model {
	byModule := map[string][]string{}
	var order []string
	for _, line := range strings.Split(out, "\n") {
		m := installTask.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		variant := m[2]
		if strings.HasSuffix(variant, "AndroidTest") || variant == "All" {
			continue
		}
		mod := ":" + m[1]
		if m[1] == "" {
			mod = ":"
		}
		if _, seen := byModule[mod]; !seen {
			order = append(order, mod)
		}
		byModule[mod] = appendUnique(byModule[mod], lowerFirst(variant))
	}
	var model Model
	for _, mod := range order {
		vs := byModule[mod]
		sort.Strings(vs)
		model.Apps = append(model.Apps, AppModule{Path: mod, Variants: vs})
	}
	return model
}

// DefaultVariant is the variant Studio would pick: "debug" when it exists,
// else the first *Debug (debug-signed, so installable), else the first.
func DefaultVariant(vs []string) string {
	for _, v := range vs {
		if v == "debug" {
			return v
		}
	}
	for _, v := range vs {
		if strings.HasSuffix(v, "Debug") {
			return v
		}
	}
	if len(vs) > 0 {
		return vs[0]
	}
	return ""
}

// Sync runs the task scan from the Gradle root.
func Sync(ctx context.Context, e Env, gradleDir string, args []string) (Model, error) {
	argv := append([]string{wrapperPath(gradleDir), "-q", "tasks", "--all"}, args...)
	out, err := e.run(ctx, gradleDir, StepSync, true, "", argv...)
	if err != nil {
		return Model{}, err
	}
	model := ParseTasks(out)
	if len(model.Apps) == 0 {
		return model, &StepError{Step: StepSync, Argv: argv, Repairable: true,
			Msg: "no app module found - no :module:install<Variant> task in `gradlew tasks --all`"}
	}
	return model, nil
}

// SyncHash fingerprints the build files; the task scan is re-run only when it
// changes, the way Studio asks for a sync after a build-file edit.
func SyncHash(projectDir string) string {
	h := sha256.New()
	var files []string
	_ = filepath.WalkDir(projectDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "build", ".gradle", ".git", ".idea", "node_modules", ".agent", ".kotlin", ".cxx":
				return filepath.SkipDir
			}
			return nil
		}
		switch d.Name() {
		case "settings.gradle", "settings.gradle.kts", "build.gradle", "build.gradle.kts",
			"gradle.properties", "libs.versions.toml":
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	for _, f := range files {
		rel, _ := filepath.Rel(projectDir, f)
		_, _ = io.WriteString(h, rel+"\x00")
		if b, err := os.ReadFile(f); err == nil {
			_, _ = h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func wrapperPath(gradleDir string) string { return filepath.Join(gradleDir, "gradlew") }

func appendUnique(xs []string, x string) []string {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
