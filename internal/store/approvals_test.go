package store

import (
	"path/filepath"
	"testing"
)

func openApprovalsTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestApprovalLifecycle(t *testing.T) {
	s := openApprovalsTest(t)
	id, err := s.CreateApproval("PLEX-1", "Bash", "cd Compass && ./gradlew test")
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ApprovalState(id); st != ApprovalPending {
		t.Fatalf("state = %q, want pending", st)
	}
	pending, err := s.PendingApprovals("PLEX-1")
	if err != nil || len(pending) != 1 || pending[0].Command != "cd Compass && ./gradlew test" {
		t.Fatalf("pending = %+v (%v)", pending, err)
	}
	if err := s.DecideApproval(id, ApprovalAllowed, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ApprovalState(id); st != ApprovalAllowed {
		t.Fatalf("state after decide = %q", st)
	}
	if pending, _ = s.PendingApprovals(""); len(pending) != 0 {
		t.Fatalf("still pending: %+v", pending)
	}
}

// First verdict wins: a decision on an already-decided row is a no-op, so the
// expiry the callback writes a heartbeat before the human's enter cannot be
// overwritten into a phantom allow nobody acts on.
func TestDecideApprovalFirstVerdictWins(t *testing.T) {
	s := openApprovalsTest(t)
	id, _ := s.CreateApproval("PLEX-2", "Bash", "adb devices")
	if err := s.DecideApproval(id, ApprovalExpired, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideApproval(id, ApprovalAllowed, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ApprovalState(id); st != ApprovalExpired {
		t.Fatalf("state = %q, want the first verdict (expired) kept", st)
	}
}

func TestPendingApprovalsFiltersByTicket(t *testing.T) {
	s := openApprovalsTest(t)
	s.CreateApproval("PLEX-1", "Bash", "a")
	s.CreateApproval("PLEX-2", "Bash", "b")
	got, _ := s.PendingApprovals("PLEX-2")
	if len(got) != 1 || got[0].Ticket != "PLEX-2" {
		t.Fatalf("filtered = %+v", got)
	}
	all, _ := s.PendingApprovals("")
	if len(all) != 2 {
		t.Fatalf("all = %+v", all)
	}
}

// ExpirePendingApprovals is the stop/restart janitor: it expires every
// pending row for one ticket and touches nothing else - not other tickets,
// not already-decided rows.
func TestExpirePendingApprovals(t *testing.T) {
	s := openApprovalsTest(t)
	a, _ := s.CreateApproval("PLEX-1", "Bash", "a")
	b, _ := s.CreateApproval("PLEX-1", "Bash", "b")
	other, _ := s.CreateApproval("PLEX-2", "Bash", "c")
	if err := s.DecideApproval(b, ApprovalAllowed, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ExpirePendingApprovals("PLEX-1"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.ApprovalState(a); st != ApprovalExpired {
		t.Errorf("pending row = %q, want expired", st)
	}
	if st, _ := s.ApprovalState(b); st != ApprovalAllowed {
		t.Errorf("decided row = %q, want untouched", st)
	}
	if st, _ := s.ApprovalState(other); st != ApprovalPending {
		t.Errorf("other ticket's row = %q, want still pending", st)
	}
}
