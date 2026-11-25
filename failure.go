package upgradeplan

import (
	"fmt"
	"strings"
)

const (
	ReasonDataLost        = "data_lost"
	ReasonRoundsExhausted = "rounds_exhausted"
	ReasonNoBroadcast     = "no_feasible_broadcast"
	ReasonInvalidPlan     = "invalid_plan"
)

// Witness is a concrete fleet used as a failure counterexample.
type Witness struct {
	Note  string `json:"note,omitempty"`
	Count int    `json:"count,omitempty"`
	Fleet Fleet  `json:"fleet"`
}

// BranchFailure explains why one observed-count child cannot be solved.
type BranchFailure struct {
	Count       int       `json:"count"`
	BeliefKey   string    `json:"belief_key"`
	Witnesses   []Witness `json:"witnesses,omitempty"`
	Failure     *Failure  `json:"failure,omitempty"`
	MissingPlan bool      `json:"missing_plan,omitempty"`
}

// CandidateFailure records the failure basis for one candidate broadcast.
type CandidateFailure struct {
	ReleaseID string          `json:"release_id"`
	Package   string          `json:"package,omitempty"`
	Reason    string          `json:"reason"`
	Branches  []BranchFailure `json:"branches,omitempty"`
}

// Failure is an exhaustive bounded-depth failure proof for one belief.
type Failure struct {
	Reason          string             `json:"reason"`
	RemainingRounds int                `json:"remaining_rounds"`
	BeliefKey       string             `json:"belief_key"`
	Witnesses       []Witness          `json:"witnesses,omitempty"`
	Candidates      []CandidateFailure `json:"candidates,omitempty"`
}

// InfeasibleError carries structured failure evidence.
type InfeasibleError struct {
	Failure *Failure
}

func (e *InfeasibleError) Error() string {
	if e == nil || e.Failure == nil {
		return "upgradeplan: infeasible"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "upgradeplan: no feasible broadcast plan (%s, %d rounds left)", e.Failure.Reason, e.Failure.RemainingRounds)
	for _, candidate := range e.Failure.Candidates {
		fmt.Fprintf(&b, "\n- release %q: %s", candidate.ReleaseID, candidate.Reason)
		counts := make([]string, 0, len(candidate.Branches))
		for _, branch := range candidate.Branches {
			counts = append(counts, fmt.Sprintf("%d", branch.Count))
		}
		if len(counts) > 0 {
			fmt.Fprintf(&b, "; failing counts: %s", strings.Join(counts, ","))
		}
	}
	return b.String()
}

// Unwrap supports errors.As/Is patterns.
func (e *InfeasibleError) Unwrap() error { return nil }

func dataLossFailure(belief *Belief, remaining int) *Failure {
	failure := &Failure{
		Reason:          ReasonDataLost,
		RemainingRounds: remaining,
		BeliefKey:       belief.Key(),
	}
	for _, fleet := range belief.Worlds {
		for i, device := range fleet {
			if reason := dataLossReason(device); reason != "" {
				failure.Witnesses = append(failure.Witnesses, Witness{
					Note:  fmt.Sprintf("device %d: %s", i, reason),
					Fleet: fleet,
				})
				break
			}
		}
	}
	return failure
}

func exhaustedFailure(np *normalizedProblem, belief *Belief, remaining int) *Failure {
	failure := &Failure{
		Reason:          ReasonRoundsExhausted,
		RemainingRounds: remaining,
		BeliefKey:       belief.Key(),
	}
	for _, fleet := range belief.Worlds {
		if ok, reason := np.goalFleet(fleet); !ok {
			failure.Witnesses = append(failure.Witnesses, Witness{Note: reason, Fleet: fleet})
		}
	}
	if len(failure.Witnesses) == 0 && len(belief.Worlds) > 0 {
		failure.Witnesses = append(failure.Witnesses, Witness{Note: "round budget exhausted", Fleet: belief.Worlds[0]})
	}
	return failure
}
