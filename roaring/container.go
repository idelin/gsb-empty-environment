// Package roaring implements a compressed bitmap with automatic container
// selection (array / bitmap / run-length-encoded) and fast boolean set ops.
package roaring

import (
	"fmt"
	"math/bits"
)

type containerType uint8

const (
	typeArray containerType = iota
	typeBitmap
	typeRun
)

func (t containerType) String() string {
	switch t {
	case typeArray:
		return "array"
	case typeBitmap:
		return "bitmap"
	case typeRun:
		return "run"
	}
	return "unknown"
}

const (
	// arrayDefaultMaxSize is the cardinality threshold above which an
	// array container is converted to a bitmap container.
	arrayDefaultMaxSize  = 4096
	bitmapContainerWords = 1024 // 1024 * 64 bits = 65536 values
	// MaxValue is the size of the 32-bit key space.
	MaxValue = uint64(1) << 32
)

// container stores the low 16 bits of a set of uint32 keys that share the
// same high 16 bits.
type container interface {
	ctype() containerType
	cardinality() int
	contains(x uint16) bool
	add(x uint16) (container, bool)
	remove(x uint16) (container, bool)
	addRange(lo, hi uint16) container // inclusive [lo, hi]
	removeRange(lo, hi uint16) container
	forEach(fn func(x uint16))
	clone() container
	minimum() uint16
	maximum() uint16
	// optimize converts the container to the most compact representation.
	optimize() container
	// numRuns returns the number of maximal runs of consecutive values.
	numRuns() int
	validate() error
	sizeInBytes() int
}

// pickContainerType selects the most compact container type for the given
// cardinality and number of runs.
func pickContainerType(card, runs int) containerType {
	arraySize := int(^uint(0) >> 1)
	if card <= arrayDefaultMaxSize {
		arraySize = 2 * card
	}
	runSize := 4 * runs
	bitmapSize := 8 * bitmapContainerWords
	switch {
	case arraySize <= runSize && arraySize <= bitmapSize:
		return typeArray
	case runSize <= bitmapSize:
		return typeRun
	default:
		return typeBitmap
	}
}

func arrayContainerFrom(src container) *arrayContainer {
	ac := &arrayContainer{content: make([]uint16, 0, src.cardinality())}
	src.forEach(func(x uint16) { ac.content = append(ac.content, x) })
	return ac
}

func bitmapContainerFrom(src container) *bitmapContainer {
	bc := newBitmapContainer()
	src.forEach(func(x uint16) { bc.set(x) })
	return bc
}

func runContainerFrom(src container) *runContainer {
	if bc, ok := src.(*bitmapContainer); ok {
		return runContainerFromBitmap(bc)
	}
	rc := &runContainer{}
	prev := uint32(0)
	first := true
	src.forEach(func(x uint16) {
		if !first && uint32(x) == prev+1 {
			rc.runs[len(rc.runs)-1].length++
		} else {
			rc.runs = append(rc.runs, interval16{start: x})
		}
		prev = uint32(x)
		first = false
	})
	return rc
}

// runContainerFromBitmap extracts runs from a bitmap container with
// word-level operations instead of per-value iteration.
func runContainerFromBitmap(bc *bitmapContainer) *runContainer {
	rc := &runContainer{}
	for w := 0; w < len(bc.bitmap); w++ {
		word := bc.bitmap[w]
		for word != 0 {
			s := bits.TrailingZeros64(word)
			runLen := bits.TrailingZeros64(^(word >> uint(s)))
			start := w<<6 + s
			if n := len(rc.runs); n > 0 && runEnd(rc.runs[n-1])+1 == uint32(start) {
				rc.runs[n-1].length += uint16(runLen)
			} else {
				rc.runs = append(rc.runs, interval16{start: uint16(start), length: uint16(runLen - 1)})
			}
			if runLen == 64 {
				word = 0
			} else {
				word &^= ((uint64(1) << uint(runLen)) - 1) << uint(s)
			}
		}
	}
	return rc
}

// newContainerForRange creates a fresh container holding [lo, hi].
func newContainerForRange(lo, hi uint16) container {
	count := int(hi) - int(lo) + 1
	if count == 1<<16 {
		return &runContainer{runs: []interval16{{start: 0, length: 0xFFFF}}}
	}
	if count > arrayDefaultMaxSize {
		bc := newBitmapContainer()
		return bc.addRange(lo, hi)
	}
	ac := newArrayContainer()
	return ac.addRange(lo, hi)
}

func containerFromType(t containerType, src container) container {
	switch t {
	case typeArray:
		return arrayContainerFrom(src)
	case typeBitmap:
		return bitmapContainerFrom(src)
	case typeRun:
		return runContainerFrom(src)
	}
	panic(fmt.Sprintf("unknown container type %d", t))
}
