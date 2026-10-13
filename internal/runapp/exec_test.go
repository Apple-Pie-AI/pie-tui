package runapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAdb writes a stub adb that logs argv to $ADB_LOG and answers from env:
// ADB_INSTALL (install output), ADB_RESOLVE (resolve-activity output),
// ADB_PIDOF (pidof output).
func fakeAdb(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "adb.log")
	script := `#!/bin/sh
echo "$*" >> "` + log + `"
shift 2
case "$1" in
uninstall) echo "Success" ;;
install|install-multiple) echo "${ADB_INSTALL:-Success}" ;;
shell)
  case "$2" in
  cmd) printf '%s\n' "priority=0 preferredOrder=0" "${ADB_RESOLVE:-com.acme.staging/com.acme.MainActivity}" ;;
  am) echo "Status: ok" ;;
  pidof) echo "${ADB_PIDOF-4242}" ;;
  esac ;;
logcat) echo "E AndroidRuntime: FATAL EXCEPTION: main" ;;
esac
`
	p := filepath.Join(dir, "adb")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p, log
}

func testEnv(adb string) Env {
	return Env{ADB: adb, ToolEnv: os.Environ(), Log: &strings.Builder{}}
}

func adbCalls(log string) string { b, _ := os.ReadFile(log); return string(b) }

// A failing build reports its argv, exit code and output tail, repairable.
func TestBuildFailureCarriesTheTail(t *testing.T) {
	dir := t.TempDir()
	gw := "#!/bin/sh\necho \"> Task :app:compileKotlin FAILED\"\necho \"e: Unresolved reference: Foo\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "gradlew"), []byte(gw), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Build(context.Background(), testEnv("adb"), dir, ":app:assembleDebug", nil, "ANDROID_SERIAL=emulator-5554\n")
	var se *StepError
	if !errors.As(err, &se) || !se.Repairable || se.ExitCode != 1 || !strings.Contains(se.Tail, "Unresolved reference") {
		t.Fatalf("build error = %#v", err)
	}
}

func writeMetadata(t *testing.T, moduleDir, variant, appID string, apks ...string) {
	t.Helper()
	dir := filepath.Join(moduleDir, "build", "outputs", "apk", variant)
	_ = os.MkdirAll(dir, 0o755)
	els := ""
	for i, a := range apks {
		if i > 0 {
			els += ","
		}
		els += `{"type":"SINGLE","outputFile":"` + a + `"}`
	}
	md := `{"version":3,"applicationId":"` + appID + `","variantName":"` + variant + `","elements":[` + els + `]}`
	if err := os.WriteFile(filepath.Join(dir, "output-metadata.json"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The applicationId and APK come from AGP's metadata for the chosen variant.
func TestReadOutput(t *testing.T) {
	mod := t.TempDir()
	writeMetadata(t, mod, "prodDebug", "com.acme", "app-prod-debug.apk")
	writeMetadata(t, mod, "stagingDebug", "com.acme.staging", "app-staging-debug.apk")
	o, err := ReadOutput(mod, "stagingDebug")
	if err != nil || o.ApplicationID != "com.acme.staging" || !strings.HasSuffix(o.APKs[0], "stagingDebug/app-staging-debug.apk") {
		t.Fatalf("ReadOutput = %+v, %v", o, err)
	}
	if _, err := ReadOutput(mod, "release"); err == nil {
		t.Error("no metadata for the variant must be an error")
	}
}

// The old version is removed BEFORE installing - the fix for storage and
// signature-mismatch failures.
func TestReinstallUninstallsFirst(t *testing.T) {
	adb, log := fakeAdb(t)
	o := Output{ApplicationID: "com.acme.staging", APKs: []string{"/x/app.apk"}}
	if err := Reinstall(context.Background(), testEnv(adb), "emulator-5554", o); err != nil {
		t.Fatal(err)
	}
	calls := adbCalls(log)
	un := strings.Index(calls, "-s emulator-5554 uninstall com.acme.staging")
	in := strings.Index(calls, "-s emulator-5554 install -t /x/app.apk")
	if un < 0 || in < 0 || un > in {
		t.Fatalf("want uninstall then install:\n%s", calls)
	}
}

func TestReinstallOutOfStorage(t *testing.T) {
	adb, _ := fakeAdb(t)
	t.Setenv("ADB_INSTALL", "Failure [INSTALL_FAILED_INSUFFICIENT_STORAGE]")
	err := Reinstall(context.Background(), testEnv(adb), "emulator-5554",
		Output{ApplicationID: "com.acme", APKs: []string{"/x/app.apk"}})
	var se *StepError
	if !errors.As(err, &se) || se.Repairable || !strings.Contains(se.Error(), "out of storage") {
		t.Fatalf("err = %#v", err)
	}
}

func TestResolveAndLaunch(t *testing.T) {
	old := launchSettle
	launchSettle = time.Millisecond
	defer func() { launchSettle = old }()
	adb, log := fakeAdb(t)
	env := testEnv(adb)
	act, err := ResolveActivity(context.Background(), env, "emulator-5554", "com.acme.staging")
	if err != nil || act != "com.acme.staging/com.acme.MainActivity" {
		t.Fatalf("ResolveActivity = %q, %v", act, err)
	}
	if err := Launch(context.Background(), env, "emulator-5554", "com.acme.staging", act); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(adbCalls(log), "-s emulator-5554 shell am start -W -n com.acme.staging/com.acme.MainActivity") {
		t.Errorf("launch argv:\n%s", adbCalls(log))
	}

	// No LAUNCHER activity: repairable (the recipe can name one).
	t.Setenv("ADB_RESOLVE", "No activity found")
	env = testEnv(adb) // the stub reads its answers from the env it is given
	var se *StepError
	if _, err := ResolveActivity(context.Background(), env, "emulator-5554", "com.acme"); !errors.As(err, &se) || !se.Repairable {
		t.Errorf("no launcher = %#v", err)
	}

	// Started then died: a crash, not a run problem - and its log is captured.
	t.Setenv("ADB_PIDOF", "")
	env = testEnv(adb)
	err = Launch(context.Background(), env, "emulator-5554", "com.acme.staging", act)
	if !errors.As(err, &se) || se.Repairable || !strings.Contains(se.Error(), "crashed") {
		t.Fatalf("crash = %#v", err)
	}
	if !strings.Contains(adbCalls(log), "logcat -d -t 300 *:E") {
		t.Error("a crash must dump the error log")
	}
}
