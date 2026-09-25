package vcs

import (
	"os"
	"testing"
)

// TestMain points PIE_HOME at a throwaway directory for the whole package.
// RenderPRBody and RenderJiraComment prefer a user-editable template from
// paths.Templates(); without an isolated home, a developer who has customized
// ~/.pie/templates/pr_body.tmpl silently tests their template instead of
// the built-in default the assertions describe.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "pie-vcs-test-*")
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
