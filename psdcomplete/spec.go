package psdcomplete

import (
	"fmt"
	"math/big"
)

// MaxN is the largest supported matrix order.
const MaxN = 4

// MaxCombos is the largest supported number of candidate combinations.
const MaxCombos = 1_000_000

// Spec describes a semidefinite-completion problem: an n x n symmetric
// matrix (n <= 4) in which every upper-triangular position is either fixed
// to a rational value or must be chosen from a finite set of rational
// candidates.
type Spec struct {
	N     int
	fixed map[[2]int]*big.Rat
	cands map[[2]int][]*big.Rat
}

// NewSpec creates an empty spec for an n x n matrix, 1 <= n <= 4.
func NewSpec(n int) (*Spec, error) {
	if n < 1 || n > MaxN {
		return nil, fmt.Errorf("psdcomplete: n must be in [1,%d], got %d", MaxN, n)
	}
	return &Spec{
		N:     n,
		fixed: make(map[[2]int]*big.Rat),
		cands: make(map[[2]int][]*big.Rat),
	}, nil
}

func normPos(i, j int) [2]int {
	if i > j {
		i, j = j, i
	}
	return [2]int{i, j}
}

// Fix sets entry (i,j) — and, by symmetry, (j,i) — to the exact value v.
func (s *Spec) Fix(i, j int, v *big.Rat) error {
	if err := s.checkPos(i, j); err != nil {
		return err
	}
	p := normPos(i, j)
	if _, ok := s.cands[p]; ok {
		return fmt.Errorf("psdcomplete: position (%d,%d) already has candidates", i+1, j+1)
	}
	s.fixed[p] = cloneRat(v)
	return nil
}

// SetCandidates declares that entry (i,j) (and (j,i)) must be chosen from
// the given finite candidate set. Values are stored sorted and deduplicated.
func (s *Spec) SetCandidates(i, j int, vals ...*big.Rat) error {
	if err := s.checkPos(i, j); err != nil {
		return err
	}
	if len(vals) == 0 {
		return fmt.Errorf("psdcomplete: position (%d,%d) has an empty candidate set", i+1, j+1)
	}
	p := normPos(i, j)
	if _, ok := s.fixed[p]; ok {
		return fmt.Errorf("psdcomplete: position (%d,%d) is already fixed", i+1, j+1)
	}
	s.cands[p] = sortedUniqueRats(vals)
	return nil
}

func (s *Spec) checkPos(i, j int) error {
	if i < 0 || j < 0 || i >= s.N || j >= s.N {
		return fmt.Errorf("psdcomplete: position (%d,%d) out of range for n=%d", i+1, j+1, s.N)
	}
	return nil
}

// ComboCount returns the total number of candidate combinations.
func (s *Spec) ComboCount() int64 {
	total := int64(1)
	for _, c := range s.cands {
		total *= int64(len(c))
		if total > MaxCombos {
			return total // early exit; caller only needs the bound
		}
	}
	return total
}

// Validate checks that every upper-triangular position is either fixed or
// has a non-empty candidate set, and that the total number of combinations
// does not exceed MaxCombos.
func (s *Spec) Validate() error {
	for i := 0; i < s.N; i++ {
		for j := i; j < s.N; j++ {
			p := [2]int{i, j}
			_, isFixed := s.fixed[p]
			_, hasCands := s.cands[p]
			if !isFixed && !hasCands {
				return fmt.Errorf("psdcomplete: position (%d,%d) is neither fixed nor given candidates", i+1, j+1)
			}
		}
	}
	if total := s.ComboCount(); total > MaxCombos {
		return fmt.Errorf("psdcomplete: %d candidate combinations exceed the limit of %d", total, MaxCombos)
	}
	return nil
}
