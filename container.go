package cbitmap

import (
	"math/bits"
	"sort"
)

const (
	// arrayMaxSize is the cardinality above which an array container is
	// converted into a bitmap container.
	arrayMaxSize = 4096
	// runMaxCount is the number of runs above which a run container is no
	// longer considered compact and is converted into a bitmap container.
	runMaxCount = 2048

	containerWidth = 1 << 16
	bitmapWords    = containerWidth / 64
)

// interval is an inclusive [start, last] range of 16-bit values.
type interval struct {
	start uint16
	last  uint16
}

func (iv interval) cardinality() int { return int(iv.last) - int(iv.start) + 1 }

// container stores the low 16 bits of a set of uint32 values that share the
// same high 16 bits.
type container interface {
	cardinality() int
	contains(x uint16) bool
	// add returns the (possibly converted) container and true if x was new.
	add(x uint16) (container, bool)
	// remove returns the (possibly converted) container and true if x existed.
	remove(x uint16) (container, bool)
	// addRange adds the inclusive range [start, last].
	addRange(start, last uint16) container
	clone() container
	// numRuns returns how many runs an RLE encoding would need.
	numRuns() int
	// sizeInBytes approximates the in-memory footprint.
	sizeInBytes() int
	toRun() *runContainer
	toBitmap() *bitmapContainer
	toArray() *arrayContainer
	appendTo(base uint32, out []uint32) []uint32
}

// containerKind names the concrete container implementation.
func containerKind(c container) string {
	switch c.(type) {
	case *arrayContainer:
		return "array"
	case *bitmapContainer:
		return "bitmap"
	case *runContainer:
		return "run"
	}
	return "unknown"
}

// bitmask returns a 64-bit mask with bits [lo, hi] set.
func bitmask(lo, hi int) uint64 {
	return (^uint64(0) << uint(lo)) & (^uint64(0) >> uint(63-hi))
}

// ---------------------------------------------------------------------------
// arrayContainer
// ---------------------------------------------------------------------------

type arrayContainer struct {
	data []uint16 // sorted, unique
}

func (ac *arrayContainer) cardinality() int { return len(ac.data) }

func (ac *arrayContainer) search(x uint16) (int, bool) {
	i := sort.Search(len(ac.data), func(i int) bool { return ac.data[i] >= x })
	if i < len(ac.data) && ac.data[i] == x {
		return i, true
	}
	return i, false
}

func (ac *arrayContainer) contains(x uint16) bool {
	_, ok := ac.search(x)
	return ok
}

func (ac *arrayContainer) add(x uint16) (container, bool) {
	i, found := ac.search(x)
	if found {
		return ac, false
	}
	if len(ac.data) >= arrayMaxSize {
		bc := ac.toBitmap()
		bc.set(x)
		return bc, true
	}
	ac.data = append(ac.data, 0)
	copy(ac.data[i+1:], ac.data[i:])
	ac.data[i] = x
	return ac, true
}

func (ac *arrayContainer) remove(x uint16) (container, bool) {
	i, found := ac.search(x)
	if !found {
		return ac, false
	}
	ac.data = append(ac.data[:i], ac.data[i+1:]...)
	return ac, true
}

func (ac *arrayContainer) addRange(start, last uint16) container {
	n := int(last) - int(start) + 1
	if len(ac.data)+n > arrayMaxSize {
		bc := ac.toBitmap()
		bc.setRange(int(start), int(last))
		return bc
	}
	for v := int(start); v <= int(last); v++ {
		ac.add(uint16(v)) // guaranteed to stay an array container
	}
	return ac
}

func (ac *arrayContainer) clone() container {
	nd := make([]uint16, len(ac.data))
	copy(nd, ac.data)
	return &arrayContainer{data: nd}
}

func (ac *arrayContainer) numRuns() int {
	if len(ac.data) == 0 {
		return 0
	}
	runs := 1
	for i := 1; i < len(ac.data); i++ {
		if ac.data[i] != ac.data[i-1]+1 {
			runs++
		}
	}
	return runs
}

func (ac *arrayContainer) sizeInBytes() int { return 2 * len(ac.data) }

func (ac *arrayContainer) toRun() *runContainer {
	rc := &runContainer{}
	if len(ac.data) == 0 {
		return rc
	}
	cur := interval{ac.data[0], ac.data[0]}
	for i := 1; i < len(ac.data); i++ {
		if ac.data[i] == cur.last+1 {
			cur.last = ac.data[i]
		} else {
			rc.runs = append(rc.runs, cur)
			cur = interval{ac.data[i], ac.data[i]}
		}
	}
	rc.runs = append(rc.runs, cur)
	rc.card = len(ac.data)
	return rc
}

func (ac *arrayContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{}
	for _, v := range ac.data {
		bc.words[v>>6] |= uint64(1) << (v & 63)
	}
	bc.card = len(ac.data)
	return bc
}

func (ac *arrayContainer) toArray() *arrayContainer { return ac }

func (ac *arrayContainer) appendTo(base uint32, out []uint32) []uint32 {
	for _, v := range ac.data {
		out = append(out, base|uint32(v))
	}
	return out
}

// ---------------------------------------------------------------------------
// bitmapContainer
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	words [bitmapWords]uint64
	card  int
}

func (bc *bitmapContainer) cardinality() int { return bc.card }

func (bc *bitmapContainer) contains(x uint16) bool {
	return bc.words[x>>6]>>(x&63)&1 == 1
}

// set sets the bit for x, returning true if it was not already set.
func (bc *bitmapContainer) set(x uint16) bool {
	w := &bc.words[x>>6]
	mask := uint64(1) << (x & 63)
	if *w&mask != 0 {
		return false
	}
	*w |= mask
	bc.card++
	return true
}

func (bc *bitmapContainer) add(x uint16) (container, bool) {
	return bc, bc.set(x)
}

func (bc *bitmapContainer) remove(x uint16) (container, bool) {
	w := &bc.words[x>>6]
	mask := uint64(1) << (x & 63)
	if *w&mask == 0 {
		return bc, false
	}
	*w &^= mask
	bc.card--
	if bc.card <= arrayMaxSize {
		return bc.toArray(), true
	}
	return bc, true
}

// setRange sets all bits in the inclusive range [start, last].
func (bc *bitmapContainer) setRange(start, last int) {
	wi, wj := start>>6, last>>6
	for w := wi; w <= wj; w++ {
		lo, hi := 0, 63
		if w == wi {
			lo = start & 63
		}
		if w == wj {
			hi = last & 63
		}
		mask := bitmask(lo, hi)
		bc.card += bits.OnesCount64(mask &^ bc.words[w])
		bc.words[w] |= mask
	}
}

// flipRange toggles all bits in the inclusive range [start, last].
func (bc *bitmapContainer) flipRange(start, last int) {
	wi, wj := start>>6, last>>6
	for w := wi; w <= wj; w++ {
		lo, hi := 0, 63
		if w == wi {
			lo = start & 63
		}
		if w == wj {
			hi = last & 63
		}
		mask := bitmask(lo, hi)
		bc.card += bits.OnesCount64(mask) - 2*bits.OnesCount64(bc.words[w]&mask)
		bc.words[w] ^= mask
	}
}

func (bc *bitmapContainer) addRange(start, last uint16) container {
	bc.setRange(int(start), int(last))
	return bc
}

func (bc *bitmapContainer) clone() container {
	nb := *bc
	return &nb
}

func (bc *bitmapContainer) numRuns() int {
	runs := 0
	prev := uint64(0)
	for _, w := range bc.words {
		shifted := (w << 1) | (prev >> 63)
		runs += bits.OnesCount64(w &^ shifted)
		prev = w
	}
	return runs
}

func (bc *bitmapContainer) sizeInBytes() int { return 8 * bitmapWords }

func (bc *bitmapContainer) toRun() *runContainer {
	rc := &runContainer{}
	open := false
	var cur interval
	for wi := 0; wi < bitmapWords; wi++ {
		w := bc.words[wi]
		for w != 0 {
			pos := wi*64 + bits.TrailingZeros64(w)
			if !open {
				cur = interval{uint16(pos), uint16(pos)}
				open = true
			} else if pos == int(cur.last)+1 {
				cur.last = uint16(pos)
			} else {
				rc.runs = append(rc.runs, cur)
				cur = interval{uint16(pos), uint16(pos)}
			}
			w &= w - 1
		}
	}
	if open {
		rc.runs = append(rc.runs, cur)
	}
	rc.card = bc.card
	return rc
}

func (bc *bitmapContainer) toBitmap() *bitmapContainer { return bc }

func (bc *bitmapContainer) toArray() *arrayContainer {
	ac := &arrayContainer{data: make([]uint16, 0, bc.card)}
	for wi := 0; wi < bitmapWords; wi++ {
		w := bc.words[wi]
		for w != 0 {
			ac.data = append(ac.data, uint16(wi*64+bits.TrailingZeros64(w)))
			w &= w - 1
		}
	}
	return ac
}

func (bc *bitmapContainer) appendTo(base uint32, out []uint32) []uint32 {
	for wi := 0; wi < bitmapWords; wi++ {
		w := bc.words[wi]
		for w != 0 {
			out = append(out, base|uint32(wi*64+bits.TrailingZeros64(w)))
			w &= w - 1
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// runContainer
// ---------------------------------------------------------------------------

type runContainer struct {
	runs []interval // sorted, non-overlapping, non-adjacent
	card int
}

func (rc *runContainer) cardinality() int { return rc.card }

func (rc *runContainer) recomputeCard() {
	c := 0
	for _, r := range rc.runs {
		c += r.cardinality()
	}
	rc.card = c
}

func (rc *runContainer) contains(x uint16) bool {
	i := sort.Search(len(rc.runs), func(i int) bool { return rc.runs[i].last >= x })
	return i < len(rc.runs) && rc.runs[i].start <= x
}

func (rc *runContainer) add(x uint16) (container, bool) {
	xi := int(x)
	i := sort.Search(len(rc.runs), func(i int) bool { return int(rc.runs[i].last)+1 >= xi })
	if i == len(rc.runs) {
		rc.runs = append(rc.runs, interval{x, x})
		rc.card++
		return rc.maybeConvert()
	}
	r := rc.runs[i]
	if xi >= int(r.start) && xi <= int(r.last) {
		return rc, false
	}
	if xi == int(r.last)+1 {
		rc.runs[i].last = x
		if i+1 < len(rc.runs) && int(rc.runs[i+1].start) == xi+1 {
			rc.runs[i].last = rc.runs[i+1].last
			rc.runs = append(rc.runs[:i+1], rc.runs[i+2:]...)
		}
		rc.card++
		return rc.maybeConvert()
	}
	if xi == int(r.start)-1 {
		rc.runs[i].start = x
		rc.card++
		return rc.maybeConvert()
	}
	rc.runs = append(rc.runs, interval{})
	copy(rc.runs[i+1:], rc.runs[i:])
	rc.runs[i] = interval{x, x}
	rc.card++
	return rc.maybeConvert()
}

func (rc *runContainer) maybeConvert() (container, bool) {
	if len(rc.runs) > runMaxCount {
		return rc.toBitmap(), true
	}
	return rc, true
}

func (rc *runContainer) remove(x uint16) (container, bool) {
	xi := int(x)
	i := sort.Search(len(rc.runs), func(i int) bool { return int(rc.runs[i].last) >= xi })
	if i == len(rc.runs) {
		return rc, false
	}
	r := rc.runs[i]
	if xi < int(r.start) {
		return rc, false
	}
	rc.card--
	switch {
	case r.start == x && r.last == x:
		rc.runs = append(rc.runs[:i], rc.runs[i+1:]...)
	case r.start == x:
		rc.runs[i].start = x + 1
	case r.last == x:
		rc.runs[i].last = x - 1
	default:
		right := interval{x + 1, r.last}
		rc.runs[i].last = x - 1
		rc.runs = append(rc.runs, interval{})
		copy(rc.runs[i+2:], rc.runs[i+1:])
		rc.runs[i+1] = right
	}
	return rc, true
}

func (rc *runContainer) addRange(start, last uint16) container {
	i := sort.Search(len(rc.runs), func(i int) bool { return int(rc.runs[i].last)+1 >= int(start) })
	j := i
	newStart, newLast := start, last
	for j < len(rc.runs) && int(rc.runs[j].start) <= int(newLast)+1 {
		if rc.runs[j].start < newStart {
			newStart = rc.runs[j].start
		}
		if rc.runs[j].last > newLast {
			newLast = rc.runs[j].last
		}
		j++
	}
	merged := make([]interval, 0, len(rc.runs)-(j-i)+1)
	merged = append(merged, rc.runs[:i]...)
	merged = append(merged, interval{newStart, newLast})
	merged = append(merged, rc.runs[j:]...)
	rc.runs = merged
	rc.recomputeCard()
	if len(rc.runs) > runMaxCount {
		return rc.toBitmap()
	}
	return rc
}

func (rc *runContainer) clone() container {
	nr := make([]interval, len(rc.runs))
	copy(nr, rc.runs)
	return &runContainer{runs: nr, card: rc.card}
}

func (rc *runContainer) numRuns() int { return len(rc.runs) }

func (rc *runContainer) sizeInBytes() int { return 4*len(rc.runs) + 4 }

func (rc *runContainer) toRun() *runContainer { return rc }

func (rc *runContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{}
	for _, r := range rc.runs {
		bc.setRange(int(r.start), int(r.last))
	}
	return bc
}

func (rc *runContainer) toArray() *arrayContainer {
	ac := &arrayContainer{data: make([]uint16, 0, rc.card)}
	for _, r := range rc.runs {
		for v := int(r.start); v <= int(r.last); v++ {
			ac.data = append(ac.data, uint16(v))
		}
	}
	return ac
}

func (rc *runContainer) appendTo(base uint32, out []uint32) []uint32 {
	for _, r := range rc.runs {
		for v := int(r.start); v <= int(r.last); v++ {
			out = append(out, base|uint32(v))
		}
	}
	return out
}
