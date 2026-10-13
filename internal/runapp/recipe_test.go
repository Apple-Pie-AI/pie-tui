package runapp

import (
	"testing"
)

// Anything learned must be a plain value, never something that reads as
// more than one argument or reaches outside the project.
func TestRecipeValidate(t *testing.T) {
	ok := Recipe{
		GradleDir:  "android",
		GradleArgs: []string{"-Pflavor=staging", "--offline", "-Dorg.gradle.java.home=/Library/Java/jdk17"},
		Env:        map[string]string{"JAVA_HOME": "/Library/Java/jdk17", "GRADLE_OPTS": "-Xmx4g"},
		Overrides: map[string]Override{"stagingDebug": {
			AssembleTask: ":app:assembleStagingDebug", ApplicationID: "com.acme.staging",
			LaunchActivity: "com.acme.staging/com.acme.ui.MainActivity"}},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a sane recipe failed: %v", err)
	}
	bad := map[string]Recipe{
		"gradle dir escapes":   {GradleDir: "../other"},
		"absolute gradle dir":  {GradleDir: "/etc"},
		"arg with a space":     {GradleArgs: []string{"-Px=1 ; rm -rf ~"}},
		"arg not a flag":       {GradleArgs: []string{"clean"}},
		"command substitution": {GradleArgs: []string{"-P$(id)"}},
		"env not allowed":      {Env: map[string]string{"PATH": "/tmp"}},
		"task not assemble":    {Overrides: map[string]Override{"d": {AssembleTask: ":app:installDebug"}}},
		"task injection":       {Overrides: map[string]Override{"d": {AssembleTask: ":app:assembleDebug;id"}}},
		"bad appId":            {Overrides: map[string]Override{"d": {ApplicationID: "com.acme; id"}}},
		"bad activity":         {Overrides: map[string]Override{"d": {LaunchActivity: "com.acme/.Main && id"}}},
	}
	for name, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRecipeRoundTrip(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	repo := t.TempDir()
	r, err := LoadRecipe(repo)
	if err != nil || r.SyncHash != "" {
		t.Fatalf("missing recipe = %+v, %v", r, err)
	}
	r.Model = Model{Apps: []AppModule{{Path: ":app", Variants: []string{"debug"}}}}
	r.LastVariant = "debug"
	for i := 0; i < historyCap+5; i++ {
		r.note(StepBuild, "x")
	}
	if err := SaveRecipe(repo, r); err != nil {
		t.Fatal(err)
	}
	back, err := LoadRecipe(repo)
	if err != nil || back.LastVariant != "debug" || len(back.Model.Apps) != 1 || len(back.History) != historyCap {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	if err := SaveRecipe(repo, &Recipe{GradleDir: "../x"}); err == nil {
		t.Error("an invalid recipe must not be saved")
	}
}
