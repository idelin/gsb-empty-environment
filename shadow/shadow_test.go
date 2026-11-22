package shadow

import (
	"errors"
	"testing"
	"unsafe"
)

var accessSizes = [...]int{1, 2, 4, 8, 16}

func at(p unsafe.Pointer, off int) unsafe.Pointer {
	return unsafe.Pointer(uintptr(p) + uintptr(off))
}

func kindOf(t *testing.T, err error) ErrorKind {
	t.Helper()
	if err == nil {
		return KindOK
	}
	var ae *AccessError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *AccessError, got %T: %v", err, nil)
	}
	return ae.Kind
}

func mustPass(t *testing.T, d *Detector, p unsafe.Pointer, off, size int) {
	t.Helper()
	for _, acc := range []Access{Read, Write} {
		if err := d.Check(at(p, off), size, acc); err != nil {
			t.Fatalf("offset %d size %d %s: expected ok, got %v", off, size, acc, err)
		}
	}
}

func mustFail(t *testing.T, d *Detector, p unsafe.Pointer, off, size int, want ErrorKind) {
	t.Helper()
	for _, acc := range []Access{Read, Write} {
		if got := kindOf(t, d.Check(at(p, off), size, acc)); got != want {
			t.Fatalf("offset %d size %d %s: expected %s, got %s", off, size, acc, want, got)
		}
	}
}

// TestShadowEncoding verifies the 1:8 mapping: full cells are zero and the
// tail cell carries the number of valid prefix bytes for every block size.
func TestShadowEncoding(t *testing.T) {
	d := New(WithCapacity(1 << 20))
	for size := 1; size <= 25; size++ {
		b := d.AllocBytes(size)
		p := unsafe.Pointer(unsafe.SliceData(b))

		fullCells := size / Scale
		for i := 0; i < fullCells; i++ {
			if v, ok := d.ShadowAt(at(p, i*Scale)); !ok || v != ShadowAccessible {
				t.Fatalf("size %d cell %d: expected shadow 0, got %d (ok=%v)", size, i, v, ok)
			}
		}
		if rem := size % Scale; rem != 0 {
			if v, _ := d.ShadowAt(at(p, fullCells*Scale)); v != int8(rem) {
				t.Fatalf("size %d tail cell: expected shadow %d, got %d", size, rem, v)
			}
		}

		// Red zones flank the payload.
		if v, _ := d.ShadowAt(at(p, -Scale)); v != ShadowLeftRedzone {
			t.Fatalf("size %d left redzone: got %d", size, v)
		}
		if v, _ := d.ShadowAt(at(p, (size+7)&^7)); v != ShadowRightRedzone {
			t.Fatalf("size %d right redzone: got %d", size, v)
		}

		// A single byte past the tail must always be caught.
		mustFail(t, d, p, size, 1, KindHeapOverflow)
	}
}

// TestTailPartialBytes exercises a block ending in a partial shadow cell
// (13 bytes: tail cell value 5) with all supported access sizes.
func TestTailPartialBytes(t *testing.T) {
	d := New(WithCapacity(1 << 20))
	b := d.AllocBytes(13)
	p := unsafe.Pointer(unsafe.SliceData(b))

	if v, _ := d.ShadowAt(at(p, 8)); v != 5 {
		t.Fatalf("tail shadow: expected 5, got %d", v)
	}

	// Valid accesses at arbitrary offsets, including inside the partial cell.
	for _, s := range accessSizes {
		if s <= 13 {
			mustPass(t, d, p, 0, s)
			mustPass(t, d, p, 13-s, s)
		}
	}
	// One byte past the block, for every access size.
	for _, s := range accessSizes {
		off := 13 - (s - 1) // ends exactly one byte beyond the payload
		if off < 0 {
			continue // larger accesses would reach the left red zone
		}
		mustFail(t, d, p, off, s, KindHeapOverflow)
	}

	// Left red zone is underflow; right red zone is overflow.
	mustFail(t, d, p, -2, 2, KindHeapUnderflow)
	mustFail(t, d, p, 13, 1, KindHeapOverflow)
	mustFail(t, d, p, 16, 1, KindHeapOverflow)
}

// TestCrossSegment checks accesses that straddle shadow cell boundaries,
// including a 16-byte access spanning three cells.
func TestCrossSegment(t *testing.T) {
	d := New(WithCapacity(1 << 20))
	b := d.AllocBytes(24) // exactly three full cells
	p := unsafe.Pointer(unsafe.SliceData(b))

	// 8 bytes starting mid-cell spans two cells, both fully accessible.
	mustPass(t, d, p, 4, 8)
	// 16 bytes at an unaligned offset spans three cells.
	mustPass(t, d, p, 4, 16)
	mustPass(t, d, p, 8, 16)

	// Spilling into the next cell (right red zone) is an overflow.
	mustFail(t, d, p, 9, 16, KindHeapOverflow) // end at 25, touches cell 3
	mustFail(t, d, p, 20, 8, KindHeapOverflow) // end at 28, touches cell 3
	mustFail(t, d, p, 23, 2, KindHeapOverflow) // end at 25

	// Reaching into the left red zone.
	mustFail(t, d, p, -4, 8, KindHeapUnderflow)
}

// TestUseAfterFree verifies that freed payloads poison as ShadowFreed and
// every access size is rejected, including double and unknown frees.
func TestUseAfterFree(t *testing.T) {
	d := New(WithCapacity(1<<20), WithQuarantineLimit(1<<20))
	b := d.AllocBytes(16)
	p := unsafe.Pointer(unsafe.SliceData(b))

	if err := d.Free(p); err != nil {
		t.Fatalf("free: %v", err)
	}
	if v, ok := d.ShadowAt(p); !ok || v != ShadowFreed {
		t.Fatalf("freed shadow: expected %d, got %d", ShadowFreed, v)
	}
	for _, s := range accessSizes {
		mustFail(t, d, p, 0, s, KindUseAfterFree)
		mustFail(t, d, p, 16-s, s, KindUseAfterFree)
	}

	if err := d.Free(p); !errors.Is(err, ErrDoubleFree) {
		t.Fatalf("double free: expected ErrDoubleFree, got %v", err)
	}
	if err := d.Free(at(p, 1)); !errors.Is(err, ErrUnknownPointer) {
		t.Fatalf("unknown pointer: expected ErrUnknownPointer, got %v", err)
	}
}

// TestQuarantine verifies FIFO quarantine: eviction happens only once the
// total bytes exceed the limit, oldest first, and recycled memory is reused.
func TestQuarantine(t *testing.T) {
	const limit = 160
	d := New(WithCapacity(1<<20), WithQuarantineLimit(limit), WithRedzones(16, 16))

	a := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(64)))
	bp := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(64)))
	c := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(64)))

	free := func(p unsafe.Pointer) {
		t.Helper()
		if err := d.Free(p); err != nil {
			t.Fatalf("free: %v", err)
		}
	}

	free(a) // 64 <= 160: nothing evicted
	free(bp)
	if s := d.Snapshot(); s.Quarantined != 2 || s.ReusableBlocks != 0 ||
		s.QuarantineBytes != 128 {
		t.Fatalf("under limit: %+v", s)
	}

	free(c) // 192 > 160: oldest block a is evicted
	if s := d.Snapshot(); s.Quarantined != 2 || s.ReusableBlocks != 1 ||
		s.QuarantineBytes != 128 {
		t.Fatalf("after overflow: %+v", s)
	}
	// Evicted memory stays poisoned until reallocated.
	if kindOf(t, d.Load(a, 1)) != KindUseAfterFree {
		t.Fatalf("evicted block should remain poisoned before reuse")
	}

	// Reuse the recycled block: the payload becomes accessible again.
	reused := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(64)))
	if reused != a {
		t.Fatalf("expected evicted block %#x reused, got %#x", a, reused)
	}
	if err := d.Load(reused, 8); err != nil {
		t.Fatalf("reused block: %v", err)
	}
	if v, _ := d.ShadowAt(reused); v != ShadowAccessible {
		t.Fatalf("reused shadow: expected 0, got %d", v)
	}
	// The other freed blocks remain quarantined.
	mustFail(t, d, bp, 0, 1, KindUseAfterFree)
	mustFail(t, d, c, 0, 1, KindUseAfterFree)

	// FIFO ordering across more evictions.
	x := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(64)))
	y := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(64)))
	z := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(64)))
	free(x) // evicts bp
	free(y) // evicts c
	free(z) // evicts x
	if s := d.Snapshot(); s.ReusableBlocks != 3 || s.QuarantineBytes != 128 {
		t.Fatalf("fifo phase: %+v", s)
	}
	wantOrder := []unsafe.Pointer{bp, c, x}
	for _, want := range wantOrder {
		got := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(64)))
		if got != want {
			t.Fatalf("reuse order: expected %#x, got %#x", want, got)
		}
	}
}

// TestQuarantineAtLimit checks that reaching (not exceeding) the limit keeps
// every block quarantined.
func TestQuarantineAtLimit(t *testing.T) {
	d := New(WithCapacity(1<<20), WithQuarantineLimit(128))
	p1 := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(64)))
	p2 := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(64)))
	if err := d.Free(p1); err != nil {
		t.Fatal(err)
	}
	if err := d.Free(p2); err != nil {
		t.Fatal(err)
	}
	if s := d.Snapshot(); s.Quarantined != 2 || s.ReusableBlocks != 0 ||
		s.QuarantineBytes != 128 {
		t.Fatalf("at limit: %+v", s)
	}
}

func TestInvalidSizeAndAddress(t *testing.T) {
	d := New(WithCapacity(1 << 20))
	p := unsafe.Pointer(unsafe.SliceData(d.AllocBytes(32)))

	for _, bad := range []int{0, 3, 5, 7, 32} {
		if got := kindOf(t, d.Check(p, bad, Read)); got != KindInvalidSize {
			t.Fatalf("size %d: expected KindInvalidSize, got %s", bad, got)
		}
	}
	if got := kindOf(t, d.Check(at(p, 32), 1, Read)); got != KindHeapOverflow {
		t.Fatalf("past block: got %s", got)
	}
	if got := kindOf(t, d.Check(at(p, 1<<16), 1, Read)); got != KindInvalidAccess {
		t.Fatalf("outside arena: got %s", got)
	}
	if err := d.CheckSlice(d.AllocBytes(8), Read); err != nil {
		t.Fatalf("CheckSlice: %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	d := New(WithCapacity(1<<20), WithQuarantineLimit(4096))
	done := make(chan struct{})

	for w := 0; w < 8; w++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				b := d.AllocBytes(40)
				p := unsafe.Pointer(unsafe.SliceData(b))
				if err := d.Load(p, 8); err != nil {
					t.Errorf("load: %v", err)
					return
				}
				if kindOf(t, d.Store(at(p, 40), 1)) != KindHeapOverflow {
					t.Errorf("expected overflow at block tail")
					return
				}
				if err := d.Free(p); err != nil {
					t.Errorf("free: %v", err)
					return
				}
				if kindOf(t, d.Load(p, 1)) != KindUseAfterFree {
					t.Errorf("expected use-after-free")
					return
				}
			}
		}()
	}
	for w := 0; w < 8; w++ {
		<-done
	}
}
