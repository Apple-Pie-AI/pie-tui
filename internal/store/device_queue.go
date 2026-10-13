// Who uses the device, and who is next. Agents queue first-come first-served:
// a ticket that reaches verify stamps device_wait_since, and only the oldest
// LIVE waiter may take the device. Before this, waiters raced a 5s poll and a
// late ticket could jump the line.
package store

import "time"

// LeaseGrace is how long a ticket must have held the device before anyone may
// reclaim it as stale - long enough for its run to record its pid.
var LeaseGrace = 2 * time.Minute

// Waiter is a ticket queued for the device.
type Waiter struct {
	Ticket string
	State  string
	PID    int
	Since  int64
}

// SetDeviceWait puts ticket in the queue (keeping its place if already there).
func (s *Store) SetDeviceWait(ticket string) error {
	_, err := s.db.Exec(`UPDATE sessions SET device_wait_since = ? WHERE ticket = ? AND device_wait_since = 0`,
		now(), ticket)
	return err
}

// ClearDeviceWait takes ticket out of the queue.
func (s *Store) ClearDeviceWait(ticket string) error {
	_, err := s.db.Exec(`UPDATE sessions SET device_wait_since = 0 WHERE ticket = ?`, ticket)
	return err
}

// DeviceWaiters lists the queue, oldest first.
func (s *Store) DeviceWaiters() ([]Waiter, error) {
	rows, err := s.db.Query(`SELECT ticket, state, pid, device_wait_since FROM sessions
		WHERE device_wait_since > 0 ORDER BY device_wait_since, ticket`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ws []Waiter
	for rows.Next() {
		var w Waiter
		if err := rows.Scan(&w.Ticket, &w.State, &w.PID, &w.Since); err != nil {
			return nil, err
		}
		ws = append(ws, w)
	}
	return ws, rows.Err()
}

// NextWaiter is the head of the queue among live waiters. Dead ones (their run
// ended or was killed without leaving the queue) are dropped on the way.
func (s *Store) NextWaiter(isAlive func(int) bool) (Waiter, bool) {
	ws, err := s.DeviceWaiters()
	if err != nil {
		return Waiter{}, false
	}
	for _, w := range ws {
		if IsActive(w.State) && isAlive(w.PID) {
			return w, true
		}
		_ = s.ClearDeviceWait(w.Ticket)
	}
	return Waiter{}, false
}

// AcquireDevice gives the running device to ticket - only when nobody holds
// it AND ticket is the head of the queue. On success ticket leaves the queue.
func (s *Store) AcquireDevice(ticket string, isAlive func(int) bool) (bool, error) {
	if head, ok := s.NextWaiter(isAlive); !ok || head.Ticket != ticket {
		return false, nil
	}
	ok, err := affected(s.db.Exec(`UPDATE active_device SET holder = ?, acquired_at = ?, updated_at = ?
		WHERE id = 1 AND state = ? AND holder = ''`, ticket, now(), now(), DeviceRunning))
	if ok {
		_ = s.ClearDeviceWait(ticket)
	}
	return ok, err
}

// ReserveDevice gives the device to the human (Run app, or after taking it
// back) when it is free or already theirs; purpose says why (see Device).
func (s *Store) ReserveDevice(purpose string) (bool, error) {
	return affected(s.db.Exec(`UPDATE active_device SET holder = ?, purpose = ?, acquired_at = ?, updated_at = ?
		WHERE id = 1 AND holder IN ('', ?)`, HolderYou, purpose, now(), now(), HolderYou))
}

// SetHoldPurpose changes why HolderYou holds the device (Run app on a device
// you had taken back, and back again afterwards).
func (s *Store) SetHoldPurpose(purpose string) error {
	_, err := s.db.Exec(`UPDATE active_device SET purpose = ?, updated_at = ? WHERE id = 1 AND holder = ?`,
		purpose, now(), HolderYou)
	return err
}

// TakeDevice moves the device from one holder to another (take it back from
// an agent: from = the ticket, to = HolderYou). Guarded on from: if the agent
// released in the meantime it reports false, and the caller reserves instead.
func (s *Store) TakeDevice(from, to, purpose string) (bool, error) {
	return affected(s.db.Exec(`UPDATE active_device SET holder = ?, purpose = ?, acquired_at = ?, updated_at = ?
		WHERE id = 1 AND holder = ?`, to, purpose, now(), now(), from))
}

// ReleaseDevice hands the device back - only by its holder.
func (s *Store) ReleaseDevice(holder string) (bool, error) {
	return affected(s.db.Exec(`UPDATE active_device SET holder = '', purpose = '', acquired_at = 0, updated_at = ?
		WHERE id = 1 AND holder = ?`, now(), holder))
}

// ReclaimStaleHolder frees the device when its ticket holder is gone: its run
// no longer active, or its pid dead (SIGKILLed before releasing). minAgeSec
// keeps a holder that acquired moments ago from being second-guessed before it
// recorded its pid. HolderYou is never reclaimed.
func (s *Store) ReclaimStaleHolder(minAgeSec int64, isAlive func(int) bool) (string, bool, error) {
	d, err := s.ActiveDevice()
	if err != nil || d.Holder == "" || d.Holder == HolderYou {
		return "", false, err
	}
	if now()-d.AcquiredAt < minAgeSec {
		return "", false, nil
	}
	var state string
	var pid int
	err = s.db.QueryRow(`SELECT state, pid FROM sessions WHERE ticket = ?`, d.Holder).Scan(&state, &pid)
	if err == nil && IsActive(state) && isAlive(pid) {
		return "", false, nil
	}
	ok, err := s.ReleaseDevice(d.Holder)
	return d.Holder, ok, err
}
