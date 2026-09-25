package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// argValue returns the argument following the first occurrence of flag, or "".
func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func TestBuildArgsIncludesSettingsWhenSet(t *testing.T) {
	args := buildArgs("do the thing", Options{
		AllowedTools: "Read",
		SettingsJSON: `{"sandbox":{"enabled":false}}`,
	})
	v, ok := argValue(args, "--settings")
	if !ok {
		t.Fatalf("--settings not passed: %v", args)
	}
	if v != `{"sandbox":{"enabled":false}}` {
		t.Errorf("--settings value = %q", v)
	}
}

func TestBuildArgsOmitsSettingsWhenEmpty(t *testing.T) {
	args := buildArgs("do the thing", Options{AllowedTools: "Read"})
	if strings.Contains(strings.Join(args, " "), "--settings") {
		t.Errorf("--settings should be omitted when SettingsJSON is empty: %v", args)
	}
}

// writeAgentJSON writes <dir>/.agent/<name> with the given JSON body.
func writeAgentJSON(t *testing.T, dir, name, body string) {
	t.Helper()
	ad := filepath.Join(dir, ".agent")
	if err := os.MkdirAll(ad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ad, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadPlanAtRoot(t *testing.T) {
	wt := t.TempDir()
	writeAgentJSON(t, wt, "plan.json", `{"plan":"do it","type":"Feature","confidence":"High"}`)
	plan, err := ReadPlan(wt)
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if plan.Plan != "do it" {
		t.Errorf("plan = %q, want %q", plan.Plan, "do it")
	}
}

// When the configured repo is a module inside a larger repo, `git worktree add`
// checks out the whole repo and the agent writes .agent/ into the module subdir.
// ReadPlan must still find it.
func TestReadPlanInSubdir(t *testing.T) {
	wt := t.TempDir()
	module := filepath.Join(wt, "android")
	writeAgentJSON(t, module, "plan.json", `{"plan":"from subdir","type":"Feature","confidence":"High"}`)
	plan, err := ReadPlan(wt)
	if err != nil {
		t.Fatalf("ReadPlan (subdir): %v", err)
	}
	if plan.Plan != "from subdir" {
		t.Errorf("plan = %q, want %q", plan.Plan, "from subdir")
	}
}

// A root-level file wins over a deeper one (the contract location is preferred).
func TestReadPlanRootWinsOverSubdir(t *testing.T) {
	wt := t.TempDir()
	writeAgentJSON(t, wt, "plan.json", `{"plan":"root","type":"Feature","confidence":"High"}`)
	writeAgentJSON(t, filepath.Join(wt, "android"), "plan.json", `{"plan":"sub","type":"Feature","confidence":"High"}`)
	plan, err := ReadPlan(wt)
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if plan.Plan != "root" {
		t.Errorf("plan = %q, want root-level %q", plan.Plan, "root")
	}
}

func TestReadPlanMissing(t *testing.T) {
	if _, err := ReadPlan(t.TempDir()); err == nil {
		t.Error("expected error when plan.json is absent")
	}
}

// Verified is the only gate between the agent's work and a real PR, so its
// decoding has to be exact: absent means unverified (nil), not "false-ish", and
// an explicit false must survive as false rather than being lost by omitempty.
func TestReportVerifiedDecoding(t *testing.T) {
	cases := []struct {
		name string
		body string
		want *bool
	}{
		{"absent", `{"status":"ready_for_build"}`, nil},
		{"explicit false", `{"status":"ready_for_build","verified":false}`, boolPtr(false)},
		{"explicit true", `{"status":"ready_for_build","verified":true}`, boolPtr(true)},
		{"null", `{"status":"ready_for_build","verified":null}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wt := t.TempDir()
			writeAgentJSON(t, wt, "report.json", c.body)
			r, err := ReadReport(wt)
			if err != nil {
				t.Fatalf("ReadReport: %v", err)
			}
			switch {
			case c.want == nil && r.Verified != nil:
				t.Errorf("Verified = %v, want nil (unverified)", *r.Verified)
			case c.want != nil && r.Verified == nil:
				t.Errorf("Verified = nil, want %v", *c.want)
			case c.want != nil && *r.Verified != *c.want:
				t.Errorf("Verified = %v, want %v", *r.Verified, *c.want)
			}
			// The ship gate itself.
			shipped := r.Verified != nil && *r.Verified
			wantShip := c.want != nil && *c.want
			if shipped != wantShip {
				t.Errorf("ship gate = %v, want %v", shipped, wantShip)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

// A malformed report must fail closed rather than decoding to a zero Report that
// happens to read as unverified-but-valid.
func TestReadReportMalformedFailsClosed(t *testing.T) {
	wt := t.TempDir()
	writeAgentJSON(t, wt, "report.json", `{"status":"ready_for_build","verified":"yes"}`)
	if _, err := ReadReport(wt); err == nil {
		t.Error("expected an error for a non-boolean verified value")
	}
}

// .agent/ is git-excluded and never purged between runs, so a verify session that
// dies before rewriting report.json would otherwise leave the PREVIOUS run's
// verdict on disk and ship unverified code.
func TestReadReportSinceRejectsStaleReport(t *testing.T) {
	wt := t.TempDir()
	writeAgentJSON(t, wt, "report.json", `{"status":"ready_for_build","verified":true}`)

	stageStart := time.Now().Add(1 * time.Hour) // the report predates the stage
	if _, err := ReadReportSince(wt, stageStart); err == nil {
		t.Fatal("stale report.json was accepted - it would have gated a real PR")
	} else if !errors.Is(err, ErrStaleReport) {
		t.Errorf("error = %v, want it to wrap ErrStaleReport", err)
	}
}

func TestReadReportSinceAcceptsFreshReport(t *testing.T) {
	wt := t.TempDir()
	stageStart := time.Now().Truncate(time.Second)
	writeAgentJSON(t, wt, "report.json", `{"status":"ready_for_build","verified":true}`)

	r, err := ReadReportSince(wt, stageStart)
	if err != nil {
		t.Fatalf("ReadReportSince: %v", err)
	}
	if r.Verified == nil || !*r.Verified {
		t.Error("fresh verified report was not read back as verified")
	}
}

// The fallback walk that finds .agent/ in a module subdirectory must not become a
// way for a stale copy to satisfy the freshness gate.
func TestReadReportSinceRejectsStaleSubdirReport(t *testing.T) {
	wt := t.TempDir()
	writeAgentJSON(t, filepath.Join(wt, "android"), "report.json", `{"status":"ready_for_build","verified":true}`)

	if _, err := ReadReportSince(wt, time.Now().Add(1*time.Hour)); err == nil {
		t.Fatal("stale report.json in a subdir was accepted")
	}
}

func TestReadReportSinceMissingReport(t *testing.T) {
	if _, err := ReadReportSince(t.TempDir(), time.Now()); err == nil {
		t.Error("expected an error when report.json is absent")
	}
}
