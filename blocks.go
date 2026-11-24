package nvmerecov

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
)

// Block is one logical block. Empty is the distinguished "never written"
// marker used by NVMe zone reads. It is intentionally distinct from a written
// block whose Value is the empty byte string.
type Block struct {
	Empty bool
	Value []byte
}

// DataBlock returns a written block value. The byte slice is copied.
func DataBlock(value []byte) Block {
	return Block{Value: cloneBytes(value)}
}

// EmptyBlock returns the distinguished empty (never written) marker.
func EmptyBlock() Block {
	return Block{Empty: true}
}

// Equal compares block contents by real bytes, not by a hash.
func (b Block) Equal(other Block) bool {
	if b.Empty || other.Empty {
		return b.Empty == other.Empty
	}
	return bytes.Equal(b.Value, other.Value)
}

func (b Block) String() string {
	if b.Empty {
		return "<empty>"
	}
	return strconv.Quote(string(b.Value))
}

// Run is a run-length description of consecutive identical blocks. Count may
// be larger than any 32-bit integer and is never expanded by this package.
type Run struct {
	Empty bool
	Value []byte
	Count uint64
}

// Blocks is a run-length encoded block string.
//
// Adjacent runs with the same content are observationally equal even if they
// have different boundaries. Normalize canonicalizes those boundaries.
type Blocks []Run

// Repeat returns count copies of value. A count of zero returns an empty string.
func Repeat(value []byte, count uint64) Blocks {
	if count == 0 {
		return nil
	}
	return Blocks{{Value: cloneBytes(value), Count: count}}
}

// EmptyBlocks returns count copies of the distinct empty marker.
func EmptyBlocks(count uint64) Blocks {
	if count == 0 {
		return nil
	}
	return Blocks{{Empty: true, Count: count}}
}

// Join concatenates block strings. It does not expand long runs.
func Join(parts ...Blocks) Blocks {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	out := make(Blocks, 0, total)
	for _, part := range parts {
		for _, run := range part {
			if run.Count != 0 {
				out = append(out, Run{
					Empty: run.Empty,
					Value: cloneBytes(run.Value),
					Count: run.Count,
				})
			}
		}
	}
	return Normalize(out)
}

// Normalize removes zero-length runs, canonicalizes empty runs, and merges
// adjacent runs with equal contents. It copies block values.
func Normalize(blocks Blocks) Blocks {
	out := make(Blocks, 0, len(blocks))
	for _, run := range blocks {
		if run.Count == 0 {
			continue
		}
		next := Run{Count: run.Count}
		if run.Empty {
			next.Empty = true
		} else {
			next.Value = cloneBytes(run.Value)
		}
		if len(out) != 0 {
			last := &out[len(out)-1]
			if last.Empty == next.Empty {
				if last.Empty || bytes.Equal(last.Value, next.Value) {
					if math.MaxUint64-last.Count >= next.Count {
						last.Count += next.Count
						continue
					}
				}
			}
		}
		out = append(out, next)
	}
	return out
}

// Length returns the number of blocks described by b. The boolean result is
// false if the run counts overflow uint64.
func (b Blocks) Length() (uint64, bool) {
	var total uint64
	for _, run := range b {
		if run.Count > math.MaxUint64-total {
			return 0, false
		}
		total += run.Count
	}
	return total, true
}

// MustLength is Length for inputs whose counts are known not to overflow.
func (b Blocks) MustLength() uint64 {
	n, ok := b.Length()
	if !ok {
		panic("nvmerecov: block run count overflow")
	}
	return n
}

// Equal compares exact block content. Run boundaries and hashes are irrelevant.
func (b Blocks) Equal(other Blocks) bool {
	i, j := 0, 0
	var leftA, leftB uint64
	for {
		for i < len(b) && leftA == 0 {
			leftA = b[i].Count
			if leftA == 0 {
				i++
			}
		}
		for j < len(other) && leftB == 0 {
			leftB = other[j].Count
			if leftB == 0 {
				j++
			}
		}
		if i >= len(b) || j >= len(other) {
			return i >= len(b) && j >= len(other) && leftA == 0 && leftB == 0
		}
		if b[i].Empty != other[j].Empty {
			return false
		}
		if !b[i].Empty && !bytes.Equal(b[i].Value, other[j].Value) {
			return false
		}
		take := leftA
		if leftB < take {
			take = leftB
		}
		leftA -= take
		leftB -= take
		if leftA == 0 {
			i++
		}
		if leftB == 0 {
			j++
		}
	}
}

// Slice returns length blocks beginning at block offset start. It operates on
// compressed runs and is safe for counts much larger than 32 bits.
func (b Blocks) Slice(start, length uint64) (Blocks, bool) {
	if start > math.MaxUint64-length {
		return nil, false
	}
	if length == 0 {
		return nil, true
	}
	end := start + length
	var pos uint64
	var produced uint64
	out := make(Blocks, 0, len(b)+1)
	for _, run := range b {
		runEnd := pos + run.Count
		if runEnd <= start {
			pos = runEnd
			continue
		}
		if pos >= end {
			break
		}
		lo := uint64(0)
		if start > pos {
			lo = start - pos
		}
		hi := run.Count
		if end-pos < hi {
			hi = end - pos
		}
		if hi > lo {
			part := Run{Count: hi - lo}
			if run.Empty {
				part.Empty = true
			} else {
				part.Value = run.Value
			}
			out = append(out, part)
			produced += hi - lo
		}
		pos = runEnd
		if pos >= end {
			break
		}
	}
	if produced != length {
		return nil, false
	}
	return Normalize(out), true
}

// At returns the block at index. The boolean result is false when index is
// outside the described string.
func (b Blocks) At(index uint64) (Block, bool) {
	var pos uint64
	for _, run := range b {
		if index < pos+run.Count {
			if run.Empty {
				return EmptyBlock(), true
			}
			return Block{Value: cloneBytes(run.Value)}, true
		}
		pos += run.Count
	}
	return Block{}, false
}

func (b Blocks) String() string {
	n, ok := b.Length()
	if !ok {
		return "<overflowing block string>"
	}
	return fmt.Sprintf("<blocks:%d,runs:%d>", n, len(b))
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func concatBlocks(a, b Blocks) Blocks {
	if len(a) == 0 {
		return append(Blocks(nil), b...)
	}
	if len(b) == 0 {
		return append(Blocks(nil), a...)
	}
	out := make(Blocks, 0, len(a)+len(b))
	out = append(out, a...)
	last := &out[len(out)-1]
	first := b[0]
	if last.Empty == first.Empty && (last.Empty || bytes.Equal(last.Value, first.Value)) {
		if math.MaxUint64-last.Count >= first.Count {
			last.Count += first.Count
			out = append(out, b[1:]...)
			return out
		}
	}
	out = append(out, b...)
	return out
}
