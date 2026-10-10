package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Each stage uses its own budget when set and falls back to max_budget_usd
// otherwise - self-review to the 30% share it always had.
func TestBudgetFor(t *testing.T) {
	base := &Config{MaxBudgetUSD: 10}
	for stage, want := range map[string]float64{
		StagePlan: 10, StageImpl: 10, StageVerify: 10, StageCommentFix: 10,
		StageReview: 3, "unknown": 10,
	} {
		if got := base.BudgetFor(stage); got != want {
			t.Errorf("default %s = %v, want %v", stage, got, want)
		}
	}

	own := &Config{MaxBudgetUSD: 10, MaxBudgetPlanUSD: 1, MaxBudgetImplUSD: 20,
		MaxBudgetReviewUSD: 4, MaxBudgetVerifyUSD: 7, MaxBudgetCommentFixUSD: 2}
	for stage, want := range map[string]float64{
		StagePlan: 1, StageImpl: 20, StageReview: 4, StageVerify: 7, StageCommentFix: 2,
	} {
		if got := own.BudgetFor(stage); got != want {
			t.Errorf("override %s = %v, want %v", stage, got, want)
		}
	}

	// A negative override (hand-edited TOML) is not a budget: fall back.
	if got := (&Config{MaxBudgetUSD: 5, MaxBudgetPlanUSD: -1}).BudgetFor(StagePlan); got != 5 {
		t.Errorf("negative override = %v, want the default 5", got)
	}
}

// The per-stage keys load from config.toml under the names the docs give, and
// an absent key leaves the stage on the default.
func TestBudgetKeysLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIE_HOME", home)
	toml := "max_budget_usd = 8\n" +
		"max_budget_plan_usd = 1.5\n" +
		"max_budget_impl_usd = 12\n" +
		"max_budget_review_usd = 2\n" +
		"max_budget_verify_usd = 6\n" +
		"max_budget_comment_fix_usd = 3\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for stage, want := range map[string]float64{
		StagePlan: 1.5, StageImpl: 12, StageReview: 2, StageVerify: 6, StageCommentFix: 3,
	} {
		if got := cfg.BudgetFor(stage); got != want {
			t.Errorf("%s = %v, want %v", stage, got, want)
		}
	}

	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("max_budget_plan_usd = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.BudgetFor(StageImpl); got != 5 {
		t.Errorf("impl with only a plan override = %v, want the built-in default 5", got)
	}
	if got := cfg.BudgetFor(StagePlan); got != 2 {
		t.Errorf("plan = %v, want 2", got)
	}
}
