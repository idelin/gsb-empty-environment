package policy

import (
	"math"
	"math/big"
	"strconv"
)

// bruteSol holds, for one information state, every achievable objective
// pair (deduplicated) and the number of distinct policy subtrees.
type bruteSol struct {
	pairs []pair
	count *big.Int
}

type brute struct {
	p    *Problem
	memo map[stateKey]*bruteSol
}

// BruteForce enumerates every feasible non-clairvoyant policy tree and
// returns the optimal objective together with the number of distinct
// policy trees. It is exponential and intended for small instances; it
// serves as an independent cross-check of Solve.
func (p *Problem) BruteForce() (Objective, *big.Int, error) {
	var zero Objective
	if err := p.Validate(); err != nil {
		return zero, nil, err
	}
	b := &brute{p: p, memo: make(map[stateKey]*bruteSol)}
	full := uint32(1<<uint(p.NumWorlds)) - 1
	sol := b.solve(full, p.MaxProbes, 0)
	best := pair{worst: math.MaxInt}
	for _, pr := range sol.pairs {
		if pr.worst < best.worst ||
			(pr.worst == best.worst && (best.exp == nil || pr.exp.Cmp(best.exp) < 0)) {
			best = pr
		}
	}
	exp := new(big.Rat).Quo(best.exp, b.probSum(full))
	return Objective{Worst: best.worst, Expected: exp}, sol.count, nil
}

func (b *brute) probSum(mask uint32) *big.Rat {
	sum := new(big.Rat)
	eachWorld(mask, func(w int) { sum.Add(sum, b.p.Prob[w]) })
	return sum
}

func (b *brute) solve(mask uint32, budget int, used uint64) *bruteSol {
	key := stateKey{mask: mask, budget: uint8(budget), used: used}
	if v, ok := b.memo[key]; ok {
		return v
	}
	var pairs []pair
	seen := make(map[string]bool)
	add := func(pr pair) {
		k := strconv.Itoa(pr.worst) + "|" + pr.exp.RatString()
		if !seen[k] {
			seen[k] = true
			pairs = append(pairs, pr)
		}
	}
	count := new(big.Int)

	// Terminal actions.
	for a := 0; a < b.p.NumActions; a++ {
		worst := math.MinInt
		exp := new(big.Rat)
		eachWorld(mask, func(w int) {
			if l := b.p.Loss[a][w]; l > worst {
				worst = l
			}
			exp.Add(exp, new(big.Rat).Mul(b.p.Prob[w], new(big.Rat).SetInt64(int64(b.p.Loss[a][w]))))
		})
		add(pair{worst: worst, exp: exp})
		count.Add(count, big.NewInt(1))
	}

	// Probes: every combination of sub-policies per observation cell.
	if budget > 0 {
		for pi := range b.p.Probes {
			if used>>uint(pi)&1 == 1 {
				continue
			}
			pr := &b.p.Probes[pi]
			_, cells := partitionMask(mask, pr.Obs)
			subs := make([]*bruteSol, len(cells))
			prod := big.NewInt(1)
			for i, cm := range cells {
				subs[i] = b.solve(cm, budget-1, used|1<<uint(pi))
				prod.Mul(prod, subs[i].count)
			}
			count.Add(count, prod)
			costMass := new(big.Rat).Mul(
				new(big.Rat).SetInt64(int64(pr.Cost)), b.probSum(mask))
			idx := make([]int, len(cells))
			for {
				worst := math.MinInt
				exp := new(big.Rat).Set(costMass)
				for i := range cells {
					sp := subs[i].pairs[idx[i]]
					if sp.worst > worst {
						worst = sp.worst
					}
					exp.Add(exp, sp.exp)
				}
				add(pair{worst: worst + pr.Cost, exp: exp})
				k := len(idx) - 1
				for k >= 0 {
					idx[k]++
					if idx[k] < len(subs[k].pairs) {
						break
					}
					idx[k] = 0
					k--
				}
				if k < 0 {
					break
				}
			}
		}
	}

	sol := &bruteSol{pairs: pairs, count: count}
	b.memo[key] = sol
	return sol
}
