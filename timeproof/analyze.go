package timeproof

import "sort"

// defaultStateCap bounds enumeration when the caller does not specify one.
const defaultStateCap = 4096

// GapExclusion is independent evidence that one feasible mask cannot cover a
// probe point inside a gap. It carries the mask's own endpoint witnesses.
type GapExclusion struct {
	Probe        *Rat
	KeptDomains  []string
	KeptReceipts []int
	Interval     Interval
	// Reason is "below-lower" or "above-upper".
	Reason string
}

// Gap is a hole between two normalized feasible intervals.
type Gap struct {
	Lo         *Rat // left boundary (end of previous interval)
	Hi         *Rat // right boundary (start of next interval)
	Exclusions []GapExclusion
}

// Outcome is the feasible interval contributed by one consistent domain mask.
type Outcome struct {
	KeptDomains  []string
	KeptReceipts []int
	Interval     Interval
}

// ConflictEvidence is an inconsistent mask together with a negative cycle.
type ConflictEvidence struct {
	FaultyDomains []string
	KeptDomains   []string
	KeptReceipts  []int
	Cycle         []Edge
	CycleSum      *Rat
}

// Verdict is the certificate-window classification.
type Verdict string

const (
	VerdictValid     Verdict = "definitely-valid"
	VerdictExpired   Verdict = "definitely-expired"
	VerdictUncertain Verdict = "uncertain"
	VerdictConflict  Verdict = "conflict"
)

// Solution is the full result of a time-proof query.
type Solution struct {
	Nonce      string
	DomainName []string // all trust domains, sorted
	F          int
	Truncated  bool

	// Union is the normalized, pairwise-disjoint union of closed feasible
	// intervals. Nil Lo/Hi denote unboundedness.
	Union []Interval
	// Gaps are the holes between consecutive Union intervals.
	Gaps []Gap
	// Outcomes lists every consistent admissible mask and its interval.
	Outcomes []Outcome
	// Conflicts lists inconsistent admissible masks with negative cycles.
	Conflicts []ConflictEvidence

	// HasEvidence is false when there are no receipts at all (unbounded,
	// no conflict).
	HasEvidence bool
}

type analysis struct {
	q           *Query
	domainNames []string
	domains     [][]int // domain -> receipt indices
	cap         int
	states      int
	truncated   bool
	feasible    []maskResult2
	infeasible  []maskWithMask
}

type maskWithMask struct {
	kept   []bool
	result maskResult
}

// Analyze runs the full proof query.
func Analyze(q *Query) (*Solution, error) {
	if err := validate(q); err != nil {
		return nil, err
	}

	a := &analysis{q: q, cap: q.StateCap}
	if a.cap <= 0 {
		a.cap = defaultStateCap
	}
	a.domainNames, a.domains = buildDomains(q)

	a.enumerate()

	sol := &Solution{
		Nonce:       q.Nonce,
		DomainName:  append([]string(nil), a.domainNames...),
		F:           q.MaxFaulty,
		Truncated:   a.truncated,
		HasEvidence: len(q.Receipts) > 0,
	}

	for _, mr := range a.feasible {
		sol.Outcomes = append(sol.Outcomes, Outcome{
			KeptDomains:  domainsOfMask(q, mr.kept),
			KeptReceipts: receiptsOfMask(mr.kept),
			Interval:     mr.result.interval,
		})
	}
	for _, mm := range a.infeasible {
		sol.Conflicts = append(sol.Conflicts, ConflictEvidence{
			FaultyDomains: faultyDomains(q, a.domainNames, mm.kept),
			KeptDomains:   domainsOfMask(q, mm.kept),
			KeptReceipts:  receiptsOfMask(mm.kept),
			Cycle:         mm.result.cycle,
			CycleSum:      cycleSum(mm.result.cycle),
		})
	}

	sol.Union, sol.Gaps = normalize(q, a.feasible, a.infeasible)
	return sol, nil
}

// enumerate visits every admissible domain mask iteratively. Each bit of a
// domain word selects kept (1) or faulty (0); masks using more than f faulty
// domains are skipped. The empty mask is always evaluated first so that the
// unbounded fallback can never be lost to the state cap.
func (a *analysis) enumerate() {
	d := len(a.domains)
	total := 1 << d
	order := make([]int, 0, total)
	// Empty mask (all domains faulty) first when it is within budget.
	if d <= a.q.MaxFaulty {
		order = append(order, 0)
	}
	for word := 1; word < total; word++ {
		order = append(order, word)
	}

	for _, word := range order {
		keptDomainCount := popCount(word)
		faulty := d - keptDomainCount
		if faulty > a.q.MaxFaulty {
			continue
		}
		if a.states >= a.cap {
			a.truncated = true
			continue
		}
		kept := make([]bool, len(a.q.Receipts))
		// Bit di corresponds to a.domains[di], whose order matches the sorted
		// domain list built by buildDomains.
		for di := 0; di < d; di++ {
			if word&(1<<di) != 0 {
				for _, ri := range a.domains[di] {
					kept[ri] = true
				}
			}
		}
		a.states++
		mr := solveMask(a.q, a.domainNames, kept)
		if mr.feasible {
			a.feasible = append(a.feasible, maskResult2{kept: kept, result: mr})
		} else {
			a.infeasible = append(a.infeasible, maskWithMask{kept: kept, result: mr})
		}
	}
}

func popCount(x int) int {
	c := 0
	for x != 0 {
		x &= x - 1
		c++
	}
	return c
}

// maskResult2 pairs a solved result with its kept mask.
type maskResult2 struct {
	kept   []bool
	result maskResult
}

func receiptsOfMask(kept []bool) []int {
	var out []int
	for i, k := range kept {
		if k {
			out = append(out, i)
		}
	}
	return out
}

func faultyDomains(q *Query, names []string, kept []bool) []string {
	dropped := map[string]bool{}
	for i, r := range q.Receipts {
		if !kept[i] {
			dropped[r.Domain] = true
		}
	}
	var out []string
	for _, n := range names {
		if dropped[n] {
			out = append(out, n)
		}
	}
	return out
}

func cycleSum(cycle []Edge) *Rat {
	s := RatInt(0)
	for _, e := range cycle {
		s.Add(s, e.Weight)
	}
	return s
}

func buildDomains(q *Query) ([]string, [][]int) {
	order := []string{}
	seen := map[string]bool{}
	for _, r := range q.Receipts {
		if !seen[r.Domain] {
			seen[r.Domain] = true
			order = append(order, r.Domain)
		}
	}
	sort.Strings(order)
	domains := make([][]int, len(order))
	id := map[string]int{}
	for i, n := range order {
		id[n] = i
	}
	for i, r := range q.Receipts {
		domains[id[r.Domain]] = append(domains[id[r.Domain]], i)
	}
	return order, domains
}

// normalize merges the feasible intervals into a disjoint closed-union and
// builds independently checkable gap exclusions.
func normalize(q *Query, feasible []maskResult2, infeasible []maskWithMask) ([]Interval, []Gap) {
	type iv struct {
		lo, hi *Rat
		it     Interval
	}
	var ivs []iv
	for _, mr := range feasible {
		ivs = append(ivs, iv{
			lo: ratCopy(mr.result.interval.Lo),
			hi: ratCopy(mr.result.interval.Hi),
			it: mr.result.interval,
		})
	}
	sort.SliceStable(ivs, func(a, b int) bool {
		c := ratCmp(ivs[a].lo, ivs[b].lo)
		if c != 0 {
			return c < 0
		}
		return ratCmp(ivs[a].hi, ivs[b].hi) < 0
	})

	var merged []Interval
	for _, v := range ivs {
		if len(merged) == 0 {
			merged = append(merged, cloneInterval(v.it))
			continue
		}
		last := &merged[len(merged)-1]
		// Closed intervals touching (last.Hi >= v.lo) merge; equality merges
		// because the shared endpoint is feasible in both.
		if last.Hi == nil || v.lo == nil || last.Hi.Cmp(v.lo) >= 0 {
			if ratCmp(v.hi, last.Hi) > 0 {
				last.Hi = ratCopy(v.hi)
				last.HiWitness = v.it.HiWitness
			}
			if ratCmp(v.lo, last.Lo) < 0 {
				last.Lo = ratCopy(v.lo)
				last.LoWitness = v.it.LoWitness
			}
			continue
		}
		merged = append(merged, cloneInterval(v.it))
	}

	var gaps []Gap
	for i := 0; i+1 < len(merged); i++ {
		left, right := merged[i], merged[i+1]
		gap := Gap{Lo: ratCopy(left.Hi), Hi: ratCopy(right.Lo)}
		probe := midpoint(left.Hi, right.Lo)
		for _, mr := range feasible {
			it := mr.result.interval
			reason := ""
			if it.Lo != nil && probe.Cmp(it.Lo) < 0 {
				reason = "below-lower"
			} else if it.Hi != nil && probe.Cmp(it.Hi) > 0 {
				reason = "above-upper"
			}
			if reason == "" {
				continue
			}
			gap.Exclusions = append(gap.Exclusions, GapExclusion{
				Probe:        ratCopy(probe),
				KeptDomains:  domainsOfMask(q, mr.kept),
				KeptReceipts: receiptsOfMask(mr.kept),
				Interval:     cloneInterval(it),
				Reason:       reason,
			})
		}
		gaps = append(gaps, gap)
	}

	_ = infeasible
	return merged, gaps
}

func cloneInterval(it Interval) Interval {
	return Interval{
		Lo:        ratCopy(it.Lo),
		Hi:        ratCopy(it.Hi),
		LoWitness: it.LoWitness,
		HiWitness: it.HiWitness,
	}
}

// midpoint returns the exact rational midpoint of two finite rationals.
func midpoint(a, b *Rat) *Rat {
	s := new(Rat).Add(a, b)
	return s.Quo(s, RatInt(2))
}
