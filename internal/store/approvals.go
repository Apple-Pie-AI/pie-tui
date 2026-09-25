// Pending permission approvals: the bus between the headless approval
// callback (pie mcp-approve, spawned by claude) and the TUI. The callback
// inserts a pending row and polls it; the TUI lists pending rows, shows the
// command to the human, and writes the decision. SQLite is already the
// cross-process channel everywhere else in pie, so it is here too.
package store

import "time"

// Approval states.
const (
	ApprovalPending = "pending"
	ApprovalAllowed = "allowed"
	ApprovalDenied  = "denied"
	ApprovalExpired = "expired" // the callback gave up waiting
)

// Approval is one tool call awaiting (or past) a human verdict.
type Approval struct {
	ID          int64
	Ticket      string
	Tool        string
	Command     string
	State       string
	Remember    bool // decided as "allow & remember" - the TUI appends a rule
	RequestedAt int64
}

// CreateApproval inserts a pending approval and returns its id.
func (s *Store) CreateApproval(ticket, tool, command string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO approvals (ticket, tool, command, state, requested_at) VALUES (?,?,?,?,?)`,
		ticket, tool, command, ApprovalPending, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ApprovalState reads one approval's current state.
func (s *Store) ApprovalState(id int64) (string, error) {
	var state string
	err := s.db.QueryRow(`SELECT state FROM approvals WHERE id = ?`, id).Scan(&state)
	return state, err
}

// DecideApproval records the verdict on a pending approval. Deciding an
// already-decided row is a no-op (first verdict wins - the callback may have
// expired it a heartbeat before the human pressed enter).
func (s *Store) DecideApproval(id int64, state string, remember bool) error {
	_, err := s.db.Exec(
		`UPDATE approvals SET state = ?, remember = ?, decided_at = ? WHERE id = ? AND state = ?`,
		state, boolToInt(remember), time.Now().Unix(), id, ApprovalPending)
	return err
}

// ExpirePendingApprovals expires every pending approval for a ticket - the
// stop/restart janitor: a run being killed or relaunched must not leave a
// prompt on the dashboard that no agent is waiting behind. (There is no
// time-based expiry anywhere: a live session's prompt waits for the human
// indefinitely, by design.)
func (s *Store) ExpirePendingApprovals(ticket string) error {
	_, err := s.db.Exec(
		`UPDATE approvals SET state = ?, decided_at = ? WHERE ticket = ? AND state = ?`,
		ApprovalExpired, time.Now().Unix(), ticket, ApprovalPending)
	return err
}

// PendingApprovals lists pending rows, oldest first; ticket "" means all.
func (s *Store) PendingApprovals(ticket string) ([]Approval, error) {
	q := `SELECT id, ticket, tool, command, state, remember, requested_at
	      FROM approvals WHERE state = ?`
	args := []interface{}{ApprovalPending}
	if ticket != "" {
		q += ` AND ticket = ?`
		args = append(args, ticket)
	}
	q += ` ORDER BY id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Approval
	for rows.Next() {
		var a Approval
		var remember int
		if err := rows.Scan(&a.ID, &a.Ticket, &a.Tool, &a.Command, &a.State, &remember, &a.RequestedAt); err != nil {
			return nil, err
		}
		a.Remember = remember != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
