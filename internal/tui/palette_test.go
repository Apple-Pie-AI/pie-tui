package tui

import "testing"

func TestMenuQuitIsLast(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	items := monitorModel{}.paletteItems()
	if items[len(items)-1].key != "quit" {
		t.Errorf("Quit should be last in the menu, got %q", items[len(items)-1].key)
	}
}
