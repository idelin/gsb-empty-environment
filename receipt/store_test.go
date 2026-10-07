package receipt

import (
	"errors"
	"fmt"
	"testing"
)

func submitSeq(t *testing.T, s *Store, gen string, revs []int64, statuses []Status) {
	t.Helper()
	for i, rev := range revs {
		err := s.Submit(Receipt{Generation: gen, Revision: rev, Status: statuses[i], Evidence: fmt.Sprintf("%s-%d", gen, rev)})
		if err != nil {
			t.Fatalf("submit %s@%d: %v", gen, rev, err)
		}
	}
}

func stateFingerprint(m *Machine) string {
	out := fmt.Sprintf("current=%s", m.Current())
	for _, gen := range []string{"A", "B", "C"} {
		if v, ok := m.View(gen); ok {
			out += fmt.Sprintf(" | %s:%s@%d arch=%d comp=%d sup=%s ev=%s",
				gen, v.Status, v.Revision, len(v.Archived), len(v.Compensations), v.SupersededBy,
				evidenceKey(m.Evidence(gen)))
		}
	}
	return out
}

// TestCheckpointRecovery writes events, checkpoints mid-stream, writes more,
// then verifies that recovery from checkpoint+log-tail equals full replay.
func TestCheckpointRecovery(t *testing.T) {
	dir := t.TempDir()

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bootstrap("A"); err != nil {
		t.Fatal(err)
	}
	submitSeq(t, s, "A", []int64{1, 2}, []Status{StatusAccepted, StatusRunning})
	// B's receipt arrives before the resolution: buffered, persisted.
	if err := s.Submit(Receipt{Generation: "B", Revision: 1, Status: StatusAccepted, Evidence: "b1"}); !errors.As(err, new(*UnknownGenerationError)) {
		t.Fatalf("expected UnknownGenerationError, got %v", err)
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	// Post-checkpoint tail.
	if err := s.Supersede("A", "B"); err != nil {
		t.Fatal(err)
	}
	submitSeq(t, s, "B", []int64{2}, []Status{StatusRunning})
	// Late success of superseded A: compensation, persisted.
	if err := s.Submit(Receipt{Generation: "A", Revision: 3, Status: StatusSucceeded, Evidence: "a3"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	m := reopened.Machine()
	if m.Current() != "B" {
		t.Fatalf("current = %q, want B", m.Current())
	}
	va, _ := m.View("A")
	if len(va.Compensations) != 1 || va.Compensations[0].Evidence != "a3" {
		t.Fatalf("compensation lost across recovery: %+v", va.Compensations)
	}
	vb, _ := m.View("B")
	if vb.Status != StatusRunning || vb.Revision != 2 {
		t.Fatalf("B state after recovery: %+v", vb)
	}
	if got := len(m.Evidence("A")); got != 3 {
		t.Fatalf("A evidence after recovery: %d, want 3", got)
	}
	if got := len(m.Evidence("B")); got != 2 {
		t.Fatalf("B evidence after recovery: %d, want 2", got)
	}

	// Full replay from scratch must yield the identical state.
	fresh := NewMachine()
	fresh.Bootstrap("A")
	for _, r := range []Receipt{
		{Generation: "A", Revision: 1, Status: StatusAccepted, Evidence: "A-1"},
		{Generation: "A", Revision: 2, Status: StatusRunning, Evidence: "A-2"},
		{Generation: "B", Revision: 1, Status: StatusAccepted, Evidence: "b1"},
	} {
		fresh.applyReceipt(r)
	}
	fresh.applySupersede(SupersedeRecord{Old: "A", New: "B"})
	fresh.applyReceipt(Receipt{Generation: "B", Revision: 2, Status: StatusRunning, Evidence: "B-2"})
	fresh.applyReceipt(Receipt{Generation: "A", Revision: 3, Status: StatusSucceeded, Evidence: "a3"})
	if got, want := stateFingerprint(m), stateFingerprint(fresh); got != want {
		t.Fatalf("recovered state != full replay:\ngot  %s\nwant %s", got, want)
	}
}

// TestCheckpointTruncatesLog ensures recovery works from checkpoint alone.
func TestCheckpointTruncatesLog(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Bootstrap("A")
	submitSeq(t, s, "A", []int64{1, 2, 3}, []Status{StatusAccepted, StatusRunning, StatusSucceeded})
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	s.Close()

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	v, _ := reopened.Machine().View("A")
	if v.Status != StatusSucceeded || v.Revision != 3 {
		t.Fatalf("state lost after checkpoint recovery: %+v", v)
	}
	if got := len(reopened.Machine().Evidence("A")); got != 3 {
		t.Fatalf("evidence lost after checkpoint: %d", got)
	}
}

// TestConflictNotPersisted rejects contradictory revisions durably.
func TestConflictNotPersisted(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Bootstrap("A")
	submitSeq(t, s, "A", []int64{1}, []Status{StatusRunning})
	err := s.Submit(Receipt{Generation: "A", Revision: 1, Status: StatusFailed, Evidence: "x"})
	if !errors.As(err, new(*ConflictError)) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	s.Close()

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	v, _ := reopened.Machine().View("A")
	if v.Status != StatusRunning {
		t.Fatalf("conflict leaked into persistent state: %+v", v)
	}
	if got := len(reopened.Machine().Evidence("A")); got != 1 {
		t.Fatalf("conflicting receipt persisted: %d receipts", got)
	}
}

// TestUniqueCurrentAfterRecovery supersedes twice with a checkpoint between
// and asserts exactly one current generation survives recovery.
func TestUniqueCurrentAfterRecovery(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Bootstrap("A")
	s.Supersede("A", "B")
	s.Checkpoint()
	s.Supersede("B", "C")
	s.Close()

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	m := reopened.Machine()
	if m.Current() != "C" {
		t.Fatalf("current = %q, want unique C", m.Current())
	}
	va, _ := m.View("A")
	vb, _ := m.View("B")
	if va.SupersededBy != "B" || vb.SupersededBy != "C" {
		t.Fatalf("supersede chain broken: A->%q B->%q", va.SupersededBy, vb.SupersededBy)
	}
	// A receipt for a retired generation cannot become current again.
	if err := reopened.Submit(Receipt{Generation: "A", Revision: 9, Status: StatusSucceeded, Evidence: "late"}); err != nil {
		t.Fatal(err)
	}
	if m.Current() != "C" {
		t.Fatalf("retired generation hijacked current")
	}
	va, _ = m.View("A")
	if len(va.Compensations) != 1 {
		t.Fatalf("late terminal receipt not compensated: %+v", va.Compensations)
	}
}
