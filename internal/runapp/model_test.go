package runapp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A trimmed real `./gradlew -q tasks --all` from a flavored multi-module
// project: two app modules, a library, test-APK installs and noise.
const tasksAll = `
------------------------------------------------------------
Tasks runnable from root project 'acme'
------------------------------------------------------------

Android tasks
-------------
app:androidDependencies - Displays the Android dependencies of the project.

Install tasks
-------------
app:installProdDebug - Installs the ProdDebug build.
app:installProdDebugAndroidTest - Installs the android (on device) tests for the ProdDebug build.
app:installStagingDebug - Installs the StagingDebug build.
app:installStagingRelease - Installs the StagingRelease build.
feature:demo:installDebug - Installs the Debug build.
app:uninstallAll - Uninstall all applications.
core:assembleDebug - Assembles main outputs for the debug variant.
`

func TestParseTasks(t *testing.T) {
	got := ParseTasks(tasksAll)
	want := Model{Apps: []AppModule{
		{Path: ":app", Variants: []string{"prodDebug", "stagingDebug", "stagingRelease"}},
		{Path: ":feature:demo", Variants: []string{"debug"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseTasks =\n%+v\nwant\n%+v", got, want)
	}
	// A single-module project whose app is the Gradle root.
	root := ParseTasks("installDebug - Installs the Debug build.\ninstallRelease - Installs the Release build.\n")
	if len(root.Apps) != 1 || root.Apps[0].Path != ":" || AssembleTask(":", "debug") != "assembleDebug" {
		t.Errorf("root app = %+v", root)
	}
	if len(ParseTasks("core:assembleDebug - Assembles.\n").Apps) != 0 {
		t.Error("a library has no install task and is not an app")
	}
}

func TestDefaultVariantIsStudios(t *testing.T) {
	for in, want := range map[string]string{
		"debug,release":                      "debug",
		"prodDebug,stagingDebug,prodRelease": "prodDebug",
		"release":                            "release",
	} {
		vs := splitComma(in)
		if got := DefaultVariant(vs); got != want {
			t.Errorf("DefaultVariant(%s) = %q, want %q", in, got, want)
		}
	}
	if AssembleTask(":app", "stagingDebug") != ":app:assembleStagingDebug" {
		t.Error("AssembleTask")
	}
}

// The scan is re-run only when a build file changes - not a source file.
func TestSyncHashTracksBuildFilesOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("settings.gradle.kts", `include(":app")`)
	write("app/build.gradle.kts", `android { }`)
	write("app/src/Main.kt", "class Main")
	write("app/build/generated/build.gradle.kts", "ignored")
	h1 := SyncHash(dir)
	write("app/src/Main.kt", "class Main2")
	write("app/build/generated/build.gradle.kts", "still ignored")
	if SyncHash(dir) != h1 {
		t.Error("a source or build-output change must not invalidate the sync")
	}
	write("app/build.gradle.kts", `android { productFlavors { } }`)
	if SyncHash(dir) == h1 {
		t.Error("a build.gradle change must invalidate the sync")
	}
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
