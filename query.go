package invindex

import "sort"

// Match is one document of an AND-query result. Freqs holds the term
// frequencies aligned with the order of the query terms.
type Match struct {
	DocID uint64
	Freqs []uint32
}

// Stats reports how much work an intersection performed.
type Stats struct {
	Decoded int // postings decoded across all lists
	Skips   int // skip-pointer jumps taken
}

// And intersects posting lists with skip pointers: the shortest list drives
// the iteration and every other list is Advance()d, jumping over useless
// regions. Frequencies in the returned matches follow the input list order.
func And(lists ...*PostingList) []Match {
	matches, _ := AndStats(lists...)
	return matches
}

// AndStats is And plus intersection statistics.
func AndStats(lists ...*PostingList) ([]Match, Stats) {
	return intersect(lists, func(it *Iterator, target uint64) bool {
		return it.Advance(target)
	})
}

// AndNaive intersects posting lists with a plain linear merge (no skip
// pointers). It exists as a baseline to demonstrate the skip-pointer speedup.
func AndNaive(lists ...*PostingList) ([]Match, Stats) {
	return intersect(lists, advanceLinear)
}

func advanceLinear(it *Iterator, target uint64) bool {
	for {
		if it.idx >= 0 && it.doc >= target {
			return true
		}
		if !it.Next() {
			return false
		}
	}
}

func intersect(lists []*PostingList, advance func(it *Iterator, target uint64) bool) ([]Match, Stats) {
	var st Stats
	if len(lists) == 0 {
		return nil, st
	}
	for _, pl := range lists {
		if pl.Len() == 0 {
			return nil, st
		}
	}
	// Drive the intersection with the shortest list.
	order := make([]int, len(lists))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return lists[order[a]].Len() < lists[order[b]].Len() })

	its := make([]*Iterator, len(lists))
	for i, idx := range order {
		its[i] = lists[idx].Iterator()
	}
	collect := func() Stats {
		for _, it := range its {
			st.Decoded += it.Decoded
			st.Skips += it.Skips
		}
		return st
	}

	var out []Match
	driver := its[0]
	for driver.Next() {
		d := driver.Doc()
		ok := true
		for _, it := range its[1:] {
			if !advance(it, d) {
				// A list is exhausted: no further matches are possible.
				return out, collect()
			}
			if it.Doc() != d {
				ok = false
				break
			}
		}
		if ok {
			freqs := make([]uint32, len(lists))
			for i, it := range its {
				freqs[order[i]] = it.Freq()
			}
			out = append(out, Match{DocID: d, Freqs: freqs})
		}
	}
	return out, collect()
}
