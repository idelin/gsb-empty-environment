package netsec

import (
	"errors"
	"testing"
)

func TestRecordsFarFromWrap(t *testing.T) {
	g := NewGuard()
	for i := uint64(0); i < 1000; i++ {
		if err := g.OnRecord(); err != nil {
			t.Fatalf("record %d: unexpected error %v", i, err)
		}
	}
	if g.State() != StateReady {
		t.Fatalf("state = %v, want %v", g.State(), StateReady)
	}
	if g.Next() != 1000 {
		t.Fatalf("next = %d, want 1000", g.Next())
	}
}

func TestMaxSeqThenOneMoreRecord(t *testing.T) {
	g := NewGuard()
	g.ImportState(MaxSeq)

	// The record carrying MaxSeq is the last one accepted.
	if err := g.OnRecord(); err != nil {
		t.Fatalf("record at MaxSeq: unexpected error %v", err)
	}
	if g.State() != StateExhausted {
		t.Fatalf("state = %v, want %v", g.State(), StateExhausted)
	}

	// The next record would wrap to 0 under the same key: it must fail.
	if err := g.OnRecord(); !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("wrapped record: err = %v, want %v", err, ErrSequenceExhausted)
	}
}

func TestKeyUpdateBeforeWrap(t *testing.T) {
	g := NewGuard()
	g.ImportState(MaxSeq)
	if err := g.KeyUpdate(); err != nil {
		t.Fatalf("KeyUpdate: unexpected error %v", err)
	}
	if g.Epoch() != 1 || g.Next() != 0 || g.State() != StateReady {
		t.Fatalf("after KeyUpdate: epoch=%d next=%d state=%v", g.Epoch(), g.Next(), g.State())
	}
	if err := g.OnRecord(); err != nil {
		t.Fatalf("record after KeyUpdate: unexpected error %v", err)
	}
}

func TestKeyUpdateAfterExhaustion(t *testing.T) {
	g := NewGuard()
	g.ImportState(MaxSeq)
	if err := g.OnRecord(); err != nil {
		t.Fatalf("record at MaxSeq: unexpected error %v", err)
	}
	if err := g.KeyUpdate(); err != nil {
		t.Fatalf("KeyUpdate: unexpected error %v", err)
	}
	if err := g.OnRecord(); err != nil {
		t.Fatalf("record after KeyUpdate: unexpected error %v", err)
	}
}

func TestClose(t *testing.T) {
	g := NewGuard()
	g.Close()
	if err := g.OnRecord(); !errors.Is(err, ErrClosed) {
		t.Fatalf("record on closed guard: err = %v, want %v", err, ErrClosed)
	}
	if err := g.KeyUpdate(); !errors.Is(err, ErrClosed) {
		t.Fatalf("KeyUpdate on closed guard: err = %v, want %v", err, ErrClosed)
	}
}
