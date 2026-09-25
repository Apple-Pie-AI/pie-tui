package tui

// newRun builds a run wizard from newRunState() with the caller's overrides
// applied. Tests used to spell the five -1 prompt sentinels out by hand in every
// monitorModel literal, and one that forgot a sentinel silently routed into the
// wrong prompt handler instead of the one under test.
func newRun(set func(*runState)) runState {
	r := newRunState()
	if set != nil {
		set(&r)
	}
	return r
}
