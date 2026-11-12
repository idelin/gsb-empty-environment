package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func mustInstance(t *testing.T, b *Budget, initial, max uint32) *Instance {
	t.Helper()
	inst, err := NewInstance(b, initial, max)
	if err != nil {
		t.Fatalf("NewInstance(%d, %d): %v", initial, max, err)
	}
	return inst
}

// 65536 pages: grow up to the wasm32 limit, then confirm one more page
// fails with -1 and leaves usage statistics untouched.
func TestGrowToWasm32Limit(t *testing.T) {
	b := NewBudget(MaxPages)
	inst := mustInstance(t, b, 1, MaxPages)

	if prev := inst.Grow(MaxPages - 1); prev != 1 {
		t.Fatalf("Grow(%d) = %d, want 1", MaxPages-1, prev)
	}
	if got := inst.Pages(); got != MaxPages {
		t.Fatalf("Pages() = %d, want %d", got, MaxPages)
	}
	if got := b.Used(); got != MaxPages {
		t.Fatalf("Used() = %d, want %d", got, MaxPages)
	}

	if prev := inst.Grow(1); prev != -1 {
		t.Fatalf("Grow(1) past limit = %d, want -1", prev)
	}
	if got := inst.Pages(); got != MaxPages {
		t.Fatalf("Pages() after failed grow = %d, want %d", got, MaxPages)
	}
	if got := b.Used(); got != MaxPages {
		t.Fatalf("Used() after failed grow = %d, want %d", got, MaxPages)
	}
	if prev := inst.Grow(0); prev != MaxPages {
		t.Fatalf("Grow(0) probe = %d, want %d", prev, MaxPages)
	}
}

// Cross-instance accumulation: one tenant budget shared by two instances.
func TestCrossInstanceAccumulation(t *testing.T) {
	b := NewBudget(10)
	inst1 := mustInstance(t, b, 0, 10)
	inst2 := mustInstance(t, b, 0, 10)

	if prev := inst1.Grow(6); prev != 0 {
		t.Fatalf("inst1.Grow(6) = %d, want 0", prev)
	}
	if prev := inst2.Grow(5); prev != -1 {
		t.Fatalf("inst2.Grow(5) = %d, want -1 (budget exhausted)", prev)
	}
	if got := b.Used(); got != 6 {
		t.Fatalf("Used() = %d, want 6 after failed grow", got)
	}
	if prev := inst2.Grow(4); prev != 0 {
		t.Fatalf("inst2.Grow(4) = %d, want 0", prev)
	}
	if got := b.Used(); got != 10 {
		t.Fatalf("Used() = %d, want 10", got)
	}
	if prev := inst1.Grow(1); prev != -1 {
		t.Fatalf("inst1.Grow(1) = %d, want -1 (budget full)", prev)
	}

	// Closing an instance returns its pages to the shared budget.
	inst1.Close()
	if got := b.Used(); got != 4 {
		t.Fatalf("Used() after Close = %d, want 4", got)
	}
	if prev := inst2.Grow(1); prev != 4 {
		t.Fatalf("inst2.Grow(1) after Close = %d, want 4", prev)
	}
}

// Grow interleaved with reads/writes: validation must use the current
// memory size and views must be refreshed after every grow.
func TestGrowReadInterleaved(t *testing.T) {
	b := NewBudget(4)
	inst := mustInstance(t, b, 1, 4)

	msg := []byte("hello")
	tail := uint32(PageSize - len(msg))
	if err := inst.Write(tail, msg); err != nil {
		t.Fatalf("Write at tail: %v", err)
	}

	// A read crossing the current end is rejected before the grow.
	if _, err := inst.Read(tail, uint32(len(msg))+1); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("Read past end before grow = %v, want ErrOutOfBounds", err)
	}

	viewBefore := inst.View()
	if prev := inst.Grow(1); prev != 1 {
		t.Fatalf("Grow(1) = %d, want 1", prev)
	}
	viewAfter := inst.View()
	if &viewBefore[0] == &viewAfter[0] {
		t.Fatal("view not refreshed after grow: stale backing array still in use")
	}

	// Data survives the grow and the same read now succeeds.
	got, err := inst.Read(tail, uint32(len(msg))+1)
	if err != nil {
		t.Fatalf("Read past old end after grow: %v", err)
	}
	if !bytes.Equal(got[:len(msg)], msg) {
		t.Fatalf("Read = %q, want prefix %q", got, msg)
	}

	// Writes into the newly grown region are valid now.
	if err := inst.Write(PageSize, msg); err != nil {
		t.Fatalf("Write into grown region: %v", err)
	}
}

// ptr+len wraparound: 32-bit sums that wrap past 4 GiB must be rejected.
func TestPtrLenWraparoundRejected(t *testing.T) {
	b := NewBudget(1)
	inst := mustInstance(t, b, 1, 1)
	size := uint32(PageSize)

	cases := []struct {
		name string
		ptr  uint32
		len  uint32
	}{
		{"wrap to zero", 0xFFFFFFFF, 1},
		{"wrap small", 0xFFFFFFF0, 0x20},
		{"both max", 0xFFFFFFFF, 0xFFFFFFFF},
		{"end overflow by one", size, 1},
		{"crossing end", size - 1, 2},
	}
	for _, tc := range cases {
		if _, err := inst.Read(tc.ptr, tc.len); !errors.Is(err, ErrOutOfBounds) {
			t.Errorf("Read(%#x, %#x) = %v, want ErrOutOfBounds", tc.ptr, tc.len, err)
		}
		if err := inst.Write(tc.ptr, make([]byte, tc.len)); !errors.Is(err, ErrOutOfBounds) {
			t.Errorf("Write(%#x, %#x) = %v, want ErrOutOfBounds", tc.ptr, tc.len, err)
		}
	}

	// Boundary cases that must pass: full range, and zero-length at end.
	if _, err := inst.Read(0, size); err != nil {
		t.Errorf("Read(0, %d) = %v, want nil", size, err)
	}
	if _, err := inst.Read(size, 0); err != nil {
		t.Errorf("Read(%d, 0) = %v, want nil", size, err)
	}
	if _, err := inst.Read(size-1, 1); err != nil {
		t.Errorf("Read(%d, 1) = %v, want nil", size-1, err)
	}
}

// Failed grows must not change usage statistics.
func TestFailedGrowKeepsStats(t *testing.T) {
	b := NewBudget(3)
	inst := mustInstance(t, b, 1, 10)

	if prev := inst.Grow(5); prev != -1 {
		t.Fatalf("Grow(5) over budget = %d, want -1", prev)
	}
	if got := b.Used(); got != 1 {
		t.Fatalf("Used() = %d, want 1", got)
	}
	if got := inst.Pages(); got != 1 {
		t.Fatalf("Pages() = %d, want 1", got)
	}

	if prev := inst.Grow(2); prev != 1 {
		t.Fatalf("Grow(2) = %d, want 1", prev)
	}
	if got := b.Used(); got != 3 {
		t.Fatalf("Used() = %d, want 3", got)
	}

	// Module-declared max still binds even with budget headroom.
	big := NewBudget(100)
	capped := mustInstance(t, big, 1, 2)
	if prev := capped.Grow(2); prev != -1 {
		t.Fatalf("Grow(2) over declared max = %d, want -1", prev)
	}
	if got := big.Used(); got != 1 {
		t.Fatalf("Used() = %d, want 1", got)
	}
}

// Module declarations above the wasm32 limit are rejected up front.
func TestDeclaredMaxExceedsWasm32Limit(t *testing.T) {
	b := NewBudget(1 << 20)
	if _, err := NewInstance(b, 0, MaxPages+1); err == nil {
		t.Fatal("NewInstance with max > 65536 pages: want error, got nil")
	}
	if got := b.Used(); got != 0 {
		t.Fatalf("Used() = %d, want 0 after rejected instance", got)
	}
}
