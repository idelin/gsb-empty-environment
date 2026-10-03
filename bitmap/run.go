package bitmap

import "sort"

// runContainer stores sorted, non-adjacent inclusive intervals.
type runContainer struct {
	runs []interval16
}

func (rc *runContainer) typeName() string { return "run" }

func (rc *runContainer) cardinality() int {
	n := 0
	for _, r := range rc.runs {
		n += r.length()
	}
	return n
}

func (rc *runContainer) contains(x uint16) bool {
	i := sort.Search(len(rc.runs), func(i int) bool { return rc.runs[i].last >= x })
	return i < len(rc.runs) && rc.runs[i].start <= x
}

func (rc *runContainer) add(x uint16) (container, bool) {
	return rc.addRange(x, x)
}

func (rc *runContainer) addRange(lo, hi uint16) (container, bool) {
	before := rc.cardinality()
	out := make([]interval16, 0, len(rc.runs)+1)
	merged := interval16{lo, hi}
	placed := false
	for _, r := range rc.runs {
		switch {
		case uint32(r.last)+1 < uint32(merged.start):
			out = append(out, r)
		case uint32(merged.last)+1 < uint32(r.start):
			if !placed {
				out = append(out, merged)
				placed = true
			}
			out = append(out, r)
		default:
			if r.start < merged.start {
				merged.start = r.start
			}
			if r.last > merged.last {
				merged.last = r.last
			}
		}
	}
	if !placed {
		out = append(out, merged)
	}
	rc.runs = out
	if len(rc.runs)*runLengthOverhead > bitmapContainerBytes {
		return rc.toBitmap(), rc.cardinality() != before
	}
	return rc, rc.cardinality() != before
}

func (rc *runContainer) toBitmap() *bitmapContainer {
	bc := newBitmapContainer()
	for _, r := range rc.runs {
		for v := uint32(r.start); v <= uint32(r.last); v++ {
			bc.set(uint16(v))
		}
	}
	return bc
}

func (rc *runContainer) foreach(fn func(x uint16) bool) {
	for _, r := range rc.runs {
		for v := uint32(r.start); v <= uint32(r.last); v++ {
			if !fn(uint16(v)) {
				return
			}
		}
	}
}

type runIterator struct {
	runs []interval16
	ri   int
	cur  uint32
}

func (rc *runContainer) iter() containerIterator {
	it := &runIterator{runs: rc.runs}
	if len(rc.runs) > 0 {
		it.cur = uint32(rc.runs[0].start)
	}
	return it
}

func (it *runIterator) next() (uint16, bool) {
	if it.ri >= len(it.runs) {
		return 0, false
	}
	v := it.cur
	if v >= uint32(it.runs[it.ri].last) {
		it.ri++
		if it.ri < len(it.runs) {
			it.cur = uint32(it.runs[it.ri].start)
		}
	} else {
		it.cur++
	}
	return uint16(v), true
}

func (rc *runContainer) equals(o container) bool {
	oc, ok := o.(*runContainer)
	if !ok || len(oc.runs) != len(rc.runs) {
		return false
	}
	for i, r := range rc.runs {
		if oc.runs[i] != r {
			return false
		}
	}
	return true
}

func (rc *runContainer) clone() container {
	return &runContainer{runs: append([]interval16(nil), rc.runs...)}
}
