package slaballoc

import (
	"bytes"
	"math/rand"
	"testing"
	"unsafe"
)

// --- encoding layout & byte occupancy ---

func TestLayoutSizes(t *testing.T) {
	if got := unsafe.Sizeof(Handle(0)); got != 8 {
		t.Fatalf("Handle = %d bytes, want 8", got)
	}
	if got := unsafe.Sizeof(slotEntry{}); got != 16 {
		t.Fatalf("slotEntry = %d bytes, want 16", got)
	}
	if got := unsafe.Sizeof(extent{}); got != 8 {
		t.Fatalf("extent = %d bytes, want 8", got)
	}
}

func TestHandleBitLayout(t *testing.T) {
	h := makeHandle(0xABCD, 0x123456, 0xABCDEF)
	if h.pageIdx() != 0xABCD || h.slotIdx() != 0x123456 || h.gen() != 0xABCDEF {
		t.Fatalf("decode mismatch: %v", h)
	}
	// raw bit pattern: page<<48 | slot<<24 | gen
	want := uint64(0xABCD)<<48 | uint64(0x123456)<<24 | uint64(0xABCDEF)
	if uint64(h) != want {
		t.Fatalf("raw handle = %#x, want %#x", uint64(h), want)
	}
}

// --- basic alloc / get / free ---

func TestAllocGetFree(t *testing.T) {
	a := New(256, 1)
	h, err := a.Alloc(10)
	if err != nil {
		t.Fatal(err)
	}
	buf, err := a.Get(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) != 16 { // 10 aligned up to 16
		t.Fatalf("len = %d, want 16 (8-byte alignment)", len(buf))
	}
	copy(buf, "hello")
	if err := a.Free(h); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Get(h); err != ErrStaleHandle {
		t.Fatalf("Get after Free = %v, want ErrStaleHandle", err)
	}
	if err := a.Free(h); err != ErrStaleHandle {
		t.Fatalf("double Free = %v, want ErrStaleHandle", err)
	}
}

// --- compaction keeps handles stable ---

func TestCompactionKeepsHandlesStable(t *testing.T) {
	a := New(256, 1)
	fill := func(n int, b byte) Handle {
		h, err := a.Alloc(n)
		if err != nil {
			t.Fatal(err)
		}
		buf, _ := a.Get(h)
		for i := range buf {
			buf[i] = b
		}
		return h
	}
	ha := fill(64, 'A')
	hb := fill(64, 'B')
	hc := fill(64, 'C')
	hd := fill(64, 'D')
	if err := a.Free(hb); err != nil {
		t.Fatal(err)
	}
	if err := a.Free(hd); err != nil {
		t.Fatal(err)
	}
	st := a.PageStats(0)
	if st.FreeExtents != 2 {
		t.Fatalf("before compaction: FreeExtents = %d, want 2 (fragmented)", st.FreeExtents)
	}
	// 128 bytes free but split into two 64-byte holes: forces compaction.
	he, err := a.Alloc(128)
	if err != nil {
		t.Fatalf("alloc triggering compaction failed: %v", err)
	}
	st = a.PageStats(0)
	if st.FreeExtents != 0 {
		t.Fatalf("after compaction+alloc: FreeExtents = %d, want 0 (128B tail fully consumed)", st.FreeExtents)
	}
	// Old handles still resolve to the same objects.
	for h, want := range map[Handle]byte{ha: 'A', hc: 'C'} {
		buf, err := a.Get(h)
		if err != nil {
			t.Fatalf("handle %v invalid after compaction: %v", h, err)
		}
		if !bytes.Equal(buf, bytes.Repeat([]byte{want}, 64)) {
			t.Fatalf("handle %v: data corrupted after compaction", h)
		}
	}
	// Objects were actually moved (offsets changed), yet handles worked.
	if off, _ := a.Offset(hc); off != 64 {
		t.Fatalf("hc offset after compaction = %d, want 64", off)
	}
	_ = he
}

// --- generation invalidation on slot reuse ---

func TestGenerationInvalidation(t *testing.T) {
	a := New(256, 1)
	h1, err := a.Alloc(32)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Free(h1); err != nil {
		t.Fatal(err)
	}
	h2, err := a.Alloc(32) // reuses the same slot (LIFO free-slot list)
	if err != nil {
		t.Fatal(err)
	}
	if h1.slotIdx() != h2.slotIdx() {
		t.Fatalf("expected slot reuse: %v vs %v", h1, h2)
	}
	if h1.gen() == h2.gen() {
		t.Fatalf("generation not bumped: %v vs %v", h1, h2)
	}
	if _, err := a.Get(h1); err != ErrStaleHandle {
		t.Fatalf("old handle = %v, want ErrStaleHandle", err)
	}
	if err := a.Free(h1); err != ErrStaleHandle {
		t.Fatalf("free via old handle = %v, want ErrStaleHandle", err)
	}
	if _, err := a.Get(h2); err != nil {
		t.Fatalf("new handle broken: %v", err)
	}
}

// --- failed allocation must not corrupt the free list ---

func TestFailedAllocPreservesFreeList(t *testing.T) {
	a := New(256, 1) // single page, no growth
	var hs []Handle
	for i := 0; i < 3; i++ { // 3*64 = 192 used, 64 free
		h, err := a.Alloc(64)
		if err != nil {
			t.Fatal(err)
		}
		hs = append(hs, h)
	}
	before := a.PageStats(0)
	if _, err := a.Alloc(128); err != ErrOutOfMemory {
		t.Fatalf("oversized alloc = %v, want ErrOutOfMemory", err)
	}
	after := a.PageStats(0)
	if before != after {
		t.Fatalf("free list changed by failed alloc: %+v -> %+v", before, after)
	}
	// Page is still fully functional afterwards.
	if err := a.Free(hs[1]); err != nil {
		t.Fatal(err)
	}
	// Two 64-byte holes, none big enough alone: compaction runs, the two
	// survivors slide to offsets 0 and 64, and the new object lands at 128.
	h, err := a.Alloc(128)
	if err != nil {
		t.Fatalf("alloc after failed alloc: %v", err)
	}
	if off, _ := a.Offset(h); off != 128 {
		t.Fatalf("offset = %d, want 128 (post-compaction tail)", off)
	}
}

// --- adjacent frees coalesce ---

func TestFreeCoalescing(t *testing.T) {
	a := New(256, 1)
	h1, _ := a.Alloc(64)
	h2, _ := a.Alloc(64)
	h3, _ := a.Alloc(64)
	a.Free(h2)
	if st := a.PageStats(0); st.FreeExtents != 2 {
		t.Fatalf("FreeExtents = %d, want 2 (hole + tail)", st.FreeExtents)
	}
	a.Free(h1) // adjacent to h2's hole -> merge
	if st := a.PageStats(0); st.FreeExtents != 2 || st.FreeBytes != 192 {
		t.Fatalf("stats = %+v, want 2 extents / 192 free bytes", st)
	}
	a.Free(h3) // merges everything back into one extent
	st := a.PageStats(0)
	if st.FreeExtents != 1 || st.FreeBytes != 256 {
		t.Fatalf("stats = %+v, want 1 extent / 256 free bytes", st)
	}
}

// --- randomized invariant: free extents + live bytes == page size,
//     extents sorted, non-overlapping, non-adjacent ---

func TestRandomizedInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	a := New(512, 1)
	var live []Handle
	for step := 0; step < 5000; step++ {
		if len(live) == 0 || rng.Intn(2) == 0 {
			h, err := a.Alloc(1 + rng.Intn(64))
			if err == ErrOutOfMemory {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			buf, _ := a.Get(h)
			for i := range buf {
				buf[i] = byte(step)
			}
			live = append(live, h)
		} else {
			i := rng.Intn(len(live))
			if err := a.Free(live[i]); err != nil {
				t.Fatal(err)
			}
			live = append(live[:i], live[i+1:]...)
		}
		p := a.pages[0]
		var freeSum uint32
		for i, e := range p.free {
			freeSum += e.size
			if i > 0 {
				prev := p.free[i-1]
				if prev.off+prev.size >= e.off {
					t.Fatalf("step %d: extents overlap/touch: %v then %v", step, prev, e)
				}
			}
		}
		if freeSum+p.liveBytes != uint32(a.PageSize()) {
			t.Fatalf("step %d: free(%d)+live(%d) != page(%d)", step, freeSum, p.liveBytes, a.PageSize())
		}
	}
}
