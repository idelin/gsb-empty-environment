package addrspace

import (
	"errors"
	"reflect"
	"testing"
)

// seg builds a readable PT_LOAD segment.
func seg(off, vaddr, filesz, memsz uint64, flags uint32) Segment {
	if flags == 0 {
		flags = PF_R
	}
	return Segment{Off: off, Vaddr: vaddr, Filesz: filesz, Memsz: memsz, Flags: flags}
}

type readerAt interface {
	ReadAt(p []byte, off int64) (int, error)
}

func mustNew(t *testing.T, r readerAt, segs []Segment) *Space {
	t.Helper()
	s, err := New(r, segs)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return s
}

func readOK(t *testing.T, s *Space, addr uint64, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	if n, err := s.ReadAt(addr, got); err != nil || n != len(want) {
		t.Fatalf("ReadAt(0x%x, %d) = (%d, %v), want (%d, nil)", addr, len(want), n, err, len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadAt(0x%x) = %v, want %v", addr, got, want)
	}
}

func TestReadFileAndBSS(t *testing.T) {
	f := NewSparseFile(0x1000)
	f.WriteByteAt(0xAA, 0x100)
	f.WriteByteAt(0xBB, 0x1FF)
	s := mustNew(t, f, []Segment{
		seg(0x100, 0x400000, 0x100, 0x180, PF_R),
	})

	readOK(t, s, 0x400000, []byte{0xAA})
	readOK(t, s, 0x4000FF, []byte{0xBB})
	// BSS tail: p_filesz .. p_memsz reads as zero.
	readOK(t, s, 0x400100, make([]byte, 0x80))
	// Read straddling the file/BSS boundary.
	readOK(t, s, 0x4000FF, []byte{0xBB, 0, 0})
	if !s.Contains(0x40017F) || s.Contains(0x400180) || s.Contains(0x3FFFFF) {
		t.Fatal("Contains boundaries wrong")
	}

	// Reading BSS never touches the backing reader nor allocates blocks.
	readsBefore := f.Reads
	got := make([]byte, 0x80)
	if n, err := s.ReadAt(0x400100, got); err != nil || n != len(got) {
		t.Fatalf("BSS read = (%d, %v)", n, err)
	}
	if f.Reads != readsBefore {
		t.Fatalf("BSS read issued %d backing reads, want 0", f.Reads-readsBefore)
	}
}

func TestCrossPTLoadBoundary(t *testing.T) {
	f := NewSparseFile(0x2000)
	f.WriteByteAt(0x11, 0x0)
	f.WriteByteAt(0x22, 0x1000)
	// Adjacent mappings, including a BSS tail on the first one.
	s := mustNew(t, f, []Segment{
		seg(0x0, 0x1000, 0x8, 0x10, PF_R),
		seg(0x1000, 0x1010, 0x8, 0x8, PF_R),
	})

	// One read crosses file bytes, BSS zeros and the next segment.
	readOK(t, s, 0x1000, []byte{0x11, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x22})

	// A gap between mappings stops the read and reports the first gap address.
	gapSpace := mustNew(t, f, []Segment{
		seg(0x0, 0x2000, 0x8, 0x8, PF_R),
		seg(0x1000, 0x3000, 0x8, 0x8, PF_R),
	})
	buf := make([]byte, 0x1010)
	n, err := gapSpace.ReadAt(0x2000, buf)
	if n != 8 || !errors.Is(err, ErrUnmapped) {
		t.Fatalf("gap read = (%d, %v), want (8, ErrUnmapped)", n, err)
	}
}

func TestUnreadableSegmentIgnored(t *testing.T) {
	f := NewSparseFile(0x100)
	f.WriteByteAt(0xCC, 0)
	s := mustNew(t, f, []Segment{
		seg(0x0, 0x5000, 0x10, 0x10, PF_W),      // write-only: invisible
		seg(0x0, 0x6000, 0x10, 0x10, PF_W|PF_R), // readable
	})
	if s.Contains(0x5000) {
		t.Fatal("PF_W-only segment must not be mapped")
	}
	readOK(t, s, 0x6000, []byte{0xCC})

	if _, err := s.ReadAt(0x5000, make([]byte, 1)); !errors.Is(err, ErrUnmapped) {
		t.Fatalf("unmapped read err = %v, want ErrUnmapped", err)
	}
}

func TestOverlapConsistentAndAmbiguous(t *testing.T) {
	// Two mappings share the same file bytes: overlap is consistent.
	f := NewSparseFile(0x1000)
	f.WriteByteAt(0x77, 0x20)
	s := mustNew(t, f, []Segment{
		seg(0x0, 0x10000, 0x80, 0x80, PF_R),
		seg(0x20, 0x10020, 0x80, 0x80, PF_R), // same file bytes remapped over 0x10020..
	})
	readOK(t, s, 0x10020, []byte{0x77})

	// Same virtual range, different backing content: first disagreeing vaddr.
	conflict := NewSparseFile(0x1000)
	conflict.WriteByteAt(0x01, 0x0)
	conflict.WriteByteAt(0x02, 0x1)
	_, err := New(conflict, []Segment{
		seg(0x0, 0x20000, 0x10, 0x10, PF_R),
		seg(0x1, 0x20000, 0x10, 0x10, PF_R),
	})
	var amb *AmbiguityError
	if !errors.As(err, &amb) {
		t.Fatalf("want *AmbiguityError, got %v", err)
	}
	if amb.Addr != 0x20000 {
		t.Fatalf("ambiguous addr = 0x%x, want 0x20000", amb.Addr)
	}

	// BSS vs file byte disagreement: the file's non-zero byte conflicts
	// with the other mapping's zero BSS byte at that virtual address.
	bssConflict := NewSparseFile(0x1000)
	bssConflict.WriteByteAt(0x5A, 0x18)
	bssConflict.WriteByteAt(0xA5, 0x118)
	_, err = New(bssConflict, []Segment{
		seg(0x100, 0x30000, 0x18, 0x40, PF_R), // BSS zero starts at vaddr 0x30018
		seg(0x0, 0x30000, 0x40, 0x40, PF_R),   // fully file-backed; 0x5A lands at vaddr 0x30018
	})
	var bssAmb *AmbiguityError
	if !errors.As(err, &bssAmb) || bssAmb.Addr != 0x30018 {
		got := uint64(0)
		if bssAmb != nil {
			got = bssAmb.Addr
		}
		t.Fatalf("BSS overlap ambiguity = %v (addr 0x%x), want 0x30018", err, got)
	}

	// Multiple overlaps must report the lowest ambiguous address.
	multi := NewSparseFile(0x1000)
	multi.WriteByteAt(0x99, 0x0)
	multi.WriteByteAt(0x66, 0x100)
	_, err = New(multi, []Segment{
		seg(0x0, 0x40000, 0x20, 0x20, PF_R),
		seg(0x100, 0x40000, 0x20, 0x20, PF_R), // conflict at 0x40000
		seg(0x0, 0x50000, 0x20, 0x20, PF_R),
		seg(0x100, 0x50000, 0x20, 0x20, PF_R), // conflict at 0x50000
	})
	var multiAmb *AmbiguityError
	if !errors.As(err, &multiAmb) || multiAmb.Addr != 0x40000 {
		t.Fatalf("multi overlap = %v, want ambiguity at 0x40000", err)
	}
}

func TestOverflowNoGiantAllocation(t *testing.T) {
	f := NewSparseFile(0x100)
	s := mustNew(t, f, []Segment{seg(0, 0x1000, 0x10, 0x10, PF_R)})

	// Requested range cannot fit in uint64: fail before any allocation or
	// read, regardless of how enormous the logical length is.
	buf := make([]byte, 16)
	readsBefore := f.Reads
	var runs int
	allocs := testing.AllocsPerRun(100, func() {
		runs++
		n, err := s.ReadAt(^uint64(0)-8, buf)
		if n != 0 || !errors.Is(err, ErrOverflow) {
			t.Fatalf("overflow read = (%d, %v), want (0, ErrOverflow)", n, err)
		}
	})
	// The error path is allowed a small constant number of allocations for
	// wrapping; the point is it must not scale with the (huge) request.
	if allocs > 4 {
		t.Fatalf("overflow path allocated %v times per run, want a small constant", allocs)
	}
	if f.Reads != readsBefore {
		t.Fatalf("overflow path issued %d backing reads, want 0", f.Reads-readsBefore)
	}
	if runs != 101 { // AllocsPerRun performs one warm-up run plus 100 measured runs
		t.Fatalf("AllocsPerRun ran %d times, want 101", runs)
	}

	// A range starting at the top of the address space must fail too.
	if _, err := s.ReadAt(^uint64(0), buf[:1]); !errors.Is(err, ErrOverflow) {
		t.Fatalf("max-addr read err = %v, want ErrOverflow", err)
	}

	// Segments whose own virtual range overflows are rejected at New.
	if _, err := New(f, []Segment{seg(0, ^uint64(0)-3, 2, 5, PF_R)}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflowing segment New err = %v, want ErrOverflow", err)
	}
	// File offsets that do not fit int64 are rejected.
	if _, err := New(f, []Segment{seg(^uint64(0)-1, 0x7000, 2, 2, PF_R)}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflowing file offset New err = %v, want ErrOverflow", err)
	}
	// Malformed segment.
	if _, err := New(f, []Segment{seg(0, 0x8000, 0x10, 0x8, PF_R)}); err == nil {
		t.Fatal("New with Filesz > Memsz must fail")
	}
}

func TestGiantSparseSpanNoMaterialization(t *testing.T) {
	// One terabyte of BSS backed by a tiny sparse stand-in.
	const terabyte = uint64(1) << 40
	f := NewSparseFile(4096)
	f.WriteByteAt(0xDE, 0)
	s := mustNew(t, f, []Segment{seg(0, 0x8000, 4096, terabyte, PF_R)})

	// Blocks stay at one; reading far into the BSS allocates nothing.
	buf := make([]byte, 4096)
	allocs := testing.AllocsPerRun(50, func() {
		far := uint64(0x8000) + terabyte - 4096
		if n, err := s.ReadAt(far, buf); err != nil || n != len(buf) {
			t.Fatalf("far BSS read = (%d, %v)", n, err)
		}
	})
	if allocs != 0 {
		t.Fatalf("far BSS read allocated %v times, want 0", allocs)
	}
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("BSS byte %d = 0x%x, want zero", i, b)
		}
	}
	if f.AllocatedBlocks() != 1 {
		t.Fatalf("sparse file grew to %d blocks, want 1", f.AllocatedBlocks())
	}

	// Happy-path file reads also allocate nothing inside ReadAt.
	allocs = testing.AllocsPerRun(50, func() {
		if n, err := s.ReadAt(0x8000, buf[:1]); err != nil || n != 1 || buf[0] != 0xDE {
			t.Fatalf("file read = (%d, %v, 0x%x)", n, err, buf[0])
		}
	})
	if allocs != 0 {
		t.Fatalf("file read allocated %v times, want 0", allocs)
	}
}

func TestShortAndFaultyReads(t *testing.T) {
	// Truncated backing file is rejected at construction, never faked.
	truncated := NewSparseFile(0x8)
	if _, err := New(truncated, []Segment{seg(0, 0x1000, 0x100, 0x100, PF_R)}); err == nil {
		t.Fatal("New with truncated file must fail")
	}

	f := NewSparseFile(0x2000)
	for i := uint64(0); i < 0x2000; i++ {
		f.WriteByteAt(byte(i), i)
	}
	s := mustNew(t, f, []Segment{seg(0, 0x1000, 0x1000, 0x1000, PF_R)})

	// ReaderAt that silently truncates every read (n, nil): the Space must
	// surface ErrShortRead rather than return a full n with nil error.
	f.MaxBytes = 3
	f.ShortNil = true
	buf := make([]byte, 16)
	n, err := s.ReadAt(0x1000, buf)
	if !errors.Is(err, ErrShortRead) {
		t.Fatalf("silent short read err = %v, want ErrShortRead", err)
	}
	if n != 3 {
		t.Fatalf("silent short read n = %d, want partial 3, never a fabricated 16", n)
	}

	// Injected I/O failure mid-read on a fresh stand-in: partial bytes plus
	// the original error, never a fabricated complete result.
	faulty := NewSparseFile(0x4000)
	// Fault only inside the read window; the construction probe at 0x2FFF is
	// outside the fault range and stays healthy.
	faulty.FaultRange(0x2008, 0x2010)
	faultySpace := mustNew(t, faulty, []Segment{seg(0x2000, 0x1000, 0x1000, 0x1000, PF_R)})
	n, err = faultySpace.ReadAt(0x1000, make([]byte, 16))
	if !errors.Is(err, errFaultyReader) {
		t.Fatalf("faulty read err = %v, want errFaultyReader", err)
	}
	if n != 8 {
		t.Fatalf("faulty read n = %d, want partial 8", n)
	}
}

func TestReadOnlyAPI(t *testing.T) {
	typ := reflect.TypeOf(&Space{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		switch name {
		case "ReadAt", "Contains", "Segments":
		default:
			t.Errorf("unexpected exported method %q", name)
		}
	}
}

func TestConcurrentReadAt(t *testing.T) {
	f := NewSparseFile(0x1000)
	for i := uint64(0); i < 0x1000; i++ {
		f.WriteByteAt(byte(i), i)
	}
	s := mustNew(t, f, []Segment{seg(0, 0x1000, 0x800, 0x1000, PF_R)})
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func(base uint64) {
			defer func() { done <- struct{}{} }()
			buf := make([]byte, 64)
			for k := 0; k < 200; k++ {
				addr := 0x1000 + int((base+uint64(k))%0xF80)
				if _, err := s.ReadAt(uint64(addr), buf); err != nil {
					t.Errorf("concurrent ReadAt(0x%x): %v", addr, err)
					return
				}
			}
		}(uint64(g * 0x100))
	}
	for g := 0; g < 8; g++ {
		<-done
	}
}
