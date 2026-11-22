package lease

import (
	"errors"
	"testing"
)

func TestExpandForwardAndBackward(t *testing.T) {
	// Forward within span.
	got, err := Expand(1050, 1000, 100)
	if err != nil || got != 1050 {
		t.Fatalf("forward: got %d, err %v", got, err)
	}
	// Backward within span.
	got, err = Expand(950, 1000, 100)
	if err != nil || got != 950 {
		t.Fatalf("backward: got %d, err %v", got, err)
	}
	// Anchor itself.
	got, err = Expand(1000, 1000, 100)
	if err != nil || got != 1000 {
		t.Fatalf("anchor: got %d, err %v", got, err)
	}
}

func TestExpandRingWrap(t *testing.T) {
	anchor := uint64(SeqModulus - 10) // low 24 bits near the wrap point
	wire := uint32(5)                 // anchor + 15 wraps past 2^24
	got, err := Expand(wire, anchor, 100)
	if err != nil || got != anchor+15 {
		t.Fatalf("wrap forward: got %d, err %v", got, err)
	}
	// Backward across the wrap: wire just below anchor's low bits.
	got, err = Expand(uint32(SeqModulus-20), anchor, 100)
	if err != nil || got != anchor-10 {
		t.Fatalf("wrap backward: got %d, err %v", got, err)
	}
}

func TestExpandHalfRingAmbiguous(t *testing.T) {
	anchor := uint64(1000)
	wire := uint32((anchor + SeqHalf) & SeqMask)
	if _, err := Expand(wire, anchor, uint32(SeqHalf-1)); !errors.Is(err, ErrAmbiguousSeq) {
		t.Fatalf("half-ring ahead: want ErrAmbiguousSeq, got %v", err)
	}
	// Same ambiguity approached from the other side of the ring.
	anchor = uint64(SeqHalf)
	wire = 0
	if _, err := Expand(wire, anchor, uint32(SeqHalf-1)); !errors.Is(err, ErrAmbiguousSeq) {
		t.Fatalf("half-ring behind: want ErrAmbiguousSeq, got %v", err)
	}
	// One tick inside the boundary on each side resolves fine.
	if got, err := Expand(uint32((1000+SeqHalf-1)&SeqMask), 1000, uint32(SeqHalf-1)); err != nil || got != 1000+SeqHalf-1 {
		t.Fatalf("half-1 ahead: got %d, err %v", got, err)
	}
	anchor = 1000 + SeqHalf
	if got, err := Expand(uint32((anchor-(SeqHalf-1))&SeqMask), anchor, uint32(SeqHalf-1)); err != nil || got != anchor-(SeqHalf-1) {
		t.Fatalf("half-1 behind: got %d, err %v", got, err)
	}
}

func TestExpandOutOfSpanAndInvalid(t *testing.T) {
	if _, err := Expand(1200, 1000, 100); !errors.Is(err, ErrSeqOutOfSpan) {
		t.Fatalf("out of span: want ErrSeqOutOfSpan, got %v", err)
	}
	if _, err := Expand(uint32(SeqModulus), 1000, 100); !errors.Is(err, ErrInvalidWire) {
		t.Fatalf("wire too wide: want ErrInvalidWire, got %v", err)
	}
	if _, err := Expand(5, 1000, uint32(SeqHalf)); !errors.Is(err, ErrInvalidSpan) {
		t.Fatalf("span too wide: want ErrInvalidSpan, got %v", err)
	}
	// Backward expansion may not underflow the logical sequence space.
	if _, err := Expand(uint32(SeqModulus-1), 10, 100); !errors.Is(err, ErrSeqOutOfSpan) {
		t.Fatalf("underflow: want ErrSeqOutOfSpan, got %v", err)
	}
}
