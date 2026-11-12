package netsec

import (
	"errors"
	"math/bits"
)

// Box is an axis-aligned half-open integer box in n-dimensional space:
// for dimension i it contains every integer coordinate v with
// Lo[i] <= v < Hi[i].
//
// A box is valid when len(Lo) == len(Hi), both are non-empty, every bound
// fits into int64 and Lo[i] <= Hi[i] for every dimension. A box with some
// Lo[i] == Hi[i] is empty.
type Box struct {
	Lo []int64
	Hi []int64
}

var (
	errDimMismatch  = errors.New("netsec: box bounds have different dimensions")
	errNoDimensions = errors.New("netsec: box must have at least one dimension")
)

// Dim reports the number of dimensions of the box.
func (b Box) Dim() int { return len(b.Lo) }

// Empty reports whether the box contains no integer point.
// A malformed box (unbalanced bounds) is treated as empty.
func (b Box) Empty() bool {
	if len(b.Lo) != len(b.Hi) || len(b.Lo) == 0 {
		return true
	}
	for i := range b.Lo {
		if b.Lo[i] >= b.Hi[i] {
			return true
		}
	}
	return false
}

func (b Box) validate() error {
	if len(b.Lo) == 0 {
		return errNoDimensions
	}
	if len(b.Lo) != len(b.Hi) {
		return errDimMismatch
	}
	for i := range b.Lo {
		if b.Lo[i] > b.Hi[i] {
			return errors.New("netsec: box lower bound exceeds upper bound")
		}
	}
	return nil
}

// Contains reports whether the integer point p lies inside the box.
func (b Box) Contains(p []int64) bool {
	if len(p) != len(b.Lo) || len(b.Lo) != len(b.Hi) {
		return false
	}
	for i := range p {
		if p[i] < b.Lo[i] || p[i] >= b.Hi[i] {
			return false
		}
	}
	return true
}

// Volume returns the number of integer points in the box and an error when
// that count does not fit into int64 or the box is malformed.
func (b Box) Volume() (int64, error) {
	if err := b.validate(); err != nil {
		return 0, err
	}
	vol := int64(1)
	for i := range b.Lo {
		length := b.Hi[i] - b.Lo[i]
		var ok bool
		vol, ok = mul64(vol, length)
		if !ok {
			return 0, errors.New("netsec: box volume overflows int64")
		}
	}
	return vol, nil
}

// mul64 returns a*b and false when the product does not fit into int64.
func mul64(a, b int64) (int64, bool) {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	p := int64(lo)
	if hi != 0 || p < 0 {
		return 0, false
	}
	return p, true
}

// Subtract returns the partition of b minus the union of denied into
// pairwise disjoint, non-empty, valid boxes.
//
// Every returned box lies inside b, none intersects any denied box, and
// every integer point of b that is not denied belongs to exactly one
// returned box. Denied boxes outside b or disjoint from b are harmless.
func Subtract(b Box, denied ...Box) ([]Box, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}
	if _, err := b.Volume(); err != nil {
		return nil, err
	}
	remaining := []Box{b}
	for _, d := range denied {
		if err := d.validate(); err != nil {
			return nil, err
		}
		if d.Dim() != b.Dim() {
			return nil, errors.New("netsec: denied box dimension mismatch")
		}
		if _, err := d.Volume(); err != nil {
			return nil, err
		}
		var next []Box
		for _, cur := range remaining {
			next = append(next, subtractOne(cur, d)...)
		}
		remaining = next
	}
	return remaining, nil
}

// subtractOne returns b minus d as disjoint non-empty boxes.
func subtractOne(b, d Box) []Box {
	lo := make([]int64, len(b.Lo))
	hi := make([]int64, len(b.Hi))
	copy(lo, b.Lo)
	copy(hi, b.Hi)

	var out []Box
	for i := range lo {
		// [b.Lo[i], d.Lo[i])
		if d.Lo[i] > lo[i] && d.Lo[i] <= hi[i] {
			part := Box{Lo: append([]int64(nil), lo...), Hi: append([]int64(nil), hi...)}
			part.Hi[i] = d.Lo[i]
			out = append(out, part)
			lo[i] = d.Lo[i]
		}
		// [d.Hi[i], b.Hi[i])
		if d.Hi[i] > lo[i] && d.Hi[i] < hi[i] {
			part := Box{Lo: append([]int64(nil), lo...), Hi: append([]int64(nil), hi...)}
			part.Lo[i] = d.Hi[i]
			out = append(out, part)
			hi[i] = d.Hi[i]
		}
		// If the remaining slab does not overlap d in this dimension,
		// d cannot remove anything else from it.
		if hi[i] <= d.Lo[i] || lo[i] >= d.Hi[i] {
			if lo[i] < hi[i] {
				out = append(out, Box{Lo: lo, Hi: hi})
			}
			return out
		}
	}
	return out
}
