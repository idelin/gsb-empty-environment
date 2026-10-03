// Package bitmap implements a compressed bitmap (Roaring-style) with
// automatic container selection among array, bitmap and run-length encodings.
package bitmap

const (
	// arrayMaxSize is the cardinality threshold above which an array
	// container is no longer eligible.
	arrayMaxSize = 4096
	// bitmapContainerBytes is the fixed storage cost of a bitmap container.
	bitmapContainerBytes = 8192
	// runLengthOverhead is the storage cost of one run interval in bytes.
	runLengthOverhead = 4
)

// interval16 is an inclusive [start, last] range of 16-bit values.
type interval16 struct {
	start uint16
	last  uint16
}

func (iv interval16) length() int { return int(iv.last) - int(iv.start) + 1 }

// container stores the low 16 bits of a set of values sharing the same
// high bits.
type container interface {
	contains(x uint16) bool
	add(x uint16) (container, bool)
	addRange(lo, hi uint16) (container, bool)
	cardinality() int
	toBitmap() *bitmapContainer
	foreach(fn func(x uint16) bool)
	iter() containerIterator
	equals(o container) bool
	clone() container
	typeName() string
}

type containerIterator interface {
	next() (uint16, bool)
}

// numRunsOf counts the number of maximal runs of consecutive values.
func numRunsOf(c container) int {
	switch t := c.(type) {
	case *runContainer:
		return len(t.runs)
	case *bitmapContainer:
		return t.numRuns()
	}
	runs := 0
	var prev uint16
	inRun := false
	c.foreach(func(x uint16) bool {
		if !inRun || uint32(x) != uint32(prev)+1 {
			runs++
		}
		prev = x
		inRun = true
		return true
	})
	return runs
}

// optimize converts c to the most compact container encoding, choosing the
// smallest of array (2*card bytes), bitmap (8192 bytes) and run (4*runs
// bytes) representations.
func optimize(c container) container {
	card := c.cardinality()
	if card == 0 {
		return &arrayContainer{}
	}
	arrayBytes := int(^uint(0) >> 1)
	if card <= arrayMaxSize {
		arrayBytes = 2 * card
	}
	runBytes := runLengthOverhead * numRunsOf(c)
	best := bitmapContainerBytes
	kind := 1 // 0=array, 1=bitmap, 2=run
	if arrayBytes < best {
		best = arrayBytes
		kind = 0
	}
	if runBytes < best {
		best = runBytes
		kind = 2
	}
	switch kind {
	case 0:
		if _, ok := c.(*arrayContainer); ok {
			return c
		}
		return toArrayContainer(c)
	case 2:
		if _, ok := c.(*runContainer); ok {
			return c
		}
		return toRunContainer(c)
	default:
		if _, ok := c.(*bitmapContainer); ok {
			return c
		}
		return c.toBitmap()
	}
}

func toArrayContainer(c container) *arrayContainer {
	out := &arrayContainer{data: make([]uint16, 0, c.cardinality())}
	c.foreach(func(x uint16) bool {
		out.data = append(out.data, x)
		return true
	})
	return out
}

func toRunContainer(c container) *runContainer {
	if bc, ok := c.(*bitmapContainer); ok {
		return bitmapToRun(bc)
	}
	out := &runContainer{}
	started := false
	var cur interval16
	c.foreach(func(x uint16) bool {
		if !started {
			cur = interval16{x, x}
			started = true
			return true
		}
		if uint32(x) == uint32(cur.last)+1 {
			cur.last = x
			return true
		}
		out.runs = append(out.runs, cur)
		cur = interval16{x, x}
		return true
	})
	if started {
		out.runs = append(out.runs, cur)
	}
	return out
}
