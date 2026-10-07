package logalloc

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"testing"
)

func openTemp(t *testing.T) (*Log, string) {
	dir, err := mkTempDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wal.log")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

func mkTempDir() (string, error) {
	return ioutil.TempDir("", "logalloc-test-")
}

func reopen(t *testing.T, l *Log, path string) *Log {
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	nl, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return nl
}

func TestReserveThenCommitPublishes(t *testing.T) {
	l, _ := openTemp(t)
	defer l.Close()

	start, err := l.Reserve(3)
	if err != nil {
		t.Fatal(err)
	}
	if start != 1 {
		t.Fatalf("first reservation starts at %d, want 1", start)
	}
	for seq := start; seq < start+3; seq++ {
		if err := l.Commit(seq, []byte(fmt.Sprintf("rec-%d", seq))); err != nil {
			t.Fatal(err)
		}
	}
	cut, err := l.AdvanceCut()
	if err != nil {
		t.Fatal(err)
	}
	if cut != 3 {
		t.Fatalf("cut = %d, want 3", cut)
	}
	recs := l.Read(0, 0)
	if len(recs) != 3 || recs[0].Seq != 1 || string(recs[2].Data) != "rec-3" {
		t.Fatalf("unexpected read result: %+v", recs)
	}
}

func TestGapBlocksPublishUntilExplicitSkip(t *testing.T) {
	l, _ := openTemp(t)
	defer l.Close()

	start, _ := l.Reserve(5) // seqs 1..5
	// Commit everything except seq 2, simulating a failed write.
	for _, seq := range []uint64{start, start + 2, start + 3, start + 4} {
		if err := l.Commit(seq, []byte("data")); err != nil {
			t.Fatal(err)
		}
	}
	cut, err := l.AdvanceCut()
	if err != nil {
		t.Fatal(err)
	}
	if cut != 1 {
		t.Fatalf("cut = %d, want 1: pending gap at 2 must block publishing", cut)
	}
	if got := l.Pending(); !reflect.DeepEqual(got, []uint64{2}) {
		t.Fatalf("pending = %v, want [2]", got)
	}

	// Close the gap with an explicit skip record.
	if err := l.Skip(2, "write failed"); err != nil {
		t.Fatal(err)
	}
	cut, err = l.AdvanceCut()
	if err != nil {
		t.Fatal(err)
	}
	if cut != 5 {
		t.Fatalf("cut = %d, want 5 after skip closed the gap", cut)
	}
}

func TestRecoveryDistinguishesPendingFromSkipped(t *testing.T) {
	l, path := openTemp(t)

	l.Reserve(6) // seqs 1..6
	l.Commit(1, []byte("a"))
	l.Commit(2, []byte("b"))
	l.Skip(3, "gave up")
	l.Commit(5, []byte("e"))
	// seqs 4 and 6 left undecided; crash before advancing the cut.
	l = reopen(t, l, path)
	defer l.Close()

	if got := l.Pending(); !reflect.DeepEqual(got, []uint64{4, 6}) {
		t.Fatalf("pending after recovery = %v, want [4 6]", got)
	}
	// Skipped slot 3 must not resurface as pending.
	for _, seq := range l.Pending() {
		if seq == 3 {
			t.Fatal("skipped seq 3 reported as pending after recovery")
		}
	}
	// Cut only advances over decided records: stops at pending seq 4.
	cut, err := l.AdvanceCut()
	if err != nil {
		t.Fatal(err)
	}
	if cut != 3 {
		t.Fatalf("cut = %d, want 3", cut)
	}
	// Recovery resolves the gap: skip 4, commit recovered data for 6.
	if err := l.Skip(4, "no data found after crash"); err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(6, []byte("recovered")); err != nil {
		t.Fatal(err)
	}
	cut, err = l.AdvanceCut()
	if err != nil {
		t.Fatal(err)
	}
	if cut != 6 {
		t.Fatalf("cut = %d, want 6", cut)
	}
}

func TestNoDuplicateAllocationAcrossCrash(t *testing.T) {
	l, path := openTemp(t)

	l.Reserve(4) // seqs 1..4
	l.Commit(1, []byte("a"))
	// Crash with 2..4 reserved but undecided.
	l = reopen(t, l, path)
	defer l.Close()

	if got := l.Reserved(); got != 5 {
		t.Fatalf("next allocation = %d, want 5: reserved seqs must not be re-allocated", got)
	}
	next, err := l.Reserve(2)
	if err != nil {
		t.Fatal(err)
	}
	if next != 5 {
		t.Fatalf("reservation after crash starts at %d, want 5", next)
	}
}

func TestPersistedCutPointVerifiesContinuousConsumption(t *testing.T) {
	l, path := openTemp(t)

	// Consumer checkpoint: what the consumer has durably consumed.
	var consumed uint64
	seen := make(map[uint64]bool)

	consume := func(l *Log) {
		for _, rec := range l.Read(consumed, 0) {
			if rec.Seq <= consumed {
				t.Fatalf("seq %d delivered twice", rec.Seq)
			}
			if seen[rec.Seq] {
				t.Fatalf("seq %d already consumed", rec.Seq)
			}
			seen[rec.Seq] = true
			consumed = rec.Seq
		}
		// Continuity check: every sequence up to the cut is decided
		// exactly once (committed or explicitly skipped).
		if consumed != l.Cut() {
			t.Fatalf("consumed = %d but cut = %d: consumption not continuous", consumed, l.Cut())
		}
		if got := l.Pending(); len(got) != 0 {
			t.Fatalf("pending slots below cut: %v", got)
		}
	}

	l.Reserve(3)
	l.Commit(1, []byte("a"))
	l.Skip(2, "failed")
	l.Commit(3, []byte("c"))
	l.AdvanceCut()
	consume(l)

	// Crash, recover, produce more, consume again from persisted cut.
	l = reopen(t, l, path)
	defer l.Close()
	if l.Cut() != 3 {
		t.Fatalf("cut after recovery = %d, want 3", l.Cut())
	}
	l.Reserve(2)
	l.Commit(4, []byte("d"))
	l.Commit(5, []byte("e"))
	l.AdvanceCut()
	consume(l)

	if consumed != 5 {
		t.Fatalf("consumed = %d, want 5", consumed)
	}
	// Skipped seq 2 was never delivered as data.
	if seen[2] {
		t.Fatal("skipped seq 2 delivered to consumer")
	}
	for _, seq := range []uint64{1, 3, 4, 5} {
		if !seen[seq] {
			t.Fatalf("committed seq %d never consumed", seq)
		}
	}
}

func TestInvalidTransitions(t *testing.T) {
	l, _ := openTemp(t)
	defer l.Close()

	if _, err := l.Reserve(0); err != ErrInvalidReservation {
		t.Fatalf("Reserve(0) err = %v", err)
	}
	if err := l.Commit(1, nil); err != ErrNotReserved {
		t.Fatalf("commit before reserve err = %v", err)
	}
	l.Reserve(2)
	if err := l.Commit(1, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(1, []byte("b")); err != ErrAlreadyDecided {
		t.Fatalf("double commit err = %v", err)
	}
	if err := l.Skip(1, "late"); err != ErrAlreadyDecided {
		t.Fatalf("skip after commit err = %v", err)
	}
	if err := l.Commit(3, nil); err != ErrNotReserved {
		t.Fatalf("commit beyond reservation err = %v", err)
	}
	if _, err := l.AdvanceCut(); err != nil {
		t.Fatal(err)
	}
	// Seq 1 is at/below the cut now: decisions are rejected.
	if err := l.Skip(1, "too late"); err != ErrAlreadyConsumed {
		t.Fatalf("skip below cut err = %v", err)
	}
	// Cut stays put while seq 2 is pending.
	if cut := l.Cut(); cut != 1 {
		t.Fatalf("cut = %d, want 1", cut)
	}
}
