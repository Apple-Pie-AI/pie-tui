package agent

import (
	"reflect"
	"testing"
)

// T-remember - what "Allow & remember" may offer for one pending approval:
// the exact command and the generalized heads, so the overlay can show both
// verbatim rules before the human picks a scope (never silently choose one).
func TestRememberOptions(t *testing.T) {
	cases := []struct {
		name        string
		tool, cmd   string
		wantExact   string
		wantGeneral []string
	}{
		{"simple gradle",
			"Bash", "./gradlew test",
			"Bash(./gradlew test)", []string{"Bash(./gradlew:*)"}},
		{"compound command",
			"Bash", "cd App && ./gradlew test",
			"Bash(cd App && ./gradlew test)", []string{"Bash(cd:*)", "Bash(./gradlew:*)"}},
		{"guarded compound has no offer at all",
			"Bash", "cd App && git checkout main",
			"", nil},
		{"prose has no offer at all",
			"Bash", "Unable to execute the test script.",
			"", nil},
		{"single token collapses to general only (exact would duplicate it)",
			"Bash", "pwd",
			"", []string{"Bash(pwd:*)"}},
		{"chmod two-word head",
			"Bash", "chmod +x gradlew",
			"Bash(chmod +x gradlew)", []string{"Bash(chmod +x:*)"}},
		{"known non-Bash tool: bare name only, no exact form",
			"Skill", "company-gradle-build",
			"", []string{"Skill"}},
		{"unknown non-Bash tool: nothing offered",
			"SomeWeirdTool", "x",
			"", nil},
		{"paren-bearing command: no exact, heads still offered",
			"Bash", "cd App && echo $(date)",
			"", []string{"Bash(cd:*)", "Bash(echo:*)"}},
		{"duplicate heads in one compound collapse to one general rule",
			"Bash", "gradle help && gradle build",
			"Bash(gradle help && gradle build)", []string{"Bash(gradle:*)"}},
		// Field case (PLEX-59644): a relative path through a capitalized
		// monorepo dir is a real binary, not prose - the capital-letter guard
		// must not void the offer. Quoted pipes stay inside their segment.
		{"relative path through a capitalized dir is a real head",
			"Bash", `Compass/gradlew tasks | grep -i "test\|instrumented" | head -30`,
			`Bash(Compass/gradlew tasks | grep -i "test\|instrumented" | head -30)`,
			[]string{"Bash(Compass/gradlew:*)", "Bash(grep:*)", "Bash(head:*)"}},
		// An absolute path stays rejected: pie's worktrees are per-ticket, so a
		// rule bound to one worktree's path would never match again.
		{"absolute path head still voids the offer",
			"Bash", "/Users/x/.pie/worktrees/T-1/Compass/gradlew tasks | head -30",
			"", nil},
		{"capitalized bare word without a slash is still prose",
			"Bash", "Unable to run && ./gradlew test",
			"", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exact, general := RememberOptions(c.tool, c.cmd)
			if exact != c.wantExact {
				t.Errorf("exact = %q, want %q", exact, c.wantExact)
			}
			if !reflect.DeepEqual(general, c.wantGeneral) {
				t.Errorf("general = %#v, want %#v", general, c.wantGeneral)
			}
		})
	}
}
