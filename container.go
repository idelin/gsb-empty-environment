package bitmap

import (
	"math/bits"
	"sort"
)

// wordCount is the number of 64-bit words needed to cover 2^16 bits.
const wordCount = 1 << 10

// container stores the low 16 bits of all values sharing one high-16-bit key.
// Exactly two representations exist: arrayContainer (sorted, deduplicated)
// for sparse data and bitmapContainer for dense data.
type container interface {
	contains(v uint16) bool
	cardinality() int
	clone() container
}

// arrayContainer keeps values in a sorted, duplicate-free slice.
type arrayContainer struct {
	values []uint16
}

func searchUint16(s []uint16, v uint16) int {
	return sort.Search(len(s), func(i int) bool { return s[i] >= v })
}

func (c *arrayContainer) contains(v uint16) bool {
	i := searchUint16(c.values, v)
	return i < len(c.values) && c.values[i] == v
}

func (c *arrayContainer) cardinality() int { return len(c.values) }

func (c *arrayContainer) clone() container {
	vs := make([]uint16, len(c.values))
	copy(vs, c.values)
	return &arrayContainer{values: vs}
}

// add inserts v, returning true if it was not already present.
func (c *arrayContainer) add(v uint16) bool {
	i := searchUint16(c.values, v)
	if i < len(c.values) && c.values[i] == v {
		return false
	}
	c.values = append(c.values, 0)
	copy(c.values[i+1:], c.values[i:])
	c.values[i] = v
	return true
}

// remove deletes v, returning true if it was present.
func (c *arrayContainer) remove(v uint16) bool {
	i := searchUint16(c.values, v)
	if i >= len(c.values) || c.values[i] != v {
		return false
	}
	copy(c.values[i:], c.values[i+1:])
	c.values = c.values[:len(c.values)-1]
	return true
}

func (c *arrayContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{}
	for _, v := range c.values {
		bc.bits[v>>6] |= 1 << (v & 63)
	}
	bc.card = len(c.values)
	return bc
}

// bitmapContainer stores a full 2^16-bit bitmap with a cached cardinality.
type bitmapContainer struct {
	bits [wordCount]uint64
	card int
}

func (c *bitmapContainer) contains(v uint16) bool {
	return c.bits[v>>6]&(1<<(v&63)) != 0
}

func (c *bitmapContainer) cardinality() int { return c.card }

func (c *bitmapContainer) clone() container {
	return &bitmapContainer{bits: c.bits, card: c.card}
}

// add sets the bit for v, returning true if it was not already set.
func (c *bitmapContainer) add(v uint16) bool {
	w := &c.bits[v>>6]
	mask := uint64(1) << (v & 63)
	if *w&mask != 0 {
		return false
	}
	*w |= mask
	c.card++
	return true
}

// remove clears the bit for v, returning true if it was set.
func (c *bitmapContainer) remove(v uint16) bool {
	w := &c.bits[v>>6]
	mask := uint64(1) << (v & 63)
	if *w&mask == 0 {
		return false
	}
	*w &^= mask
	c.card--
	return true
}

// recomputeCard refreshes the cached cardinality from the bit pattern.
func (c *bitmapContainer) recomputeCard() {
	n := 0
	for _, w := range c.bits {
		n += bits.OnesCount64(w)
	}
	c.card = n
}

func (c *bitmapContainer) toArray() *arrayContainer {
	ac := &arrayContainer{values: make([]uint16, 0, c.card)}
	for wi, w := range c.bits {
		for w != 0 {
			t := bits.TrailingZeros64(w)
			ac.values = append(ac.values, uint16(wi<<6|t))
			w &= w - 1
		}
	}
	return ac
}

func toBitmapContainer(c container) *bitmapContainer {
	switch t := c.(type) {
	case *bitmapContainer:
		return t
	case *arrayContainer:
		return t.toBitmap()
	}
	panic("bitmap: unknown container type")
}

// normalize converts a container to its canonical representation for the
// given threshold: arrays hold at most threshold values, bitmaps hold more.
func normalize(c container, threshold int) container {
	switch t := c.(type) {
	case *arrayContainer:
		if len(t.values) > threshold {
			return t.toBitmap()
		}
	case *bitmapContainer:
		if t.card <= threshold {
			return t.toArray()
		}
	}
	return c
}

func andContainers(a, b container) container {
	switch at := a.(type) {
	case *arrayContainer:
		switch bt := b.(type) {
		case *arrayContainer:
			out := &arrayContainer{}
			i, j := 0, 0
			for i < len(at.values) && j < len(bt.values) {
				switch {
				case at.values[i] < bt.values[j]:
					i++
				case at.values[i] > bt.values[j]:
					j++
				default:
					out.values = append(out.values, at.values[i])
					i++
					j++
				}
			}
			return out
		case *bitmapContainer:
			out := &arrayContainer{}
			for _, v := range at.values {
				if bt.contains(v) {
					out.values = append(out.values, v)
				}
			}
			return out
		}
	case *bitmapContainer:
		switch bt := b.(type) {
		case *arrayContainer:
			out := &arrayContainer{}
			for _, v := range bt.values {
				if at.contains(v) {
					out.values = append(out.values, v)
				}
			}
			return out
		case *bitmapContainer:
			out := &bitmapContainer{}
			for i := range out.bits {
				w := at.bits[i] & bt.bits[i]
				out.bits[i] = w
				out.card += bits.OnesCount64(w)
			}
			return out
		}
	}
	panic("bitmap: unknown container type")
}

func orContainers(a, b container) container {
	if _, ok := a.(*bitmapContainer); !ok {
		if _, ok := b.(*bitmapContainer); !ok {
			at := a.(*arrayContainer)
			bt := b.(*arrayContainer)
			out := &arrayContainer{values: make([]uint16, 0, len(at.values)+len(bt.values))}
			i, j := 0, 0
			for i < len(at.values) && j < len(bt.values) {
				switch {
				case at.values[i] < bt.values[j]:
					out.values = append(out.values, at.values[i])
					i++
				case bt.values[j] < at.values[i]:
					out.values = append(out.values, bt.values[j])
					j++
				default:
					out.values = append(out.values, at.values[i])
					i++
					j++
				}
			}
			out.values = append(out.values, at.values[i:]...)
			out.values = append(out.values, bt.values[j:]...)
			return out
		}
	}
	ab := toBitmapContainer(a)
	bb := toBitmapContainer(b)
	out := &bitmapContainer{}
	for i := range out.bits {
		w := ab.bits[i] | bb.bits[i]
		out.bits[i] = w
		out.card += bits.OnesCount64(w)
	}
	return out
}

// andNotContainers computes a \ b.
func andNotContainers(a, b container) container {
	switch at := a.(type) {
	case *arrayContainer:
		switch bt := b.(type) {
		case *arrayContainer:
			out := &arrayContainer{}
			i, j := 0, 0
			for i < len(at.values) {
				for j < len(bt.values) && bt.values[j] < at.values[i] {
					j++
				}
				if j < len(bt.values) && bt.values[j] == at.values[i] {
					i++
					continue
				}
				out.values = append(out.values, at.values[i])
				i++
			}
			return out
		case *bitmapContainer:
			out := &arrayContainer{}
			for _, v := range at.values {
				if !bt.contains(v) {
					out.values = append(out.values, v)
				}
			}
			return out
		}
	case *bitmapContainer:
		bb := toBitmapContainer(b)
		out := &bitmapContainer{}
		for i := range out.bits {
			w := at.bits[i] &^ bb.bits[i]
			out.bits[i] = w
			out.card += bits.OnesCount64(w)
		}
		return out
	}
	panic("bitmap: unknown container type")
}
