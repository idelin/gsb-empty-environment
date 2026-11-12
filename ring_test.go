package netsec

import (
	"errors"
	"math"
	"testing"
)

func TestNewRingRejectsBadSizes(t *testing.T) {
	bad := []uint32{0, 1, 3, 5, 6, 7, 9, 100, 1023}
	for _, size := range bad {
		if _, err := NewRing(size); !errors.Is(err, ErrInvalidRingSize) {
			t.Fatalf("NewRing(%d) err = %v, want ErrInvalidRingSize", size, err)
		}
	}

	for _, size := range []uint32{2, 4, 8, 1024, 1 << 20} {
		r, err := NewRing(size)
		if err != nil {
			t.Fatalf("NewRing(%d) unexpected error: %v", size, err)
		}
		if r.Size() != size {
			t.Fatalf("Size() = %d, want %d", r.Size(), size)
		}
	}
}

func TestRingWrap(t *testing.T) {
	r, err := NewRing(8)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}

	tests := []struct {
		index uint32
		want  uint32
	}{
		{0, 0},
		{7, 7},
		{8, 0}, // one full turn
		{9, 1},
		{15, 7},
		{16, 0}, // two full turns
		{100, 4},
		// Producer/consumer cursors are uint32 and wrap on overflow; the
		// slot must stay within [0, Size).
		{math.MaxUint32, 7},
		{math.MaxUint32 - 7, 0},
	}
	for _, tt := range tests {
		if got := r.Wrap(tt.index); got != tt.want {
			t.Errorf("Wrap(%d) = %d, want %d", tt.index, got, tt.want)
		}
		if err := r.CheckIndex(r.Wrap(tt.index)); err != nil {
			t.Errorf("wrapped slot %d must be a valid slot: %v", tt.index, err)
		}
	}
}

func TestRingCheckIndex(t *testing.T) {
	r, err := NewRing(4)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}

	for _, index := range []uint32{0, 1, 2, 3} {
		if err := r.CheckIndex(index); err != nil {
			t.Fatalf("CheckIndex(%d) = %v, want nil", index, err)
		}
	}
	for _, index := range []uint32{4, 5, 100, math.MaxUint32} {
		if err := r.CheckIndex(index); !errors.Is(err, ErrIndexOutOfRange) {
			t.Fatalf("CheckIndex(%d) = %v, want ErrIndexOutOfRange", index, err)
		}
	}
}

// TestRingCursorWalk simulates draining a ring whose cursor values wrap far
// past the slot count: every produced slot must map back to a legal index.
func TestRingCursorWalk(t *testing.T) {
	const size uint32 = 16
	r, err := NewRing(size)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}

	const produced = uint64(size)*1000 + 13
	for cursor := uint64(0); cursor < produced; cursor++ {
		slot := r.Wrap(uint32(cursor))
		if err := r.CheckIndex(slot); err != nil {
			t.Fatalf("cursor %d wrapped to invalid slot %d: %v", cursor, slot, err)
		}
	}
}
