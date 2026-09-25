//go:build evals

// Baseline evals for PR B, agent-side. Offline and deterministic (stub claude
// on PATH). These assert the CURRENT gaps on purpose - PR B flips them into
// the honest/expected behavior. Run with:
//
//	GOTOOLCHAIN=auto go test -tags evals ./internal/agent -run TestEvalBaseline -v
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EVAL 3 (baseline): the agent subprocess inherits pie's environment verbatim.
// When pie runs without JAVA_HOME (daemon, GUI-launched, or a Mac where Java
// exists only inside Android Studio), the agent gets a Java-less world - the
// condition that made it invent gradle-daemon-jvm.properties in production.
// Baseline: pie injects nothing.
func TestEvalBaselineNoJavaHomeInjected(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
echo '{"type":"system","subtype":"init","session_id":"evalenvp","model":"stub"}'
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"JAVA_HOME=[%s]"}]}}\n' "$JAVA_HOME"
echo '{"type":"result","subtype":"success","result":"done","session_id":"evalenvp"}'
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JAVA_HOME", "") // pie's own environment has no usable JAVA_HOME

	// Deterministic across machines: point detection at a fake JDK rather than
	// whatever this laptop has installed.
	jdk := t.TempDir()
	if err := os.MkdirAll(filepath.Join(jdk, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jdk, "bin", "java"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origCandidates, origMac := javaHomeCandidates, macJavaHome
	t.Cleanup(func() { javaHomeCandidates, macJavaHome = origCandidates, origMac })
	javaHomeCandidates = []string{jdk}

	var lines []string
	logf := func(f string, a ...interface{}) { lines = append(lines, fmt.Sprintf(f, a...)) }
	if _, err := Run(context.Background(), "probe", Options{WorktreeDir: t.TempDir(), Logf: logf}); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(lines, "\n")
	t.Logf("agent saw:\n%s", all)

	// Flipped after PR B: the agent now receives the detected JDK, and the
	// injection is on the record in the log.
	if !strings.Contains(all, "JAVA_HOME=["+jdk+"]") {
		t.Errorf("agent did not receive the detected JAVA_HOME")
	}
	if !strings.Contains(all, "using detected JDK") {
		t.Errorf("injection not logged")
	}
	t.Log("FLIPPED EVAL HOLDS: detected JDK injected into the agent environment and logged")
}

// EVAL 4 (baseline): the prompts never tell the agent what it may run, never
// define the BLOCKED: reporting convention, never ban build-config workarounds,
// and never steer KMP verifies toward unit-test tasks. (The ASKME-TEST session
// burned four denied Bash calls discovering the plan-stage boundary by trial
// and error - because nothing told it.)
func TestEvalBaselinePromptGaps(t *testing.T) {
	// Flipped after PR B: every gap is now filled.
	plan := BuildPlanPrompt("T-1", "s", "d", PlanTools)
	impl := BuildImplPrompt("T-1", "s", "d", &Plan{Plan: "p"}, "Bash(x:*)", "")
	verify := BuildVerifyPrompt("T-1", "s", "/wt", false, true, "Bash(x:*)", "", "")

	checks := []struct {
		name, prompt, marker string
	}{
		{"plan prompt names its allowlist", plan, "allowlist"},
		{"impl prompt names its allowlist", impl, "allowlist"},
		{"verify prompt names its allowlist", verify, "allowlist"},
		{"verify prompt defines BLOCKED convention", verify, "BLOCKED:"},
		{"verify prompt bans build-config workarounds", verify, "gradle-daemon-jvm.properties"},
		{"verify prompt steers KMP to unit-test tasks", verify, "Multiplatform"},
	}
	for _, c := range checks {
		if !strings.Contains(c.prompt, c.marker) {
			t.Errorf("FLIPPED EVAL BROKEN: %s (%q absent)", c.name, c.marker)
		} else {
			t.Logf("FLIPPED EVAL HOLDS: %s", c.name)
		}
	}
}
