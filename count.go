package hvsched

import (
	"fmt"
	"math/big"
)

func countRegion(c *compiled, r Region) *big.Int {
	dom := regionBox(r)
	return countBox(c, &dom)
}

func regionBox(r Region) box {
	dom := box{lo: make([]int64, len(r.Lo)), hi: make([]int64, len(r.Hi))}
	for i := range r.Lo {
		dom.lo[i] = int64(r.Lo[i])
		dom.hi[i] = int64(r.Hi[i])
	}
	return dom
}

func countBox(c *compiled, dom *box) *big.Int {
	if emptyBox(dom) {
		return big.NewInt(0)
	}
	total := big.NewInt(1)
	constrained := make([]bool, c.nVar)

	for layer, idxs := range c.groupVars {
		target, ok := c.groupTarget[layer]
		if !ok {
			continue
		}
		for _, i := range idxs {
			constrained[i] = true
		}
		total.Mul(total, countGroup(dom, idxs, target))
		if total.Sign() == 0 {
			return total
		}
	}

	for i := 0; i < c.nVar; i++ {
		if constrained[i] {
			continue
		}
		width := dom.hi[i] - dom.lo[i] + 1
		total.Mul(total, big.NewInt(width))
	}
	return total
}

func emptyBox(dom *box) bool {
	for i := range dom.lo {
		if dom.lo[i] > dom.hi[i] {
			return true
		}
	}
	return false
}

func countGroup(dom *box, idxs []int, target int64) *big.Int {
	lo := make([]int64, 0, len(idxs))
	hi := make([]int64, 0, len(idxs))

	for _, i := range idxs {
		l, h := dom.lo[i], dom.hi[i]
		if l > h {
			return big.NewInt(0)
		}
		if l == h {
			target -= l
			continue
		}
		lo = append(lo, l)
		hi = append(hi, h)
	}

	if target < 0 {
		return big.NewInt(0)
	}
	n := len(lo)
	if n == 0 {
		return big.NewInt(boolToInt(target == 0))
	}

	upper := make([]int64, n)
	sumUpper := int64(0)
	for i := range lo {
		upper[i] = hi[i] - lo[i]
		sumUpper += upper[i]
	}
	shiftTarget := target
	for i := range lo {
		shiftTarget -= lo[i]
	}
	if shiftTarget < 0 || shiftTarget > sumUpper {
		return big.NewInt(0)
	}

	result := big.NewInt(0)
	masks := 1 << n
	for mask := 0; mask < masks; mask++ {
		remaining := shiftTarget
		bits := 0
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				remaining -= upper[i] + 1
				bits++
			}
		}
		if remaining < 0 {
			continue
		}
		term := combinations(remaining+int64(n)-1, n-1)
		if bits%2 == 1 {
			result.Sub(result, term)
		} else {
			result.Add(result, term)
		}
	}
	if result.Sign() < 0 {
		// Inclusion-exclusion can transiently be negative but must end
		// nonnegative for a valid domain.
		return big.NewInt(0)
	}
	return result
}

func boolToInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// combinations returns C(n, k) for small nonnegative k.
func combinations(n int64, k int) *big.Int {
	if k < 0 || n < int64(k) {
		return big.NewInt(0)
	}
	if k == 0 {
		return big.NewInt(1)
	}
	res := big.NewInt(1)
	for i := 1; i <= k; i++ {
		ni := big.NewInt(n - int64(k) + int64(i))
		res.Mul(res, ni)
		res.Div(res, big.NewInt(int64(i)))
	}
	return res
}

func (s *SolutionSet) countPrefixLE(coord int, prefix WeightVector, x uint64) *big.Int {
	total := big.NewInt(0)
	for _, region := range s.regions {
		dom := regionBox(region)
		ok := true
		for j := 0; j < coord; j++ {
			v := int64(prefix[j])
			if v < dom.lo[j] || v > dom.hi[j] {
				ok = false
				break
			}
			dom.lo[j] = v
			dom.hi[j] = v
		}
		if !ok {
			continue
		}
		if uint64(dom.hi[coord]) > x {
			dom.hi[coord] = int64(x)
		}
		if emptyBox(&dom) {
			continue
		}
		total.Add(total, countBox(s.compiled, &dom))
	}
	return total
}

// Nth returns the zero-based lexicographic solution.
func (s *SolutionSet) Nth(rank *big.Int) (WeightVector, error) {
	if rank == nil || rank.Sign() < 0 {
		return nil, fmt.Errorf("hvsched: rank must be nonnegative")
	}
	if s.total == nil || rank.Cmp(s.total) >= 0 {
		return nil, fmt.Errorf("hvsched: rank %s is out of range (count=%s)", rank, s.Count())
	}

	values := make(WeightVector, s.compiled.nVar)
	currentRank := new(big.Int).Set(rank)
	rankPlusOne := new(big.Int)

	for i := 0; i < s.compiled.nVar; i++ {
		low := s.problem.Vars[i].Min
		high := s.problem.Vars[i].Max
		for low < high {
			mid := low + (high-low)/2
			cumulative := s.countPrefixLE(i, values, mid)
			rankPlusOne.Add(currentRank, big.NewInt(1))
			if cumulative.Cmp(rankPlusOne) >= 0 {
				high = mid
			} else {
				low = mid + 1
			}
		}
		chosen := low
		var before *big.Int
		if chosen == 0 {
			before = big.NewInt(0)
		} else {
			before = s.countPrefixLE(i, values, chosen-1)
		}
		currentRank.Sub(currentRank, before)
		values[i] = chosen
	}
	return values, nil
}

// Kth returns the one-based lexicographic solution.
func (s *SolutionSet) Kth(k *big.Int) (WeightVector, error) {
	if k == nil || k.Sign() <= 0 {
		return nil, fmt.Errorf("hvsched: k must be positive and one-based")
	}
	return s.Nth(new(big.Int).Sub(k, big.NewInt(1)))
}

// KthInt is Kth for ranks that fit int.
func (s *SolutionSet) KthInt(k int) (WeightVector, error) {
	if k <= 0 {
		return nil, fmt.Errorf("hvsched: k must be positive")
	}
	return s.Kth(big.NewInt(int64(k)))
}

// Iterate calls yield for solutions in zero-based lexicographic order until
// yield returns false or all solutions have been visited.
func (s *SolutionSet) Iterate(yield func(WeightVector) bool) error {
	count := s.Count()
	step := big.NewInt(1)
	for rank := big.NewInt(0); rank.Cmp(count) < 0; rank.Add(rank, step) {
		v, err := s.Nth(new(big.Int).Set(rank))
		if err != nil {
			return err
		}
		if !yield(v) {
			return nil
		}
	}
	return nil
}
