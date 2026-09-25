//go:build repro

// Repro for BUG 1: a headless claude run denied by the --allowed-tools
// allowlist, exercised through the REAL agent.Run/buildArgs/parseStream path.
// It demonstrates both halves of the bug:
//  1. the command an agent naturally tries in a monorepo (./App/gradlew, the
//     project's wrapper invoked from the git-root cwd) is refused under the
//     default allowlist's Bash(./gradlew:*) prefix rule;
//  2. the denial is UNDETECTABLE programmatically: Run returns err=nil and no
//     denial data (parseStream drops the tool_result is_error events and never
//     reads the result event's permission_denials), so the orchestrator cannot
//     distinguish "blocked by permissions" from "red build" - it reports the
//     misleading "could not certify the build green". The model MAY mention
//     the refusal in its own prose (it did in this test's log), but that is
//     free text the pipeline can't act on - and in the production trace it was
//     paraphrased and buried.
//
// Deliberately not part of the normal suite (build tag "repro"): it spawns a
// real claude session and needs the CLI + credentials. Run with:
//
//	GOTOOLCHAIN=auto go test -tags repro -run TestReproDenialInvisible ./internal/agent -v
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestReproDenialInvisible(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "App"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A gradlew that proves execution if it ever runs.
	if err := os.WriteFile(filepath.Join(dir, "App", "gradlew"),
		[]byte("#!/bin/sh\necho GRADLE-RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var lines []string
	logf := func(format string, a ...interface{}) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, a...))
		mu.Unlock()
	}

	// The old default allowlist's only build rule, verbatim shape.
	_, err := Run(context.Background(),
		"Run exactly this bash command, once, and nothing else: ./App/gradlew help\n"+
			"Whatever the outcome, stop after that single attempt.",
		Options{
			WorktreeDir:  dir,
			AllowedTools: "Bash(./gradlew:*) Bash(ls:*)",
			Model:        "claude-haiku-4-5-20251001",
			Logf:         logf,
		})

	mu.Lock()
	all := strings.Join(lines, "\n")
	mu.Unlock()
	t.Logf("agent.Run err: %v", err)
	t.Logf("captured log stream:\n%s", all)

	if strings.Contains(all, "GRADLE-RAN") {
		t.Fatal("command executed - denial did not reproduce (allowlist behavior changed?)")
	}
	// The bug: the pipeline gets NO machine-readable signal. Run's only outputs
	// are the session id and an error, and the error is nil - there is nothing
	// a caller could branch on to tell this run from a healthy one.
	if err != nil {
		t.Fatalf("Run returned an error (%v) - a denial would at least be detectable", err)
	}
	t.Log("REPRODUCED: command refused by the allowlist 100% of the time, yet " +
		"agent.Run reports success (err=nil) with no denial data - the verify " +
		"stage can only classify this as a failed build")
}
