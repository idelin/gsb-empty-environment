package sdf

// Solve answers the bounded-buffer scheduling question described by p.
//
// It first verifies rate consistency (positive repetition vector), then
// derives a per-edge structural lower bound, and finally enumerates capacity
// vectors in increasing total capacity with edge-id lexicographic tie-break
// until an exact liveness + periodicity + sliding-window analysis succeeds.
// The first successful vector is therefore the unique minimum under the
// required ordering; malformed inputs return an error.
func Solve(p *Problem) (*Result, error) {
	idx, err := p.validate()
	if err != nil {
		return nil, err
	}
	E := len(p.Edges)

	q, rank, consistent := repVector(p, idx)
	if !consistent {
		return &Result{
			Status: StatusInconsistent,
			Reason: "no positive repetition vector: rates cannot balance in bounded buffers",
			Cert:   &Certificate{ConsistencyRank: rank},
		}, nil
	}

	lb := make([]int, E)
	witness := make([]int, E)
	for eid := range p.Edges {
		b, w, ok := edgeLowerBound(p, eid)
		if !ok {
			return &Result{
				Status:     StatusOverrun,
				Reason:     "edge " + itoa(eid) + " cannot be bounded within MaxCap",
				Repetition: q,
				Cert: &Certificate{
					EdgeLowerBound:    lb,
					LowerBoundWitness: witness,
					ConsistencyRank:   rank,
				},
			}, nil
		}
		lb[eid], witness[eid] = b, w
	}

	// Honor any user-pinned capacities.
	fixed := make([]bool, E)
	for eid, e := range p.Edges {
		if e.Cap > 0 {
			if e.Cap < lb[eid] {
				return &Result{
					Status:     StatusOverrun,
					Reason:     "pinned capacity of edge " + itoa(eid) + " below structural lower bound",
					Repetition: q,
				}, nil
			}
			lb[eid] = e.Cap
			fixed[eid] = true
		}
	}

	enumCount := 0
	var best []int
	var bestOutcome timedOutcome
	enumerateVectors(lb, fixed, p.MaxCap, func(caps []int) bool {
		enumCount++
		outcome := evaluate(p, idx, caps, q)
		if outcome.status == StatusFeasible {
			best = append([]int(nil), caps...)
			bestOutcome = outcome
			return false
		}
		// Keep the most diagnostic non-feasible outcome: a timing/window
		// result is more informative than a structural deadlock, and an
		// explicit status always beats the unknown placeholder.
		if statusPriority(outcome.status) > statusPriority(bestOutcome.status) {
			bestOutcome = outcome
		}
		return true
	})

	if best == nil {
		status := bestOutcome.status
		if status == StatusUnknown {
			status = StatusInconclusive
		}
		return &Result{
			Status:     status,
			Reason:     bestOutcome.reason,
			Repetition: q,
			Cert:       buildCert(p, idx, lb, witness, nil, enumCount, rank),
		}, nil
	}

	cert := buildCert(p, idx, lb, witness, best, enumCount, rank)
	total := 0
	for _, c := range best {
		total += c
	}
	return &Result{
		Status:     StatusFeasible,
		Reason:     "minimum total edge capacity with edge-id lexicographic tie-break",
		Cap:        best,
		TotalCap:   total,
		Schedule:   bestOutcome.sched,
		Cert:       cert,
		Repetition: q,
	}, nil
}

// evaluate runs untimed liveness first (cheap structural proof) and then the
// timed periodic simulation for window and overrun analysis.
func evaluate(p *Problem, idx *index, caps []int, q []int64) timedOutcome {
	live := checkLiveness(p, idx, caps)
	if live.truncated {
		return timedOutcome{status: StatusInconclusive, reason: "liveness search envelope exhausted"}
	}
	if !live.live {
		reason := "deadlock: no firing can proceed"
		if live.deadEdge >= 0 {
			reason = describeStarve(p, live.deadEdge)
		} else if live.fullEdge >= 0 {
			reason = describeFull(p, live.fullEdge)
		}
		return timedOutcome{status: StatusDeadlock, reason: reason}
	}
	return simulate(p, idx, caps, q)
}

// statusPriority orders non-feasible outcomes by diagnostic value.
func statusPriority(s Status) int {
	switch s {
	case StatusWindow, StatusOverrun:
		return 3
	case StatusDeadlock:
		return 2
	case StatusInconclusive:
		return 1
	default:
		return 0
	}
}

// enumerateVectors visits all capacity vectors respecting lower bounds, fixed
// pins and MaxCap, in increasing total capacity then lexicographic edge order.
// callback returning false stops enumeration.
func enumerateVectors(lb []int, fixed []bool, maxCap int, cb func(caps []int) bool) {
	E := len(lb)
	minTotal := 0
	for _, v := range lb {
		minTotal += v
	}
	maxTotal := E * maxCap
	caps := append([]int(nil), lb...)
	for total := minTotal; total <= maxTotal; total++ {
		var dfs func(pos, remaining int) bool
		dfs = func(pos, remaining int) bool {
			if pos == E {
				return remaining == 0 && !cb(caps)
			}
			if fixed[pos] {
				need := caps[pos] - lb[pos]
				if need > remaining {
					return false
				}
				return dfs(pos+1, remaining-need)
			}
			for v := lb[pos]; v <= maxCap && v-lb[pos] <= remaining; v++ {
				caps[pos] = v
				if dfs(pos+1, remaining-(v-lb[pos])) {
					return true
				}
			}
			caps[pos] = lb[pos]
			return false
		}
		if dfs(0, total-minTotal) {
			return
		}
	}
}

// buildCert assures the no-further-reduction witness. Every chosen edge whose
// capacity exceeds its structural lower bound is re-evaluated at cap-1; the
// certificate marks it irreducible exactly when that decrement fails.
func buildCert(p *Problem, idx *index, lb, witness, chosen []int, enumCount, rank int) *Certificate {
	E := len(p.Edges)
	cert := &Certificate{
		EdgeLowerBound:    append([]int(nil), lb...),
		LowerBoundWitness: append([]int(nil), witness...),
		Reduction:         make([]bool, E),
		Enumeration:       enumCount,
		ConsistencyRank:   rank,
	}
	if chosen == nil {
		return cert
	}
	q, _, ok := repVector(p, idx)
	if !ok {
		return cert
	}
	for eid := range chosen {
		if chosen[eid] <= lb[eid] {
			continue // certified irreducible by the structural lower bound
		}
		reduced := append([]int(nil), chosen...)
		reduced[eid]--
		outcome := evaluate(p, idx, reduced, q)
		cert.Reduction[eid] = outcome.status != StatusFeasible
	}
	return cert
}
