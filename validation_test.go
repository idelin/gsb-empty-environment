package batchlease

import (
	"errors"
	"testing"
)

func TestConfirmContiguityAndValidation(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	defer s.Close()
	l := mustAcquire(t, s, "batch-A", 1, 5, "A")

	if err := s.Confirm(l.Token, 2); !errors.Is(err, ErrNonContiguous) {
		t.Fatalf("first confirm skips seq 1: got %v", err)
	}
	mustConfirm(t, s, l.Token, 1)
	if err := s.Confirm(l.Token, 1); !errors.Is(err, ErrNonContiguous) {
		t.Fatalf("re-confirm: got %v", err)
	}
	if err := s.Confirm(l.Token, 3); !errors.Is(err, ErrNonContiguous) {
		t.Fatalf("gap: got %v", err)
	}
	mustConfirm(t, s, l.Token, 2)

	if _, err := s.Acquire("batch-B", BatchSummary{FirstSeq: 3, LastSeq: 4, Hash: h("B")}); !errors.Is(err, ErrOverlap) {
		t.Fatalf("overlap: got %v", err)
	}

	other := Token{BatchID: "batch-A", Epoch: l.Token.Epoch + 1}
	if err := s.Confirm(other, 3); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong epoch token: got %v", err)
	}
	unknown := Token{BatchID: "nope", Epoch: 1}
	if err := s.Confirm(unknown, 3); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown batch: got %v", err)
	}

	l2 := mustAcquire(t, s, "batch-B", 6, 7, "B")
	if err := s.Confirm(l2.Token, 6); !errors.Is(err, ErrNonContiguous) {
		t.Fatalf("confirming later batch before contiguous prefix: got %v", err)
	}
	if err := s.Confirm(l.Token, 99); !errors.Is(err, ErrSequenceOutOfRange) {
		t.Fatalf("out of range: got %v", err)
	}
}

func TestAcquireValidation(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	defer s.Close()

	if _, err := s.Acquire("", BatchSummary{FirstSeq: 1, LastSeq: 1, Hash: h("x")}); err == nil {
		t.Fatal("empty batch id accepted")
	}
	if _, err := s.Acquire("b", BatchSummary{FirstSeq: 0, LastSeq: 1, Hash: h("x")}); err == nil {
		t.Fatal("zero first seq accepted")
	}
	if _, err := s.Acquire("b", BatchSummary{FirstSeq: 5, LastSeq: 4, Hash: h("x")}); err == nil {
		t.Fatal("inverted range accepted")
	}
	if _, err := s.Acquire("b", BatchSummary{FirstSeq: 1, LastSeq: 1, Hash: nil}); err == nil {
		t.Fatal("empty hash accepted")
	}
}
