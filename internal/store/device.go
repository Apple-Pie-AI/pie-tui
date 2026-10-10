// The one device pie works with, and who is using it. Golden rule: ONE device,
// ONE user of it at a time - a ticket's verify agent, or "you" (taken back
// from an agent, or running the app). Every transition is a conditional
// UPDATE on the single row, because runners, the TUI and the daemon are
// separate processes: losing a race means RowsAffected == 0, never a
// double-booked device.
package store

import (
	"database/sql"
	"errors"
	"time"
)

const deviceSchema = `
CREATE TABLE IF NOT EXISTS active_device (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  kind        TEXT NOT NULL DEFAULT '',     -- '' (none) | emulator | usb
  avd         TEXT NOT NULL DEFAULT '',
  serial      TEXT NOT NULL DEFAULT '',
  state       TEXT NOT NULL DEFAULT 'off',  -- off | starting | running
  pid         INTEGER NOT NULL DEFAULT 0,   -- emulator pie launched (0 = adopted / usb)
  started_at  INTEGER NOT NULL DEFAULT 0,
  running_at  INTEGER NOT NULL DEFAULT 0,
  holder      TEXT NOT NULL DEFAULT '',     -- '' | <ticket> | you
  acquired_at INTEGER NOT NULL DEFAULT 0,
  chosen      INTEGER NOT NULL DEFAULT 0,   -- 1 once the user picked a device (stops avd_name seeding)
  updated_at  INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO active_device (id) VALUES (1);
CREATE TABLE IF NOT EXISTS avd_boots (
  avd  TEXT PRIMARY KEY,
  secs INTEGER NOT NULL
);`

// Device kinds and states.
const (
	DeviceEmulator = "emulator"
	DeviceUSB      = "usb"

	DeviceOff      = "off"
	DeviceStarting = "starting"
	DeviceRunning  = "running"

	// HolderYou is the human: the device was taken back from an agent, or is
	// running the app. Never reclaimed as stale - only the human releases it.
	HolderYou = "you"
)

// ErrDeviceBusy is returned when a change needs the device free.
var ErrDeviceBusy = errors.New("the device is in use")

// Device is the active device row.
type Device struct {
	Kind, AVD, Serial, State, Holder string
	// Purpose says why HolderYou holds the device - "run-app:<target>" or
	// "taken-back:<ticket>" - so the dashboard names the ticket and branch
	// instead of a bare "reserved by you".
	Purpose                          string
	PID                              int
	StartedAt, RunningAt, AcquiredAt int64
}

// Hold purposes.
const (
	PurposeRunApp    = "run-app:"
	PurposeTakenBack = "taken-back:"
)

// RunAppTarget is the target ("main" or a ticket) Run app holds the device
// for, or "".
func (d Device) RunAppTarget() string { return purposeArg(d, PurposeRunApp) }

// TakenBackFrom is the ticket the device was taken back from, or "".
func (d Device) TakenBackFrom() string { return purposeArg(d, PurposeTakenBack) }

func purposeArg(d Device, prefix string) string {
	if d.Holder != HolderYou || len(d.Purpose) <= len(prefix) || d.Purpose[:len(prefix)] != prefix {
		return ""
	}
	return d.Purpose[len(prefix):]
}

// None reports that no device is chosen.
func (d Device) None() bool { return d.Kind == "" }

// Label is what the UI calls the device: the AVD name, or the USB serial.
func (d Device) Label() string {
	if d.Kind == DeviceUSB {
		return d.Serial
	}
	return d.AVD
}

// ActiveDevice reads the row.
func (s *Store) ActiveDevice() (Device, error) {
	var d Device
	err := s.db.QueryRow(`SELECT kind, avd, serial, state, holder, purpose, pid, started_at, running_at, acquired_at
		FROM active_device WHERE id = 1`).Scan(&d.Kind, &d.AVD, &d.Serial, &d.State, &d.Holder, &d.Purpose,
		&d.PID, &d.StartedAt, &d.RunningAt, &d.AcquiredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{State: DeviceOff}, nil
	}
	return d, err
}

// SeedDevice makes avd the active device - once, before the user ever chose
// one. It is how a config `avd_name` carries over to the device model.
func (s *Store) SeedDevice(avd string) error {
	if avd == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE active_device SET kind = ?, avd = ?, updated_at = ?
		WHERE id = 1 AND chosen = 0 AND kind = ''`, DeviceEmulator, avd, now())
	return err
}

// UseEmulator makes avd the active device (off until started), only while
// nobody holds the device. running reports an emulator of that AVD already up
// (adopted: pid 0, pie didn't launch it).
func (s *Store) UseEmulator(avd, runningSerial string) error {
	state, serial, runAt := DeviceOff, "", int64(0)
	if runningSerial != "" {
		state, serial, runAt = DeviceRunning, runningSerial, now()
	}
	return s.change(`UPDATE active_device SET kind = ?, avd = ?, serial = ?, state = ?, pid = 0,
		started_at = 0, running_at = ?, chosen = 1, updated_at = ? WHERE id = 1 AND holder = ''`,
		DeviceEmulator, avd, serial, state, runAt, now())
}

// UseUSB makes a connected phone the active device.
func (s *Store) UseUSB(serial string) error {
	return s.change(`UPDATE active_device SET kind = ?, avd = '', serial = ?, state = ?, pid = 0,
		started_at = 0, running_at = ?, chosen = 1, updated_at = ? WHERE id = 1 AND holder = ''`,
		DeviceUSB, serial, DeviceRunning, now(), now())
}

// ForgetDevice clears the choice ("stop using this phone").
func (s *Store) ForgetDevice() error {
	return s.change(`UPDATE active_device SET kind = '', avd = '', serial = '', state = ?, pid = 0,
		started_at = 0, running_at = 0, chosen = 1, updated_at = ? WHERE id = 1 AND holder = ''`,
		DeviceOff, now())
}

// BeginStart claims the right to launch the active emulator: off → starting.
// Exactly one caller wins; the others wait for it to come up.
func (s *Store) BeginStart(avd string, pid int) (bool, error) {
	res, err := s.db.Exec(`UPDATE active_device SET state = ?, pid = ?, started_at = ?, updated_at = ?
		WHERE id = 1 AND kind = ? AND avd = ? AND state = ?`,
		DeviceStarting, pid, now(), now(), DeviceEmulator, avd, DeviceOff)
	return affected(res, err)
}

// SetStartPID records the launched process once BeginStart won.
func (s *Store) SetStartPID(pid int) error {
	_, err := s.db.Exec(`UPDATE active_device SET pid = ? WHERE id = 1 AND state = ?`, pid, DeviceStarting)
	return err
}

// MarkRunning records the booted emulator's serial and, when pie timed the
// boot, how long it took (the next start's progress estimate).
func (s *Store) MarkRunning(avd, serial string) error {
	d, err := s.ActiveDevice()
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE active_device SET state = ?, serial = ?, running_at = ?, updated_at = ?
		WHERE id = 1 AND avd = ? AND state IN (?, ?)`,
		DeviceRunning, serial, now(), now(), avd, DeviceOff, DeviceStarting); err != nil {
		return err
	}
	if d.AVD == avd && d.State == DeviceStarting && d.StartedAt > 0 {
		if secs := now() - d.StartedAt; secs > 0 {
			_, _ = s.db.Exec(`INSERT INTO avd_boots (avd, secs) VALUES (?, ?)
				ON CONFLICT(avd) DO UPDATE SET secs = excluded.secs`, avd, secs)
		}
	}
	return nil
}

// MarkOff records that the emulator is gone (stopped, cancelled, crashed). Its
// holder is cleared with it: there is nothing left to hold.
func (s *Store) MarkOff() error {
	_, err := s.db.Exec(`UPDATE active_device SET state = ?, serial = CASE WHEN kind = ? THEN serial ELSE '' END,
		pid = 0, started_at = 0, running_at = 0, holder = '', purpose = '', acquired_at = 0, updated_at = ? WHERE id = 1`,
		DeviceOff, DeviceUSB, now())
	return err
}

// BootSecs is how long avd took to boot last time (0 = never timed).
func (s *Store) BootSecs(avd string) int64 {
	var secs int64
	_ = s.db.QueryRow(`SELECT secs FROM avd_boots WHERE avd = ?`, avd).Scan(&secs)
	return secs
}

// change runs a guarded UPDATE and maps "no row changed" to ErrDeviceBusy.
func (s *Store) change(q string, args ...interface{}) error {
	ok, err := affected(s.db.Exec(q, args...))
	if err != nil {
		return err
	}
	if !ok {
		return ErrDeviceBusy
	}
	return nil
}

func affected(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func now() int64 { return time.Now().Unix() }
