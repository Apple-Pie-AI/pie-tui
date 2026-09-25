package cmd

import (
	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeTicket(t *testing.T) {
	cases := map[string]string{
		"proj-1":    "PROJ-1",
		"  abc-2  ": "ABC-2",
		"X-9":       "X-9",
	}
	for in, want := range cases {
		if got := ticket.Normalize(in); got != want {
			t.Errorf("ticket.Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// A ticket id becomes a path element under ~/.pie and the worktree path is
// handed to os.RemoveAll, so resolveSpecs must reject traversal before any work
// fans out. `pie run ../../..` previously resolved outside the Apple Pie home.
func TestResolveSpecsRejectsTraversalIDs(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	// Note: an arg that IS an existing file takes the local-.md branch by design,
	// so these are all ids that do not resolve to a file on disk.
	for _, arg := range []string{"../../..", "..", "a/b", `a\b`, "/nope/nothing-here", "PROJ-1/../.."} {
		t.Run(arg, func(t *testing.T) {
			specs, err := resolveSpecs([]string{arg}, runOpts{})
			if err == nil {
				t.Fatalf("resolveSpecs(%q) = %+v, want an error", arg, specs)
			}
			if !strings.Contains(err.Error(), "invalid ticket id") {
				t.Errorf("error = %v, want it to name the invalid ticket id", err)
			}
		})
	}
}

func TestResolveSpecsAcceptsNormalTicketIDs(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	specs, err := resolveSpecs([]string{"kan-1", "PROJ-22"}, runOpts{})
	if err != nil {
		t.Fatalf("resolveSpecs: %v", err)
	}
	if len(specs) != 2 || specs[0].Ticket != "KAN-1" || specs[1].Ticket != "PROJ-22" {
		t.Errorf("specs = %+v, want normalized KAN-1 and PROJ-22", specs)
	}
}

func TestDedupeSpecs(t *testing.T) {
	in := []ticket.Spec{
		{Ticket: "DUP"}, {Ticket: "DUP"}, {Ticket: "DUP"}, {Ticket: "OTHER"},
	}
	out := dedupeSpecs(in)
	got := []string{out[0].Ticket, out[1].Ticket, out[2].Ticket, out[3].Ticket}
	want := []string{"DUP", "DUP-2", "DUP-3", "OTHER"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dedupe[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveSpecs(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "ticket_9.md")
	if err := os.WriteFile(f, []byte("# Nine\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The flags are per-call arguments now, so each sub-case just states the
	// options it needs - no package state to reset between them.
	t.Run("jira key fallback", func(t *testing.T) {
		specs, err := resolveSpecs([]string{"proj-123"}, runOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 1 || specs[0].Ticket != "PROJ-123" || specs[0].Local {
			t.Errorf("got %+v", specs)
		}
	})

	t.Run("file is local", func(t *testing.T) {
		specs, err := resolveSpecs([]string{f}, runOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 1 || specs[0].Ticket != "TICKET-9" || !specs[0].Local || specs[0].Summary != "Nine" {
			t.Errorf("got %+v", specs)
		}
	})

	t.Run("mixed file + key", func(t *testing.T) {
		specs, err := resolveSpecs([]string{f, "ABC-1"}, runOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 2 || !specs[0].Local || specs[1].Local {
			t.Errorf("got %+v", specs)
		}
	})

	t.Run("legacy --local needs --title", func(t *testing.T) {
		if _, err := resolveSpecs([]string{"HELLO-1"}, runOpts{local: true}); err == nil {
			t.Error("expected error for --local without --title")
		}
	})

	t.Run("legacy single --title", func(t *testing.T) {
		specs, err := resolveSpecs([]string{"HELLO-1"}, runOpts{local: true, title: "do x"})
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 1 || specs[0].Ticket != "HELLO-1" || specs[0].Summary != "do x" || !specs[0].Local {
			t.Errorf("got %+v", specs)
		}
	})

	t.Run("--title rejected with multiple args", func(t *testing.T) {
		if _, err := resolveSpecs([]string{f, "ABC-1"}, runOpts{title: "x"}); err == nil {
			t.Error("expected error: --title with multiple args")
		}
	})
}

// --from-branch flips the spec into checkout mode carrying the branch name;
// it is per-ticket (like --branch) and mutually exclusive with --branch.
func TestResolveSpecsFromBranch(t *testing.T) {
	specs, err := resolveSpecs([]string{"proj-9"}, runOpts{fromBranch: "pr-fix"})
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Branch != "pr-fix" || !specs[0].FromBranch {
		t.Errorf("got %+v, want Branch=pr-fix FromBranch=true", specs[0])
	}
	if _, err := resolveSpecs([]string{"a-1", "b-2"}, runOpts{fromBranch: "x"}); err == nil {
		t.Error("multiple tickets with --from-branch must be rejected")
	}
	if _, err := resolveSpecs([]string{"a-1"}, runOpts{fromBranch: "x", branch: "y"}); err == nil {
		t.Error("--from-branch with --branch must be rejected")
	}
	// Plain --branch keeps FromBranch off.
	specs, err = resolveSpecs([]string{"proj-9"}, runOpts{branch: "new-name"})
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Branch != "new-name" || specs[0].FromBranch {
		t.Errorf("got %+v, want Branch=new-name FromBranch=false", specs[0])
	}
}

// An explicit --review-plan (either direction) must win over the config
// default; only an UNSET flag falls back to it. This is the regression that
// let an enabled config default silently override the TUI wizard's explicit
// "No" back to "Yes" - the old `o.reviewPlan || cfg.ReviewPlans` couldn't
// tell "explicitly false" from "never passed".
func TestEffectiveReviewPlan(t *testing.T) {
	cases := []struct {
		name       string
		o          runOpts
		cfgDefault bool
		want       bool
	}{
		{"unset flag, config off", runOpts{}, false, false},
		{"unset flag, config on", runOpts{}, true, true},
		{"explicit yes, config off", runOpts{reviewPlan: true, reviewPlanSet: true}, false, true},
		{"explicit no overrides config on", runOpts{reviewPlan: false, reviewPlanSet: true}, true, false},
		{"explicit yes, config on", runOpts{reviewPlan: true, reviewPlanSet: true}, true, true},
	}
	for _, c := range cases {
		if got := effectiveReviewPlan(c.o, c.cfgDefault); got != c.want {
			t.Errorf("%s: effectiveReviewPlan(%+v, %v) = %v, want %v", c.name, c.o, c.cfgDefault, got, c.want)
		}
	}
}
