package bitmap

import (
	"math/bits"
	"sort"
)

const (
	// arrayMaxCardinality is the threshold above which an array
	// container is promoted to a bitmap container.
	arrayMaxCardinality = 4096
	// bitmapContainerBytes is the fixed memory footprint of a bitmap container.
	bitmapContainerBytes = 8192
)

// run is an inclusive interval [start, last] of 16-bit values.
type run struct {
	start uint16
	last  uint16
}

// container stores the low 16 bits of a set of uint32 keys that
// share the same high 16 bits.
type container interface {
	cardinality() int
	contains(v uint16) bool
	// add inserts v and returns the (possibly converted) container.
	add(v uint16) container
	// optimize returns the most compact equivalent container.
	optimize() container
	toSlice() []uint16
	clone() container
}

// ---------------- array container ----------------

type arrayContainer struct {
	data []uint16 // sorted, unique
}

func (ac *arrayContainer) cardinality() int { return len(ac.data) }

func (ac *arrayContainer) search(v uint16) (int, bool) {
	i := sort.Search(len(ac.data), func(i int) bool { return ac.data[i] >= v })
	return i, i < len(ac.data) && ac.data[i] == v
}

func (ac *arrayContainer) contains(v uint16) bool {
	_, ok := ac.search(v)
	return ok
}

func (ac *arrayContainer) add(v uint16) container {
	i, ok := ac.search(v)
	if ok {
		return ac
	}
	ac.data = append(ac.data, 0)
	copy(ac.data[i+1:], ac.data[i:])
	ac.data[i] = v
	if len(ac.data) > arrayMaxCardinality {
		return ac.toBitmap()
	}
	return ac
}

func (ac *arrayContainer) optimize() container {
	rc := ac.toRun()
	if 4*len(rc.runs) < 2*len(ac.data) {
		return rc
	}
	return ac
}

func (ac *arrayContainer) toSlice() []uint16 {
	out := make([]uint16, len(ac.data))
	copy(out, ac.data)
	return out
}

func (ac *arrayContainer) clone() container {
	return &arrayContainer{data: ac.toSlice()}
}

func (ac *arrayContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{}
	for _, v := range ac.data {
		bc.words[v>>6] |= 1 << (v & 63)
	}
	bc.card = len(ac.data)
	return bc
}

func (ac *arrayContainer) toRun() *runContainer {
	rc := &runContainer{}
	if len(ac.data) == 0 {
		return rc
	}
	start, prev := ac.data[0], ac.data[0]
	for _, v := range ac.data[1:] {
		if v == prev+1 {
			prev = v
			continue
		}
		rc.runs = append(rc.runs, run{start, prev})
		start, prev = v, v
	}
	rc.runs = append(rc.runs, run{start, prev})
	return rc
}

// ---------------- bitmap container ----------------

type bitmapContainer struct {
	words [1024]uint64
	card  int
}

func (bc *bitmapContainer) cardinality() int { return bc.card }

func (bc *bitmapContainer) contains(v uint16) bool {
	return bc.words[v>>6]&(1<<(v&63)) != 0
}

func (bc *bitmapContainer) add(v uint16) container {
	mask := uint64(1) << (v & 63)
	w := &bc.words[v>>6]
	if *w&mask == 0 {
		*w |= mask
		bc.card++
	}
	return bc
}

func (bc *bitmapContainer) optimize() container {
	if bc.card <= arrayMaxCardinality {
		ac := bc.toArray()
		return ac.optimize()
	}
	rc := bc.toRun()
	if 4*len(rc.runs) < bitmapContainerBytes {
		return rc
	}
	return bc
}

func (bc *bitmapContainer) toSlice() []uint16 {
	out := make([]uint16, 0, bc.card)
	for i, w := range bc.words {
		for w != 0 {
			b := bits.TrailingZeros64(w)
			out = append(out, uint16(i<<6+b))
			w &= w - 1
		}
	}
	return out
}

func (bc *bitmapContainer) clone() container {
	out := &bitmapContainer{card: bc.card}
	copy(out.words[:], bc.words[:])
	return out
}

func (bc *bitmapContainer) toArray() *arrayContainer {
	return &arrayContainer{data: bc.toSlice()}
}

func (bc *bitmapContainer) toRun() *runContainer {
	rc := &runContainer{}
	inRun := false
	var start uint16
	for i := 0; i < 65536; i++ {
		v := uint16(i)
		if bc.contains(v) {
			if !inRun {
				inRun = true
				start = v
			}
		} else if inRun {
			inRun = false
			rc.runs = append(rc.runs, run{start, v - 1})
		}
	}
	if inRun {
		rc.runs = append(rc.runs, run{start, 0xFFFF})
	}
	return rc
}

// ---------------- run container (RLE) ----------------

type runContainer struct {
	runs []run // sorted, disjoint, non-adjacent
}

func (rc *runContainer) cardinality() int {
	n := 0
	for _, r := range rc.runs {
		n += int(r.last) - int(r.start) + 1
	}
	return n
}

func (rc *runContainer) contains(v uint16) bool {
	i := sort.Search(len(rc.runs), func(i int) bool { return rc.runs[i].last >= v })
	return i < len(rc.runs) && rc.runs[i].start <= v
}

func (rc *runContainer) add(v uint16) container {
	// Find the first run whose last+1 >= v: v may extend or merge it.
	i := sort.Search(len(rc.runs), func(i int) bool {
		return int(rc.runs[i].last)+1 >= int(v)
	})
	if i < len(rc.runs) && int(rc.runs[i].start)-1 <= int(v) {
		r := &rc.runs[i]
		if v < r.start {
			r.start = v
		}
		if v > r.last {
			r.last = v
		}
		// Merge with the following run if they now touch.
		if i+1 < len(rc.runs) && int(rc.runs[i].last)+1 >= int(rc.runs[i+1].start) {
			rc.runs[i].last = rc.runs[i+1].last
			rc.runs = append(rc.runs[:i+1], rc.runs[i+2:]...)
		}
		// Merge with the previous run if they now touch.
		if i > 0 && int(rc.runs[i-1].last)+1 >= int(rc.runs[i].start) {
			rc.runs[i-1].last = rc.runs[i].last
			rc.runs = append(rc.runs[:i], rc.runs[i+1:]...)
		}
		return rc
	}
	rc.runs = append(rc.runs, run{})
	copy(rc.runs[i+1:], rc.runs[i:])
	rc.runs[i] = run{v, v}
	return rc
}

func (rc *runContainer) optimize() container {
	card := rc.cardinality()
	if card <= arrayMaxCardinality && 2*card < 4*len(rc.runs) {
		return rc.toArray()
	}
	if 4*len(rc.runs) > bitmapContainerBytes {
		return rc.toBitmap()
	}
	return rc
}

func (rc *runContainer) toSlice() []uint16 {
	out := make([]uint16, 0, rc.cardinality())
	for _, r := range rc.runs {
		for v := r.start; ; v++ {
			out = append(out, v)
			if v == r.last {
				break
			}
		}
	}
	return out
}

func (rc *runContainer) clone() container {
	out := &runContainer{runs: make([]run, len(rc.runs))}
	copy(out.runs, rc.runs)
	return out
}

func (rc *runContainer) toArray() *arrayContainer {
	return &arrayContainer{data: rc.toSlice()}
}

func (rc *runContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{}
	for _, r := range rc.runs {
		for v := r.start; ; v++ {
			bc.words[v>>6] |= 1 << (v & 63)
			bc.card++
			if v == r.last {
				break
			}
		}
	}
	return bc
}
