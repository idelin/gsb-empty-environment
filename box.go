package netsec

import "fmt"

// MaxDims is the maximum number of dimensions a Box may have.
const MaxDims = 4

// Box is a half-open integer hyper-rectangle [Lo, Hi) in Dims dimensions.
// Components at indices >= Dims are ignored and should be zero.
type Box struct {
	Dims   int
	Lo, Hi [MaxDims]int64
}

// NewBox builds a Box from per-dimension bounds. lo and hi must have the
// same length, between 1 and MaxDims, and satisfy lo[d] < hi[d].
func NewBox(lo, hi []int64) (Box, error) {
	if len(lo) != len(hi) {
		return Box{}, fmt.Errorf("netsec: lo and hi length mismatch (%d vs %d)", len(lo), len(hi))
	}
	if len(lo) == 0 || len(lo) > MaxDims {
		return Box{}, fmt.Errorf("netsec: dimension count %d out of range [1,%d]", len(lo), MaxDims)
	}
	var b Box
	b.Dims = len(lo)
	for d := 0; d < b.Dims; d++ {
		if lo[d] >= hi[d] {
			return Box{}, fmt.Errorf("netsec: empty range on dim %d: [%d,%d)", d, lo[d], hi[d])
		}
		b.Lo[d], b.Hi[d] = lo[d], hi[d]
	}
	return b, nil
}

// Empty reports whether the box has zero volume.
func (b Box) Empty() bool {
	for d := 0; d < b.Dims; d++ {
		if b.Lo[d] >= b.Hi[d] {
			return true
		}
	}
	return false
}

// Volume returns the number of integer grid points in the box.
func (b Box) Volume() int64 {
	v := int64(1)
	for d := 0; d < b.Dims; d++ {
		v *= b.Hi[d] - b.Lo[d]
	}
	return v
}

// ContainsBox reports whether b fully contains o.
func (b Box) ContainsBox(o Box) bool {
	if b.Dims != o.Dims {
		return false
	}
	for d := 0; d < b.Dims; d++ {
		if o.Lo[d] < b.Lo[d] || o.Hi[d] > b.Hi[d] {
			return false
		}
	}
	return true
}

// ContainsPoint reports whether p lies inside b.
func (b Box) ContainsPoint(p [MaxDims]int64) bool {
	for d := 0; d < b.Dims; d++ {
		if p[d] < b.Lo[d] || p[d] >= b.Hi[d] {
			return false
		}
	}
	return true
}

// Intersects reports whether b and o share at least one grid point.
func (b Box) Intersects(o Box) bool {
	if b.Dims != o.Dims {
		return false
	}
	for d := 0; d < b.Dims; d++ {
		if b.Lo[d] >= o.Hi[d] || o.Lo[d] >= b.Hi[d] {
			return false
		}
	}
	return true
}

// Intersect returns the overlap of b and o, or false if they are disjoint.
func (b Box) Intersect(o Box) (Box, bool) {
	if !b.Intersects(o) {
		return Box{}, false
	}
	r := b
	for d := 0; d < b.Dims; d++ {
		if o.Lo[d] > r.Lo[d] {
			r.Lo[d] = o.Lo[d]
		}
		if o.Hi[d] < r.Hi[d] {
			r.Hi[d] = o.Hi[d]
		}
	}
	return r, true
}

// Subtract returns a set of disjoint boxes covering a \ b. The result is
// empty when b fully covers a. Boxes are produced by slicing one dimension
// at a time, so at most 2*Dims boxes are returned.
func Subtract(a, b Box) []Box {
	if !a.Intersects(b) {
		return []Box{a}
	}
	var out []Box
	rem := a
	for d := 0; d < a.Dims; d++ {
		if b.Lo[d] > rem.Lo[d] {
			lo := rem
			lo.Hi[d] = b.Lo[d]
			out = append(out, lo)
			rem.Lo[d] = b.Lo[d]
		}
		if b.Hi[d] < rem.Hi[d] {
			hi := rem
			hi.Lo[d] = b.Hi[d]
			out = append(out, hi)
			rem.Hi[d] = b.Hi[d]
		}
	}
	return out
}

func (b Box) String() string {
	s := "["
	for d := 0; d < b.Dims; d++ {
		if d > 0 {
			s += " x "
		}
		s += fmt.Sprintf("[%d,%d)", b.Lo[d], b.Hi[d])
	}
	return s + "]"
}
