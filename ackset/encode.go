package ackset

import (
	"encoding/binary"
	"math"
	"slices"
)

// uvarintLen returns the number of bytes x takes as a LEB128 varint.
func uvarintLen(x uint64) int {
	n := 1
	for x >= 0x80 {
		x >>= 7
		n++
	}
	return n
}

// normalize returns the sorted, deduplicated elements of set.
func normalize(set []uint32) []uint32 {
	if len(set) == 0 {
		return nil
	}
	s := make([]uint32, len(set))
	copy(s, set)
	slices.Sort(s)
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// segHeader is the per-segment overhead: 1 type byte + 4 start bytes.
const segHeader = 5

// Encode returns the canonical, byte-minimal encoding of set. Elements
// are taken modulo 2^32, so sets straddling sequence number wraparound
// are supported as long as their minimal covering window is smaller
// than MaxWindow.
func Encode(set []uint32) ([]byte, error) {
	elems := normalize(set)
	n := len(elems)
	if n == 0 {
		return []byte{modeEmpty}, nil
	}
	if n > MaxElements {
		return nil, ErrTooLarge
	}

	// Anchor after the largest circular gap: this minimizes the span.
	// Because gaps between segments are free in this format and any
	// encodable set has a largest gap far bigger than one segment
	// header, cutting the circle at the largest gap is always optimal.
	start := 0
	if n > 1 {
		var maxGap uint32
		for i := 0; i < n; i++ {
			gap := elems[(i+1)%n] - elems[i] // mod 2^32
			if gap > maxGap {
				maxGap = gap
				start = (i + 1) % n
			}
		}
		if span := uint64(0xFFFFFFFF-maxGap) + 1; span >= MaxWindow {
			return nil, ErrTooSparse
		}
	}

	anchor := elems[start]
	off := make([]uint64, n) // modular offsets from anchor, ascending
	for k := 0; k < n; k++ {
		off[k] = uint64(elems[(start+k)%n] - anchor)
	}

	// rs[k] is the first index of the maximal run of consecutive
	// sequence numbers containing k.
	rs := make([]int, n)
	for k := 1; k < n; k++ {
		if off[k] == off[k-1]+1 {
			rs[k] = rs[k-1]
		} else {
			rs[k] = k
		}
	}

	// dp[j] is the best encoding of the first j elements. Costs are
	// compared lexicographically by (payload bytes, segment count);
	// since a varint length grows by at most 1 per boundary and a
	// competing payload differs by at least 1, this also minimizes
	// payload + uvarintLen(seg_count).
	type entry struct {
		cost, nseg, prev int
		bitmap           bool
	}
	dp := make([]entry, n+1)
	dp[0] = entry{prev: -1}
	// varint length boundaries for a window size.
	wBounds := []uint64{0x7F, 0x3FFF, 0x1FFFFF, 0xFFFFFFF, MaxWindow - 1}

	for j := 1; j <= n; j++ {
		best := entry{cost: math.MaxInt, nseg: math.MaxInt}
		try := func(i, cost int, bitmap bool) {
			cand := dp[i].cost + cost
			ns := dp[i].nseg + 1
			if cand < best.cost || (cand == best.cost && ns < best.nseg) {
				best = entry{cost: cand, nseg: ns, prev: i, bitmap: bitmap}
			}
		}

		// Range option: segment (i..j-1) must be contiguous, i.e.
		// i >= rs[j-1]. dp cost is non-decreasing in i, so within one
		// varint-length bucket only the smallest i can be optimal.
		lo := rs[j-1]
		maxW := off[j-1] - off[lo] + 1
		loW := uint64(1)
		for _, hiW := range wBounds {
			if loW > maxW {
				break
			}
			hi := hiW
			if hi > maxW {
				hi = maxW
			}
			// smallest i with w(i) <= hi: off[i] >= off[j-1]+1-hi
			thr := off[j-1] + 1 - hi
			i, _ := slices.BinarySearch(off[:j], thr)
			if i < lo {
				i = lo
			}
			w := off[j-1] - off[i] + 1
			try(i, segHeader+uvarintLen(w), false)
			loW = hiW + 1
		}

		// Bitmap option: scan back while the window fits.
		for i := j - 1; i >= 0; i-- {
			w := off[j-1] - off[i] + 1
			if w > MaxBitmapBits {
				break
			}
			if w >= 2 && w != uint64(j-i) { // contiguous => range wins
				try(i, segHeader+uvarintLen(w)+int((w+7)/8), true)
			}
		}

		dp[j] = best
	}

	// Reconstruct segments.
	type seg struct {
		i, j   int
		bitmap bool
	}
	var segs []seg
	for j := n; j > 0; j = dp[j].prev {
		segs = append(segs, seg{i: dp[j].prev, j: j, bitmap: dp[j].bitmap})
	}
	slices.Reverse(segs)

	out := []byte{modeSegments}
	out = binary.AppendUvarint(out, uint64(len(segs)))
	for _, s := range segs {
		w := off[s.j-1] - off[s.i] + 1
		segStart := anchor + uint32(off[s.i]) // mod 2^32
		if s.bitmap {
			out = append(out, segBitmap)
			out = binary.BigEndian.AppendUint32(out, segStart)
			out = binary.AppendUvarint(out, w)
			bm := make([]byte, (w+7)/8)
			for k := s.i; k < s.j; k++ {
				d := off[k] - off[s.i]
				bm[d/8] |= 1 << (d % 8)
			}
			out = append(out, bm...)
		} else {
			out = append(out, segRange)
			out = binary.BigEndian.AppendUint32(out, segStart)
			out = binary.AppendUvarint(out, w)
		}
	}
	return out, nil
}
