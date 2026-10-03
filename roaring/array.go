package roaring

import (
	"fmt"
	"sort"
)

// arrayContainer stores up to arrayDefaultMaxSize sorted uint16 values.
type arrayContainer struct {
	content []uint16
}

func newArrayContainer() *arrayContainer {
	return &arrayContainer{content: make([]uint16, 0, 8)}
}

func (ac *arrayContainer) ctype() containerType { return typeArray }
func (ac *arrayContainer) cardinality() int     { return len(ac.content) }
func (ac *arrayContainer) sizeInBytes() int     { return 2 * len(ac.content) }

func (ac *arrayContainer) search(x uint16) (int, bool) {
	i := sort.Search(len(ac.content), func(i int) bool { return ac.content[i] >= x })
	if i < len(ac.content) && ac.content[i] == x {
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
	ac.content = append(ac.content, 0)
	copy(ac.content[i+1:], ac.content[i:])
	ac.content[i] = x
	if len(ac.content) > arrayDefaultMaxSize {
		return ac.toBitmap(), true
	}
	return ac, true
}

func (ac *arrayContainer) remove(x uint16) (container, bool) {
	i, found := ac.search(x)
	if !found {
		return ac, false
	}
	ac.content = append(ac.content[:i], ac.content[i+1:]...)
	return ac, true
}

func (ac *arrayContainer) addRange(lo, hi uint16) container {
	count := int(hi) - int(lo) + 1
	if len(ac.content)+count > arrayDefaultMaxSize {
		bc := ac.toBitmap()
		return bc.addRange(lo, hi)
	}
	out := make([]uint16, 0, len(ac.content)+count)
	i := 0
	for i < len(ac.content) && ac.content[i] < lo {
		out = append(out, ac.content[i])
		i++
	}
	for v := lo; ; v++ {
		out = append(out, v)
		if v == hi {
			break
		}
	}
	for i < len(ac.content) && ac.content[i] <= hi {
		i++
	}
	out = append(out, ac.content[i:]...)
	ac.content = out
	return ac
}

func (ac *arrayContainer) removeRange(lo, hi uint16) container {
	out := ac.content[:0]
	for _, v := range ac.content {
		if v < lo || v > hi {
			out = append(out, v)
		}
	}
	ac.content = out
	return ac
}

func (ac *arrayContainer) forEach(fn func(x uint16)) {
	for _, v := range ac.content {
		fn(v)
	}
}

func (ac *arrayContainer) clone() container {
	out := &arrayContainer{content: make([]uint16, len(ac.content))}
	copy(out.content, ac.content)
	return out
}

func (ac *arrayContainer) minimum() uint16 { return ac.content[0] }
func (ac *arrayContainer) maximum() uint16 { return ac.content[len(ac.content)-1] }

func (ac *arrayContainer) optimize() container {
	if len(ac.content) == 0 {
		return ac
	}
	if pickContainerType(len(ac.content), ac.numRuns()) == typeRun {
		return runContainerFrom(ac)
	}
	return ac
}

func (ac *arrayContainer) numRuns() int {
	if len(ac.content) == 0 {
		return 0
	}
	runs := 1
	for i := 1; i < len(ac.content); i++ {
		if ac.content[i] != ac.content[i-1]+1 {
			runs++
		}
	}
	return runs
}

func (ac *arrayContainer) validate() error {
	if len(ac.content) == 0 {
		return fmt.Errorf("array container is empty")
	}
	if len(ac.content) > arrayDefaultMaxSize {
		return fmt.Errorf("array container too large: %d", len(ac.content))
	}
	for i := 1; i < len(ac.content); i++ {
		if ac.content[i] <= ac.content[i-1] {
			return fmt.Errorf("array container not strictly sorted at %d", i)
		}
	}
	return nil
}

func (ac *arrayContainer) toBitmap() *bitmapContainer {
	bc := newBitmapContainer()
	for _, v := range ac.content {
		bc.set(v)
	}
	return bc
}
