package timeproof

import "fmt"

// VerifyWitness independently re-checks an endpoint witness: the assignment
// satisfies every reconstructed constraint, the tight path telescopes to the
// claimed endpoint, and that endpoint matches the assignment at the query
// node. It does not re-run Bellman-Ford.
func VerifyWitness(q *Query, w *Witness) error {
	if w == nil {
		return fmt.Errorf("timeproof: nil witness")
	}
	kept := keptFromReceipts(q, w.KeptReceipts)
	nodes, edges := buildGraph(q, kept)
	index := map[NodeLabel]int{}
	for i, n := range nodes {
		index[n] = i
	}

	assign := map[NodeLabel]*Rat{}
	for _, nv := range w.Assignment {
		if _, ok := index[nv.Node]; !ok {
			return fmt.Errorf("timeproof: witness assigns unknown node %v", nv.Node)
		}
		if nv.Value == nil {
			return fmt.Errorf("timeproof: witness has nil assignment")
		}
		assign[nv.Node] = nv.Value
	}
	if len(assign) != len(nodes) {
		return fmt.Errorf("timeproof: witness assignment is incomplete")
	}

	// Feasibility: x_To - x_From <= Weight for every constraint.
	for _, e := range edges {
		lhs := new(Rat).Sub(assign[e.To], assign[e.From])
		if lhs.Cmp(e.Weight) > 0 {
			return fmt.Errorf("timeproof: witness assignment violates edge %v->%v", e.From, e.To)
		}
	}

	// Path continuity and telescoping.
	if len(w.Path) == 0 {
		return fmt.Errorf("timeproof: witness has empty tight path")
	}
	sum := RatInt(0)
	cur := w.Path[0].From
	for _, e := range w.Path {
		if e.From != cur {
			return fmt.Errorf("timeproof: witness path is not contiguous")
		}
		sum.Add(sum, e.Weight)
		cur = e.To
	}
	queryValue := assign[queryNode()]
	switch w.Bound {
	case BoundUpper:
		if w.Path[0].From != originNode() {
			return fmt.Errorf("timeproof: upper path must start at origin")
		}
		if cur != queryNode() {
			return fmt.Errorf("timeproof: upper path must end at query node")
		}
		if sum.Cmp(w.BoundValue) != 0 {
			return fmt.Errorf("timeproof: upper path sum %v != bound %v", sum, w.BoundValue)
		}
		if queryValue.Cmp(w.BoundValue) != 0 {
			return fmt.Errorf("timeproof: upper assignment does not attain bound")
		}
	case BoundLower:
		if w.Path[0].From != queryNode() {
			return fmt.Errorf("timeproof: lower path must start at query")
		}
		if w.Path[len(w.Path)-1].To != originNode() {
			return fmt.Errorf("timeproof: lower path must end at origin")
		}
		if negRat(sum).Cmp(w.BoundValue) != 0 {
			return fmt.Errorf("timeproof: lower path sum %v must telescope to -bound %v", sum, w.BoundValue)
		}
		if queryValue.Cmp(w.BoundValue) != 0 {
			return fmt.Errorf("timeproof: lower assignment does not attain bound")
		}
	default:
		return fmt.Errorf("timeproof: unknown bound kind %q", w.Bound)
	}
	return nil
}

// VerifyCycle independently re-checks that a negative cycle is real: edges are
// contiguous and their weights sum to a strictly negative rational.
func VerifyCycle(c ConflictEvidence) error {
	if len(c.Cycle) == 0 {
		return fmt.Errorf("timeproof: empty conflict cycle")
	}
	sum := RatInt(0)
	cur := c.Cycle[0].From
	for _, e := range c.Cycle {
		if e.From != cur {
			return fmt.Errorf("timeproof: conflict cycle is not contiguous")
		}
		sum.Add(sum, e.Weight)
		cur = e.To
	}
	if cur != c.Cycle[0].From {
		return fmt.Errorf("timeproof: conflict cycle does not close")
	}
	if sum.Sign() >= 0 {
		return fmt.Errorf("timeproof: cycle weight %v is not negative", sum)
	}
	if c.CycleSum == nil || sum.Cmp(c.CycleSum) != 0 {
		return fmt.Errorf("timeproof: cycle sum mismatch")
	}
	return nil
}

// VerifyGap independently re-checks a gap exclusion. The probe lies strictly
// inside the gap, every consistent mask's witness is valid and excludes the
// probe, and each exclusion reason is arithmetically correct. reference is the
// recomputed solution used to confirm coverage of every consistent mask; pass
// nil to recompute it (slower).
func VerifyGap(q *Query, g Gap, reference *Solution) error {
	if g.Lo == nil || g.Hi == nil {
		return fmt.Errorf("timeproof: gap must be bounded on both sides")
	}
	if g.Lo.Cmp(g.Hi) >= 0 {
		return fmt.Errorf("timeproof: gap has no positive width")
	}
	covered := map[int]bool{}
	for _, ex := range g.Exclusions {
		if ex.Probe.Cmp(g.Lo) <= 0 || ex.Probe.Cmp(g.Hi) >= 0 {
			return fmt.Errorf("timeproof: gap probe %v outside (%v,%v)", ex.Probe, g.Lo, g.Hi)
		}
		it := ex.Interval
		if it.LoWitness != nil {
			if err := VerifyWitness(q, it.LoWitness); err != nil {
				return err
			}
		}
		if it.HiWitness != nil {
			if err := VerifyWitness(q, it.HiWitness); err != nil {
				return err
			}
		}
		switch ex.Reason {
		case "below-lower":
			if it.Lo == nil || ex.Probe.Cmp(it.Lo) >= 0 {
				return fmt.Errorf("timeproof: below-lower exclusion is invalid")
			}
		case "above-upper":
			if it.Hi == nil || ex.Probe.Cmp(it.Hi) <= 0 {
				return fmt.Errorf("timeproof: above-upper exclusion is invalid")
			}
		default:
			return fmt.Errorf("timeproof: unknown gap exclusion reason %q", ex.Reason)
		}
		for _, ri := range ex.KeptReceipts {
			covered[ri] = true
		}
	}
	// Every consistent mask must contribute an exclusion (identified by its
	// kept receipts). Verify against the recomputed solution.
	re := reference
	if re == nil {
		var err error
		re, err = Analyze(q)
		if err != nil {
			return err
		}
	}
	got := map[string]bool{}
	for _, ex := range g.Exclusions {
		got[keyReceipts(ex.KeptReceipts)] = true
	}
	for _, oc := range re.Outcomes {
		if !got[keyReceipts(oc.KeptReceipts)] {
			return fmt.Errorf("timeproof: gap lacks exclusion for a consistent mask")
		}
	}
	_ = covered
	return nil
}

// Verify re-runs the whole analysis and cross-checks every structural claim of
// the returned solution, including all endpoint witnesses, cycles, gaps and
// the normalized disjoint union.
func (sol *Solution) Verify(q *Query) error {
	again, err := Analyze(q)
	if err != nil {
		return err
	}
	if sol.Truncated != again.Truncated {
		return fmt.Errorf("timeproof: truncated flag mismatch")
	}
	if len(sol.DomainName) != len(again.DomainName) {
		return fmt.Errorf("timeproof: domain list mismatch")
	}
	for i := range again.DomainName {
		if sol.DomainName[i] != again.DomainName[i] {
			return fmt.Errorf("timeproof: domain list mismatch")
		}
	}

	if len(sol.Outcomes) != len(again.Outcomes) {
		return fmt.Errorf("timeproof: outcome count mismatch")
	}
	wantOutcomes := map[string]Interval{}
	for _, oc := range again.Outcomes {
		wantOutcomes[keyReceipts(oc.KeptReceipts)] = oc.Interval
	}
	for _, oc := range sol.Outcomes {
		want, ok := wantOutcomes[keyReceipts(oc.KeptReceipts)]
		if !ok {
			return fmt.Errorf("timeproof: unexpected outcome mask")
		}
		if ratCmp(oc.Interval.Lo, want.Lo) != 0 || ratCmp(oc.Interval.Hi, want.Hi) != 0 {
			return fmt.Errorf("timeproof: outcome interval mismatch")
		}
	}

	if len(sol.Conflicts) != len(again.Conflicts) {
		return fmt.Errorf("timeproof: conflict count mismatch")
	}
	for _, c := range sol.Conflicts {
		if err := VerifyCycle(c); err != nil {
			return err
		}
	}

	if len(sol.Union) != len(again.Union) {
		return fmt.Errorf("timeproof: union count mismatch")
	}
	for i, it := range sol.Union {
		want := again.Union[i]
		if ratCmp(it.Lo, want.Lo) != 0 || ratCmp(it.Hi, want.Hi) != 0 {
			return fmt.Errorf("timeproof: union interval mismatch")
		}
		if it.LoWitness != nil {
			if err := VerifyWitness(q, it.LoWitness); err != nil {
				return err
			}
		}
		if it.HiWitness != nil {
			if err := VerifyWitness(q, it.HiWitness); err != nil {
				return err
			}
		}
		if i > 0 && it.Lo != nil && sol.Union[i-1].Hi != nil && it.Lo.Cmp(sol.Union[i-1].Hi) <= 0 {
			return fmt.Errorf("timeproof: union is not disjoint with positive gaps")
		}
	}

	if len(sol.Gaps) != len(again.Gaps) {
		return fmt.Errorf("timeproof: gap count mismatch")
	}
	for _, g := range sol.Gaps {
		if err := VerifyGap(q, g, again); err != nil {
			return err
		}
	}
	return nil
}

func keptFromReceipts(q *Query, receipts []int) []bool {
	kept := make([]bool, len(q.Receipts))
	for _, i := range receipts {
		if i < 0 || i >= len(kept) {
			return kept
		}
		kept[i] = true
	}
	return kept
}

func keyReceipts(receipts []int) string {
	key := ""
	for _, r := range receipts {
		key += fmt.Sprintf("%d,", r)
	}
	return key
}
