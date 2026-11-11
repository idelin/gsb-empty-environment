package addrspace

import (
	"errors"
	"fmt"
	"sync"
)

// errFaultyReader is injected by SparseFile to simulate failing backing I/O.
var errFaultyReader = errors.New("addrspace test: injected read failure")

const sparseBlockBits = 12
const sparseBlockSize = 1 << sparseBlockBits

// SparseFile is an in-memory sparse-file stand-in for io.ReaderAt.
//
// It emulates a huge sparse file without allocating its logical size: holes
// (never-written 4 KiB blocks) read as zeros and never allocate backing
// storage, while written blocks live in a map. This makes it possible to
// drive the address space against terabyte-sized virtual spans in tests and
// assert that the implementation never materializes them.
//
// The knobs simulate the hostile conditions a symbolization service may hit:
//   - MaxBytes caps how many bytes a single ReadAt may return (short reads).
//   - ShortNil reports the cap as (n, nil) instead of (n, errUnexpectedEOF),
//     matching a misbehaving ReaderAt that hides its shortness.
//   - FaultAt makes reads touching a file offset fail with errFaultyReader.
type SparseFile struct {
	mu       sync.RWMutex
	blocks   map[uint64][sparseBlockSize]byte
	Size     uint64
	Reads    int
	BytesGot uint64

	MaxBytes int   // 0 means unlimited
	ShortNil bool  // report capped reads with nil error
	FaultAt  int64 // >= 0: ReadAt fails for offsets in [FaultAt, FaultEnd)
	FaultEnd int64 // 0 means the fault range extends to the end of the file
}

// NewSparseFile creates a sparse stand-in of the given logical size.
func NewSparseFile(size uint64) *SparseFile {
	return &SparseFile{blocks: make(map[uint64][sparseBlockSize]byte), Size: size, FaultAt: -1}
}

// FaultRange injects failures for file offsets in [at, end).
func (f *SparseFile) FaultRange(at, end int64) {
	f.FaultAt = at
	f.FaultEnd = end
}

func (f *SparseFile) faulted(pos uint64) bool {
	if f.FaultAt < 0 || int64(pos) < f.FaultAt {
		return false
	}
	return f.FaultEnd == 0 || int64(pos) < f.FaultEnd
}

// WriteByte stores one byte at off, allocating exactly one 4 KiB block.
func (f *SparseFile) WriteByteAt(b byte, off uint64) {
	if off >= f.Size {
		panic(fmt.Sprintf("sparse test file: write offset %d beyond size %d", off, f.Size))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	blk := f.blocks[off>>sparseBlockBits]
	blk[off&(sparseBlockSize-1)] = b
	f.blocks[off>>sparseBlockBits] = blk
}

// AllocatedBlocks reports how many 4 KiB blocks are physically backed.
func (f *SparseFile) AllocatedBlocks() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.blocks)
}

// ReadAt implements io.ReaderAt over the sparse representation.
func (f *SparseFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if off < 0 {
		return 0, fmt.Errorf("sparse test file: negative offset %d", off)
	}
	uo := uint64(off)
	if uo >= f.Size {
		return 0, fmt.Errorf("sparse test file: offset %d beyond size %d: %w", off, f.Size, errFaultyReader)
	}
	f.Reads++

	limit := len(p)
	if f.MaxBytes > 0 && f.MaxBytes < limit {
		limit = f.MaxBytes
	}

	n := 0
	for n < limit {
		pos := uo + uint64(n)
		if f.faulted(pos) {
			f.BytesGot += uint64(n)
			return n, errFaultyReader
		}
		blkIdx := pos >> sparseBlockBits
		inBlk := pos & (sparseBlockSize - 1)
		take := sparseBlockSize - int(inBlk)
		if rem := int(f.Size - pos); take > rem {
			take = rem
		}
		if rem := limit - n; take > rem {
			take = rem
		}
		// Trim the chunk so it never includes a faulted byte.
		if f.FaultAt >= 0 && uint64(f.FaultAt) > pos && uint64(f.FaultAt) < pos+uint64(take) {
			take = int(uint64(f.FaultAt) - pos)
		}
		if take > 0 {
			if blk, ok := f.blocks[blkIdx]; ok {
				copy(p[n:n+take], blk[inBlk:inBlk+uint64(take)])
			} else {
				// Hole: serve zeros without allocating a block or a scratch slice.
				for k := range p[n : n+take] {
					p[n+k] = 0
				}
			}
			n += take
		}
		if f.faulted(uo + uint64(n)) {
			f.BytesGot += uint64(n)
			return n, errFaultyReader
		}
		if n >= limit || uo+uint64(n) >= f.Size {
			break
		}
	}

	f.BytesGot += uint64(n)
	if n < len(p) {
		if f.ShortNil {
			return n, nil
		}
		return n, errors.New("sparse test file: short read")
	}
	return n, nil
}
