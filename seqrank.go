// Package seqrank implements exact randomization inference for a
// sequentially monitored two-sample rank-sum test.
//
// Observations arrive in a fixed time order. All n samples share one final
// random assignment into a treated group of size Treat and a control group
// of size n-Treat; every prefix of the time order is labeled by that same
// final assignment. At each specified look time (a prefix size), the
// Wilcoxon rank-sum of the treated group is computed from the average
// (mid)ranks of the pooled prefix observations, and the two-sided statistic
//
//	stat = |W - E[W]| / Norm
//
// is compared against Threshold, where E[W] = m*(k+1)/2 for m treated among
// the k prefix samples. Equality counts as crossing. A look at a prefix in
// which either group is empty is skipped. Monitoring stops at the first
// crossing look.
//
// The package enumerates all C(n, Treat) final assignments independently
// and recomputes every prefix for each of them, yielding the exact joint
// randomization distribution of the first crossing time and of the
// ever-crossing event. It never multiplies per-look p-values: repeated
// looks are inherently dependent because all prefixes share one assignment.
// Tied values are distinct samples (assignment is by sample identity) and
// share average ranks.
//
// Enumeration can be budgeted: State.Step processes at most a given number
// of assignments and the State (including unfinished counts and a resume
// cursor) can be saved and continued later. Probability accessors refuse to
// report exact probabilities until enumeration is Complete, so a partial
// enumeration is never mistaken for an exact probability.
package seqrank

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/bits"
)

var bigOne = big.NewInt(1)

// Config describes one sequential monitoring plan.
type Config struct {
	// Values are the observations in time order; len(Values) is n.
	// Equal values are tied and share average ranks, but remain
	// distinct samples for the assignment enumeration.
	Values []float64
	// Treat is the final treated-group size, 0 <= Treat <= n.
	Treat int
	// Looks are the prefix sizes at which the statistic is inspected,
	// strictly increasing, each in [1, n]. Looks at prefixes where one
	// group is empty are skipped automatically.
	Looks []int
	// Threshold is the stopping threshold on the normalized statistic.
	// Crossing means stat >= Threshold (equality counts).
	Threshold *big.Rat
	// Norm is the positive rational normalization factor.
	Norm *big.Rat
}

// Plan is a validated, precompiled monitoring plan.
type Plan struct {
	n      int
	treat  int
	looks  []int
	isLook []bool
	rank2  [][]int64 // rank2[k-1][i] = 2 * average rank of sample i within prefix k (i < k)
	crit   int64     // crossing iff |2W - m*(k+1)| >= crit
	total  *big.Int
}

// Prepare validates cfg and precomputes per-prefix ranks and the integer
// crossing criterion.
func Prepare(cfg *Config) (*Plan, error) {
	n := len(cfg.Values)
	if n < 1 || n > 62 {
		return nil, fmt.Errorf("seqrank: need 1 <= n <= 62 samples, got %d", n)
	}
	for i, v := range cfg.Values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("seqrank: Values[%d] is not finite", i)
		}
	}
	if cfg.Treat < 0 || cfg.Treat > n {
		return nil, fmt.Errorf("seqrank: Treat %d outside [0, %d]", cfg.Treat, n)
	}
	if cfg.Threshold == nil {
		return nil, errors.New("seqrank: nil Threshold")
	}
	if cfg.Norm == nil || cfg.Norm.Sign() <= 0 {
		return nil, errors.New("seqrank: Norm must be a positive rational")
	}
	isLook := make([]bool, n+1)
	looks := make([]int, len(cfg.Looks))
	for i, k := range cfg.Looks {
		if k < 1 || k > n {
			return nil, fmt.Errorf("seqrank: look %d outside [1, %d]", k, n)
		}
		if i > 0 && k <= cfg.Looks[i-1] {
			return nil, errors.New("seqrank: Looks must be strictly increasing")
		}
		isLook[k] = true
		looks[i] = k
	}

	// Twice the average rank of sample i within the prefix of length k:
	// 2*rank_i = 1 + 2*#{j: v_j < v_i} + #{j: v_j = v_i}.
	rank2 := make([][]int64, n)
	for k := 1; k <= n; k++ {
		r := make([]int64, k)
		for i := 0; i < k; i++ {
			var less, eq int64
			for j := 0; j < k; j++ {
				switch {
				case cfg.Values[j] < cfg.Values[i]:
					less++
				case cfg.Values[j] == cfg.Values[i]:
					eq++
				}
			}
			r[i] = 1 + 2*less + eq
		}
		rank2[k-1] = r
	}

	// Crossing condition: |W - E| / Norm >= Threshold
	//   <=> |2W - m*(k+1)| >= 2*Threshold*Norm.
	// The left side is an integer, so it crosses iff it is >= crit with
	// crit = ceil(2*Threshold*Norm). |2W - m*(k+1)| <= n*(n+1), so a
	// larger crit can never cross and a non-positive crit always does.
	maxD2 := int64(n) * int64(n+1)
	t2 := new(big.Rat).Mul(cfg.Threshold, cfg.Norm)
	t2.Mul(t2, big.NewRat(2, 1))
	crit := int64(0)
	if t2.Sign() > 0 {
		num, den := t2.Num(), t2.Denom()
		q, r := new(big.Int).QuoRem(num, den, new(big.Int))
		if r.Sign() != 0 {
			q.Add(q, bigOne)
		}
		if !q.IsInt64() || q.Int64() > maxD2 {
			crit = maxD2 + 1 // never crosses
		} else {
			crit = q.Int64()
		}
	}

	total := new(big.Int).Binomial(int64(n), int64(cfg.Treat))
	return &Plan{
		n: n, treat: cfg.Treat, looks: looks, isLook: isLook,
		rank2: rank2, crit: crit, total: total,
	}, nil
}

// Total returns the exact number C(n, Treat) of final assignments.
func (p *Plan) Total() *big.Int { return new(big.Int).Set(p.total) }

// Looks returns the look times (prefix sizes) of the plan.
func (p *Plan) Looks() []int { return append([]int(nil), p.looks...) }

// eval walks one final assignment through all prefixes and reports the
// first look at which the statistic crosses, if any. mask bit i marks
// sample i (in time order) as treated. The rank-sum at each look uses the
// ranks recomputed within that prefix, not the ranks samples had on arrival.
func (p *Plan) eval(mask uint64) (look int, crossed bool) {
	var m int64
	for k := 1; k <= p.n; k++ {
		if mask>>(k-1)&1 == 1 {
			m++
		}
		if !p.isLook[k] || m == 0 || m == int64(k) {
			continue
		}
		var w2 int64
		r := p.rank2[k-1]
		sub := mask & (uint64(1)<<k - 1)
		for sub != 0 {
			w2 += r[bits.TrailingZeros64(sub)]
			sub &= sub - 1
		}
		d := w2 - m*int64(k+1)
		if d < 0 {
			d = -d
		}
		if d >= p.crit {
			return k, true
		}
	}
	return 0, false
}

// firstMask returns the lowest n-bit mask with k bits set.
func firstMask(k int) uint64 { return uint64(1)<<k - 1 }

// nextComb steps to the next bit mask with the same popcount
// (Gosper's hack). It returns 0 once the enumeration is exhausted.
func nextComb(x uint64) uint64 {
	c := x & -x
	if c == 0 {
		return 0
	}
	r := x + c
	return (((r ^ x) >> 2) / c) | r
}

// State holds the exact counts accumulated over enumerated assignments.
// It doubles as a resumable cursor: all fields are exported so a State
// (including unfinished partial counts) can be serialized, e.g. with
// encoding/gob, and continued later with Step.
type State struct {
	Total    *big.Int         // total number of final assignments
	Done     *big.Int         // assignments enumerated so far
	Crossed  *big.Int         // assignments that crossed at some look
	Cross    map[int]*big.Int // first-crossing counts keyed by look prefix size
	Next     uint64           // resume cursor: next assignment mask to process
	Complete bool             // true once every assignment has been enumerated
}

// NewState returns a fresh enumeration state for p.
func NewState(p *Plan) *State {
	return &State{
		Total:   new(big.Int).Set(p.total),
		Done:    new(big.Int),
		Crossed: new(big.Int),
		Cross:   make(map[int]*big.Int),
		Next:    firstMask(p.treat),
	}
}

// Step enumerates at most budget further assignments (budget <= 0 means
// all remaining) and accumulates exact counts into s.
func (s *State) Step(p *Plan, budget int64) {
	if s.Complete {
		return
	}
	mask := s.Next
	var done int64
	for {
		if budget > 0 && done >= budget {
			s.Next = mask
			return
		}
		look, crossed := p.eval(mask)
		done++
		s.Done.Add(s.Done, bigOne)
		if crossed {
			s.Crossed.Add(s.Crossed, bigOne)
			c := s.Cross[look]
			if c == nil {
				c = new(big.Int)
				s.Cross[look] = c
			}
			c.Add(c, bigOne)
		}
		next := nextComb(mask)
		if next == 0 || next>>p.n != 0 {
			s.Complete = true
			s.Next = 0
			return
		}
		mask = next
	}
}

// Run prepares cfg and enumerates all assignments in one shot.
func Run(cfg *Config) (*State, error) {
	p, err := Prepare(cfg)
	if err != nil {
		return nil, err
	}
	s := NewState(p)
	s.Step(p, 0)
	return s, nil
}

// NeverCrossed returns the number of enumerated assignments that did not
// cross at any look.
func (s *State) NeverCrossed() *big.Int {
	return new(big.Int).Sub(s.Done, s.Crossed)
}

// Remaining returns the number of assignments not yet enumerated.
func (s *State) Remaining() *big.Int {
	return new(big.Int).Sub(s.Total, s.Done)
}

// Progress returns the enumerated fraction Done/Total. It is a progress
// indicator, not a probability.
func (s *State) Progress() *big.Rat {
	return new(big.Rat).SetFrac(s.Done, s.Total)
}

// ProbEverCrossed returns the exact randomization probability that
// monitoring stops at some look. ok is false while enumeration is
// incomplete: partial counts are never reported as exact probabilities.
func (s *State) ProbEverCrossed() (p *big.Rat, ok bool) {
	if !s.Complete {
		return nil, false
	}
	return new(big.Rat).SetFrac(s.Crossed, s.Total), true
}

// ProbNeverCrossed returns the exact probability mass that never crosses.
func (s *State) ProbNeverCrossed() (p *big.Rat, ok bool) {
	if !s.Complete {
		return nil, false
	}
	return new(big.Rat).SetFrac(s.NeverCrossed(), s.Total), true
}

// ProbFirstCross returns the exact probability that the first crossing
// happens at the given look (prefix size), i.e. the probability of an
// observed stopping time.
func (s *State) ProbFirstCross(look int) (p *big.Rat, ok bool) {
	if !s.Complete {
		return nil, false
	}
	c := s.Cross[look]
	if c == nil {
		c = new(big.Int)
	}
	return new(big.Rat).SetFrac(c, s.Total), true
}

// Distribution returns the exact first-crossing time distribution: a map
// from look prefix size to its probability, plus the never-crossing mass.
// The probabilities sum to 1. ok is false unless enumeration is complete.
func (s *State) Distribution() (first map[int]*big.Rat, never *big.Rat, ok bool) {
	if !s.Complete {
		return nil, nil, false
	}
	first = make(map[int]*big.Rat, len(s.Cross))
	for look, c := range s.Cross {
		first[look] = new(big.Rat).SetFrac(c, s.Total)
	}
	return first, new(big.Rat).SetFrac(s.NeverCrossed(), s.Total), true
}
