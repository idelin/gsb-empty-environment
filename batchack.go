// Package batchack implements a byte-minimal codec for batch
// acknowledgement of discrete sequence numbers (uint32 with wraparound).
//
// A set of sequence numbers is encoded as an anchor plus a combination of
// contiguous intervals (ranges) and a finite bitmap:
//
//	byte 0      control: RRRRR B MM
//	              RRRRR: number of range blocks (0..31)
//	              B:     bitmap present flag
//	              MM:    bitmap length code (0..3 => 1..4 bytes);
//	                     must be 0 when B is 0
//	byte 1..4   anchor: uint32, big-endian. Every acknowledged sequence
//	            number n is represented as n == anchor+off (mod 2^32) with
//	            0 <= off <= MaxOffset.
//	then RRRRR range blocks, 8 bytes each: startOff uint32 BE, endOff
//	    uint32 BE, both inclusive. Ranges are sorted by startOff, never
//	    overlap and are never adjacent (adjacent ranges must be merged).
//	then the bitmap (if B): MM+1 bytes. Bit k (byte k/8, bit k%8) covers
//	    offset k, i.e. sequence number anchor+k (mod 2^32).
//
// The bitmap covers offsets [0, 8*len); ranges only cover offsets
// >= 8*len. The encoder enumerates every bitmap length and picks the
// combination with the smallest total size, so the encoding is
// byte-minimal under this format. The decoder rejects overlapping ranges
// and any non-canonical (non byte-minimal) form.
//
// Wraparound: sequence numbers are ordered circularly. The anchor is the
// element immediately after the largest circular gap, so offsets never
// wrap; a run of consecutive sequence numbers crossing 2^32-1 -> 0 stays
// a single range. A set is encodable only if it fits inside a window of
// 2^31 sequence numbers (largest circular gap >= 2^31+1).
package batchack

import (
	"encoding/binary"
	"errors"
	"sort"
)

const (
	// MaxOffset is the largest forward offset from the anchor that can be
	// represented, keeping the anchor of any encoded set unambiguous.
	MaxOffset = 1<<31 - 1

	maxRanges      = 31
	maxBitmapBytes = 4
	headerLen      = 5
	rangeLen       = 8
)

var (
	// ErrSpanTooLarge reports a set that does not fit in a window of
	// 2^31 sequence numbers and therefore has no unambiguous anchor.
	ErrSpanTooLarge = errors.New("batchack: set spans more than 2^31-1 sequence numbers")
	// ErrTooManyRanges reports a set needing more than 31 range blocks.
	ErrTooManyRanges = errors.New("batchack: set needs more than 31 ranges")
	// ErrTruncated reports an encoding shorter than its header implies.
	ErrTruncated = errors.New("batchack: truncated encoding")
	// ErrTrailingData reports extra bytes after a complete encoding.
	ErrTrailingData = errors.New("batchack: trailing bytes after encoding")
	// ErrOverlap reports overlapping (or misordered) range blocks.
	ErrOverlap = errors.New("batchack: overlapping ranges")
	// ErrInvalidRange reports a range with start greater than end.
	ErrInvalidRange = errors.New("batchack: range start greater than end")
	// ErrOffsetTooLarge reports a range offset above MaxOffset.
	ErrOffsetTooLarge = errors.New("batchack: range offset exceeds 2^31-1")
	// ErrNonCanonical reports an encoding that is not the unique
	// byte-minimal form the encoder would produce (adjacent ranges,
	// range inside the bitmap window, trailing zero bitmap byte,
	// reserved bits set, anchor not itself a member, or a suboptimal
	// bitmap/range split).
	ErrNonCanonical = errors.New("batchack: non-canonical encoding")
)

type rng struct{ start, end uint32 }

// Range is a closed interval of offsets from the message anchor.
type Range struct{ Start, End uint32 }

// Message is the compact decoded form of an encoding. It acknowledges
// every sequence number Anchor+off (mod 2^32) where off is set in Bitmap
// (bit k of byte k/8) or lies in one of the Ranges.
type Message struct {
	Anchor uint32
	Ranges []Range
	Bitmap []byte
}

// Contains reports whether sequence number n is acknowledged.
func (m *Message) Contains(n uint32) bool {
	off := n - m.Anchor // mod 2^32
	if off > MaxOffset {
		return false
	}
	if off < uint32(len(m.Bitmap))*8 {
		return m.Bitmap[off/8]>>(off%8)&1 == 1
	}
	i := sort.Search(len(m.Ranges), func(i int) bool { return m.Ranges[i].End >= off })
	return i < len(m.Ranges) && m.Ranges[i].Start <= off
}

// ForEach calls f for every acknowledged sequence number, in ascending
// offset order from the anchor, stopping early if f returns false.
func (m *Message) ForEach(f func(uint32) bool) {
	for k := 0; k < len(m.Bitmap)*8; k++ {
		if m.Bitmap[k/8]>>(uint(k)%8)&1 == 1 {
			if !f(m.Anchor + uint32(k)) {
				return
			}
		}
	}
	for _, r := range m.Ranges {
		for off := r.Start; ; off++ {
			if !f(m.Anchor + off) {
				return
			}
			if off == r.End {
				break
			}
		}
	}
}

// Count returns the number of acknowledged sequence numbers.
func (m *Message) Count() uint64 {
	var n uint64
	for _, b := range m.Bitmap {
		for ; b != 0; b &= b - 1 {
			n++
		}
	}
	for _, r := range m.Ranges {
		n += uint64(r.End) - uint64(r.Start) + 1
	}
	return n
}

// Members returns all acknowledged sequence numbers in ascending numeric
// order. A message may acknowledge up to 2^31 numbers; callers handling
// untrusted input should check Count first.
func (m *Message) Members() []uint32 {
	var out []uint32
	m.ForEach(func(n uint32) bool {
		out = append(out, n)
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Encode returns the canonical, byte-minimal encoding of the given set of
// sequence numbers. The input may be unordered and contain duplicates.
func Encode(nums []uint32) ([]byte, error) {
	sorted := sortedUnique(nums)
	if len(sorted) == 0 {
		return make([]byte, headerLen), nil
	}
	anchor, offsets, err := anchorOffsets(sorted)
	if err != nil {
		return nil, err
	}
	bestLen := -1
	bestCost := 0
	var bestRanges []rng
	for l := 0; l <= maxBitmapBytes; l++ {
		ranges := rangesOutside(offsets, uint32(l)*8)
		if len(ranges) > maxRanges {
			continue
		}
		cost := headerLen + rangeLen*len(ranges) + l
		if bestLen < 0 || cost < bestCost {
			bestLen, bestCost, bestRanges = l, cost, ranges
		}
	}
	if bestLen < 0 {
		return nil, ErrTooManyRanges
	}
	control := byte(len(bestRanges)) << 3
	if bestLen > 0 {
		control |= 0x04 | byte(bestLen-1)
	}
	out := make([]byte, 0, bestCost)
	out = append(out, control)
	out = binary.BigEndian.AppendUint32(out, anchor)
	for _, r := range bestRanges {
		out = binary.BigEndian.AppendUint32(out, r.start)
		out = binary.BigEndian.AppendUint32(out, r.end)
	}
	if bestLen > 0 {
		bitmap := make([]byte, bestLen)
		for _, off := range offsets {
			if off < uint32(bestLen)*8 {
				bitmap[off/8] |= 1 << (off % 8)
			}
		}
		out = append(out, bitmap...)
	}
	return out, nil
}

// Parse decodes an encoding into its compact form, rejecting overlapping
// ranges and any non-canonical form. It never expands ranges, so it is
// safe to use on untrusted input.
func Parse(data []byte) (*Message, error) {
	anchor, ranges, bitmap, err := parse(data)
	if err != nil {
		return nil, err
	}
	var bitmapOffs []uint32
	for k := 0; k < len(bitmap)*8; k++ {
		if bitmap[k/8]>>(uint(k)%8)&1 == 1 {
			bitmapOffs = append(bitmapOffs, uint32(k))
		}
	}
	if len(ranges) == 0 && len(bitmapOffs) == 0 {
		if anchor != 0 {
			return nil, ErrNonCanonical
		}
		return &Message{}, nil
	}
	// In the canonical form the anchor is itself a member: it is the
	// element immediately after the largest circular gap.
	// Bitmap offsets always precede every range offset; at least one
	// of the two is non-empty here.
	var minOff uint32
	if len(bitmapOffs) > 0 {
		minOff = bitmapOffs[0]
	} else {
		minOff = ranges[0].start
	}
	if minOff != 0 {
		return nil, ErrNonCanonical
	}
	// The bitmap length must be the byte-minimal choice for the decoded
	// set. The minimum is unique: costs differ by 8 per range but by at
	// most 4 per bitmap length, so ties are impossible.
	encodedCost := headerLen + rangeLen*len(ranges) + len(bitmap)
	for l := 0; l <= maxBitmapBytes; l++ {
		runs := runsOutsideMembers(uint32(l)*8, bitmapOffs, ranges)
		if runs > maxRanges {
			continue
		}
		if cost := headerLen + rangeLen*runs + l; cost < encodedCost {
			return nil, ErrNonCanonical
		}
	}
	msg := &Message{Anchor: anchor, Bitmap: append([]byte(nil), bitmap...)}
	for _, r := range ranges {
		msg.Ranges = append(msg.Ranges, Range{Start: r.start, End: r.end})
	}
	return msg, nil
}

// Decode parses an encoding and returns the acknowledged sequence numbers
// in ascending numeric order. It rejects overlapping ranges and any
// non-canonical form. Note that a short encoding can acknowledge up to
// 2^31 numbers; use Parse and Message.Count on untrusted input.
func Decode(data []byte) ([]uint32, error) {
	msg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return msg.Members(), nil
}

// parse decodes the wire format and performs structural validation.
func parse(data []byte) (anchor uint32, ranges []rng, bitmap []byte, err error) {
	if len(data) < headerLen {
		return 0, nil, nil, ErrTruncated
	}
	control := data[0]
	numRanges := int(control >> 3)
	hasBitmap := control&0x04 != 0
	bitmapLen := 0
	if hasBitmap {
		bitmapLen = int(control&0x03) + 1
	} else if control&0x03 != 0 {
		return 0, nil, nil, ErrNonCanonical // reserved bits set
	}
	want := headerLen + rangeLen*numRanges + bitmapLen
	if len(data) < want {
		return 0, nil, nil, ErrTruncated
	}
	if len(data) > want {
		return 0, nil, nil, ErrTrailingData
	}
	anchor = binary.BigEndian.Uint32(data[1:headerLen])
	pos := headerLen
	ranges = make([]rng, numRanges)
	for i := range ranges {
		ranges[i].start = binary.BigEndian.Uint32(data[pos:])
		ranges[i].end = binary.BigEndian.Uint32(data[pos+4:])
		pos += rangeLen
	}
	bitmap = data[pos:want]
	for i, r := range ranges {
		if r.start > r.end {
			return 0, nil, nil, ErrInvalidRange
		}
		if r.end > MaxOffset {
			return 0, nil, nil, ErrOffsetTooLarge
		}
		if i > 0 {
			prev := ranges[i-1]
			if r.start <= prev.end {
				return 0, nil, nil, ErrOverlap
			}
			if r.start == prev.end+1 {
				return 0, nil, nil, ErrNonCanonical // adjacent ranges must be merged
			}
		}
		if hasBitmap && r.start < uint32(bitmapLen)*8 {
			return 0, nil, nil, ErrNonCanonical // range overlaps bitmap window
		}
	}
	if hasBitmap && bitmap[bitmapLen-1] == 0 {
		return 0, nil, nil, ErrNonCanonical // trailing zero bitmap byte
	}
	return anchor, ranges, bitmap, nil
}

// sortedUnique returns the input sorted ascending with duplicates removed.
func sortedUnique(nums []uint32) []uint32 {
	if len(nums) == 0 {
		return nil
	}
	sorted := make([]uint32, len(nums))
	copy(sorted, nums)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	out := sorted[:1]
	for _, v := range sorted[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// anchorOffsets picks the anchor (the element immediately after the
// largest circular gap) and returns modular offsets from it. Offsets
// never wrap: a consecutive run crossing 2^32-1 -> 0 stays contiguous.
func anchorOffsets(sorted []uint32) (uint32, []uint32, error) {
	n := len(sorted)
	bestGap := uint64(0)
	bestIdx := 0
	for i := 0; i < n; i++ {
		var gap uint64
		if i == n-1 {
			gap = uint64(sorted[0]) + (1 << 32) - uint64(sorted[n-1])
		} else {
			gap = uint64(sorted[i+1]) - uint64(sorted[i])
		}
		if gap > bestGap {
			bestGap, bestIdx = gap, i
		}
	}
	if bestGap < (1<<31)+1 {
		return 0, nil, ErrSpanTooLarge
	}
	anchor := sorted[(bestIdx+1)%n]
	offsets := make([]uint32, n)
	for i, v := range sorted {
		offsets[i] = v - anchor // mod 2^32
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	return anchor, offsets, nil
}

// rangesOutside merges the offsets >= lo into maximal contiguous ranges.
func rangesOutside(offsets []uint32, lo uint32) []rng {
	var out []rng
	for _, off := range offsets {
		if off < lo {
			continue
		}
		if n := len(out); n > 0 && out[n-1].end == off-1 {
			out[n-1].end = off
		} else {
			out = append(out, rng{start: off, end: off})
		}
	}
	return out
}

// runsOutsideMembers counts the runs of members with offset >= lo, given
// the compact member representation (bitmap bit offsets, all below every
// range, plus sorted non-adjacent ranges).
func runsOutsideMembers(lo uint32, bitmapOffs []uint32, ranges []rng) int {
	runs := 0
	var prevEnd uint32
	open := false
	extend := func(start, end uint32) {
		if open && start <= prevEnd+1 {
			prevEnd = end
		} else {
			runs++
			prevEnd = end
		}
		open = true
	}
	for _, off := range bitmapOffs {
		if off >= lo {
			extend(off, off)
		}
	}
	for _, r := range ranges {
		if r.end >= lo {
			extend(max(r.start, lo), r.end)
		}
	}
	return runs
}
