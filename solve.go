package policy

import (
	"math"
	"math/big"
	"sort"
)

// Objective is the quality measure of a policy: the worst-case total loss
// over all worlds, then the expected total loss under the world
// probabilities (normalized to a probability distribution).
type Objective struct {
	Worst    int
	Expected *big.Rat
}

// Solution is the result of solving a problem.
type Solution struct {
	Tree *Node
	Objective
}

// pair is an unnormalized objective at a DP state: worst-case future total
// loss over the worlds consistent with the observation history, and the
// probability-weighted sum of future total losses over those worlds.
type pair struct {
	worst int
	exp   *big.Rat
}

// choice records how a frontier entry at a DP state is realized.
type choice struct {
	isProbe  bool
	action   int
	probe    int
	cellKeys []string
	cellIdx  []int // chosen frontier index per cell
}

type stateSol struct {
	front []pair   // Pareto-optimal, sorted by worst asc / exp asc
	ch    []choice // parallel to front
}

type stateKey struct {
	mask   uint32
	budget uint8
	used   uint64
}

// solver computes, for every information state (set of worlds consistent
// with the observation history, remaining probe budget, probes already
// used), the Pareto frontier of achievable (worst, expected) pairs. A
// frontier is needed because the lexicographic objective does not decompose
// per state: a branch with slightly worse worst-case but much better
// expectation can be globally optimal when a sibling branch already forces
// the worst case.
type solver struct {
	p        *Problem
	memo     map[stateKey]*stateSol
	probSums map[uint32]*big.Rat
}

// Solve computes an optimal non-clairvoyant policy tree.
func (p *Problem) Solve() (*Solution, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	s := &solver{
		p:        p,
		memo:     make(map[stateKey]*stateSol),
		probSums: make(map[uint32]*big.Rat),
	}
	full := uint32(1<<uint(p.NumWorlds)) - 1
	sol := s.solve(full, p.MaxProbes, 0)
	best := sol.front[0] // lexicographic minimum: smallest worst, then smallest exp
	tree := s.build(full, p.MaxProbes, 0, sol, 0)
	exp := new(big.Rat).Quo(best.exp, s.probSum(full))
	return &Solution{
		Tree:      tree,
		Objective: Objective{Worst: best.worst, Expected: exp},
	}, nil
}

func (s *solver) probSum(mask uint32) *big.Rat {
	if r, ok := s.probSums[mask]; ok {
		return r
	}
	sum := new(big.Rat)
	eachWorld(mask, func(w int) { sum.Add(sum, s.p.Prob[w]) })
	s.probSums[mask] = sum
	return sum
}

func (s *solver) solve(mask uint32, budget int, used uint64) *stateSol {
	key := stateKey{mask: mask, budget: uint8(budget), used: used}
	if v, ok := s.memo[key]; ok {
		return v
	}
	var pairs []pair
	var chs []choice

	// Option 1: stop probing and take a terminal action.
	for a := 0; a < s.p.NumActions; a++ {
		worst := math.MinInt
		exp := new(big.Rat)
		eachWorld(mask, func(w int) {
			if l := s.p.Loss[a][w]; l > worst {
				worst = l
			}
			exp.Add(exp, new(big.Rat).Mul(s.p.Prob[w], new(big.Rat).SetInt64(int64(s.p.Loss[a][w]))))
		})
		pairs = append(pairs, pair{worst: worst, exp: exp})
		chs = append(chs, choice{action: a, probe: -1})
	}

	// Option 2: perform an unused probe, then continue adaptively.
	if budget > 0 {
		for pi := range s.p.Probes {
			if used>>uint(pi)&1 == 1 {
				continue
			}
			pr := &s.p.Probes[pi]
			keys, cells := partitionMask(mask, pr.Obs)
			sols := make([]*stateSol, len(cells))
			for i, cm := range cells {
				sols[i] = s.solve(cm, budget-1, used|1<<uint(pi))
			}
			costMass := new(big.Rat).Mul(
				new(big.Rat).SetInt64(int64(pr.Cost)), s.probSum(mask))
			idx := make([]int, len(cells))
			for {
				worst := math.MinInt
				exp := new(big.Rat).Set(costMass)
				for i := range cells {
					fp := sols[i].front[idx[i]]
					if fp.worst > worst {
						worst = fp.worst
					}
					exp.Add(exp, fp.exp)
				}
				pairs = append(pairs, pair{worst: worst + pr.Cost, exp: exp})
				chs = append(chs, choice{
					isProbe:  true,
					action:   -1,
					probe:    pi,
					cellKeys: keys,
					cellIdx:  append([]int(nil), idx...),
				})
				// Odometer: next combination of cell frontier entries.
				k := len(idx) - 1
				for k >= 0 {
					idx[k]++
					if idx[k] < len(sols[k].front) {
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

	front, fch := pruneFront(pairs, chs)
	sol := &stateSol{front: front, ch: fch}
	s.memo[key] = sol
	return sol
}

// pruneFront keeps the Pareto-optimal pairs: sorted by worst ascending, an
// entry survives only if its expectation is strictly smaller than every
// entry with a no-larger worst case. Equal pairs are deduplicated, keeping
// the first (deterministic) choice.
func pruneFront(pairs []pair, chs []choice) ([]pair, []choice) {
	ord := make([]int, len(pairs))
	for i := range ord {
		ord[i] = i
	}
	sort.SliceStable(ord, func(i, j int) bool {
		a, b := pairs[ord[i]], pairs[ord[j]]
		if a.worst != b.worst {
			return a.worst < b.worst
		}
		return a.exp.Cmp(b.exp) < 0
	})
	var fp []pair
	var fc []choice
	var bestExp *big.Rat
	for _, i := range ord {
		if bestExp != nil && pairs[i].exp.Cmp(bestExp) >= 0 {
			continue
		}
		fp = append(fp, pairs[i])
		fc = append(fc, chs[i])
		bestExp = pairs[i].exp
	}
	return fp, fc
}

// build reconstructs the policy tree for the frontier entry idx at a state.
func (s *solver) build(mask uint32, budget int, used uint64, sol *stateSol, idx int) *Node {
	c := sol.ch[idx]
	if !c.isProbe {
		return &Node{Action: c.action, Probe: -1}
	}
	n := &Node{Action: -1, Probe: c.probe, Children: make(map[string]*Node)}
	_, cells := partitionMask(mask, s.p.Probes[c.probe].Obs)
	nextUsed := used | 1<<uint(c.probe)
	for i, key := range c.cellKeys {
		childSol := s.solve(cells[i], budget-1, nextUsed)
		n.Children[key] = s.build(cells[i], budget-1, nextUsed, childSol, c.cellIdx[i])
	}
	return n
}
