package tui

import (
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

const budgetQ = "The plan stage reached its $5.00 budget ($5.02 spent so far). Continue with another $5.00?"

// A budget question is Continue / Stop - no allow-once wording, and never a
// "remember" row (there is no rule to write).
func TestBudgetApprovalRows(t *testing.T) {
	rows := approvalRows(store.Approval{Tool: agent.BudgetTool, Command: budgetQ})
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want Continue and Stop", rowLabels(rows))
	}
	if !strings.HasPrefix(rows[0].label, "Continue") || rows[0].state != store.ApprovalAllowed {
		t.Errorf("first row = %+v, want Continue → allowed", rows[0])
	}
	if !strings.HasPrefix(rows[1].label, "Stop") || rows[1].state != store.ApprovalDenied {
		t.Errorf("second row = %+v, want Stop → denied", rows[1])
	}
	for _, r := range rows {
		if len(r.rules) > 0 || strings.Contains(r.label, "remember") {
			t.Errorf("budget row offers a rule: %+v", r)
		}
	}
}

// The overlay names the budget and shows the question verbatim, not as a
// "Budget: ..." command line; the banner calls it a budget question.
func TestBudgetApprovalRendersAsBudget(t *testing.T) {
	m, st := approvalModel(t)
	if _, err := st.CreateApproval("PLEX-1", agent.BudgetTool, budgetQ); err != nil {
		t.Fatal(err)
	}
	m.reload()
	m.openApprovals("")
	out := m.renderApprovals(120)
	for _, want := range []string{"PLEX-1 reached its budget", "continuing resumes the same session", "$5.02 spent so far", "Continue", "Stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("overlay missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "wants to run a command") || strings.Contains(out, "Budget: ") {
		t.Errorf("budget question rendered as a command:\n%s", out)
	}
	if head := m.renderDashboardHeader(120); !strings.Contains(head, "1 budget question waiting") {
		t.Errorf("banner should say budget question:\n%s", head)
	}
}

// Enter on Continue records allowed (the runner resumes); Stop records denied.
// Neither writes the allowlist.
func TestBudgetApprovalDecisions(t *testing.T) {
	for _, tc := range []struct {
		row, want string
	}{{"Continue", store.ApprovalAllowed}, {"Stop", store.ApprovalDenied}} {
		t.Run(tc.row, func(t *testing.T) {
			m, st := approvalModel(t)
			id, _ := st.CreateApproval("PLEX-1", agent.BudgetTool, budgetQ)
			m.reload()
			pending, _ := st.PendingApprovals("PLEX-1")
			m.openApprovals("PLEX-1")
			m = selectRow(t, m, pending[0], tc.row)
			mm, _ := m.updateApprovals(enter())
			m = mm.(monitorModel)
			if state, _ := st.ApprovalState(id); state != tc.want {
				t.Fatalf("state = %q, want %q", state, tc.want)
			}
			if !strings.Contains(m.notice, "budget") {
				t.Errorf("notice = %q, want it to mention the budget", m.notice)
			}
			if cfg, _ := config.Load(); cfg.ExtraAllowedTools != "" {
				t.Errorf("a budget decision wrote the allowlist: %q", cfg.ExtraAllowedTools)
			}
		})
	}
}

// The banner noun follows what is waiting: commands, budget questions, or
// "requests" when both are.
func TestApprovalNoun(t *testing.T) {
	cmd := store.Approval{Tool: "Bash"}
	bud := store.Approval{Tool: agent.BudgetTool}
	for _, tc := range []struct {
		in   []store.Approval
		want string
	}{
		{[]store.Approval{cmd}, "command"},
		{[]store.Approval{bud, bud}, "budget question"},
		{[]store.Approval{cmd, bud}, "request"},
	} {
		if got := approvalNoun(tc.in); got != tc.want {
			t.Errorf("approvalNoun(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The Edit config budget rows round-trip through the form: values written,
// blanks and garbage stored as 0 ("use the default"), "$" tolerated.
func TestBudgetFieldsRoundTrip(t *testing.T) {
	cfg := &config.Config{MaxBudgetUSD: 5, MaxBudgetVerifyUSD: 2.5}
	fields := budgetFields(cfg)
	if got := fieldValue(fields, fldBudgetVerify); got != "2.5" {
		t.Errorf("verify field = %q, want 2.5", got)
	}
	if got := fieldValue(fields, fldBudgetPlan); got != "" {
		t.Errorf("unset plan field = %q, want blank", got)
	}

	set := map[string]string{
		fldBudgetDefault: "8", fldBudgetPlan: "$1.5", fldBudgetImpl: "12",
		fldBudgetReview: "", fldBudgetVerify: "nope", fldBudgetCommentFix: "-3",
	}
	for i := range fields {
		fields[i].value = set[fields[i].key]
	}
	applyConfigFields(cfg, fields)
	if cfg.MaxBudgetUSD != 8 || cfg.MaxBudgetPlanUSD != 1.5 || cfg.MaxBudgetImplUSD != 12 {
		t.Errorf("parsed = default %v plan %v impl %v", cfg.MaxBudgetUSD, cfg.MaxBudgetPlanUSD, cfg.MaxBudgetImplUSD)
	}
	if cfg.MaxBudgetReviewUSD != 0 || cfg.MaxBudgetVerifyUSD != 0 || cfg.MaxBudgetCommentFixUSD != 0 {
		t.Errorf("blank/garbage/negative must store 0: review %v verify %v commentfix %v",
			cfg.MaxBudgetReviewUSD, cfg.MaxBudgetVerifyUSD, cfg.MaxBudgetCommentFixUSD)
	}
	if got := cfg.BudgetFor(config.StageVerify); got != 8 {
		t.Errorf("verify falls back to the default: got %v, want 8", got)
	}
}
