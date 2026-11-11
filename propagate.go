package netsec

// unaryConstraint restricts a single address variable.
type unaryConstraint struct {
	variable int
	domain   []interval
}

// diffConstraint states (a - b) mod 2^width == delta (all values unsigned).
type diffConstraint struct {
	a     int
	b     int
	delta uint64
}

type solver struct {
	widthMask uint64
	unary     []unaryConstraint
	diffs     []diffConstraint
	budget    int64
}

// subImage computes the set { (y + delta) mod modulus : y in dom }.
// dom is sorted and disjoint; the image is the union of shifted intervals,
// which is re-merged afterwards.
func subImage(dom []interval, delta, modulus, mask uint64) []interval {
	out := make([]interval, 0, len(dom)*2)
	for _, iv := range dom {
		lo := (iv.lo + delta) & mask
		hi := (iv.hi + delta) & mask
		if lo <= hi {
			out = append(out, interval{lo, hi})
		} else {
			// Sorted by value: zero-adjacent head first, then the high tail.
			out = append(out, interval{0, hi}, interval{lo, mask})
		}
	}
	return mergeIntervals(out)
}

// propagate applies every constraint until the domains reach a fixed point.
// It returns ok=false on unsatisfiability or budget exhaustion; callers cannot
// distinguish the two from the domains alone, so the budget flag is carried by
// the solver and checked by the search driver.
func (s *solver) propagate(domains [][]interval) (ok bool) {
	for {
		changed := false
		for _, uc := range s.unary {
			next := intersectDomains(domains[uc.variable], uc.domain)
			if len(next) == 0 {
				return false
			}
			if !sameDomain(next, domains[uc.variable]) {
				domains[uc.variable] = next
				changed = true
			}
		}
		for _, dc := range s.diffs {
			s.budget--
			if s.budget <= 0 {
				return false
			}
			// x = y + delta, so x must live in shift(image of y).
			image := subImage(domains[dc.b], dc.delta, s.widthMask+1, s.widthMask)
			nextX := intersectDomains(domains[dc.a], image)
			if len(nextX) == 0 {
				return false
			}
			if !sameDomain(nextX, domains[dc.a]) {
				domains[dc.a] = nextX
				changed = true
			}
			// Inverse: y = x - delta, i.e. shift x by (modulus - delta).
			back := (s.widthMask + 1 - dc.delta) & s.widthMask
			preImage := subImage(domains[dc.a], back, s.widthMask+1, s.widthMask)
			nextY := intersectDomains(domains[dc.b], preImage)
			if len(nextY) == 0 {
				return false
			}
			if !sameDomain(nextY, domains[dc.b]) {
				domains[dc.b] = nextY
				changed = true
			}
		}
		if !changed {
			return true
		}
	}
}

func sameDomain(a, b []interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneDomains(domains [][]interval) [][]interval {
	out := make([][]interval, len(domains))
	for i, dom := range domains {
		out[i] = append([]interval(nil), dom...)
	}
	return out
}

// splitAsc splits a variable's domain into two non-empty pieces, preferring a
// cut at the lowest interval boundary so that branch "low" contains the
// smallest values. For a single interval it is bisected.
func splitAsc(dom []interval) (low, high []interval) {
	if len(dom) >= 2 {
		low = append([]interval(nil), dom[:1]...)
		high = append([]interval(nil), dom[1:]...)
		return low, high
	}
	iv := dom[0]
	mid := iv.lo + (iv.hi-iv.lo)/2
	low = []interval{{iv.lo, mid}}
	high = []interval{{mid + 1, iv.hi}}
	return low, high
}

// search performs depth-first branch and bound. Variable 0 is branched before
// variable 1 and every split enumerates low values first, so the first
// satisfying leaf holds the lexicographically smallest (x0, x1) pair, which is
// exactly the smallest unsigned pair.
func (s *solver) search(domains [][]interval) (x0, x1 uint64, status Status) {
	if !s.propagate(domains) {
		if s.budget <= 0 {
			return 0, 0, StatusUnknown
		}
		return 0, 0, StatusUnsatisfiable
	}

	branchVar := -1
	for i, dom := range domains {
		if domainCardinality(dom, s.widthMask) > 1 {
			branchVar = i
			break
		}
	}
	if branchVar == -1 {
		return domains[0][0].lo, domains[1][0].lo, StatusSatisfiable
	}

	s.budget--
	if s.budget <= 0 {
		return 0, 0, StatusUnknown
	}
	low, high := splitAsc(domains[branchVar])
	for _, piece := range [][]interval{low, high} {
		child := cloneDomains(domains)
		child[branchVar] = piece
		v0, v1, st := s.search(child)
		if st == StatusSatisfiable {
			return v0, v1, st
		}
		if st == StatusUnknown {
			return 0, 0, StatusUnknown
		}
	}
	return 0, 0, StatusUnsatisfiable
}
