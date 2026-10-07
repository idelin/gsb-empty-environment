// Package seqrank implements an exact sequential rank-sum test under
// randomization inference.
//
// Samples are observed in a fixed time order. The final group sizes N1
// and N2 are fixed by design, and every prefix of the observation
// sequence shares the same final assignment. At each specified look (a
// prefix length), a rank-sum statistic is computed from the combined
// average ranks of that prefix only, and the absolute deviation from
// its randomization expectation is standardized by a user-supplied
// rational factor. The experiment stops at the first look whose
// standardized deviation reaches its threshold; equality counts as
// crossing (two-sided). A look whose prefix leaves either group empty
// is skipped.
//
// The ever-crossing probability is computed exactly by enumerating all
// C(N1+N2, N1) final assignments and recomputing every prefix statistic
// under that shared assignment. Tied values are still distinct sample
// identities for the enumeration. P-values across looks are never
// multiplied together; the joint distribution comes from the shared
// assignment.
package seqrank

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// MaxN is the largest supported total sample size. It keeps every
// binomial coefficient exactly representable in an int64 and every
// assignment representable in a uint64 bitmask.
const MaxN = 24

// Look describes one inspection time of the sequential procedure.
type Look struct {
	// Prefix is the prefix length, in observation order, inspected at
	// this look. 1 <= Prefix <= total sample size.
	Prefix int
	// Scale is the positive rational normalization factor: the
	// statistic is |R - E| / Scale.
	Scale *big.Rat
	// Threshold is the non-negative rational crossing boundary;
	// |R - E| / Scale >= Threshold counts as crossing.
	Threshold *big.Rat
}

// Spec defines a sequential rank-sum experiment.
type Spec struct {
	// Values are the observed outcomes in time (observation) order.
	// Equal values are ties and share average ranks, but remain
	// distinct identities for assignment enumeration.
	Values []float64
	// N1 and N2 are the final group sizes; N1+N2 must equal
	// len(Values). The rank sum is taken over group 1.
	N1, N2 int
	// Looks are the inspection times, applied in the given order; the
	// first crossing look in this order stops the experiment.
	Looks []Look
	// Budget caps the number of final assignments enumerated. If the
	// total number of assignments exceeds Budget, enumeration stops
	// early and the result is marked incomplete: partial counts are
	// preserved but must not be read as exact probabilities.
	// Budget <= 0 means no limit.
	Budget int64
}

// Result holds the exact (or, when truncated by Budget, partial)
// distribution of the first crossing time.
type Result struct {
	// Complete is false when Budget truncated the enumeration; the
	// counts then cover only the first Processed assignments.
	Complete bool
	// Processed is the number of assignments actually enumerated.
	Processed int64
	// Total is the full number of assignments C(N1+N2, N1).
	Total int64
	// FirstCross[i] counts assignments whose first crossing occurs at
	// Looks[i].
	FirstCross []int64
	// Never counts assignments that never cross at any look.
	Never int64
}

// CrossProbability returns the exact probability of ever crossing, or
// nil when the enumeration is incomplete.
func (r Result) CrossProbability() *big.Rat {
	if !r.Complete || r.Total == 0 {
		return nil
	}
	crossed := r.Total - r.Never
	return new(big.Rat).SetFrac64(crossed, r.Total)
}

// FirstCrossingDistribution returns, per look, the exact probability
// that the first crossing happens at that look, followed by the
// never-crossing mass as the last element. It returns nil when the
// enumeration is incomplete.
func (r Result) FirstCrossingDistribution() []*big.Rat {
	if !r.Complete || r.Total == 0 {
		return nil
	}
	out := make([]*big.Rat, len(r.FirstCross)+1)
	for i, c := range r.FirstCross {
		out[i] = new(big.Rat).SetFrac64(c, r.Total)
	}
	out[len(r.FirstCross)] = new(big.Rat).SetFrac64(r.Never, r.Total)
	return out
}

// Rat is a convenience constructor for rational parameters.
func Rat(num, den int64) *big.Rat {
	return new(big.Rat).SetFrac64(num, den)
}

func validate(spec Spec) error {
	n := len(spec.Values)
	if n == 0 {
		return errors.New("seqrank: no observations")
	}
	if n > MaxN {
		return fmt.Errorf("seqrank: total sample size %d exceeds MaxN=%d", n, MaxN)
	}
	if spec.N1 < 0 || spec.N2 < 0 || spec.N1+spec.N2 != n {
		return fmt.Errorf("seqrank: group sizes %d+%d do not match %d observations", spec.N1, spec.N2, n)
	}
	if len(spec.Looks) == 0 {
		return errors.New("seqrank: no looks specified")
	}
	for i, lk := range spec.Looks {
		if lk.Prefix < 1 || lk.Prefix > n {
			return fmt.Errorf("seqrank: look %d has prefix %d outside [1, %d]", i, lk.Prefix, n)
		}
		if lk.Scale == nil || lk.Scale.Sign() <= 0 {
			return fmt.Errorf("seqrank: look %d needs a positive rational scale", i)
		}
		if lk.Threshold == nil || lk.Threshold.Sign() < 0 {
			return fmt.Errorf("seqrank: look %d needs a non-negative rational threshold", i)
		}
	}
	return nil
}

// doubledRanks assigns combined average ranks to values, sharing the
// average rank within each tie group, and returns them doubled so they
// are exact integers: a tie block at sorted positions i..j (0-based)
// has average rank (i+j+2)/2, hence doubled rank i+j+2.
func doubledRanks(values []float64) []int64 {
	n := len(values)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return values[order[a]] < values[order[b]] })
	dr := make([]int64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && values[order[j+1]] == values[order[i]] {
			j++
		}
		d := int64(i + j + 2)
		for k := i; k <= j; k++ {
			dr[order[k]] = d
		}
		i = j + 1
	}
	return dr
}

// lookPlan is a look compiled for fast exact evaluation: ranks are
// doubled integers and the crossing rule |R - E|/Scale >= Threshold is
// pre-multiplied into the integer comparison
//
//	|2R - m1*(p+1)| >= 2*Scale*Threshold.
type lookPlan struct {
	prefix int
	ranks  []int64  // doubled average ranks of the prefix positions
	bound  *big.Rat // 2*Scale*Threshold
}

func compileLook(values []float64, lk Look) lookPlan {
	bound := new(big.Rat).Mul(lk.Scale, lk.Threshold)
	bound.Add(bound, bound)
	return lookPlan{
		prefix: lk.Prefix,
		ranks:  doubledRanks(values[:lk.Prefix]),
		bound:  bound,
	}
}

// crosses reports whether the assignment given by mask (bit set =
// group 1) crosses at this look. A look whose prefix leaves either
// group empty is not inspected and never crosses.
func (lp lookPlan) crosses(mask uint64) bool {
	var m1, sum2 int64
	for pos := 0; pos < lp.prefix; pos++ {
		if mask>>uint(pos)&1 == 1 {
			m1++
			sum2 += lp.ranks[pos]
		}
	}
	p := int64(lp.prefix)
	if m1 == 0 || m1 == p {
		return false
	}
	// Doubled randomization expectation of the group-1 rank sum.
	dev2 := sum2 - m1*(p+1)
	if dev2 < 0 {
		dev2 = -dev2
	}
	return new(big.Rat).SetInt64(dev2).Cmp(lp.bound) >= 0
}

// firstCrossing returns the index of the first look that crosses for
// the given assignment mask, or -1.
func firstCrossing(mask uint64, plans []lookPlan) int {
	for i := range plans {
		if plans[i].crosses(mask) {
			return i
		}
	}
	return -1
}

func compileLooks(values []float64, looks []Look) []lookPlan {
	plans := make([]lookPlan, len(looks))
	for i, lk := range looks {
		plans[i] = compileLook(values, lk)
	}
	return plans
}

// Enumerate computes the exact distribution of the first crossing time
// by enumerating every final assignment of N1 samples to group 1 and
// recomputing each prefix statistic under that shared assignment.
func Enumerate(spec Spec) (Result, error) {
	if err := validate(spec); err != nil {
		return Result{}, err
	}
	n := len(spec.Values)
	total := binomial(n, spec.N1)
	res := Result{
		Complete:   true,
		Total:      total,
		FirstCross: make([]int64, len(spec.Looks)),
	}

	// Prefix ranks depend only on the values, so compile each look
	// once; the per-assignment work is an integer rank-sum over the
	// prefix.
	plans := compileLooks(spec.Values, spec.Looks)

	limit := total
	if spec.Budget > 0 && spec.Budget < total {
		limit = spec.Budget
		res.Complete = false
	}

	mask := uint64(1)<<uint(spec.N1) - 1
	for count := int64(0); count < limit; count++ {
		if first := firstCrossing(mask, plans); first >= 0 {
			res.FirstCross[first]++
		} else {
			res.Never++
		}
		res.Processed++
		mask = nextCombination(mask)
	}
	return res, nil
}

// StoppingTime returns the index of the first look at which the
// observed assignment crosses, and whether it crosses at all.
// group1[i] must be true when the i-th observed sample belongs to
// group 1.
func StoppingTime(values []float64, group1 []bool, looks []Look) (int, bool, error) {
	if len(values) != len(group1) {
		return -1, false, fmt.Errorf("seqrank: %d values but %d labels", len(values), len(group1))
	}
	n1 := 0
	for _, g := range group1 {
		if g {
			n1++
		}
	}
	spec := Spec{Values: values, N1: n1, N2: len(values) - n1, Looks: looks}
	if err := validate(spec); err != nil {
		return -1, false, err
	}
	var mask uint64
	for i, g := range group1 {
		if g {
			mask |= 1 << uint(i)
		}
	}
	plans := compileLooks(values, looks)
	if first := firstCrossing(mask, plans); first >= 0 {
		return first, true, nil
	}
	return -1, false, nil
}

// nextCombination steps to the next uint64 with the same popcount
// (Gosper's hack), enumerating every final assignment exactly once.
func nextCombination(x uint64) uint64 {
	u := x & -x
	v := u + x
	if v == 0 {
		return 0
	}
	return v + ((v^x)/u)>>2
}

// binomial returns C(n, k); exact for n <= MaxN.
func binomial(n, k int) int64 {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	r := int64(1)
	for i := 0; i < k; i++ {
		r = r * int64(n-i) / int64(i+1)
	}
	return r
}
