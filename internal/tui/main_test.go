package tui

import (
	"os"
	"testing"
)

// TestMain points PIE_HOME at a throwaway directory for the whole package.
// Several tests (isStopped, glyphFor, anything reaching paths.LogFor) stat files
// under the Apple Pie home; without this they read the developer's real ~/.pie
// and pass or fail depending on what happens to be on that machine.
// Individual tests may still t.Setenv their own home - that just overrides this.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "pie-tui-test-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("PIE_HOME", home); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
