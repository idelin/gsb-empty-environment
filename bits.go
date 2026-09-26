// Package rlebits implements a mutable bit sequence compressed with
// run-length encoding (RLE), supporting interval flip, insert, delete,
// and rank/select queries.
//
// In-memory layout:
//
//	first  uint8    value (0/1) of the first run; meaningless when empty
//	lens   []uint32 run lengths, every entry > 0, values alternate implicitly
//	preTot []uint64 length index: preTot[i] = sum(lens[:i]), len = runs+1
//	preOne []uint64 length index: preOne[i] = # of 1-bits in lens[:i]
//
// Serialized layout (Encode):
//
//	byte 0        : value of first run (0 or 1)
//	uvarint       : number of runs n
//	uvarint x n   : run lengths
//
// Adjacent runs always hold opposite values; every structural mutation
// re-merges same-value neighbors so no zero-length or duplicate-value
// adjacent runs ever persist.
package rlebits

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// Bits is an RLE-compressed mutable bit sequence.
type Bits struct {
	first  uint8
	lens   []uint32
	preTot []uint64
	preOne []uint64
}

// New builds a Bits from a string of '0'/'1' characters.
func New(s string) *Bits {
	b := &Bits{}
	for i := 0; i < len(s); {
		if s[i] != '0' && s[i] != '1' {
			panic(fmt.Sprintf("rlebits: invalid bit char %q", s[i]))
		}
		j := i
		for j < len(s) && s[j] == s[i] {
			j++
		}
		b.appendRun(s[i]-'0', uint32(j-i))
		i = j
	}
	b.reindex()
	return b
}

func (b *Bits) appendRun(v uint8, n uint32) {
	if n == 0 {
		return
	}
	if len(b.lens) == 0 {
		b.first = v
		b.lens = append(b.lens, n)
		return
	}
	if b.runValue(len(b.lens)-1) == v {
		b.lens[len(b.lens)-1] += n
	} else {
		b.lens = append(b.lens, n)
	}
}

// runValue returns the bit value of run i (runs alternate from first).
// Only valid when the alternation invariant holds (outside mutations).
func (b *Bits) runValue(i int) uint8 {
	return b.first ^ (uint8(i) & 1)
}

// reindex rebuilds the length index (prefix sums) from lens.
func (b *Bits) reindex() {
	n := len(b.lens)
	if cap(b.preTot) < n+1 {
		b.preTot = make([]uint64, n+1)
		b.preOne = make([]uint64, n+1)
	} else {
		b.preTot = b.preTot[:n+1]
		b.preOne = b.preOne[:n+1]
	}
	for i := 0; i < n; i++ {
		b.preTot[i+1] = b.preTot[i] + uint64(b.lens[i])
		b.preOne[i+1] = b.preOne[i]
		if b.runValue(i) == 1 {
			b.preOne[i+1] += uint64(b.lens[i])
		}
	}
}

// Len returns the total number of bits.
func (b *Bits) Len() uint64 {
	if len(b.lens) == 0 {
		return 0
	}
	return b.preTot[len(b.lens)]
}

// Runs returns the number of runs.
func (b *Bits) Runs() int { return len(b.lens) }

// locate returns the run i containing position pos and the offset within it.
// pos must be < Len().
func (b *Bits) locate(pos uint64) (int, uint64) {
	i := sort.Search(len(b.lens), func(j int) bool { return b.preTot[j+1] > pos })
	return i, pos - b.preTot[i]
}

// At returns the bit at position pos.
func (b *Bits) At(pos uint64) uint8 {
	i, _ := b.locate(pos)
	return b.runValue(i)
}

// ensureBoundary makes pos a run boundary (splitting a run if needed) and
// returns the number of runs starting before pos. pos must be in (0, Len()).
// Note: a fresh split creates two adjacent runs of equal value, so the
// alternation invariant is broken until the caller finishes the mutation.
func (b *Bits) ensureBoundary(pos uint64) int {
	i, off := b.locate(pos)
	if off == 0 {
		return i
	}
	left, right := uint32(off), b.lens[i]-uint32(off)
	b.lens[i] = left
	b.lens = append(b.lens, 0)
	copy(b.lens[i+2:], b.lens[i+1:])
	b.lens[i+1] = right
	b.reindex()
	return i + 1
}

// toggleBoundary flips whether pos is a run boundary: splitting the run
// there, or merging the two adjacent runs if a boundary already exists.
// pos must be in (0, Len()).
func (b *Bits) toggleBoundary(pos uint64) {
	i, off := b.locate(pos)
	if off == 0 {
		// Boundary exists: merge runs i-1 and i (same value after a flip).
		b.lens[i-1] += b.lens[i]
		b.lens = append(b.lens[:i], b.lens[i+1:]...)
	} else {
		left, right := uint32(off), b.lens[i]-uint32(off)
		b.lens[i] = left
		b.lens = append(b.lens, 0)
		copy(b.lens[i+2:], b.lens[i+1:])
		b.lens[i+1] = right
	}
	b.reindex()
}

// Flip inverts every bit in the half-open interval [l, r).
// Flipping a range is equivalent to toggling its two endpoints in the
// run-boundary set, which automatically merges same-value neighbors.
func (b *Bits) Flip(l, r uint64) {
	n := b.Len()
	if l >= r || r > n {
		return
	}
	if l == 0 {
		b.first ^= 1
	} else {
		b.toggleBoundary(l)
	}
	if r < n {
		b.toggleBoundary(r)
	}
	b.reindex()
}

// Insert inserts count bits of value val starting at position pos
// (0 <= pos <= Len()). Inserting at a run boundary extends the matching
// neighbor instead of creating a redundant run.
func (b *Bits) Insert(pos uint64, val uint8, count uint32) {
	if count == 0 {
		return
	}
	n := b.Len()
	if pos > n {
		panic("rlebits: insert position out of range")
	}
	if len(b.lens) == 0 {
		b.first = val
		b.lens = []uint32{count}
		b.reindex()
		return
	}
	switch {
	case pos == n:
		if b.runValue(len(b.lens)-1) == val {
			b.lens[len(b.lens)-1] += count
		} else {
			b.lens = append(b.lens, count)
		}
	case pos == 0:
		if b.first == val {
			b.lens[0] += count
		} else {
			b.lens = append([]uint32{count}, b.lens...)
			b.first = val
		}
	default:
		// Capture real neighbor values before ensureBoundary splits a run,
		// because a fresh split temporarily breaks value alternation.
		leftVal := b.At(pos - 1)
		rightVal := b.At(pos)
		k := b.ensureBoundary(pos)
		// Neighbors now straddle pos: run k-1 (value leftVal), run k (rightVal).
		switch {
		case leftVal == rightVal && leftVal == val:
			// pos was inside a run of val: extend it and heal the split.
			b.lens[k-1] += count + b.lens[k]
			b.lens = append(b.lens[:k], b.lens[k+1:]...)
		case leftVal == rightVal:
			// pos was inside a run of the opposite value: splice a new run.
			b.lens = append(b.lens, 0)
			copy(b.lens[k+1:], b.lens[k:])
			b.lens[k] = count
		case leftVal == val:
			// Natural boundary; extend the left run.
			b.lens[k-1] += count
		default:
			// Natural boundary; rightVal == val, extend the right run.
			b.lens[k] += count
		}
	}
	b.reindex()
}

// Delete removes the bits in the half-open interval [l, r) and merges the
// two survivors if they now hold the same value, leaving no zero-length runs.
func (b *Bits) Delete(l, r uint64) {
	n := b.Len()
	if l >= r || r > n {
		return
	}
	// Capture real values adjacent to the cut before ensureBoundary splits
	// runs, because a fresh split temporarily breaks value alternation.
	var leftVal, rightVal uint8
	if l > 0 {
		leftVal = b.At(l - 1)
	}
	if r < n {
		rightVal = b.At(r)
	}
	var a, k int
	if l == 0 {
		a = 0
	} else {
		a = b.ensureBoundary(l)
	}
	if r == n {
		k = len(b.lens)
	} else {
		k = b.ensureBoundary(r)
	}
	b.lens = append(b.lens[:a], b.lens[k:]...)
	if len(b.lens) == 0 {
		b.first = 0
		b.reindex()
		return
	}
	if a == 0 {
		// Sequence now starts with the run that began at r.
		b.first = rightVal
	} else if a < len(b.lens) && leftVal == rightVal {
		b.lens[a-1] += b.lens[a]
		b.lens = append(b.lens[:a], b.lens[a+1:]...)
	}
	b.reindex()
}

// Rank returns the number of bits equal to val in [0, pos).
func (b *Bits) Rank(pos uint64, val uint8) uint64 {
	n := b.Len()
	if pos > n {
		pos = n
	}
	if pos == 0 {
		return 0
	}
	// First run j with preTot[j+1] >= pos; offset may equal lens[j] at the end.
	i := sort.Search(len(b.lens), func(j int) bool { return b.preTot[j+1] >= pos })
	off := pos - b.preTot[i]
	ones := b.preOne[i]
	if b.runValue(i) == 1 {
		ones += off
	}
	if val == 1 {
		return ones
	}
	return pos - ones
}

// Select returns the position of the k-th (0-indexed) bit equal to val,
// or -1 if fewer than k+1 such bits exist.
func (b *Bits) Select(k uint64, val uint8) int64 {
	if len(b.lens) == 0 {
		return -1
	}
	if val == 1 {
		if k >= b.preOne[len(b.lens)] {
			return -1
		}
		i := sort.Search(len(b.lens), func(j int) bool { return b.preOne[j+1] > k })
		return int64(b.preTot[i] + (k - b.preOne[i]))
	}
	zeros := b.Len() - b.preOne[len(b.lens)]
	if k >= zeros {
		return -1
	}
	preZero := func(j int) uint64 { return b.preTot[j] - b.preOne[j] }
	i := sort.Search(len(b.lens), func(j int) bool { return preZero(j+1) > k })
	return int64(b.preTot[i] + (k - preZero(i)))
}

// String renders the full bit sequence as '0'/'1' characters.
func (b *Bits) String() string {
	var sb strings.Builder
	for i := range b.lens {
		c := byte('0') + b.runValue(i)
		for j := uint32(0); j < b.lens[i]; j++ {
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// Encode serializes to the compact layout: 1 header byte (first run value),
// uvarint run count, then uvarint per run length.
func (b *Bits) Encode() []byte {
	buf := make([]byte, 0, 2+len(b.lens)*binary.MaxVarintLen32)
	buf = append(buf, b.first)
	var tmp [binary.MaxVarintLen64]byte
	m := binary.PutUvarint(tmp[:], uint64(len(b.lens)))
	buf = append(buf, tmp[:m]...)
	for _, l := range b.lens {
		m := binary.PutUvarint(tmp[:], uint64(l))
		buf = append(buf, tmp[:m]...)
	}
	return buf
}

// Decode restores a Bits from Encode output.
func Decode(data []byte) (*Bits, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("rlebits: truncated encoding")
	}
	b := &Bits{first: data[0]}
	if b.first > 1 {
		return nil, fmt.Errorf("rlebits: bad first-bit byte %d", data[0])
	}
	rest := data[1:]
	n, m := binary.Uvarint(rest)
	if m <= 0 {
		return nil, fmt.Errorf("rlebits: bad run count")
	}
	rest = rest[m:]
	b.lens = make([]uint32, 0, n)
	for i := uint64(0); i < n; i++ {
		l, k := binary.Uvarint(rest)
		if k <= 0 {
			return nil, fmt.Errorf("rlebits: bad run length %d", i)
		}
		if l == 0 || l > 1<<32-1 {
			return nil, fmt.Errorf("rlebits: invalid run length %d", l)
		}
		rest = rest[k:]
		b.lens = append(b.lens, uint32(l))
	}
	b.reindex()
	return b, nil
}

// EncodedBytes reports the serialized size in bytes (len of Encode output).
func (b *Bits) EncodedBytes() int { return len(b.Encode()) }

// HeapBytes estimates the in-memory footprint of the structure itself:
// 1 byte first-bit + 4 bytes per run length + 2 x 8 bytes per prefix-sum slot.
func (b *Bits) HeapBytes() int {
	runs := len(b.lens)
	return 1 + 4*runs + 2*8*(runs+1)
}

// CheckInvariants verifies structural invariants; used by tests and demos.
// It returns an error describing the first violation found.
func (b *Bits) CheckInvariants() error {
	if len(b.lens) == 0 {
		return nil
	}
	if b.first > 1 {
		return fmt.Errorf("first bit %d out of range", b.first)
	}
	if len(b.preTot) != len(b.lens)+1 || len(b.preOne) != len(b.lens)+1 {
		return fmt.Errorf("length index out of sync: %d runs, preTot=%d, preOne=%d",
			len(b.lens), len(b.preTot), len(b.preOne))
	}
	var tot, ones uint64
	for i, l := range b.lens {
		if l == 0 {
			return fmt.Errorf("zero-length run at index %d", i)
		}
		if b.preTot[i] != tot {
			return fmt.Errorf("preTot[%d]=%d, want %d", i, b.preTot[i], tot)
		}
		if b.preOne[i] != ones {
			return fmt.Errorf("preOne[%d]=%d, want %d", i, b.preOne[i], ones)
		}
		tot += uint64(l)
		if b.runValue(i) == 1 {
			ones += uint64(l)
		}
	}
	if b.preTot[len(b.lens)] != tot || b.preOne[len(b.lens)] != ones {
		return fmt.Errorf("length index totals out of sync")
	}
	return nil
}
