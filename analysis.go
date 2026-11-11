package netsec

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Direction identifies which side of an adjacent pair (c, c+1) plays the
// role of p in the divergence delta(p || q).
type Direction int

const (
	// Forward: p is the distribution at count c, q at count c+1.
	Forward Direction = iota
	// Backward: p is the distribution at count c+1, q at count c.
	Backward
)

func (d Direction) String() string {
	if d == Forward {
		return "forward"
	}
	return "backward"
}

// advantage returns p(o) - rho*q(o) as an exact rational.
func advantage(p, q map[int]*big.Rat, rho *big.Rat, o int) *big.Rat {
	term := new(big.Rat)
	if po, ok := p[o]; ok {
		term.Set(po)
	}
	if qo, ok := q[o]; ok {
		term.Sub(term, new(big.Rat).Mul(rho, qo))
	}
	return term
}

// HockeyStick computes the exact hockey-stick divergence
//
//	delta(p || q, rho) = sum_o max(0, p(o) - rho*q(o))
//
// over the union of both supports. It is the minimal additive relaxation
// delta such that P(M(D) in S) <= rho * P(M(D') in S) + delta holds for
// every output set S, for this ordered pair of adjacent datasets.
func HockeyStick(p, q map[int]*big.Rat, rho *big.Rat) *big.Rat {
	delta := new(big.Rat)
	for o := range unionSupport(p, q) {
		if term := advantage(p, q, rho, o); term.Sign() > 0 {
			delta.Add(delta, term)
		}
	}
	return delta
}

// WorstSet returns the sorted output set attaining the hockey-stick
// divergence: all outputs o with p(o) > rho*q(o). Outputs on the boundary
// (p(o) == rho*q(o)) contribute nothing and are excluded.
func WorstSet(p, q map[int]*big.Rat, rho *big.Rat) []int {
	var set []int
	for o := range unionSupport(p, q) {
		if advantage(p, q, rho, o).Sign() > 0 {
			set = append(set, o)
		}
	}
	sort.Ints(set)
	return set
}

func unionSupport(p, q map[int]*big.Rat) map[int]struct{} {
	u := make(map[int]struct{}, len(p)+len(q))
	for o := range p {
		u[o] = struct{}{}
	}
	for o := range q {
		u[o] = struct{}{}
	}
	return u
}

// Result is the outcome of an exact worst-case analysis.
type Result struct {
	// Delta is the minimal additive relaxation for the given factor rho,
	// as an exact rational.
	Delta *big.Rat
	// Count is the smaller true count of the maximizing adjacent pair
	// (Count, Count+1).
	Count int
	// Direction tells which side of the pair maximizes the divergence.
	Direction Direction
	// Set is a worst output set attaining Delta for that pair.
	Set []int
}

// Analyze enumerates every adjacent pair of true counts (c, c+1) inside
// [Lo, Hi], builds both output distributions exactly, and returns the
// largest hockey-stick divergence over both directions, together with a
// worst output set. rho is the claimed multiplicative factor (e.g. e^eps)
// given as an exact rational.
func Analyze(t NoiseTable, s Spec, rho *big.Rat) (Result, error) {
	if rho == nil || rho.Sign() <= 0 {
		return Result{}, errors.New("netsec: multiplicative factor must be a positive rational")
	}
	if err := t.Validate(); err != nil {
		return Result{}, err
	}
	if err := s.validate(); err != nil {
		return Result{}, err
	}
	if s.Hi == s.Lo {
		return Result{}, errors.New("netsec: need at least two adjacent counts in range")
	}
	best := Result{Delta: new(big.Rat)}
	for c := s.Lo; c < s.Hi; c++ {
		pc, err := t.Distribution(c, s)
		if err != nil {
			return Result{}, err
		}
		pn, err := t.Distribution(c+1, s)
		if err != nil {
			return Result{}, err
		}
		for _, dir := range []Direction{Forward, Backward} {
			p, q := pc, pn
			if dir == Backward {
				p, q = pn, pc
			}
			d := HockeyStick(p, q, rho)
			if d.Cmp(best.Delta) > 0 {
				best = Result{
					Delta:     d,
					Count:     c,
					Direction: dir,
					Set:       WorstSet(p, q, rho),
				}
			}
		}
	}
	return best, nil
}

// CheckClaim verifies a claimed (rho, claimedDelta) privacy bound for the
// mechanism. It reports whether the claim holds and returns the exact
// minimal delta, so a config change that silently breaks the bound is
// detected with the true violation magnitude.
func CheckClaim(t NoiseTable, s Spec, rho, claimedDelta *big.Rat) (bool, *big.Rat, error) {
	if claimedDelta == nil {
		return false, nil, errors.New("netsec: claimed delta must not be nil")
	}
	res, err := Analyze(t, s, rho)
	if err != nil {
		return false, nil, err
	}
	return res.Delta.Cmp(claimedDelta) <= 0, res.Delta, nil
}

// exhaustiveLimit caps the support size for ExhaustiveDelta.
const exhaustiveLimit = 25

// ExhaustiveDelta independently recomputes the hockey-stick divergence by
// enumerating every subset of the combined support and returning the
// maximizing set. It proves by exhaustion that WorstSet is optimal, at
// exponential cost; supports larger than exhaustiveLimit are rejected.
func ExhaustiveDelta(p, q map[int]*big.Rat, rho *big.Rat) (*big.Rat, []int, error) {
	u := unionSupport(p, q)
	if len(u) > exhaustiveLimit {
		return nil, nil, fmt.Errorf("netsec: support size %d exceeds exhaustive limit %d", len(u), exhaustiveLimit)
	}
	outs := make([]int, 0, len(u))
	for o := range u {
		outs = append(outs, o)
	}
	sort.Ints(outs)
	gains := make([]*big.Rat, len(outs))
	for i, o := range outs {
		gains[i] = advantage(p, q, rho, o)
	}
	best := new(big.Rat)
	var bestSet []int
	n := uint(len(outs))
	for mask := uint64(0); mask < (uint64(1) << n); mask++ {
		sum := new(big.Rat)
		var set []int
		for i := uint(0); i < n; i++ {
			if mask&(uint64(1)<<i) != 0 {
				sum.Add(sum, gains[i])
				set = append(set, outs[i])
			}
		}
		if sum.Cmp(best) > 0 {
			best = sum
			bestSet = set
		}
	}
	return best, bestSet, nil
}
