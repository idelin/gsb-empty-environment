package delegation

import "slices"

func (p *Prover) Explain(tuple Tuple) (*Explanation, error) {
	if p.graph == nil {
		return nil, ErrCompiledGraphUnavailable
	}
	for _, symbol := range tuple.Path {
		if !slices.Contains(p.graph.universe.PathAlphabet, symbol) {
			return nil, &TupleError{Tuple: tuple, Reason: "path symbol outside alphabet"}
		}
	}
	if !validTagSet(tuple.Tags, p.graph.universe.LabelRules) || !slices.IsSorted(tuple.Tags) {
		return nil, &TupleError{Tuple: tuple, Reason: "tags violate label constraints or are not sorted"}
	}
	return p.explain(p.defaultDelegationID(), tuple), nil
}

func (p *Prover) ExplainDelegation(delegationID string, tuple Tuple) (*Explanation, error) {
	if p.graph == nil {
		return nil, ErrCompiledGraphUnavailable
	}
	if _, ok := p.graph.reachable[delegationID]; !ok {
		return nil, &TupleError{Tuple: tuple, Reason: "delegation is not reachable"}
	}
	return p.explain(delegationID, tuple), nil
}

func (p *Prover) defaultDelegationID() string {
	ids := append([]string(nil), p.graph.order...)
	slices.Sort(ids)
	leafID := p.graph.universe.RootDelegationID
	bestDepth := 0
	for _, id := range ids {
		depth := 0
		current := id
		for current != p.graph.universe.RootDelegationID {
			parents := p.graph.reachable[current].Delegation.Parents
			if len(parents) == 0 {
				break
			}
			current = parents[0].DelegationID
			depth++
		}
		if depth > bestDepth {
			bestDepth = depth
			leafID = id
		}
	}
	return leafID
}

func (p *Prover) explain(id string, tuple Tuple) *Explanation {
	graph := p.graph
	d := graph.reachable[id].Delegation
	localEvaluations := evaluationList(id, d, tuple)
	decision := EffectDeny
	for i := len(localEvaluations) - 1; i >= 0; i-- {
		if localEvaluations[i].Matched {
			decision = localEvaluations[i].Effect
			break
		}
	}
	parentVotes := map[string]Effect{}
	evaluations := make([]RuleEvaluation, 0, len(localEvaluations)+len(d.Parents)*4)
	if decision == EffectAllow && id != graph.universe.RootDelegationID {
		for _, parentID := range parentIDs(d) {
			parentExplanation := p.explain(parentID, tuple)
			parentVotes[parentID] = parentExplanation.Decision
			evaluations = append(evaluations, parentExplanation.Evaluations...)
		}
		achieved := 0
		for _, vote := range parentVotes {
			if vote == EffectAllow {
				achieved++
			}
		}
		if achieved < d.QuorumK {
			decision = EffectDeny
		}
	}
	evaluations = append(evaluations, localEvaluations...)
	return &Explanation{
		Tuple:       tuple,
		Decision:    decision,
		Evaluations: evaluations,
		ParentVotes: parentVotes,
		QuorumK:     d.QuorumK,
	}
}

func evaluationList(id string, d Delegation, tuple Tuple) []RuleEvaluation {
	evaluations := make([]RuleEvaluation, 0, len(d.Rules))
	for i, rule := range d.Rules {
		pathMatch := patternMatchesPath(rule.Pattern, tuple.Path)
		tagMatch := predicateMatches(rule.Predicate, tuple.Tags)
		timeMatch := ruleActive(rule, tuple.Timestamp)
		matched := pathMatch && tagMatch && timeMatch
		effect := Effect("")
		reason := ""
		if matched {
			effect = rule.Effect
		} else {
			switch {
			case !pathMatch:
				reason = "path does not match"
			case !tagMatch:
				reason = "tag predicate does not match"
			default:
				reason = "timestamp is outside [start,end)"
			}
		}
		evaluations = append(evaluations, RuleEvaluation{
			DelegationID: id,
			RuleIndex:    i,
			Matched:      matched,
			Effect:       effect,
			Reason:       reason,
		})
	}
	return evaluations
}

type TupleError struct {
	Tuple  Tuple  `json:"tuple"`
	Reason string `json:"reason"`
}

func (err *TupleError) Error() string {
	return err.Reason
}

var ErrCompiledGraphUnavailable = &staticError{"compiled graph is unavailable"}

type staticError struct {
	message string
}

func (err *staticError) Error() string {
	return err.message
}
