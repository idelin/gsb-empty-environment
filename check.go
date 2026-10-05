package main

import "fmt"

// Verdict summarizes the overall checking outcome.
type Verdict string

const (
	// Proved: all three conditions hold and no transition left the finite range.
	Proved Verdict = "PROVED"
	// Failed: at least one concrete counterexample was found inside the range.
	Failed Verdict = "FAILED"
	// Indeterminate: no in-range counterexample, but some transition stepped
	// out of the finite range, so the bounded check cannot serve as a proof.
	Indeterminate Verdict = "INDETERMINATE"
)

// Transition is a concrete pre-state and its successor under the loop body.
type Transition struct {
	Pre  State `json:"pre"`
	Post State `json:"post"`
}

func (t Transition) String() string {
	return fmt.Sprintf("%s -> %s", t.Pre, t.Post)
}

// CheckResult is the outcome of one of the three verification conditions.
type CheckResult struct {
	Holds bool `json:"holds"`
	// Counterexamples (capped) witnessing the failure.
	Counterexamples []State      `json:"counterexamples,omitempty"`
	Transitions     []Transition `json:"transitions,omitempty"`
	// Total number of violations found (may exceed the stored samples).
	Violations int `json:"violations"`
}

// Report is the full checker output.
type Report struct {
	Init         CheckResult `json:"init"`
	Preservation CheckResult `json:"preservation"`
	Post         CheckResult `json:"post"`
	// Transitions whose post-state falls outside the finite range.
	// They are NOT proof failures, but their existence means the bounded
	// check does not constitute a proof: verdict becomes INDETERMINATE.
	OutOfBounds []Transition `json:"outOfBounds,omitempty"`
	// Total number of out-of-range transitions (may exceed stored samples).
	OutOfBoundsCount int     `json:"outOfBoundsCount"`
	Verdict          Verdict `json:"verdict"`
	StatesExplored   int64   `json:"statesExplored"`
}

const maxSamples = 5

// stateSpaceSize computes the number of states in the finite range,
// returning an error if it exceeds limit.
func (p *Program) stateSpaceSize(limit int64) (int64, error) {
	total := int64(1)
	for _, v := range p.Variables {
		b := p.Bounds.forVar(v)
		n := b.Max - b.Min + 1
		if limit > 0 && total > limit/n {
			return 0, fmt.Errorf("state space exceeds limit of %d states", limit)
		}
		total *= n
	}
	return total, nil
}

// enumerate calls fn for every state in the finite range (odometer order).
func (p *Program) enumerate(fn func(State)) {
	n := len(p.Variables)
	bounds := make([]Bound, n)
	for i, v := range p.Variables {
		bounds[i] = p.Bounds.forVar(v)
	}
	cur := make([]int64, n)
	for i, b := range bounds {
		cur[i] = b.Min
	}
	for {
		s := State{}
		for i, v := range p.Variables {
			s[v] = cur[i]
		}
		fn(s)
		i := n - 1
		for i >= 0 {
			cur[i]++
			if cur[i] <= bounds[i].Max {
				break
			}
			cur[i] = bounds[i].Min
			i--
		}
		if i < 0 {
			return
		}
	}
}

// Check runs all three verification conditions over the finite state space.
// limit caps the state space size (<=0 means the default limit).
func (p *Program) Check(limit int64) (*Report, error) {
	if limit <= 0 {
		limit = 10000000
	}
	if _, err := p.stateSpaceSize(limit); err != nil {
		return nil, err
	}

	rep := &Report{}
	inv, guard := p.Invariant, p.Guard

	p.enumerate(func(s State) {
		rep.StatesExplored++
		invHolds := allHold(inv, s)

		// Condition 1: init ==> invariant.
		if allHold(p.Init, s) && !invHolds {
			rep.Init.Violations++
			if len(rep.Init.Counterexamples) < maxSamples {
				rep.Init.Counterexamples = append(rep.Init.Counterexamples, s)
			}
		}

		// Condition 2: invariant /\ guard ==> invariant' (one step).
		if invHolds && allHold(guard, s) {
			next := p.step(s)
			tr := Transition{Pre: s, Post: next}
			if !p.inRange(next) {
				rep.OutOfBoundsCount++
				if len(rep.OutOfBounds) < maxSamples {
					rep.OutOfBounds = append(rep.OutOfBounds, tr)
				}
			} else if !allHold(inv, next) {
				rep.Preservation.Violations++
				if len(rep.Preservation.Transitions) < maxSamples {
					rep.Preservation.Transitions = append(rep.Preservation.Transitions, tr)
				}
			}
		}

		// Condition 3: invariant /\ !guard ==> post.
		if invHolds && !allHold(guard, s) && !allHold(p.Post, s) {
			rep.Post.Violations++
			if len(rep.Post.Counterexamples) < maxSamples {
				rep.Post.Counterexamples = append(rep.Post.Counterexamples, s)
			}
		}
	})

	rep.Init.Holds = rep.Init.Violations == 0
	rep.Preservation.Holds = rep.Preservation.Violations == 0
	rep.Post.Holds = rep.Post.Violations == 0

	switch {
	case !rep.Init.Holds || !rep.Preservation.Holds || !rep.Post.Holds:
		rep.Verdict = Failed
	case rep.OutOfBoundsCount > 0:
		rep.Verdict = Indeterminate
	default:
		rep.Verdict = Proved
	}
	return rep, nil
}
