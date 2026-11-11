package netsec

import (
	"errors"
	"fmt"
	"math/big"
)

// Mode selects how raw outputs outside [Lo, Hi] are mapped back into range.
type Mode int

const (
	// Truncate clamps raw outputs to [Lo, Hi]; out-of-range probability mass
	// accumulates on the nearest boundary output.
	Truncate Mode = iota
	// Wrap folds raw outputs into [Lo, Hi] modulo the range size.
	Wrap
)

func (m Mode) String() string {
	switch m {
	case Truncate:
		return "truncate"
	case Wrap:
		return "wrap"
	default:
		return "unknown"
	}
}

// Entry is one row of the discrete noise table: adding Offset to the true
// count occurs with probability Prob. Probabilities are exact rationals.
type Entry struct {
	Offset int
	Prob   *big.Rat
}

// NoiseTable is a finite discrete noise distribution.
type NoiseTable struct {
	Entries []Entry
}

// Validate checks that all probabilities are non-negative rationals that
// sum to exactly one.
func (t NoiseTable) Validate() error {
	if len(t.Entries) == 0 {
		return errors.New("netsec: noise table is empty")
	}
	sum := new(big.Rat)
	for i, e := range t.Entries {
		if e.Prob == nil {
			return fmt.Errorf("netsec: entry %d has nil probability", i)
		}
		if e.Prob.Sign() < 0 {
			return fmt.Errorf("netsec: entry %d has negative probability %s", i, e.Prob)
		}
		sum.Add(sum, e.Prob)
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		return fmt.Errorf("netsec: probabilities sum to %s, want exactly 1", sum)
	}
	return nil
}

// Spec describes the output domain and the boundary handling of the
// mechanism. True counts are assumed to lie in [Lo, Hi]; adjacent datasets
// have true counts c and c+1 that both lie in this range.
type Spec struct {
	Lo, Hi int
	Mode   Mode
}

func (s Spec) validate() error {
	if s.Hi < s.Lo {
		return fmt.Errorf("netsec: empty output range [%d, %d]", s.Lo, s.Hi)
	}
	switch s.Mode {
	case Truncate, Wrap:
		return nil
	default:
		return fmt.Errorf("netsec: unknown mode %d", int(s.Mode))
	}
}

// Distribution returns the exact output distribution P(output = o) for a
// dataset whose true count is c, applying truncation or wrap-around folding
// at the boundaries of [Lo, Hi]. Duplicate offsets are combined.
func (t NoiseTable) Distribution(c int, s Spec) (map[int]*big.Rat, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	if c < s.Lo || c > s.Hi {
		return nil, fmt.Errorf("netsec: true count %d outside [%d, %d]", c, s.Lo, s.Hi)
	}
	n := s.Hi - s.Lo + 1
	dist := make(map[int]*big.Rat)
	add := func(o int, p *big.Rat) {
		if p.Sign() == 0 {
			return
		}
		if acc, ok := dist[o]; ok {
			acc.Add(acc, p)
		} else {
			dist[o] = new(big.Rat).Set(p)
		}
	}
	for _, e := range t.Entries {
		raw := c + e.Offset
		var o int
		switch s.Mode {
		case Truncate:
			o = raw
			if o < s.Lo {
				o = s.Lo
			}
			if o > s.Hi {
				o = s.Hi
			}
		case Wrap:
			r := (raw - s.Lo) % n
			if r < 0 {
				r += n
			}
			o = s.Lo + r
		}
		add(o, e.Prob)
	}
	return dist, nil
}
