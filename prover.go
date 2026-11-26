package delegation

import (
	"fmt"
	"slices"
)

type Prover struct {
	graph        *compiledGraph
	certificates map[string]*DelegationCertificate
	failures     map[string]*Counterexample
}

func NewProver(u *Universe) (*Prover, error) {
	graph, err := compileGraph(u)
	if err != nil {
		return nil, err
	}
	return &Prover{
		graph:        graph,
		certificates: map[string]*DelegationCertificate{},
		failures:     map[string]*Counterexample{},
	}, nil
}

func Prove(u *Universe) (*CertificateBundle, *Counterexample, error) {
	prover, err := NewProver(u)
	if err != nil {
		return nil, nil, fmt.Errorf("compile delegation universe: %w", err)
	}
	return prover.Prove()
}

func (p *Prover) Prove() (*CertificateBundle, *Counterexample, error) {
	rootID := p.graph.universe.RootDelegationID
	var best *Counterexample
	for _, id := range p.graph.order {
		failure, err := p.prove(id)
		if err != nil {
			return nil, nil, err
		}
		if failure != nil {
			if best == nil || compareTuple(failure, best) <= 0 {
				best = failure
			}
		}
	}
	if best != nil {
		return nil, best, nil
	}
	bundle := &CertificateBundle{RootID: rootID, Certificates: map[string]*DelegationCertificate{}}
	for id, cert := range p.certificates {
		bundle.Certificates[id] = cert
	}
	if err := bundle.SetHash(); err != nil {
		return nil, nil, err
	}
	return bundle, nil, nil
}

func (p *Prover) prove(id string) (*Counterexample, error) {
	if _, ok := p.certificates[id]; ok {
		return nil, nil
	}
	if failure, ok := p.failures[id]; ok {
		return failure, nil
	}
	d := p.graph.reachable[id].Delegation
	parentIDs := parentIDs(d)
	for _, parentID := range parentIDs {
		failure, err := p.prove(parentID)
		if err != nil {
			return nil, err
		}
		if failure != nil {
			structural := p.minimalNonEmptyTuple()
			structural.FailedParent = &FailedParentAttribution{
				DelegationID: id,
				ParentID:     failure.delegationID(),
				Reason:       "parent proof failed",
			}
			p.failures[id] = &structural
			return &structural, nil
		}
	}
	cert, failure, err := p.buildCertificate(id)
	if err != nil {
		return nil, err
	}
	if failure != nil {
		p.failures[id] = failure
		return failure, nil
	}
	p.certificates[id] = cert
	return nil, nil
}

func (p *Prover) buildCertificate(id string) (*DelegationCertificate, *Counterexample, error) {
	graph := p.graph
	d := graph.reachable[id].Delegation
	machine := graph.machines[id]
	cert := &DelegationCertificate{
		DelegationID:         id,
		Tenant:               d.Tenant,
		Action:               d.Action,
		QuorumK:              d.QuorumK,
		ParentCertificateIDs: parentIDs(d),
		PathAlphabet:         graph.universe.PathAlphabet,
		TimeSegments:         graph.segments,
		PathStates:           make([]PathState, 0, len(machine.states)),
	}
	for stateID, ruleState := range machine.states {
		cert.PathStates = append(cert.PathStates, PathState{
			ID:         stateID,
			RuleState:  append([]int(nil), ruleState...),
			Transition: append([]int(nil), machine.transitions[stateID]...),
		})
	}
	failure := p.findFailure(id)
	if failure != nil {
		return nil, failure, nil
	}
	for stateID := range machine.states {
		path := p.pathToState(id, stateID)
		for _, tags := range graph.tagSets {
			for segmentIndex, segment := range graph.segments {
				output := p.evaluate(id, path, stateID, tags, segmentIndex, segment.Start)
				cert.Outputs = append(cert.Outputs, output)
			}
		}
	}
	if err := cert.setHash(); err != nil {
		return nil, nil, err
	}
	return cert, nil, nil
}

func (p *Prover) findFailure(id string) *Counterexample {
	graph := p.graph
	machine := graph.machines[id]
	type queueItem struct {
		state int
		path  []string
	}
	queue := []queueItem{}
	for _, symbol := range graph.universe.PathAlphabet {
		next := machine.transitions[0][slices.Index(graph.universe.PathAlphabet, symbol)]
		queue = append(queue, queueItem{state: next, path: []string{symbol}})
	}
	visited := map[int]struct{}{0: {}}
	for _, item := range queue {
		visited[item.state] = struct{}{}
	}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if failure := p.failureAtState(id, item.state, item.path); failure != nil {
			return failure
		}
		for symbolIndex, symbol := range graph.universe.PathAlphabet {
			next := machine.transitions[item.state][symbolIndex]
			if _, ok := visited[next]; ok {
				continue
			}
			visited[next] = struct{}{}
			path := append(append([]string(nil), item.path...), symbol)
			queue = append(queue, queueItem{state: next, path: path})
		}
	}
	return nil
}

func (p *Prover) failureAtState(id string, state int, path []string) *Counterexample {
	graph := p.graph
	machine := graph.machines[id]
	var best *Counterexample
	for _, tags := range graph.tagSets {
		for _, segment := range graph.segments {
			local, _ := localDecision(graph.reachable[id].Delegation, machine.matchingRules(state), tags, segment.Start)
			if local != EffectAllow || id == graph.universe.RootDelegationID {
				continue
			}
			allowing := p.allowingParents(id, path, tags, segment.Start)
			if len(allowing) >= graph.reachable[id].Delegation.QuorumK {
				continue
			}
			candidate := &Counterexample{
				Path:      append([]string(nil), path...),
				Tags:      append([]string(nil), tags...),
				Timestamp: segment.Start,
				FailedQuorum: &FailedQuorumAttribution{
					DelegationID:    id,
					Required:        graph.reachable[id].Delegation.QuorumK,
					Achieved:        len(allowing),
					AllowingParents: append([]string(nil), allowing...),
				},
			}
			if best == nil || compareTuple(candidate, best) < 0 {
				best = candidate
			}
		}
	}
	return best
}

func compareTuple(a, b *Counterexample) int {
	if len(a.Path) != len(b.Path) {
		return len(a.Path) - len(b.Path)
	}
	for i := range a.Path {
		if a.Path[i] != b.Path[i] {
			if a.Path[i] < b.Path[i] {
				return -1
			}
			return 1
		}
	}
	if result := compareStringSlices(a.Tags, b.Tags); result != 0 {
		return result
	}
	if a.Timestamp < b.Timestamp {
		return -1
	}
	if a.Timestamp > b.Timestamp {
		return 1
	}
	return 0
}

func (p *Prover) evaluate(id string, path []string, stateID int, tags []string, segmentIndex int, timestamp int64) StateOutput {
	graph := p.graph
	d := graph.reachable[id].Delegation
	local, lastRule := localDecision(d, graph.machines[id].matchingRules(stateID), tags, timestamp)
	output := StateOutput{
		PathStateID:      stateID,
		TagSet:           append([]string(nil), tags...),
		TimeSegmentIndex: segmentIndex,
		LocalEffect:      local,
		LastRule:         lastRule,
		Decision:         EffectDeny,
	}
	if local == EffectDeny || id == graph.universe.RootDelegationID {
		if local == EffectAllow {
			output.Decision = EffectAllow
		}
		return output
	}
	allowing := p.allowingParents(id, path, tags, timestamp)
	output.Achieved = len(allowing)
	output.AllowingParents = allowing
	if len(allowing) >= d.QuorumK {
		output.Decision = EffectAllow
	}
	return output
}

func (p *Prover) allowingParents(id string, path, tags []string, timestamp int64) []string {
	d := p.graph.reachable[id].Delegation
	var allowing []string
	for _, parentID := range parentIDs(d) {
		if p.decision(parentID, path, tags, timestamp) == EffectAllow {
			allowing = append(allowing, parentID)
		}
	}
	return allowing
}

func (p *Prover) decision(id string, path, tags []string, timestamp int64) Effect {
	graph := p.graph
	machine := graph.machines[id]
	state := 0
	for _, symbol := range path {
		state = machine.transition(state, symbol)
	}
	local, _ := localDecision(graph.reachable[id].Delegation, machine.matchingRules(state), tags, timestamp)
	if local == EffectDeny {
		return EffectDeny
	}
	if id == graph.universe.RootDelegationID {
		return EffectAllow
	}
	if len(p.allowingParents(id, path, tags, timestamp)) >= graph.reachable[id].Delegation.QuorumK {
		return EffectAllow
	}
	return EffectDeny
}

func (p *Prover) pathToState(id string, target int) []string {
	machine := p.graph.machines[id]
	type queueItem struct {
		state int
		path  []string
	}
	queue := []queueItem{{state: 0, path: []string{}}}
	visited := map[int]struct{}{0: {}}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if item.state == target {
			return item.path
		}
		for symbolIndex, symbol := range p.graph.universe.PathAlphabet {
			next := machine.transitions[item.state][symbolIndex]
			if _, ok := visited[next]; ok {
				continue
			}
			visited[next] = struct{}{}
			queue = append(queue, queueItem{
				state: next,
				path:  append(append([]string(nil), item.path...), symbol),
			})
		}
	}
	return nil
}

func (p *Prover) minimalTuple() Counterexample {
	return Counterexample{
		Path:      []string{},
		Tags:      append([]string(nil), p.graph.tagSets[0]...),
		Timestamp: p.graph.segments[0].Start,
	}
}

func (p *Prover) minimalNonEmptyTuple() Counterexample {
	tuple := p.minimalTuple()
	tuple.Path = []string{p.graph.universe.PathAlphabet[0]}
	return tuple
}

func parentIDs(d Delegation) []string {
	ids := make([]string, 0, len(d.Parents))
	for _, parent := range d.Parents {
		ids = append(ids, parent.DelegationID)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func localDecision(d Delegation, matched []int, tags []string, timestamp int64) (Effect, *RuleRef) {
	last := -1
	for _, ruleIndex := range matched {
		rule := d.Rules[ruleIndex]
		if ruleActive(rule, timestamp) && predicateMatches(rule.Predicate, tags) {
			last = ruleIndex
		}
	}
	if last < 0 {
		return EffectDeny, nil
	}
	rule := d.Rules[last]
	return rule.Effect, &RuleRef{DelegationID: d.ID, RuleIndex: last, Effect: rule.Effect}
}

func (counterexample *Counterexample) delegationID() string {
	if counterexample.FailedParent != nil {
		return counterexample.FailedParent.DelegationID
	}
	if counterexample.FailedQuorum != nil {
		return counterexample.FailedQuorum.DelegationID
	}
	return ""
}
