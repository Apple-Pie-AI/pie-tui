package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
)

// The template-filled PR body must survive finishShip's disk re-read: the
// verify agent rewriting (or dropping) prBody in report.json is exactly the
// PLEX-60299 failure, and the in-memory restore in verifyWithAgent is what
// the caller hands us - it outranks whatever the last agent left on disk.
// writeReportJSON drops a report.json into <dir>/.agent for reloadReport to find.
func writeReportJSON(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent", "report.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// reloadReport is finishShip's re-read of report.json: the disk copy wins for
// every field EXCEPT a lost/rewritten PR body, where the caller's copy (which
// carries verifyWithAgent's in-memory restore) is authoritative.
func TestReloadReport(t *testing.T) {
	const filled = "## Summary\nfilled from the repo template"
	logf := func(string, ...interface{}) {}

	t.Run("disk dropped the body", func(t *testing.T) {
		dir := t.TempDir()
		writeReportJSON(t, dir, `{"status":"ready_for_build","summary":"verify pass"}`)
		got := reloadReport(dir, "T-1", &agent.Report{PRBody: filled}, logf)
		if got.PRBody != filled {
			t.Fatalf("PRBody = %q, want the template-filled body", got.PRBody)
		}
		if got.Summary != "verify pass" {
			t.Fatalf("Summary = %q - the disk copy must win for other fields", got.Summary)
		}
	})

	t.Run("disk rewrote the body", func(t *testing.T) {
		dir := t.TempDir()
		writeReportJSON(t, dir, `{"status":"ready_for_build","prBody":"verification narrative"}`)
		got := reloadReport(dir, "T-1", &agent.Report{PRBody: filled}, logf)
		if got.PRBody != filled {
			t.Fatalf("PRBody = %q, want the template-filled body", got.PRBody)
		}
	})

	t.Run("caller had no body", func(t *testing.T) {
		dir := t.TempDir()
		writeReportJSON(t, dir, `{"status":"ready_for_build","prBody":"disk body"}`)
		got := reloadReport(dir, "T-1", &agent.Report{}, logf)
		if got.PRBody != "disk body" {
			t.Fatalf("PRBody = %q, want the disk body", got.PRBody)
		}
	})

	t.Run("nil caller report", func(t *testing.T) {
		dir := t.TempDir()
		writeReportJSON(t, dir, `{"status":"ready_for_build","prBody":"disk body"}`)
		got := reloadReport(dir, "T-1", nil, logf)
		if got == nil || got.PRBody != "disk body" {
			t.Fatalf("got %+v, want the disk report", got)
		}
	})

	t.Run("no report on disk keeps the caller's", func(t *testing.T) {
		prev := &agent.Report{PRBody: filled}
		if got := reloadReport(t.TempDir(), "T-1", prev, logf); got != prev {
			t.Fatalf("got %+v, want the caller's report back", got)
		}
	})
}

// adoptVerifyReport guards the self-review fix-round window: verifyWithAgent's
// prBody snapshot is taken after the fix round, so an emptied body must be
// refilled from the implement-stage report - and only an emptied one.
func TestAdoptVerifyReport(t *testing.T) {
	const filled = "## Summary\nfilled from the repo template"
	logf := func(string, ...interface{}) {}

	impl := func() *agent.Report { return &agent.Report{PRBody: filled, Summary: "impl"} }

	t.Run("nil vreport keeps impl", func(t *testing.T) {
		i := impl()
		if got := adoptVerifyReport(i, nil, "T-1", logf); got != i {
			t.Fatalf("got %+v, want the implement report back", got)
		}
	})

	t.Run("dropped body restored from impl", func(t *testing.T) {
		v := &agent.Report{Summary: "verify"}
		got := adoptVerifyReport(impl(), v, "T-1", logf)
		if got != v {
			t.Fatal("the verify report must still be the one adopted")
		}
		if got.PRBody != filled {
			t.Fatalf("PRBody = %q, want the implement-stage body", got.PRBody)
		}
	})

	t.Run("non-empty rewrite is kept", func(t *testing.T) {
		v := &agent.Report{PRBody: "fix round's own update"}
		if got := adoptVerifyReport(impl(), v, "T-1", logf); got.PRBody != "fix round's own update" {
			t.Fatalf("PRBody = %q - a non-empty rewrite may be legitimate and must survive", got.PRBody)
		}
	})

	t.Run("nil impl tolerated", func(t *testing.T) {
		v := &agent.Report{}
		if got := adoptVerifyReport(nil, v, "T-1", logf); got != v {
			t.Fatalf("got %+v, want the verify report", got)
		}
	})
}

func TestRestorePRBody(t *testing.T) {
	const filled = "## Summary\nfilled from the repo template"

	cases := []struct {
		name     string
		fresh    *agent.Report
		prev     string
		restored bool
		want     string
	}{
		{name: "disk dropped the body", fresh: &agent.Report{PRBody: ""}, prev: filled, restored: true, want: filled},
		{name: "disk rewrote the body", fresh: &agent.Report{PRBody: "verification narrative"}, prev: filled, restored: true, want: filled},
		{name: "disk unchanged", fresh: &agent.Report{PRBody: filled}, prev: filled, restored: false, want: filled},
		{name: "no body to protect", fresh: &agent.Report{PRBody: "disk body"}, prev: "", restored: false, want: "disk body"},
		{name: "whitespace-only prev", fresh: &agent.Report{PRBody: "disk body"}, prev: "  \n", restored: false, want: "disk body"},
		{name: "nil report", fresh: nil, prev: filled, restored: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := restorePRBody(tc.fresh, tc.prev)
			if got != tc.restored {
				t.Fatalf("restorePRBody() = %v, want %v", got, tc.restored)
			}
			if tc.fresh != nil && tc.fresh.PRBody != tc.want {
				t.Fatalf("PRBody = %q, want %q", tc.fresh.PRBody, tc.want)
			}
		})
	}
}
