// The budget rows of the config form: the default --max-budget-usd and the
// per-stage overrides (config.BudgetFor). Free-text dollars, like the model
// fields are free-text ids; blank on a stage row means "use the default".
package tui

import (
	"strconv"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

const (
	fldBudgetDefault    = "budget.default"
	fldBudgetPlan       = "budget.plan"
	fldBudgetImpl       = "budget.impl"
	fldBudgetReview     = "budget.review"
	fldBudgetVerify     = "budget.verify"
	fldBudgetCommentFix = "budget.commentfix"
)

// budgetFields are the form rows for the stage budgets, in pipeline order.
func budgetFields(cfg *config.Config) []formField {
	return []formField{
		{key: fldBudgetDefault, label: "Budget per stage run, USD (when a stage hits it you're asked to continue)", value: dollars(cfg.MaxBudgetUSD)},
		{key: fldBudgetPlan, label: "Budget for planning, USD (blank = the default above)", value: dollars(cfg.MaxBudgetPlanUSD)},
		{key: fldBudgetImpl, label: "Budget for implementation and rework, USD (blank = default)", value: dollars(cfg.MaxBudgetImplUSD)},
		{key: fldBudgetReview, label: "Budget for self-review, USD (blank = 30% of the default)", value: dollars(cfg.MaxBudgetReviewUSD)},
		{key: fldBudgetVerify, label: "Budget for verify, USD (blank = default)", value: dollars(cfg.MaxBudgetVerifyUSD)},
		{key: fldBudgetCommentFix, label: "Budget for review-comment fixes, USD (blank = default)", value: dollars(cfg.MaxBudgetCommentFixUSD)},
	}
}

// applyBudgetField writes one budget row back, reporting whether key was a
// budget row at all. Blank, unparseable, or negative input stores 0 - "use the
// default" - rather than a value claude would reject at spawn time, hours later.
func applyBudgetField(cfg *config.Config, key, value string) bool {
	var dst *float64
	switch key {
	case fldBudgetDefault:
		dst = &cfg.MaxBudgetUSD
	case fldBudgetPlan:
		dst = &cfg.MaxBudgetPlanUSD
	case fldBudgetImpl:
		dst = &cfg.MaxBudgetImplUSD
	case fldBudgetReview:
		dst = &cfg.MaxBudgetReviewUSD
	case fldBudgetVerify:
		dst = &cfg.MaxBudgetVerifyUSD
	case fldBudgetCommentFix:
		dst = &cfg.MaxBudgetCommentFixUSD
	default:
		return false
	}
	*dst = parseDollars(value)
	return true
}

// dollars renders a budget for the form: "" for unset, no trailing zeros.
func dollars(v float64) string {
	if v <= 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// parseDollars reads a form value, tolerating a leading "$".
func parseDollars(s string) float64 {
	s = strings.TrimPrefix(strings.TrimSpace(s), "$")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
