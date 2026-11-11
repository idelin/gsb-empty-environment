// Package addrspace provides a read-only view of an ELF64 process address
// space built from PT_LOAD program headers.
//
// The caller supplies the backing ELF file as an io.ReaderAt (for example
// *os.File or *bytes.Reader). Reads of virtual addresses are translated to
// file offsets; bytes beyond p_filesz but inside p_memsz (the BSS tail) read
// as zero. A virtual address not covered by a readable PT_LOAD segment, or a
// range crossing such a gap, fails with an error instead of producing
// fabricated data.
//
// Only PT_LOAD segments carrying PF_R participate: the view is strictly
// read-only and Space exposes no mutating methods.
//
// Memory-safety properties:
//   - ReadAt never allocates a buffer proportional to the requested length;
//     it reads directly into the caller's buffer and performs no internal
//     length-sized allocation.
//   - Address ranges that overflow the uint64 virtual address space fail with
//     ErrOverflow before any read is attempted.
//   - A short or faulty read from the underlying ReaderAt is surfaced as a
//     non-nil error together with the partial byte count; ReadAt never
//     reports a complete read it did not perform.
//
// A Space is safe for concurrent ReadAt calls to the extent that the
// underlying ReaderAt is.
package addrspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

// ELF segment permission flags (p_flags).
const (
	PF_X = 0x1
	PF_W = 0x2
	PF_R = 0x4
)

// Sentinel errors. Match with errors.Is/errors.As rather than message text.
var (
	// ErrOverflow means a virtual or file range cannot be represented in
	// uint64 (or int64 for the io.ReaderAt offset).
	ErrOverflow = errors.New("addrspace: address range overflow")
	// ErrUnmapped means a virtual address is not covered by any readable
	// PT_LOAD segment.
	ErrUnmapped = errors.New("addrspace: address not mapped by a readable segment")
	// ErrShortRead means the underlying ReaderAt returned fewer bytes than
	// requested without an error, violating the io.ReaderAt contract.
	ErrShortRead = errors.New("addrspace: short read from underlying reader")
)

// AmbiguityError reports the first virtual address at which two overlapping
// mappings disagree about their contents.
type AmbiguityError struct {
	// Addr is the lowest ambiguous virtual address.
	Addr uint64
}

func (e *AmbiguityError) Error() string {
	return fmt.Sprintf("addrspace: overlapping mappings disagree at vaddr 0x%x", e.Addr)
}

// Segment carries the PT_LOAD program-header fields needed by the address
// space. It is a subset of debug/elf.ProgHeader, so a caller parsing an ELF
// file can convert with a struct literal:
//
//	seg := addrspace.Segment{
//	    Off: prog.Off, Vaddr: prog.Vaddr, Filesz: prog.Filesz,
//	    Memsz: prog.Memsz, Flags: prog.Flags,
//	}
type Segment struct {
	Off    uint64 // p_offset: offset within the backing file
	Vaddr  uint64 // p_vaddr: first virtual address of the segment
	Filesz uint64 // p_filesz: bytes backed by the file
	Memsz  uint64 // p_memsz: bytes mapped in memory; the tail past Filesz is BSS
	Flags  uint32 // p_flags; a segment is mapped only when PF_R is set
}

type mapping struct {
	seg     Segment
	end     uint64 // exclusive virtual end: Vaddr + Memsz
	fileEnd uint64 // exclusive virtual end of file-backed part: Vaddr + Filesz
}

// Space is a read-only virtual address space backed by an io.ReaderAt.
type Space struct {
	r    io.ReaderAt
	maps []mapping // sorted by Vaddr; overlaps allowed only when consistent
}

// New builds a Space from readable PT_LOAD segments and the backing reader.
// Segments without PF_R and zero-length segments are ignored. Overlapping
// mappings are accepted while their contents agree; the first disagreement
// yields an *AmbiguityError. Truncated or unreadable backing files, overflow
// and malformed segments (Memsz < Filesz) are rejected up front.
func New(r io.ReaderAt, segs []Segment) (*Space, error) {
	if r == nil {
		return nil, errors.New("addrspace: nil ReaderAt")
	}

	maps := make([]mapping, 0, len(segs))
	for i, seg := range segs {
		if seg.Flags&PF_R == 0 || seg.Memsz == 0 {
			continue
		}
		if seg.Memsz < seg.Filesz {
			return nil, fmt.Errorf("addrspace: segment %d: p_memsz (%d) < p_filesz (%d)", i, seg.Memsz, seg.Filesz)
		}
		end, ok := checkedAdd(seg.Vaddr, seg.Memsz)
		if !ok {
			return nil, fmt.Errorf("addrspace: segment %d virtual range: %w", i, ErrOverflow)
		}
		if seg.Filesz > 0 {
			fileEnd, ok := checkedAdd(seg.Off, seg.Filesz)
			if !ok || fileEnd-1 > math.MaxInt64 {
				return nil, fmt.Errorf("addrspace: segment %d file range: %w", i, ErrOverflow)
			}
			// Fail fast on a truncated backing file by probing its last
			// backed byte. One bounded one-byte read per segment.
			var probe [1]byte
			if _, err := r.ReadAt(probe[:], int64(fileEnd-1)); err != nil {
				return nil, fmt.Errorf("addrspace: segment %d backing file unreadable at offset %d: %w", i, fileEnd-1, err)
			}
		}
		maps = append(maps, mapping{
			seg:     seg,
			end:     end,
			fileEnd: seg.Vaddr + seg.Filesz,
		})
	}

	sort.Slice(maps, func(i, j int) bool {
		if maps[i].seg.Vaddr != maps[j].seg.Vaddr {
			return maps[i].seg.Vaddr < maps[j].seg.Vaddr
		}
		return maps[i].seg.Off < maps[j].seg.Off
	})

	if err := checkOverlaps(r, maps); err != nil {
		return nil, err
	}
	return &Space{r: r, maps: maps}, nil
}

// ambiguityChunk bounds every comparison buffer used during construction;
// overlap checking never allocates proportionally to segment sizes.
const ambiguityChunk = 8 << 10

func checkOverlaps(r io.ReaderAt, maps []mapping) error {
	var firstAmbiguous uint64
	found := false
	bufA := make([]byte, ambiguityChunk)
	bufB := make([]byte, ambiguityChunk)

	for i := 0; i < len(maps); i++ {
		for j := i + 1; j < len(maps); j++ {
			lo := max(maps[i].seg.Vaddr, maps[j].seg.Vaddr)
			hi := min(maps[i].end, maps[j].end)
			if lo >= hi {
				continue
			}
			addr, consistent, err := firstMismatch(r, maps[i], maps[j], lo, hi, bufA, bufB)
			if err != nil {
				return fmt.Errorf("addrspace: overlap check of vaddr 0x%x: %w", lo, err)
			}
			if !consistent && (!found || addr < firstAmbiguous) {
				firstAmbiguous = addr
				found = true
			}
		}
	}
	if found {
		return &AmbiguityError{Addr: firstAmbiguous}
	}
	return nil
}

// firstMismatch returns the first virtual address in [lo, hi) where the two
// mappings produce different bytes. consistent is true when the whole range
// agrees.
func firstMismatch(r io.ReaderAt, a, b mapping, lo, hi uint64, bufA, bufB []byte) (addr uint64, consistent bool, err error) {
	for va := lo; va < hi; {
		n := min(uint64(ambiguityChunk), hi-va)
		if err := fillMapping(r, a, va, bufA[:n]); err != nil {
			return 0, false, err
		}
		if err := fillMapping(r, b, va, bufB[:n]); err != nil {
			return 0, false, err
		}
		if !bytes.Equal(bufA[:n], bufB[:n]) {
			for k := uint64(0); k < n; k++ {
				if bufA[k] != bufB[k] {
					return va + k, false, nil
				}
			}
		}
		va += n
	}
	return 0, true, nil
}

// fillMapping writes the mapped contents of m at virtual address va into buf.
// File-backed bytes come from the reader; BSS bytes are zeroed.
func fillMapping(r io.ReaderAt, m mapping, va uint64, buf []byte) error {
	// Start from a known-zero buffer so BSS bytes (and any bytes a faulty
	// reader does not overwrite) can never inherit stale contents.
	clear(buf)
	if va >= m.fileEnd {
		return nil
	}
	fileAvail := m.fileEnd - va
	if uint64(len(buf)) <= fileAvail {
		off := m.seg.Off + (va - m.seg.Vaddr)
		n, err := r.ReadAt(buf, int64(off))
		if err != nil {
			return err
		}
		if n != len(buf) {
			return fmt.Errorf("%w at file offset %d: got %d of %d bytes", ErrShortRead, off, n, len(buf))
		}
		return nil
	}
	head := buf[:fileAvail]
	off := m.seg.Off + (va - m.seg.Vaddr)
	n, err := r.ReadAt(head, int64(off))
	if err != nil {
		return err
	}
	if uint64(n) != fileAvail {
		return fmt.Errorf("%w at file offset %d: got %d of %d bytes", ErrShortRead, off, n, fileAvail)
	}
	// The remainder of buf lies in the mapping's BSS tail.
	clear(buf[fileAvail:])
	return nil
}

// ReadAt reads len(p) bytes from virtual address addr into p. It follows
// io.ReaderAt semantics over the virtual address space: a returned error is
// always non-nil when n < len(p), so callers can never mistake a partial or
// zero-filled result for a complete one. BSS bytes read as zero; reads may
// span adjacent PT_LOAD segments.
func (s *Space) ReadAt(addr uint64, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if _, ok := checkedAdd(addr, uint64(len(p))); !ok {
		return 0, fmt.Errorf("%w: vaddr 0x%x plus %d bytes", ErrOverflow, addr, len(p))
	}

	idx, ok := s.locate(addr)
	if !ok {
		return 0, fmt.Errorf("%w: vaddr 0x%x", ErrUnmapped, addr)
	}

	n := 0
	for n < len(p) {
		m := s.maps[idx]
		cur := addr + uint64(n)
		avail := m.end - cur
		want := uint64(len(p) - n)
		c := min(want, avail)
		dst := p[n : n+int(c)]

		if cur >= m.fileEnd {
			// Entirely inside BSS.
			clear(dst)
		} else {
			fileAvail := m.fileEnd - cur
			if c <= fileAvail {
				k, err := s.r.ReadAt(dst, int64(m.seg.Off+(cur-m.seg.Vaddr)))
				if err != nil {
					return n + k, fmt.Errorf("addrspace: file read for vaddr 0x%x: %w", cur, err)
				}
				if uint64(k) != c {
					return n + k, fmt.Errorf("%w: vaddr 0x%x got %d of %d bytes", ErrShortRead, cur, k, c)
				}
			} else {
				head := dst[:fileAvail]
				k, err := s.r.ReadAt(head, int64(m.seg.Off+(cur-m.seg.Vaddr)))
				if err != nil {
					return n + k, fmt.Errorf("addrspace: file read for vaddr 0x%x: %w", cur, err)
				}
				if uint64(k) != fileAvail {
					return n + k, fmt.Errorf("%w: vaddr 0x%x got %d of %d file bytes", ErrShortRead, cur, k, fileAvail)
				}
				clear(dst[fileAvail:])
			}
		}

		n += int(c)
		if n == len(p) {
			return n, nil
		}
		// Re-locate at the boundary so content-consistent overlaps and
		// nested segments are handled uniformly. A gap stops the read.
		next := addr + uint64(n)
		idx, ok = s.locate(next)
		if !ok {
			return n, fmt.Errorf("%w: vaddr 0x%x", ErrUnmapped, next)
		}
	}
	return n, nil
}

// locate returns the first mapping covering addr via binary search.
func (s *Space) locate(addr uint64) (int, bool) {
	idx := sort.Search(len(s.maps), func(i int) bool { return addr < s.maps[i].end })
	if idx == len(s.maps) || addr < s.maps[idx].seg.Vaddr {
		return 0, false
	}
	return idx, true
}

// Contains reports whether addr lies inside a readable mapping.
func (s *Space) Contains(addr uint64) bool {
	_, ok := s.locate(addr)
	return ok
}

// Segments returns a copy of the readable PT_LOAD segments in ascending
// virtual-address order.
func (s *Space) Segments() []Segment {
	out := make([]Segment, len(s.maps))
	for i, m := range s.maps {
		out[i] = m.seg
	}
	return out
}

func checkedAdd(a, b uint64) (uint64, bool) {
	sum := a + b
	if sum < a {
		return 0, false
	}
	return sum, true
}
