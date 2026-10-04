package roaring

import "math/bits"

const (
	// arrayMaxCardinality is the threshold above which an array
	// container is promoted to a bitmap container.
	arrayMaxCardinality = 4096
	// bitmapContainerWords is the number of uint64 words needed to
	// cover the full 16-bit container key space (1024 * 64 = 65536).
	bitmapContainerWords = 1024
)

const (
	typeArray = iota
	typeBitmap
	typeRun
)

// container stores the low 16 bits of a set of uint32 values that share
// the same high 16 bits. Containers published inside a Bitmap are
// immutable: writers always clone-modify-publish so readers never block.
type container interface {
	contains(x uint16) bool
	cardinality() int
	ctype() int
	clone() container
	// add/remove/addRange mutate the receiver in place and may return a
	// different (converted) container. Callers must own the receiver.
	add(x uint16) (c container, added bool)
	remove(x uint16) (c container, removed bool)
	addRange(lo, hi uint16) container // inclusive [lo, hi]
	runCount() int
	toArray() *arrayContainer
	toBitmap() *bitmapContainer
	toRun() *runContainer
}

// ---------------------------------------------------------------------------
// arrayContainer: sorted slice of uint16, used while cardinality <= 4096.
// ---------------------------------------------------------------------------

type arrayContainer struct {
	data []uint16 // sorted, no duplicates
}

func (ac *arrayContainer) ctype() int               { return typeArray }
func (ac *arrayContainer) cardinality() int         { return len(ac.data) }
func (ac *arrayContainer) toArray() *arrayContainer { return ac }

func (ac *arrayContainer) search(x uint16) int {
	lo, hi := 0, len(ac.data)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if ac.data[mid] < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (ac *arrayContainer) contains(x uint16) bool {
	i := ac.search(x)
	return i < len(ac.data) && ac.data[i] == x
}

func (ac *arrayContainer) clone() container {
	out := &arrayContainer{data: make([]uint16, len(ac.data))}
	copy(out.data, ac.data)
	return out
}

func (ac *arrayContainer) add(x uint16) (container, bool) {
	i := ac.search(x)
	if i < len(ac.data) && ac.data[i] == x {
		return ac, false
	}
	if len(ac.data) >= arrayMaxCardinality {
		bc := ac.toBitmap()
		bc.add(x)
		return bc, true
	}
	ac.data = append(ac.data, 0)
	copy(ac.data[i+1:], ac.data[i:])
	ac.data[i] = x
	return ac, true
}

func (ac *arrayContainer) remove(x uint16) (container, bool) {
	i := ac.search(x)
	if i >= len(ac.data) || ac.data[i] != x {
		return ac, false
	}
	copy(ac.data[i:], ac.data[i+1:])
	ac.data = ac.data[:len(ac.data)-1]
	return ac, true
}

func (ac *arrayContainer) addRange(lo, hi uint16) container {
	n := int(hi) - int(lo) + 1
	if len(ac.data)+n > arrayMaxCardinality {
		bc := ac.toBitmap()
		return bc.addRange(lo, hi)
	}
	out := make([]uint16, 0, len(ac.data)+n)
	i := 0
	for i < len(ac.data) && ac.data[i] < lo {
		out = append(out, ac.data[i])
		i++
	}
	for i < len(ac.data) && ac.data[i] <= hi {
		i++
	}
	for v := lo; ; v++ {
		out = append(out, v)
		if v == hi {
			break
		}
	}
	for ; i < len(ac.data); i++ {
		out = append(out, ac.data[i])
	}
	ac.data = out
	return ac
}

func (ac *arrayContainer) runCount() int {
	n := 0
	for i, v := range ac.data {
		if i == 0 || v != ac.data[i-1]+1 {
			n++
		}
	}
	return n
}

func (ac *arrayContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{card: len(ac.data)}
	for _, v := range ac.data {
		bc.bits[v>>6] |= uint64(1) << (v & 63)
	}
	return bc
}

func (ac *arrayContainer) toRun() *runContainer {
	rc := &runContainer{}
	for i := 0; i < len(ac.data); {
		j := i
		for j+1 < len(ac.data) && ac.data[j+1] == ac.data[j]+1 {
			j++
		}
		rc.runs = append(rc.runs, interval{ac.data[i], ac.data[j]})
		i = j + 1
	}
	return rc
}

// ---------------------------------------------------------------------------
// bitmapContainer: fixed 65536-bit (8 KiB) bitmap.
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	bits [bitmapContainerWords]uint64
	card int
}

func (bc *bitmapContainer) ctype() int                 { return typeBitmap }
func (bc *bitmapContainer) cardinality() int           { return bc.card }
func (bc *bitmapContainer) toBitmap() *bitmapContainer { return bc }

func (bc *bitmapContainer) contains(x uint16) bool {
	return bc.bits[x>>6]&(uint64(1)<<(x&63)) != 0
}

func (bc *bitmapContainer) clone() container {
	out := *bc
	return &out
}

func (bc *bitmapContainer) add(x uint16) (container, bool) {
	mask := uint64(1) << (x & 63)
	w := &bc.bits[x>>6]
	if *w&mask != 0 {
		return bc, false
	}
	*w |= mask
	bc.card++
	return bc, true
}

func (bc *bitmapContainer) remove(x uint16) (container, bool) {
	mask := uint64(1) << (x & 63)
	w := &bc.bits[x>>6]
	if *w&mask == 0 {
		return bc, false
	}
	*w &^= mask
	bc.card--
	return bc, true
}

func (bc *bitmapContainer) addRange(lo, hi uint16) container {
	firstWord := int(lo >> 6)
	lastWord := int(hi >> 6)
	if firstWord == lastWord {
		// hi-lo+1 <= 64; a shift of 64 yields 0, so (1<<64)-1 wraps to all
		// ones, which is exactly the mask we want for a full word.
		mask := ((uint64(1) << (hi - lo + 1)) - 1) << (lo & 63)
		bc.card += bits.OnesCount64(mask &^ bc.bits[firstWord])
		bc.bits[firstWord] |= mask
		return bc
	}
	headMask := ^uint64(0) << (lo & 63)
	tailMask := ^uint64(0) >> (63 - (hi & 63))
	bc.card += bits.OnesCount64(headMask &^ bc.bits[firstWord])
	bc.bits[firstWord] |= headMask
	for w := firstWord + 1; w < lastWord; w++ {
		bc.card += 64 - bits.OnesCount64(bc.bits[w])
		bc.bits[w] = ^uint64(0)
	}
	bc.card += bits.OnesCount64(tailMask &^ bc.bits[lastWord])
	bc.bits[lastWord] |= tailMask
	return bc
}

func (bc *bitmapContainer) runCount() int {
	runs := 0
	prevBit := uint64(0)
	for _, w := range bc.bits {
		t := bits.OnesCount64(w &^ (w << 1))
		if w&1 == 1 && prevBit == 1 {
			t--
		}
		runs += t
		prevBit = w >> 63
	}
	return runs
}

func (bc *bitmapContainer) toArray() *arrayContainer {
	ac := &arrayContainer{data: make([]uint16, 0, bc.card)}
	for i, w := range bc.bits {
		for w != 0 {
			t := bits.TrailingZeros64(w)
			ac.data = append(ac.data, uint16(i*64+t))
			w &= w - 1
		}
	}
	return ac
}

func (bc *bitmapContainer) toRun() *runContainer {
	rc := &runContainer{}
	for i := 0; i < bitmapContainerWords; i++ {
		w := bc.bits[i]
		off := 0 // bits of this word already consumed
		for w != 0 {
			t := bits.TrailingZeros64(w)
			rl := bits.TrailingZeros64(^(w >> uint(t)))
			start := uint16(i*64 + off + t)
			last := start + uint16(rl) - 1
			if n := len(rc.runs); n > 0 && rc.runs[n-1].last == start-1 {
				rc.runs[n-1].last = last
			} else {
				rc.runs = append(rc.runs, interval{start, last})
			}
			off += t + rl
			w >>= uint(t + rl) // t+rl == 64 shifts the word to zero
		}
	}
	return rc
}

// ---------------------------------------------------------------------------
// runContainer: run-length encoded sorted, non-adjacent intervals.
// ---------------------------------------------------------------------------

type interval struct {
	start uint16
	last  uint16
}

type runContainer struct {
	runs []interval // sorted, disjoint, non-adjacent
}

func (rc *runContainer) ctype() int           { return typeRun }
func (rc *runContainer) toRun() *runContainer { return rc }

func (rc *runContainer) cardinality() int {
	n := 0
	for _, r := range rc.runs {
		n += int(r.last) - int(r.start) + 1
	}
	return n
}

func (rc *runContainer) runCount() int { return len(rc.runs) }

func (rc *runContainer) contains(x uint16) bool {
	lo, hi := 0, len(rc.runs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if rc.runs[mid].last < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo < len(rc.runs) && rc.runs[lo].start <= x
}

func (rc *runContainer) clone() container {
	out := &runContainer{runs: make([]interval, len(rc.runs))}
	copy(out.runs, rc.runs)
	return out
}

func (rc *runContainer) add(x uint16) (container, bool) {
	runs := rc.runs
	lo, hi := 0, len(runs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if runs[mid].last < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(runs) && runs[lo].start <= x {
		return rc, false
	}
	leftMerge := lo > 0 && runs[lo-1].last == x-1
	rightMerge := lo < len(runs) && runs[lo].start == x+1
	switch {
	case leftMerge && rightMerge:
		runs[lo-1].last = runs[lo].last
		rc.runs = append(runs[:lo], runs[lo+1:]...)
	case leftMerge:
		runs[lo-1].last = x
	case rightMerge:
		runs[lo].start = x
	default:
		rc.runs = append(runs, interval{})
		copy(rc.runs[lo+1:], runs[lo:])
		rc.runs[lo] = interval{x, x}
	}
	return rc, true
}

func (rc *runContainer) remove(x uint16) (container, bool) {
	lo, hi := 0, len(rc.runs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if rc.runs[mid].last < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(rc.runs) || rc.runs[lo].start > x {
		return rc, false
	}
	r := rc.runs[lo]
	switch {
	case r.start == x && r.last == x:
		rc.runs = append(rc.runs[:lo], rc.runs[lo+1:]...)
	case r.start == x:
		rc.runs[lo].start = x + 1
	case r.last == x:
		rc.runs[lo].last = x - 1
	default:
		rc.runs = append(rc.runs, interval{})
		copy(rc.runs[lo+2:], rc.runs[lo+1:])
		rc.runs[lo] = interval{r.start, x - 1}
		rc.runs[lo+1] = interval{x + 1, r.last}
	}
	return rc, true
}

func (rc *runContainer) addRange(lo16, hi16 uint16) container {
	lo, hi := int(lo16), int(hi16)
	runs := rc.runs
	i := 0
	for i < len(runs) && int(runs[i].last) < lo-1 {
		i++
	}
	j := i
	nlo, nhi := lo, hi
	for j < len(runs) && int(runs[j].start) <= hi+1 {
		if int(runs[j].start) < nlo {
			nlo = int(runs[j].start)
		}
		if int(runs[j].last) > nhi {
			nhi = int(runs[j].last)
		}
		j++
	}
	// Build a fresh slice: appending into runs[:i] could clobber runs[j]
	// when no existing run was absorbed (i == j).
	out := make([]interval, 0, len(runs)-(j-i)+1)
	out = append(out, runs[:i]...)
	out = append(out, interval{uint16(nlo), uint16(nhi)})
	out = append(out, runs[j:]...)
	rc.runs = out
	return rc
}

func (rc *runContainer) toArray() *arrayContainer {
	ac := &arrayContainer{data: make([]uint16, 0, rc.cardinality())}
	for _, r := range rc.runs {
		for v := r.start; ; v++ {
			ac.data = append(ac.data, v)
			if v == r.last {
				break
			}
		}
	}
	return ac
}

func (rc *runContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{}
	for _, r := range rc.runs {
		bc.addRange(r.start, r.last)
	}
	return bc
}

// ---------------------------------------------------------------------------
// normalize picks the most compact representation for a container.
// It returns nil for an empty container.
// ---------------------------------------------------------------------------

func serializedSize(c container, card int) int {
	switch c.(type) {
	case *arrayContainer:
		return 2 * card
	case *bitmapContainer:
		return 8 * bitmapContainerWords
	case *runContainer:
		return 4 * len(c.(*runContainer).runs)
	}
	return 0
}

func normalize(c container) container {
	card := c.cardinality()
	if card == 0 {
		return nil
	}
	cur := c
	if bc, ok := cur.(*bitmapContainer); ok && card <= arrayMaxCardinality {
		cur = bc.toArray()
	}
	runs := cur.runCount()
	curBytes := serializedSize(cur, card)
	runBytes := 4 * runs
	if rc, ok := cur.(*runContainer); ok {
		if runBytes <= curBytes {
			return rc
		}
		if card <= arrayMaxCardinality {
			return rc.toArray()
		}
		return rc.toBitmap()
	}
	if runBytes < curBytes {
		return cur.toRun()
	}
	return cur
}

// nonRun converts a run container to its canonical non-run form so that
// mixed-type operations only need the array/bitmap code paths.
func nonRun(c container) container {
	if rc, ok := c.(*runContainer); ok {
		if rc.cardinality() <= arrayMaxCardinality {
			return rc.toArray()
		}
		return rc.toBitmap()
	}
	return c
}
