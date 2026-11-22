package layout

import (
	"fmt"
	"math/big"
)

// Rank returns the zero-based lexicographic position of l among all legal
// layouts, ordered by (start[0], start[1], ...).
func (p *Planner) Rank(l Layout) (*big.Int, error) {
	if err := p.Validate(l); err != nil {
		return nil, err
	}
	prefix := make([]int, p.n)
	for i := range prefix {
		prefix[i] = -1
	}

	rank := new(big.Int)
	for i := 0; i < p.n; i++ {
		actual := l.Starts[i]
		for _, s := range p.domains[i] {
			if s >= actual {
				break
			}
			candidate := append([]int(nil), prefix...)
			candidate[i] = s
			rank.Add(rank, p.countPrefix(candidate))
		}
		prefix[i] = actual
	}
	return rank, nil
}

// Unrank returns the layout with the given zero-based lexicographic rank.
func (p *Planner) Unrank(rank *big.Int) (Layout, error) {
	if rank == nil || rank.Sign() < 0 {
		return Layout{}, ErrRankOutOfRange
	}
	total := p.Count()
	if total.Sign() == 0 {
		return Layout{}, ErrNoLayouts
	}
	if rank.Cmp(total) >= 0 {
		return Layout{}, ErrRankOutOfRange
	}

	remaining := new(big.Int).Set(rank)
	prefix := make([]int, p.n)
	for i := range prefix {
		prefix[i] = -1
	}
	starts := make([]int, p.n)

	for i := 0; i < p.n; i++ {
		chosen := false
		for _, s := range p.domains[i] {
			candidate := append([]int(nil), prefix...)
			candidate[i] = s
			count := p.countPrefix(candidate)
			if remaining.Cmp(count) < 0 {
				starts[i] = s
				prefix[i] = s
				chosen = true
				break
			}
			remaining.Sub(remaining, count)
		}
		if !chosen {
			return Layout{}, fmt.Errorf("%w: rank cannot be resolved", ErrRankOutOfRange)
		}
	}
	if remaining.Sign() != 0 {
		return Layout{}, fmt.Errorf("%w: rank cannot be resolved", ErrRankOutOfRange)
	}
	return Layout{Starts: starts}, nil
}
