package delegation

import (
	"fmt"
	"slices"
)

type compiledGraph struct {
	universe  *Universe
	reachable map[string]*SignedDelegation
	order     []string
	children  map[string][]string
	machines  map[string]*pathMachine
	tagSets   [][]string
	segments  []TimeSegment
}

type TimeSegment struct {
	Start int64  `json:"start"`
	End   *int64 `json:"end,omitempty"`
}

func compileGraph(u *Universe) (*compiledGraph, error) {
	if err := validateUniverse(u); err != nil {
		return nil, err
	}
	byID := map[string]*SignedDelegation{}
	for i := range u.Delegations {
		signed := &u.Delegations[i]
		byID[signed.Delegation.ID] = signed
	}
	reachable, order, err := reachableOrder(byID, u.RootDelegationID)
	if err != nil {
		return nil, err
	}
	for _, id := range order {
		d := reachable[id].Delegation
		if id == u.RootDelegationID {
			if len(d.Parents) != 0 || d.QuorumK != 0 {
				return nil, fmt.Errorf("root delegation %q must have no parents and quorum_k zero", id)
			}
			continue
		}
		if len(d.Parents) == 0 {
			return nil, fmt.Errorf("non-root delegation %q requires parents", id)
		}
		parentSeen := map[string]struct{}{}
		for _, parent := range d.Parents {
			if _, ok := parentSeen[parent.DelegationID]; ok {
				return nil, fmt.Errorf("delegation %q contains duplicate parent %q", id, parent.DelegationID)
			}
			parentSeen[parent.DelegationID] = struct{}{}
		}
		if d.QuorumK < 1 || d.QuorumK > len(d.Parents) {
			return nil, fmt.Errorf("delegation %q quorum_k must be in [1,%d]", id, len(d.Parents))
		}
	}
	children := map[string][]string{}
	machines := map[string]*pathMachine{}
	var boundaries []int64
	for _, id := range order {
		d := reachable[id].Delegation
		for _, parent := range d.Parents {
			children[parent.DelegationID] = append(children[parent.DelegationID], id)
		}
		machine, err := newPathMachine(u.PathAlphabet, d.Rules)
		if err != nil {
			return nil, fmt.Errorf("delegation %q: %w", id, err)
		}
		machines[id] = machine
		for _, rule := range d.Rules {
			boundaries = append(boundaries, rule.Start, rule.End)
		}
	}
	for id := range children {
		slices.Sort(children[id])
	}
	return &compiledGraph{
		universe:  u,
		reachable: reachable,
		order:     order,
		children:  children,
		machines:  machines,
		tagSets:   validTagSets(u.Labels, u.LabelRules),
		segments:  timeSegments(boundaries),
	}, nil
}

func reachableOrder(byID map[string]*SignedDelegation, rootID string) (map[string]*SignedDelegation, []string, error) {
	reachable := map[string]*SignedDelegation{}
	children := map[string][]string{}
	for id, signed := range byID {
		for _, parent := range signed.Delegation.Parents {
			children[parent.DelegationID] = append(children[parent.DelegationID], id)
		}
	}
	queue := []string{rootID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		signed, ok := byID[id]
		if !ok {
			return nil, nil, fmt.Errorf("delegation %q is absent", id)
		}
		if _, ok := reachable[id]; ok {
			continue
		}
		reachable[id] = signed
		next := append([]string(nil), children[id]...)
		slices.Sort(next)
		queue = append(queue, next...)
	}
	var order []string
	indegree := map[string]int{}
	queue = []string{}
	for id, signed := range reachable {
		indegree[id] = len(signed.Delegation.Parents)
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	slices.Sort(queue)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		nextIDs := append([]string(nil), children[id]...)
		slices.Sort(nextIDs)
		for _, childID := range nextIDs {
			indegree[childID]--
			if indegree[childID] == 0 {
				queue = append(queue, childID)
			}
		}
	}
	if len(order) != len(reachable) {
		return nil, nil, fmt.Errorf("delegation graph contains a cycle reachable from %q", rootID)
	}
	return reachable, order, nil
}

func timeSegments(boundaries []int64) []TimeSegment {
	sorted := append([]int64(nil), boundaries...)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	if len(sorted) == 0 {
		return []TimeSegment{{Start: -1 << 63}}
	}
	firstEnd := sorted[0]
	segments := []TimeSegment{{Start: -1 << 63, End: &firstEnd}}
	segments = append(segments, TimeSegment{Start: sorted[0]})
	for i := 1; i < len(sorted); i++ {
		end := sorted[i]
		segments[len(segments)-1].End = &end
		segments = append(segments, TimeSegment{Start: sorted[i]})
	}
	return segments
}

func ruleActive(rule Rule, timestamp int64) bool {
	return rule.Start <= timestamp && timestamp < rule.End
}
