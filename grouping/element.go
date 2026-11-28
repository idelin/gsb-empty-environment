package grouping

import "strings"

// Element is a single grouping element of a GROUP BY clause.
// Construct elements with Col, Tuple, Rollup, Cube and GroupingSets.
type Element interface {
	// expand returns the ordered list of grouping sets this element
	// produces. Each set is a deduplicated, canonically ordered column
	// list; the empty slice denotes the grand-total set ().
	expand() [][]string
}

// Col is an ordinary grouping column.
func Col(name string) Element { return colEl(name) }

// Tuple is a composite column group (c1, ..., ck): an atomic item that
// ROLLUP and CUBE never split apart.
func Tuple(cols ...string) Element { return tupleEl(cols) }

// Rollup expands to the prefix hierarchy of its elements:
// ROLLUP(e1, ..., en) yields the cartesian combination of e1..ei for
// i = n, n-1, ..., 1, followed by the empty set ().
func Rollup(elems ...Element) Element { return rollupEl(elems) }

// Cube expands to the cartesian combination of every subset of its
// elements, from the full set down to ().
func Cube(elems ...Element) Element { return cubeEl(elems) }

// GroupingSets lists its argument sets verbatim; each inner slice is
// one grouping set and may be empty (the grand total ()).
func GroupingSets(sets ...[]string) Element { return setsEl(sets) }

type colEl string

func (c colEl) expand() [][]string { return [][]string{{string(c)}} }

type tupleEl []string

func (t tupleEl) expand() [][]string { return [][]string{dedupCols([]string(t))} }

type setsEl [][]string

func (s setsEl) expand() [][]string {
	out := make([][]string, len(s))
	for i, set := range s {
		out[i] = dedupCols(set)
	}
	return out
}

type rollupEl []Element

func (r rollupEl) expand() [][]string {
	exps := make([][][]string, len(r))
	for i, e := range r {
		exps[i] = e.expand()
	}
	var out [][]string
	for i := len(r); i >= 1; i-- {
		out = append(out, cartesian(exps[:i])...)
	}
	return append(out, []string{})
}

type cubeEl []Element

func (c cubeEl) expand() [][]string {
	exps := make([][][]string, len(c))
	for i, e := range c {
		exps[i] = e.expand()
	}
	n := len(c)
	var out [][]string
	for mask := 1<<n - 1; mask >= 0; mask-- {
		var picked [][][]string
		for i := 0; i < n; i++ {
			if mask&(1<<(n-1-i)) != 0 {
				picked = append(picked, exps[i])
			}
		}
		if len(picked) == 0 {
			out = append(out, []string{})
			continue
		}
		out = append(out, cartesian(picked)...)
	}
	return out
}

// Expand computes the ordered grouping-set list of a whole GROUP BY
// clause: the cartesian product of the per-element expansions, with
// duplicate columns inside each set removed in canonical order. When
// distinct is true (GROUP BY DISTINCT) identical sets are deduplicated
// keeping the first occurrence; otherwise (GROUP BY ALL) duplicates
// are preserved.
func Expand(elements []Element, distinct bool) [][]string {
	lists := make([][][]string, len(elements))
	for i, e := range elements {
		lists[i] = e.expand()
	}
	sets := cartesian(lists)
	if distinct {
		sets = dedupSets(sets)
	}
	return sets
}

// cartesian combines lists of grouping sets left to right, merging each
// pair of sets with mergeSet.
func cartesian(lists [][][]string) [][]string {
	out := [][]string{{}}
	for _, list := range lists {
		var next [][]string
		for _, prefix := range out {
			for _, set := range list {
				next = append(next, mergeSet(prefix, set))
			}
		}
		out = next
	}
	return out
}

// mergeSet concatenates two sets removing duplicate column names; the
// first occurrence of a column fixes its canonical position.
func mergeSet(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	seen := make(map[string]bool, len(a)+len(b))
	for _, c := range a {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, c := range b {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func dedupCols(cols []string) []string {
	return mergeSet(nil, cols)
}

func dedupSets(sets [][]string) [][]string {
	seen := make(map[string]bool, len(sets))
	out := make([][]string, 0, len(sets))
	for _, set := range sets {
		key := strings.Join(set, "\x00")
		if !seen[key] {
			seen[key] = true
			out = append(out, set)
		}
	}
	return out
}
