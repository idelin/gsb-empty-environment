package roaring

import (
	"fmt"
	"sort"
)

// interval16 is an inclusive run [start, start+length].
type interval16 struct {
	start  uint16
	length uint16
}

// runContainer stores sorted, non-overlapping, non-adjacent intervals.
type runContainer struct {
	runs []interval16
}

func (rc *runContainer) ctype() containerType { return typeRun }

func (rc *runContainer) cardinality() int {
	c := 0
	for _, r := range rc.runs {
		c += int(r.length) + 1
	}
	return c
}

func (rc *runContainer) sizeInBytes() int { return 4 * len(rc.runs) }

func (rc *runContainer) numRuns() int { return len(rc.runs) }

func runEnd(r interval16) uint32 { return uint32(r.start) + uint32(r.length) }

func (rc *runContainer) contains(x uint16) bool {
	i := sort.Search(len(rc.runs), func(i int) bool {
		return runEnd(rc.runs[i]) >= uint32(x)
	})
	return i < len(rc.runs) && rc.runs[i].start <= x
}

func (rc *runContainer) add(x uint16) (container, bool) {
	x32 := uint32(x)
	for i := 0; i < len(rc.runs); i++ {
		s := uint32(rc.runs[i].start)
		e := runEnd(rc.runs[i])
		switch {
		case x32 >= s && x32 <= e:
			return rc, false
		case x32+1 == s:
			rc.runs[i].start = x
			rc.runs[i].length++
			if i > 0 && runEnd(rc.runs[i-1])+1 >= x32 {
				rc.runs[i-1].length = uint16(runEnd(rc.runs[i]) - uint32(rc.runs[i-1].start))
				rc.runs = append(rc.runs[:i], rc.runs[i+1:]...)
			}
			return rc, true
		case x32 == e+1:
			rc.runs[i].length++
			if i+1 < len(rc.runs) && uint32(rc.runs[i+1].start) == x32+1 {
				rc.runs[i].length += rc.runs[i+1].length + 1
				rc.runs = append(rc.runs[:i+1], rc.runs[i+2:]...)
			}
			return rc, true
		case x32 < s:
			rc.runs = append(rc.runs, interval16{})
			copy(rc.runs[i+1:], rc.runs[i:])
			rc.runs[i] = interval16{start: x}
			return rc, true
		}
	}
	rc.runs = append(rc.runs, interval16{start: x})
	return rc, true
}

func (rc *runContainer) remove(x uint16) (container, bool) {
	x32 := uint32(x)
	for i := 0; i < len(rc.runs); i++ {
		s := uint32(rc.runs[i].start)
		e := runEnd(rc.runs[i])
		if x32 < s {
			return rc, false
		}
		if x32 > e {
			continue
		}
		switch {
		case rc.runs[i].length == 0:
			rc.runs = append(rc.runs[:i], rc.runs[i+1:]...)
		case x32 == s:
			rc.runs[i].start++
			rc.runs[i].length--
		case x32 == e:
			rc.runs[i].length--
		default:
			right := interval16{start: x + 1, length: uint16(e - x32 - 1)}
			rc.runs[i].length = uint16(x32 - s - 1)
			rc.runs = append(rc.runs, interval16{})
			copy(rc.runs[i+2:], rc.runs[i+1:])
			rc.runs[i+1] = right
		}
		return rc, true
	}
	return rc, false
}

func (rc *runContainer) addRange(lo, hi uint16) container {
	newStart, newEnd := uint32(lo), uint32(hi)
	out := make([]interval16, 0, len(rc.runs)+1)
	inserted := false
	for _, r := range rc.runs {
		s := uint32(r.start)
		e := runEnd(r)
		switch {
		case e+1 < newStart:
			out = append(out, r)
		case newEnd+1 < s:
			if !inserted {
				out = append(out, interval16{start: uint16(newStart), length: uint16(newEnd - newStart)})
				inserted = true
			}
			out = append(out, r)
		default:
			if s < newStart {
				newStart = s
			}
			if e > newEnd {
				newEnd = e
			}
		}
	}
	if !inserted {
		out = append(out, interval16{start: uint16(newStart), length: uint16(newEnd - newStart)})
	}
	rc.runs = out
	return rc
}

func (rc *runContainer) removeRange(lo, hi uint16) container {
	lo32, hi32 := uint32(lo), uint32(hi)
	// A single input run can produce two output runs, so filtering in
	// place would clobber unread entries. Allocate a fresh slice.
	out := make([]interval16, 0, len(rc.runs)+1)
	for _, r := range rc.runs {
		s := uint32(r.start)
		e := runEnd(r)
		if e < lo32 || s > hi32 {
			out = append(out, r)
			continue
		}
		if s < lo32 {
			out = append(out, interval16{start: uint16(s), length: uint16(lo32 - 1 - s)})
		}
		if e > hi32 {
			out = append(out, interval16{start: uint16(hi32 + 1), length: uint16(e - hi32 - 1)})
		}
	}
	rc.runs = out
	return rc
}

func (rc *runContainer) forEach(fn func(x uint16)) {
	for _, r := range rc.runs {
		for v := uint32(r.start); v <= runEnd(r); v++ {
			fn(uint16(v))
		}
	}
}

func (rc *runContainer) clone() container {
	out := &runContainer{runs: make([]interval16, len(rc.runs))}
	copy(out.runs, rc.runs)
	return out
}

func (rc *runContainer) minimum() uint16 { return rc.runs[0].start }
func (rc *runContainer) maximum() uint16 { return uint16(runEnd(rc.runs[len(rc.runs)-1])) }

func (rc *runContainer) optimize() container {
	card := rc.cardinality()
	t := pickContainerType(card, len(rc.runs))
	if t == typeRun {
		return rc
	}
	return containerFromType(t, rc)
}

func (rc *runContainer) validate() error {
	if len(rc.runs) == 0 {
		return fmt.Errorf("run container is empty")
	}
	for i := 1; i < len(rc.runs); i++ {
		prevEnd := runEnd(rc.runs[i-1])
		if prevEnd+1 >= uint32(rc.runs[i].start) {
			return fmt.Errorf("run container intervals not coalesced at %d", i)
		}
	}
	return nil
}
