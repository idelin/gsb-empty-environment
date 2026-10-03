package roaring

import (
	"fmt"
	"math/bits"
)

// bitmapContainer stores a full 65536-bit bitmap as 1024 uint64 words.
type bitmapContainer struct {
	bitmap []uint64
	card   int
}

func newBitmapContainer() *bitmapContainer {
	return &bitmapContainer{bitmap: make([]uint64, bitmapContainerWords)}
}

func (bc *bitmapContainer) ctype() containerType { return typeBitmap }
func (bc *bitmapContainer) cardinality() int     { return bc.card }
func (bc *bitmapContainer) sizeInBytes() int     { return 8 * len(bc.bitmap) }

func (bc *bitmapContainer) contains(x uint16) bool {
	return bc.bitmap[x>>6]&(uint64(1)<<(x&63)) != 0
}

func (bc *bitmapContainer) set(x uint16) {
	w := x >> 6
	mask := uint64(1) << (x & 63)
	if bc.bitmap[w]&mask == 0 {
		bc.bitmap[w] |= mask
		bc.card++
	}
}

func (bc *bitmapContainer) unset(x uint16) {
	w := x >> 6
	mask := uint64(1) << (x & 63)
	if bc.bitmap[w]&mask != 0 {
		bc.bitmap[w] &^= mask
		bc.card--
	}
}

func (bc *bitmapContainer) add(x uint16) (container, bool) {
	if bc.contains(x) {
		return bc, false
	}
	bc.set(x)
	return bc, true
}

func (bc *bitmapContainer) remove(x uint16) (container, bool) {
	if !bc.contains(x) {
		return bc, false
	}
	bc.unset(x)
	return bc, true
}

// rangeMask returns a word with bits [lo, hi] (both in [0,63]) set.
func rangeMask(lo, hi uint16) uint64 {
	return (^uint64(0) << (lo & 63)) & (^uint64(0) >> (63 - (hi & 63)))
}

func (bc *bitmapContainer) addRange(lo, hi uint16) container {
	loW, hiW := lo>>6, hi>>6
	if loW == hiW {
		mask := rangeMask(lo, hi)
		bc.card += bits.OnesCount64(mask &^ bc.bitmap[loW])
		bc.bitmap[loW] |= mask
		return bc
	}
	head := rangeMask(lo, 63)
	bc.card += bits.OnesCount64(head &^ bc.bitmap[loW])
	bc.bitmap[loW] |= head
	for w := loW + 1; w < hiW; w++ {
		bc.card += 64 - bits.OnesCount64(bc.bitmap[w])
		bc.bitmap[w] = ^uint64(0)
	}
	tail := rangeMask(0, hi)
	bc.card += bits.OnesCount64(tail &^ bc.bitmap[hiW])
	bc.bitmap[hiW] |= tail
	return bc
}

func (bc *bitmapContainer) removeRange(lo, hi uint16) container {
	loW, hiW := lo>>6, hi>>6
	if loW == hiW {
		mask := rangeMask(lo, hi)
		bc.card -= bits.OnesCount64(mask & bc.bitmap[loW])
		bc.bitmap[loW] &^= mask
		return bc
	}
	head := rangeMask(lo, 63)
	bc.card -= bits.OnesCount64(head & bc.bitmap[loW])
	bc.bitmap[loW] &^= head
	for w := loW + 1; w < hiW; w++ {
		bc.card -= bits.OnesCount64(bc.bitmap[w])
		bc.bitmap[w] = 0
	}
	tail := rangeMask(0, hi)
	bc.card -= bits.OnesCount64(tail & bc.bitmap[hiW])
	bc.bitmap[hiW] &^= tail
	return bc
}

func (bc *bitmapContainer) forEach(fn func(x uint16)) {
	for w := 0; w < len(bc.bitmap); w++ {
		word := bc.bitmap[w]
		for word != 0 {
			bit := bits.TrailingZeros64(word)
			fn(uint16(w<<6 + bit))
			word &= word - 1
		}
	}
}

func (bc *bitmapContainer) clone() container {
	out := &bitmapContainer{bitmap: make([]uint64, len(bc.bitmap)), card: bc.card}
	copy(out.bitmap, bc.bitmap)
	return out
}

func (bc *bitmapContainer) minimum() uint16 {
	for w := 0; w < len(bc.bitmap); w++ {
		if bc.bitmap[w] != 0 {
			return uint16(w<<6 + bits.TrailingZeros64(bc.bitmap[w]))
		}
	}
	return 0
}

func (bc *bitmapContainer) maximum() uint16 {
	for w := len(bc.bitmap) - 1; w >= 0; w-- {
		if bc.bitmap[w] != 0 {
			return uint16(w<<6 + 63 - bits.LeadingZeros64(bc.bitmap[w]))
		}
	}
	return 0
}

func (bc *bitmapContainer) numRuns() int {
	runs := 0
	prev := uint64(0)
	for _, w := range bc.bitmap {
		shifted := (w << 1) | (prev >> 63)
		runs += bits.OnesCount64(w &^ shifted)
		prev = w
	}
	return runs
}

func (bc *bitmapContainer) optimize() container {
	t := pickContainerType(bc.card, bc.numRuns())
	if t == typeBitmap {
		return bc
	}
	return containerFromType(t, bc)
}

func (bc *bitmapContainer) validate() error {
	card := 0
	for _, w := range bc.bitmap {
		card += bits.OnesCount64(w)
	}
	if card != bc.card {
		return fmt.Errorf("bitmap container cardinality mismatch: bookkept %d, actual %d", bc.card, card)
	}
	if bc.card == 0 {
		return fmt.Errorf("bitmap container is empty")
	}
	return nil
}
