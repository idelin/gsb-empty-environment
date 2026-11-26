package gdict

import (
	"fmt"
	"math"
	"sort"
)

const (
	negInf int64 = math.MinInt64 / 4
	posInf int64 = math.MaxInt64 / 4
)

// Page is one exception-translation page: for every old code c in
// [OldLo,OldHi], the corresponding global code is c+Delta. NewLo and NewHi
// are included for independent review.
type Page struct {
	OldLo uint64 `json:"old_lo"`
	OldHi uint64 `json:"old_hi"`
	Delta int64  `json:"delta"`
	NewLo uint64 `json:"new_lo"`
	NewHi uint64 `json:"new_hi"`
}

type pagePos struct {
	code uint64
	lo   int64
	hi   int64
	desc string
}

// computePages derives the minimum number of constant-delta source intervals
// for one partition given a complete assignment. It uses the classic greedy
// interval-intersection argument: a segment can contain a position exactly
// when the intersection of the positions' admissible delta intervals is
// non-empty, and segment feasibility is hereditary, so cutting at the first
// empty intersection is optimal.
func computePages(n *normalized, p partitionInfo, codeOf map[int]uint64) ([]Page, error) {
	var positions []pagePos
	for _, e := range p.entries {
		newCode := codeOf[e.group]
		d := int64(newCode) - int64(e.oldCode)
		positions = append(positions, pagePos{code: e.oldCode, lo: d, hi: d, desc: "entry " + e.key.String()})
	}
	for _, c := range p.cuts {
		lo, hi, err := cutDeltaInterval(p, c, codeOf)
		if err != nil {
			return nil, err
		}
		positions = append(positions, pagePos{code: c.OldCode, lo: lo, hi: hi, desc: "cut " + c.Name})
	}
	if len(positions) == 0 {
		return nil, nil
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].code != positions[j].code {
			return positions[i].code < positions[j].code
		}
		return positions[i].desc < positions[j].desc
	})

	// Merge positions that share an old code, including an entry and a cut
	// at exactly that code.
	merged := positions[:0]
	for _, pos := range positions {
		if len(merged) == 0 || merged[len(merged)-1].code != pos.code {
			merged = append(merged, pos)
			continue
		}
		last := &merged[len(merged)-1]
		if pos.lo > last.lo {
			last.lo = pos.lo
		}
		if pos.hi < last.hi {
			last.hi = pos.hi
		}
		last.desc += "; " + pos.desc
		if last.lo > last.hi {
			return nil, fmt.Errorf("gdict: partition %s has incompatible page requirements at old code %d (%s)", p.name, pos.code, last.desc)
		}
	}

	type rawPage struct {
		start, end uint64
		lo, hi     int64
	}
	var raws []rawPage
	segStart := merged[0].code
	curLo, curHi := merged[0].lo, merged[0].hi
	segEnd := merged[0].code
	for _, pos := range merged[1:] {
		nlo, nhi := curLo, curHi
		if pos.lo > nlo {
			nlo = pos.lo
		}
		if pos.hi < nhi {
			nhi = pos.hi
		}
		if nlo > nhi {
			raws = append(raws, rawPage{start: segStart, end: segEnd, lo: curLo, hi: curHi})
			segStart, segEnd = pos.code, pos.code
			curLo, curHi = pos.lo, pos.hi
			continue
		}
		curLo, curHi = nlo, nhi
		segEnd = pos.code
	}
	raws = append(raws, rawPage{start: segStart, end: segEnd, lo: curLo, hi: curHi})

	pages := make([]Page, 0, len(raws))
	var prevDelta int64
	havePrev := false
	for _, r := range raws {
		d := chooseDelta(r.lo, r.hi, prevDelta, havePrev)
		newLo, ok1 := addDelta(r.start, d)
		newHi, ok2 := addDelta(r.end, d)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("gdict: partition %s translation overflows uint64 code space on [%d,%d]+%d", p.name, r.start, r.end, d)
		}
		pages = append(pages, Page{OldLo: r.start, OldHi: r.end, Delta: d, NewLo: newLo, NewHi: newHi})
		prevDelta, havePrev = d, true
	}
	return pages, nil
}

func chooseDelta(lo, hi int64, prefer int64, havePrefer bool) int64 {
	if havePrefer && lo <= prefer && prefer <= hi {
		return prefer
	}
	if lo <= 0 && 0 <= hi {
		return 0
	}
	if lo > 0 {
		return lo
	}
	return hi
}

func addDelta(code uint64, d int64) (uint64, bool) {
	if d >= 0 {
		add := uint64(d)
		if code > math.MaxUint64-add {
			return 0, false
		}
		return code + add, true
	}
	sub := uint64(-(d + 1)) + 1 // safe even for MinInt64
	if code < sub {
		return 0, false
	}
	return code - sub, true
}

func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

// cutDeltaInterval returns the interval of page deltas d for which the old
// predicate at cut c is exactly the new predicate at code c.OldCode+d.
func cutDeltaInterval(p partitionInfo, c Cut, codeOf map[int]uint64) (int64, int64, error) {
	var haveL, haveR bool
	var maxL, minR uint64
	leftCount, rightCount := 0, 0
	for _, e := range p.entries {
		before := e.oldCode < c.OldCode
		if c.Bound == LE {
			before = e.oldCode <= c.OldCode
		}
		g := codeOf[e.group]
		if before {
			leftCount++
			if !haveL || g > maxL {
				maxL = g
				haveL = true
			}
		} else {
			rightCount++
			if !haveR || g < minR {
				minR = g
				haveR = true
			}
		}
	}
	b := int64(c.OldCode)
	var lo, hi int64
	if c.Bound == LT {
		// L: new < b+d; R: new >= b+d.
		if haveL {
			lo = int64(maxL) - b + 1
		} else {
			lo = negInf
		}
		if haveR {
			hi = int64(minR) - b
		} else {
			hi = posInf
		}
	} else {
		// L: new <= b+d; R: new > b+d.
		if haveL {
			lo = int64(maxL) - b
		} else {
			lo = negInf
		}
		if haveR {
			hi = int64(minR) - b - 1
		} else {
			hi = posInf
		}
	}
	if lo > hi {
		return 0, 0, fmt.Errorf("gdict: cut %s (%s %d, L=%d R=%d) cannot be translated by one affine page", c.Name, c.Bound, c.OldCode, leftCount, rightCount)
	}
	return lo, hi, nil
}
