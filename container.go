package bitmap

import (
	"math/bits"
	"sort"
)

const (
	// maxContainerCard is the number of distinct values per container.
	maxContainerCard = 1 << 16
	// bitmapWords is the number of uint64 words in a bitmap container.
	bitmapWords = maxContainerCard / 64
)

type containerKind uint8

const (
	kindArray containerKind = iota
	kindBitmap
)

// container holds the low 16 bits of all values sharing one high key.
// Exactly one of array/words is non-nil, matching kind. card caches the
// cardinality and is always kept in sync with the payload.
type container struct {
	kind  containerKind
	card  int
	array []uint16
	words []uint64
}

// newArrayContainer builds an array container from sorted unique values.
func newArrayContainer(vals []uint16) container {
	return container{kind: kindArray, card: len(vals), array: vals}
}

// newBitmapContainer builds a bitmap container from words, computing card.
func newBitmapContainer(words []uint64) container {
	return container{kind: kindBitmap, card: popcount(words), words: words}
}

func popcount(words []uint64) int {
	n := 0
	for _, w := range words {
		n += bits.OnesCount64(w)
	}
	return n
}

// contains reports whether v is present.
func (c *container) contains(v uint16) bool {
	if c.kind == kindArray {
		i := sort.Search(len(c.array), func(i int) bool { return c.array[i] >= v })
		return i < len(c.array) && c.array[i] == v
	}
	return c.words[v>>6]&(1<<(v&63)) != 0
}

// add inserts v, which must not be present. Returns true if inserted.
func (c *container) add(v uint16) bool {
	if c.kind == kindArray {
		i := sort.Search(len(c.array), func(i int) bool { return c.array[i] >= v })
		if i < len(c.array) && c.array[i] == v {
			return false
		}
		c.array = append(c.array, 0)
		copy(c.array[i+1:], c.array[i:])
		c.array[i] = v
	} else {
		mask := uint64(1) << (v & 63)
		if c.words[v>>6]&mask != 0 {
			return false
		}
		c.words[v>>6] |= mask
	}
	c.card++
	return true
}

// remove deletes v. Returns true if removed.
func (c *container) remove(v uint16) bool {
	if c.kind == kindArray {
		i := sort.Search(len(c.array), func(i int) bool { return c.array[i] >= v })
		if i >= len(c.array) || c.array[i] != v {
			return false
		}
		c.array = append(c.array[:i], c.array[i+1:]...)
	} else {
		mask := uint64(1) << (v & 63)
		if c.words[v>>6]&mask == 0 {
			return false
		}
		c.words[v>>6] &^= mask
	}
	c.card--
	return true
}

// toBitmap converts the container to bitmap form in place.
func (c *container) toBitmap() {
	if c.kind == kindBitmap {
		return
	}
	words := make([]uint64, bitmapWords)
	for _, v := range c.array {
		words[v>>6] |= 1 << (v & 63)
	}
	c.kind = kindBitmap
	c.words = words
	c.array = nil
}

// toArray converts the container to array form in place.
func (c *container) toArray() {
	if c.kind == kindArray {
		return
	}
	arr := make([]uint16, 0, c.card)
	for i, w := range c.words {
		base := uint16(i * 64)
		for w != 0 {
			arr = append(arr, base+uint16(bits.TrailingZeros64(w)))
			w &= w - 1
		}
	}
	c.kind = kindArray
	c.array = arr
	c.words = nil
}

// normalize switches the container to its canonical representation for
// the given threshold: array when card <= threshold, bitmap otherwise.
func (c *container) normalize(threshold int) {
	if c.card <= threshold {
		c.toArray()
	} else {
		c.toBitmap()
	}
}

// flipRange toggles all values in [lo, hi) within this container,
// where lo/hi are container-local offsets (0..65536).
func (c *container) flipRange(lo, hi uint32) {
	if lo >= hi {
		return
	}
	if c.kind == kindArray && hi-lo <= 2*uint32(c.card)+64 {
		// Small range relative to size: flip element-wise.
		for v := lo; v < hi; v++ {
			if !c.remove(uint16(v)) {
				c.add(uint16(v))
			}
		}
		return
	}
	c.toBitmap()
	for w := lo >> 6; w <= (hi-1)>>6; w++ {
		var mask uint64 = ^uint64(0)
		if w == lo>>6 {
			mask &= ^uint64(0) << (lo & 63)
		}
		if w == (hi-1)>>6 {
			mask &= ^uint64(0) >> (63 - (hi-1)&63)
		}
		c.words[w] ^= mask
	}
	c.card = popcount(c.words)
}

// clone returns a deep copy.
func (c *container) clone() container {
	out := container{kind: c.kind, card: c.card}
	if c.kind == kindArray {
		out.array = append([]uint16(nil), c.array...)
	} else {
		out.words = append([]uint64(nil), c.words...)
	}
	return out
}

// equal reports value equality of two containers.
func (c *container) equal(o *container) bool {
	if c.kind != o.kind || c.card != o.card {
		return false
	}
	if c.kind == kindArray {
		if len(c.array) != len(o.array) {
			return false
		}
		for i := range c.array {
			if c.array[i] != o.array[i] {
				return false
			}
		}
		return true
	}
	for i := range c.words {
		if c.words[i] != o.words[i] {
			return false
		}
	}
	return true
}

// appendTo appends all values (as low 16 bits) to out in ascending order.
func (c *container) appendTo(out []uint16) []uint16 {
	if c.kind == kindArray {
		return append(out, c.array...)
	}
	for i, w := range c.words {
		base := uint16(i * 64)
		for w != 0 {
			out = append(out, base+uint16(bits.TrailingZeros64(w)))
			w &= w - 1
		}
	}
	return out
}
