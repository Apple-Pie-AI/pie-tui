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
		{key: fldBudgetDefault, label: "Default budget per stage run, USD (hitting it asks you to continue)", value: dollars(cfg.MaxBudgetUSD)},
		{key: fldBudgetPlan, label: "Budget for planning, USD", value: dollars(cfg.MaxBudgetPlanUSD)},
		{key: fldBudgetImpl, label: "Budget for implementation and rework, USD", value: dollars(cfg.MaxBudgetImplUSD)},
		{key: fldBudgetReview, label: "Budget for self-review, USD", value: dollars(cfg.MaxBudgetReviewUSD)},
		{key: fldBudgetVerify, label: "Budget for verify, USD", value: dollars(cfg.MaxBudgetVerifyUSD)},
		{key: fldBudgetCommentFix, label: "Budget for review-comment fixes, USD", value: dollars(cfg.MaxBudgetCommentFixUSD)},
	}
}

// withBudgetPlaceholders returns fields with each empty stage-budget row's
// effective value as its placeholder - "$5 (default)" - computed from the
// default row as it currently reads in the form, so editing the default
// updates every stage that follows it before anything is saved. A copy: the
// placeholders are display only and never reach the config.
func withBudgetPlaceholders(fields []formField) []formField {
	// Resolve through config.BudgetFor itself, so the shown values can't
	// drift from what a run will actually pass to --max-budget-usd.
	eff := config.Config{MaxBudgetUSD: parseDollars(fieldValue(fields, fldBudgetDefault))}
	if eff.MaxBudgetUSD == 0 {
		eff.MaxBudgetUSD = config.DefaultMaxBudgetUSD
	}
	out := make([]formField, len(fields))
	copy(out, fields)
	for i := range out {
		switch out[i].key {
		case fldBudgetDefault:
			out[i].placeholder = "$" + dollars(config.DefaultMaxBudgetUSD) + " (built-in default)"
		case fldBudgetPlan:
			out[i].placeholder = "$" + dollars(eff.BudgetFor(config.StagePlan)) + " (default)"
		case fldBudgetImpl:
			out[i].placeholder = "$" + dollars(eff.BudgetFor(config.StageImpl)) + " (default)"
		case fldBudgetReview:
			out[i].placeholder = "$" + dollars(eff.BudgetFor(config.StageReview)) + " (30% of default)"
		case fldBudgetVerify:
			out[i].placeholder = "$" + dollars(eff.BudgetFor(config.StageVerify)) + " (default)"
		case fldBudgetCommentFix:
			out[i].placeholder = "$" + dollars(eff.BudgetFor(config.StageCommentFix)) + " (default)"
		}
	}
	return out
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
