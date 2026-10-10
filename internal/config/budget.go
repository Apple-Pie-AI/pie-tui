package config

// Budget stages: the keys BudgetFor resolves. They mirror the per-stage model
// keys, so a stage's model and its budget are tuned side by side.
const (
	StagePlan       = "plan"
	StageImpl       = "impl"
	StageReview     = "review"
	StageVerify     = "verify"
	StageCommentFix = "comment_fix"
)

// DefaultMaxBudgetUSD is max_budget_usd when the config leaves it unset.
const DefaultMaxBudgetUSD = 5

// reviewBudgetShare is the self-review stage's share of max_budget_usd when it
// has no budget of its own - the fixed 30% it always had.
const reviewBudgetShare = 0.3

// BudgetFor is the --max-budget-usd ceiling for one invocation of a stage: the
// stage's own max_budget_<stage>_usd when set (> 0), otherwise max_budget_usd
// (30% of it for self-review). Every spawn site goes through this - reading
// MaxBudgetUSD directly would silently skip the per-stage override.
//
// It caps ONE claude invocation, not the ticket: when a stage reaches it the
// run asks in the dashboard, and continuing grants the same amount again.
func (c *Config) BudgetFor(stage string) float64 {
	var own float64
	switch stage {
	case StagePlan:
		own = c.MaxBudgetPlanUSD
	case StageImpl:
		own = c.MaxBudgetImplUSD
	case StageReview:
		own = c.MaxBudgetReviewUSD
	case StageVerify:
		own = c.MaxBudgetVerifyUSD
	case StageCommentFix:
		own = c.MaxBudgetCommentFixUSD
	}
	if own > 0 {
		return own
	}
	if stage == StageReview {
		return c.MaxBudgetUSD * reviewBudgetShare
	}
	return c.MaxBudgetUSD
}
