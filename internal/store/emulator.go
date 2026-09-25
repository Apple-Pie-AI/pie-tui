// The emulator semaphore. Concurrent tickets share one AVD, so booting and
// holding it are guarded by compare-and-set UPDATEs rather than an in-process
// lock: the runners are separate OS processes.
package store

import (
	"database/sql"
	"time"
)

// LeaseGrace is how long an emulator lease must have been held before anyone
// second-guesses its holder. It only has to exceed the gap between
// AcquireEmulator and the holder recording its own pid and state, so a lease
// legitimately taken a moment ago is never reclaimed out from under it. Shared
// by the runner (which reclaims while waiting) and the daemon (which reclaims
// when nobody is waiting at all) so the two can't drift apart.
//
// A var rather than a const only so tests can drop the window and exercise
// reclaim without sleeping two minutes; nothing in the binary reassigns it.
var LeaseGrace = 2 * time.Minute

// RegisterEmulator upserts an idle emulator row if not already present.
func (s *Store) RegisterEmulator(avdName string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(
		`INSERT INTO emulators (avd_name, state, updated_at)
		 VALUES (?, 'idle', ?)
		 ON CONFLICT(avd_name) DO NOTHING`,
		avdName, now,
	)
	return err
}

// SetEmulatorBooting atomically transitions idle→booting and records the pid.
// Returns true if this caller won the race (only the winner should actually
// run the emulator process).
func (s *Store) SetEmulatorBooting(avdName string, pid int) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE emulators SET state='booting', pid=?, updated_at=? WHERE avd_name=? AND state='idle'`,
		pid, time.Now().Unix(), avdName,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// MarkEmulatorReady announces that the AVD is up and available: idle or
// booting → ready. It cannot touch a busy row, and that restriction is the
// whole point.
//
// Every path through bootOrWait ends by saying "this emulator is ready", and it
// used to say so by calling ReleaseEmulator - which had no state or holder
// predicate at all. A runner that started while a peer was mid-verify therefore
// flipped that peer's busy lease to ready and blanked its holder, and both runs
// then drove the same emulator. The semaphore only became real once "mark
// ready" and "hand back the lease" stopped being the same statement.
func (s *Store) MarkEmulatorReady(avdName string) error {
	now := time.Now().Unix()
	// last_used_at is stamped here too: it drives the daemon's idle-shutdown
	// timer, and a row that reached ready without one reads as "idle since the
	// epoch" and gets killed on the next poll.
	_, err := s.db.Exec(
		`UPDATE emulators SET state='ready', last_used_at=?, updated_at=?
		 WHERE avd_name=? AND state IN ('idle','booting')`,
		now, now, avdName,
	)
	return err
}

// AcquireEmulator atomically claims the emulator for a ticket.
// Returns true only if the row was in state=ready and this caller won.
func (s *Store) AcquireEmulator(avdName, ticket string) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE emulators SET state='busy', holder=?, updated_at=? WHERE avd_name=? AND state='ready'`,
		ticket, time.Now().Unix(), avdName,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ReleaseEmulator hands the AVD back, busy → ready. It is conditional on the
// caller actually being the holder, so releasing a lease you don't own is a
// no-op rather than a theft - which also makes this the reclaim primitive: a
// waiter that has established the holder is dead calls it with that holder's
// ticket, and exactly one of several waiters can win.
//
// Returns whether this call was the one that released it.
func (s *Store) ReleaseEmulator(avdName, holder string) (bool, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`UPDATE emulators SET state='ready', holder='', last_used_at=?, updated_at=?
		 WHERE avd_name=? AND state='busy' AND holder=?`,
		now, now, avdName, holder,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ReclaimStaleLease releases a busy lease whose holding ticket is no longer
// being driven by a live process, and reports which ticket it took it from.
//
// The liveness rule is the one cmd/run.go already uses to refuse a second run:
// the session must still claim to be running AND its recorded pid must answer.
// A terminal state with a live pid means the pid was recycled; a running state
// with a dead pid means the driver was killed before its deferred release. Both
// are leaked leases, and without this nothing in the tree ever cleared one - the
// daemon's reaper only looks at ready rows.
//
// isAlive is passed in rather than imported so store keeps its position as the
// package that depends on nothing else internal. minAge keeps a lease that was
// legitimately taken moments ago from being second-guessed before its holder has
// finished recording its own pid and state.
func (s *Store) ReclaimStaleLease(avdName string, minAge time.Duration, isAlive func(pid int) bool) (holder string, reclaimed bool, err error) {
	var since int64
	var state sql.NullString
	var pid sql.NullInt64
	row := s.db.QueryRow(
		`SELECT e.holder, e.updated_at, s.state, s.pid
		   FROM emulators e LEFT JOIN sessions s ON s.ticket = e.holder
		  WHERE e.avd_name=? AND e.state='busy'`, avdName)
	if err = row.Scan(&holder, &since, &state, &pid); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil // nobody holds it
		}
		return "", false, err
	}
	if holder == "" || time.Since(time.Unix(since, 0)) < minAge {
		return "", false, nil
	}
	// No session row at all (LEFT JOIN missed) means the holder is unknowable -
	// treat it as gone rather than waiting on a ticket nothing is tracking.
	if state.Valid && IsActive(state.String) && isAlive(int(pid.Int64)) {
		return "", false, nil
	}
	// Deliberately NOT ReleaseEmulator: that stamps last_used_at, and a reclaim
	// is not a use. Refreshing it here would tell the daemon's idle reaper - which
	// runs on the very same poll - that the AVD was just handed back, buying the
	// abandoned emulator a whole extra idle timeout instead of shutting it down.
	res, err := s.db.Exec(
		`UPDATE emulators SET state='ready', holder='', updated_at=?
		 WHERE avd_name=? AND state='busy' AND holder=?`,
		time.Now().Unix(), avdName, holder,
	)
	if err != nil {
		return "", false, err
	}
	n, _ := res.RowsAffected()
	return holder, n == 1, nil
}

// EmulatorState returns the current state and pid for an AVD.
func (s *Store) EmulatorState(avdName string) (state string, pid int, err error) {
	row := s.db.QueryRow(`SELECT state, pid FROM emulators WHERE avd_name=?`, avdName)
	err = row.Scan(&state, &pid)
	return
}

// SetEmulatorIdle resets state to idle and clears the pid (post-kill).
func (s *Store) SetEmulatorIdle(avdName string) error {
	_, err := s.db.Exec(
		`UPDATE emulators SET state='idle', pid=0, holder='', updated_at=? WHERE avd_name=?`,
		time.Now().Unix(), avdName,
	)
	return err
}

// EmulatorLastUsed returns the last_used_at unix timestamp for an AVD.
func (s *Store) EmulatorLastUsed(avdName string) (int64, error) {
	var t int64
	err := s.db.QueryRow(`SELECT last_used_at FROM emulators WHERE avd_name=?`, avdName).Scan(&t)
	return t, err
}
