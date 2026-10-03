package bitmap

import "sort"

// arrayContainer stores up to arrayMaxSize sorted 16-bit values.
type arrayContainer struct {
	data []uint16
}

func (ac *arrayContainer) typeName() string { return "array" }

func (ac *arrayContainer) cardinality() int { return len(ac.data) }

func (ac *arrayContainer) search(x uint16) (int, bool) {
	i := sort.Search(len(ac.data), func(i int) bool { return ac.data[i] >= x })
	return i, i < len(ac.data) && ac.data[i] == x
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
	ac.data = append(ac.data, 0)
	copy(ac.data[i+1:], ac.data[i:])
	ac.data[i] = x
	if len(ac.data) > arrayMaxSize {
		return ac.toBitmap(), true
	}
	return ac, true
}

func (ac *arrayContainer) addRange(lo, hi uint16) (container, bool) {
	n := int(hi) - int(lo) + 1
	if len(ac.data)+n > arrayMaxSize {
		bc := ac.toBitmap()
		return bc.addRange(lo, hi)
	}
	out := make([]uint16, 0, len(ac.data)+n)
	i := 0
	for i < len(ac.data) && ac.data[i] < lo {
		out = append(out, ac.data[i])
		i++
	}
	for v := lo; ; v++ {
		out = append(out, v)
		if v == hi {
			break
		}
	}
	for i < len(ac.data) && ac.data[i] <= hi {
		i++
	}
	out = append(out, ac.data[i:]...)
	changed := len(out) != len(ac.data)
	ac.data = out
	return ac, changed
}

func (ac *arrayContainer) toBitmap() *bitmapContainer {
	bc := newBitmapContainer()
	for _, v := range ac.data {
		bc.set(v)
	}
	return bc
}

func (ac *arrayContainer) foreach(fn func(x uint16) bool) {
	for _, v := range ac.data {
		if !fn(v) {
			return
		}
	}
}

type arrayIterator struct {
	data []uint16
	i    int
}

func (ac *arrayContainer) iter() containerIterator {
	return &arrayIterator{data: ac.data}
}

func (it *arrayIterator) next() (uint16, bool) {
	if it.i >= len(it.data) {
		return 0, false
	}
	v := it.data[it.i]
	it.i++
	return v, true
}

func (ac *arrayContainer) equals(o container) bool {
	oc, ok := o.(*arrayContainer)
	if !ok || len(oc.data) != len(ac.data) {
		return false
	}
	for i, v := range ac.data {
		if oc.data[i] != v {
			return false
		}
	}
	return true
}

func (ac *arrayContainer) clone() container {
	return &arrayContainer{data: append([]uint16(nil), ac.data...)}
}
