package hvsched

import (
	"fmt"
	"math/big"
)

// Solve returns every integer weight vector in p that reproduces the full
// observation. The result is represented by disjoint feasible boxes and exact
// counts; candidates are not expanded one value at a time.
func Solve(p Problem) (*SolutionSet, error) {
	for i, obs := range p.Observations {
		if obs.Leaf == "" {
			return nil, fmt.Errorf("hvsched: observation %d has empty leaf ID", i)
		}
	}
	c, err := compileProblem(p)
	if err != nil {
		return nil, err
	}

	root := box{lo: append([]int64(nil), c.varLo...), hi: append([]int64(nil), c.varHi...)}
	set := &SolutionSet{problem: p, compiled: c, badPrefixLength: -1}

	if !propagateSums(c, &root) {
		set.total = big.NewInt(0)
		set.badPrefixLength = 0
		return set, nil
	}

	regions := make([]Region, 0)
	collectRegions(c, root, len(p.Observations), &regions)
	set.regions = regions

	set.total = big.NewInt(0)
	for i := range set.regions {
		set.total.Add(set.total, countRegion(c, set.regions[i]))
	}

	if set.total.Sign() == 0 {
		set.badPrefixLength = shortestContradiction(c)
	}
	return set, nil
}

// Infer is a descriptive alias for Solve.
func Infer(p Problem) (*SolutionSet, error) { return Solve(p) }

// Diagnose is a descriptive alias for Solve.
func Diagnose(p Problem) (*SolutionSet, error) { return Solve(p) }

// ContradictionPrefix solves p and returns the shortest observation prefix
// that excludes every candidate. A length of -1 means the full trace is
// feasible. The returned slice is a copy.
func ContradictionPrefix(p Problem) (length int, prefix []Observation, set *SolutionSet, err error) {
	set, err = Solve(p)
	if err != nil {
		return 0, nil, nil, err
	}
	length = set.BadPrefixLength()
	if length < 0 {
		return -1, nil, set, nil
	}
	prefix = append([]Observation(nil), p.Observations[:length]...)
	return length, prefix, set, nil
}

// BadPrefix returns the shortest impossible observation prefix, or nil when
// the complete problem is feasible.
func (s *SolutionSet) BadPrefix() []Observation {
	if s.badPrefixLength <= 0 {
		if s.badPrefixLength == 0 {
			return []Observation{}
		}
		return nil
	}
	return append([]Observation(nil), s.problem.Observations[:s.badPrefixLength]...)
}

func propagateSums(c *compiled, dom *box) bool {
	for changed := true; changed; {
		changed = false
		for layer, idxs := range c.groupVars {
			target, constrained := c.groupTarget[layer]
			if !constrained {
				continue
			}
			for _, i := range idxs {
				var loOther, hiOther int64
				for _, j := range idxs {
					if j == i {
						continue
					}
					loOther += dom.lo[j]
					hiOther += dom.hi[j]
				}
				newLo := target - hiOther
				if newLo < dom.lo[i] {
					newLo = dom.lo[i]
				}
				newHi := target - loOther
				if newHi > dom.hi[i] {
					newHi = dom.hi[i]
				}
				if newLo > newHi {
					return false
				}
				if newLo > dom.lo[i] {
					dom.lo[i] = newLo
					changed = true
				}
				if newHi < dom.hi[i] {
					dom.hi[i] = newHi
					changed = true
				}
			}
		}
	}
	return !emptyBox(dom)
}

func collectRegions(c *compiled, dom box, prefix int, out *[]Region) {
	if !propagateSums(c, &dom) {
		return
	}

	if allSingleton(&dom) {
		if verifyPrefix(c, &dom, prefix) {
			*out = append(*out, publicRegion(&dom))
		}
		return
	}

	switch c.classify(&dom, prefix) {
	case boxAll:
		*out = append(*out, publicRegion(&dom))
		return
	case boxNone:
		return
	}

	idx := splitIndex(&dom)
	if idx < 0 {
		if verifyPrefix(c, &dom, prefix) {
			*out = append(*out, publicRegion(&dom))
		}
		return
	}

	mid := dom.lo[idx] + (dom.hi[idx]-dom.lo[idx])/2
	left := cloneBox(&dom)
	left.hi[idx] = mid
	collectRegions(c, left, prefix, out)

	right := cloneBox(&dom)
	right.lo[idx] = mid + 1
	collectRegions(c, right, prefix, out)
}

func searchExists(c *compiled, dom box, prefix int) bool {
	if !propagateSums(c, &dom) {
		return false
	}
	if allSingleton(&dom) {
		return verifyPrefix(c, &dom, prefix)
	}
	switch c.classify(&dom, prefix) {
	case boxAll:
		return true
	case boxNone:
		return false
	}

	idx := splitIndex(&dom)
	if idx < 0 {
		return verifyPrefix(c, &dom, prefix)
	}
	mid := dom.lo[idx] + (dom.hi[idx]-dom.lo[idx])/2

	left := cloneBox(&dom)
	left.hi[idx] = mid
	if searchExists(c, left, prefix) {
		return true
	}

	right := cloneBox(&dom)
	right.lo[idx] = mid + 1
	return searchExists(c, right, prefix)
}

func cloneBox(dom *box) box {
	return box{lo: append([]int64(nil), dom.lo...), hi: append([]int64(nil), dom.hi...)}
}

func existsPrefix(c *compiled, prefix int) bool {
	root := box{lo: append([]int64(nil), c.varLo...), hi: append([]int64(nil), c.varHi...)}
	return searchExists(c, root, prefix)
}

func shortestContradiction(c *compiled) int {
	m := len(c.p.Observations)
	if !existsPrefix(c, 0) {
		return 0
	}
	low, high := 0, m
	for high-low > 1 {
		mid := low + (high-low)/2
		if existsPrefix(c, mid) {
			low = mid
		} else {
			high = mid
		}
	}
	return high
}

func allSingleton(dom *box) bool {
	for i := range dom.lo {
		if dom.lo[i] != dom.hi[i] {
			return false
		}
	}
	return true
}

func splitIndex(dom *box) int {
	idx := -1
	var width int64
	for i := range dom.lo {
		w := dom.hi[i] - dom.lo[i]
		if w > width {
			width = w
			idx = i
		}
	}
	return idx
}

func publicRegion(dom *box) Region {
	r := Region{Lo: make([]uint64, len(dom.lo)), Hi: make([]uint64, len(dom.hi))}
	for i := range dom.lo {
		r.Lo[i] = uint64(dom.lo[i])
		r.Hi[i] = uint64(dom.hi[i])
	}
	return r
}

func verifyPrefix(c *compiled, dom *box, prefix int) bool {
	wv := make([]uint64, c.nVar)
	for i := range dom.lo {
		if dom.lo[i] != dom.hi[i] {
			return false
		}
		wv[i] = uint64(dom.lo[i])
	}
	res, err := c.replay(wv, prefix)
	if err != nil || len(res.Emissions) != prefix {
		return false
	}
	for t := 0; t < prefix; t++ {
		obs := c.p.Observations[t]
		got := res.Emissions[t]
		if got.Leaf != obs.Leaf {
			return false
		}
		if finish := obs.completionConstraint(); finish != nil {
			node := obs.FinishNode
			if node == "" {
				node = obs.Leaf
			}
			tag := got.Finishes[node]
			if tag == nil || tag.Cmp(finish) != 0 {
				return false
			}
		}
	}
	return true
}
