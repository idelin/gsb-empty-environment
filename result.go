package bgpsim

// Status is the terminal status of a synchronous simulation.
type Status string

const (
	// StatusConverged means the control state reached a fixed point.
	StatusConverged Status = "converged"
	// StatusOscillating means a state repeated with period >= 2.
	StatusOscillating Status = "oscillating"
	// StatusLimitReached means no repetition was detected before MaxRounds.
	StatusLimitReached Status = "limit_reached"
)

// CandidateEvaluation explains how one candidate fared in a node's
// sequential best-path selection during a round.
type CandidateEvaluation struct {
	Order    int   `json:"order"`
	Route    Route `json:"route"`
	Selected bool  `json:"selected"`

	// EliminatedAt is the stage that decided this candidate lost to
	// LostTo when Selected is false.
	EliminatedAt Stage  `json:"eliminated_at,omitempty"`
	LostTo       *Route `json:"lost_to,omitempty"`

	// MEDCompared is true when MED was actually compared with LostTo.
	MEDCompared bool `json:"med_compared,omitempty"`
	// MEDIncomparable is true when the MED step against LostTo was
	// skipped because the neighboring AS numbers differ.
	MEDIncomparable bool `json:"med_incomparable,omitempty"`
	// LowerMEDThanRival marks the salient "better MED but inadmissible
	// comparison" casualty: this candidate has a strictly lower MED than
	// LostTo yet MED was not compared.
	LowerMEDThanRival bool `json:"lower_med_than_rival,omitempty"`

	// IncomparabilityEliminated is set for a non-selected candidate that,
	// compared directly with the final selected best, loses at a post-MED
	// step after MED was skipped because the neighboring AS numbers differ.
	// This is the "sub-optimal candidate eliminated due to MED
	// incomparability" marker relative to the node's final best.
	IncomparabilityEliminated bool `json:"incomparability_eliminated,omitempty"`
}

// NodeChoice is one node's best route for one prefix in one round.
type NodeChoice struct {
	Node   NodeID `json:"node"`
	Prefix string `json:"prefix"`
	Best   *Route `json:"best,omitempty"`
	// Candidates is in canonical fold order, including the winner.
	Candidates []CandidateEvaluation `json:"candidates,omitempty"`
}

// DiscardReason classifies a rejected advertisement.
type DiscardReason string

const (
	// DiscardClusterLoop means the receiver's own ClusterID was present.
	DiscardClusterLoop DiscardReason = "cluster_loop"
)

// DiscardEvent records an incoming advertisement that was rejected.
type DiscardEvent struct {
	Round       int           `json:"round"`
	Node        NodeID        `json:"node"`
	From        NodeID        `json:"from"`
	Prefix      string        `json:"prefix"`
	Reason      DiscardReason `json:"reason"`
	ClusterList []ClusterID   `json:"cluster_list,omitempty"`
}

// EliminationMark flags a sub-optimal candidate whose decisive loss came
// after MED comparison was skipped due to neighboring-AS incomparability.
type EliminationMark struct {
	Round  int    `json:"round"`
	Node   NodeID `json:"node"`
	Prefix string `json:"prefix"`

	Candidate Route `json:"candidate"`
	Winner    Route `json:"winner"`

	// Stage is the post-MED step that actually eliminated the candidate.
	Stage Stage `json:"stage"`
	// CandidateLowerMED reports the candidate carries a lower MED.
	CandidateLowerMED bool `json:"candidate_lower_med"`
}

// RoundReport is the complete selection state after one synchronous
// receive/select step, before that round's advertisements are sent.
type RoundReport struct {
	Round int `json:"round"`

	// Choices is keyed by node then prefix.
	Choices map[NodeID]map[string]*NodeChoice `json:"choices"`

	// Events contains cluster-loop discards observed while receiving.
	Events []DiscardEvent `json:"events,omitempty"`

	// Fingerprint is the deterministic full control-state hash key used
	// for repetition detection.
	Fingerprint string `json:"fingerprint"`

	// Repeated is true for the round that closes a detected cycle.
	Repeated bool `json:"repeated,omitempty"`
	// EqualsRound is the earlier round whose state this round repeats.
	EqualsRound int `json:"equals_round,omitempty"`
}

// Oscillation describes the first detected periodic orbit.
type Oscillation struct {
	// FirstRound is the first occurrence of the repeated state.
	FirstRound int `json:"first_round"`
	// RepeatRound is the later round that reproduced FirstRound's state.
	RepeatRound int `json:"repeat_round"`
	// Period is the minimum oscillation period.
	Period int `json:"period"`
	// FirstState is the state that repeats.
	FirstState *RoundReport `json:"first_state"`
	// RepeatedState is the closing observation equal to FirstState at
	// RepeatRound (its Repeated flag is true).
	RepeatedState *RoundReport `json:"repeated_state,omitempty"`
	// Cycle contains the distinct states FirstRound..RepeatRound-1.
	Cycle []*RoundReport `json:"cycle"`
}

// Result is the outcome of Simulate.
type Result struct {
	Status Status `json:"status"`
	// Converged is true exactly when Status == StatusConverged.
	Converged bool `json:"converged"`

	AlwaysCompareMED bool `json:"always_compare_med"`

	// Rounds contains every executed round, including the closing
	// repeated round when an oscillation or fixed point is detected.
	Rounds []*RoundReport `json:"rounds"`

	// Best is the terminal per-node, per-prefix choice.
	Best map[NodeID]map[string]*NodeChoice `json:"best"`

	// Peers is the derived BGP session graph (sorted neighbor lists).
	Peers map[NodeID][]NodeID `json:"peers"`

	// Oscillation is set when Status == StatusOscillating.
	Oscillation *Oscillation `json:"oscillation,omitempty"`

	// MEDIncomparableEliminations collects all marked casualties across
	// every round. Use CasualtiesForPrefix to scope them.
	MEDIncomparableEliminations []EliminationMark `json:"med_incomparable_eliminations,omitempty"`

	// ClusterLoopDiscards collects every cluster-list loop rejection.
	ClusterLoopDiscards []DiscardEvent `json:"cluster_loop_discards,omitempty"`
}

// CasualtiesForPrefix returns the incomparability elimination marks for
// one prefix.
func (r *Result) CasualtiesForPrefix(prefix string) []EliminationMark {
	var out []EliminationMark
	for _, m := range r.MEDIncomparableEliminations {
		if m.Prefix == prefix {
			out = append(out, m)
		}
	}
	return out
}

// BestRoute returns the terminal best route for node/prefix or nil.
func (r *Result) BestRoute(node NodeID, prefix string) *Route {
	if byPrefix, ok := r.Best[node]; ok {
		if c, ok := byPrefix[prefix]; ok {
			return c.Best
		}
	}
	return nil
}

// BestRouteAt returns the best route for node/prefix observed in round i,
// or nil when i is out of range or no route was selected.
func (r *Result) BestRouteAt(round int, node NodeID, prefix string) *Route {
	if round < 0 || round >= len(r.Rounds) {
		return nil
	}
	if c, ok := r.Rounds[round].Choices[node][prefix]; ok && c != nil {
		return c.Best
	}
	return nil
}
