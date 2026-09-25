package store

import "testing"

// checked-out is idle: no driver process, re-runs allowed.
func TestCheckedOutIsIdle(t *testing.T) {
	if IsActive(StateCheckedOut) {
		t.Fatal("checked-out must not be an active state")
	}
}
