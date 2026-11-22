package changeaccept

import (
	"fmt"
	"sort"
)

// Validate checks structural invariants without consulting an authorizer.
func (t Topology) Validate() error {
	seen := make(map[EndpointID]struct{}, len(t.Endpoints))
	for index, endpoint := range t.Endpoints {
		if endpoint.ID == "" {
			return fmt.Errorf("endpoints[%d]: empty endpoint ID", index)
		}
		if endpoint.Tenant == "" {
			return fmt.Errorf("endpoint %q: empty tenant ID", endpoint.ID)
		}
		if _, exists := seen[endpoint.ID]; exists {
			return fmt.Errorf("endpoint %q is duplicated", endpoint.ID)
		}
		seen[endpoint.ID] = struct{}{}
		if err := endpoint.Ingress.validate(fmt.Sprintf("endpoint %q ingress", endpoint.ID)); err != nil {
			return err
		}
		if err := endpoint.Egress.validate(fmt.Sprintf("endpoint %q egress", endpoint.ID)); err != nil {
			return err
		}
	}

	edges := make(map[Edge]struct{}, len(t.Edges))
	for index, edge := range t.Edges {
		if _, ok := seen[edge.From]; !ok {
			return fmt.Errorf("edges[%d]: unknown from endpoint %q", index, edge.From)
		}
		if _, ok := seen[edge.To]; !ok {
			return fmt.Errorf("edges[%d]: unknown to endpoint %q", index, edge.To)
		}
		if _, exists := edges[edge]; exists {
			return fmt.Errorf("edge %q -> %q is duplicated", edge.From, edge.To)
		}
		edges[edge] = struct{}{}
	}
	return nil
}

// Labels returns every label mentioned by endpoint policies or label graphs.
func (t Topology) Labels() []Label {
	seen := make(map[Label]struct{})
	for _, endpoint := range t.Endpoints {
		for _, policy := range []Policy{endpoint.Ingress, endpoint.Egress} {
			for _, rule := range policy {
				for _, label := range rule.Match.Labels {
					seen[label] = struct{}{}
				}
			}
		}
		for _, label := range endpoint.Changes.labels() {
			seen[label] = struct{}{}
		}
	}
	return sortedLabelSet(seen)
}

func (t Topology) clone() Topology {
	result := Topology{
		Endpoints: make([]Endpoint, len(t.Endpoints)),
		Edges:     append([]Edge(nil), t.Edges...),
	}
	for index, endpoint := range t.Endpoints {
		result.Endpoints[index] = endpoint.clone()
	}
	return result
}

func (t Topology) endpointMap() map[EndpointID]Endpoint {
	result := make(map[EndpointID]Endpoint, len(t.Endpoints))
	for _, endpoint := range t.Endpoints {
		result[endpoint.ID] = endpoint
	}
	return result
}

func (t Topology) outgoingMap() map[EndpointID][]EndpointID {
	result := make(map[EndpointID][]EndpointID, len(t.Endpoints))
	for _, edge := range t.Edges {
		result[edge.From] = append(result[edge.From], edge.To)
	}
	for from := range result {
		sort.Slice(result[from], func(i, j int) bool {
			return result[from][i] < result[from][j]
		})
	}
	return result
}

// ApplyGroups materializes the old/new rules in order on a copy of the base
// topology. Generation is advanced once per applied group.
func ApplyGroups(base Topology, baseGeneration Generation, groups map[GroupID]SwitchGroup, order []GroupID) (Snapshot, error) {
	if err := base.Validate(); err != nil {
		return Snapshot{}, err
	}
	topology := base.clone()
	endpoints := make(map[EndpointID]*Endpoint, len(topology.Endpoints))
	for index := range topology.Endpoints {
		endpoints[topology.Endpoints[index].ID] = &topology.Endpoints[index]
	}
	seenGroups := make(map[GroupID]struct{}, len(order))
	for _, id := range order {
		if _, exists := seenGroups[id]; exists {
			return Snapshot{}, fmt.Errorf("group %d appears more than once", id)
		}
		seenGroups[id] = struct{}{}
		group, ok := groups[id]
		if !ok {
			return Snapshot{}, fmt.Errorf("group %d is not defined", id)
		}
		for _, change := range group.Changes {
			endpoint, ok := endpoints[change.Endpoint]
			if !ok {
				return Snapshot{}, fmt.Errorf("group %d: unknown endpoint %q", id, change.Endpoint)
			}
			if !change.Plane.valid() {
				return Snapshot{}, fmt.Errorf("group %d: invalid plane %d", id, change.Plane)
			}
			if change.Old != nil && !equalPolicy(change.Old, endpoint.policy(change.Plane)) {
				return Snapshot{}, fmt.Errorf("group %d: old %s policy at %q does not match current policy", id, change.Plane, change.Endpoint)
			}
			endpoint.setPolicy(change.Plane, change.New.clone())
		}
	}
	return Snapshot{
		Generation: baseGeneration + Generation(len(order)),
		Topology:   topology,
	}, nil
}

// Snapshot is a complete policy state. Auth is shared across generations.
type Snapshot struct {
	Generation Generation
	Topology   Topology
	Auth       *Authorizer
	// Labels is the exact finite initial-label universe when non-empty. If
	// empty, it is inferred from endpoint rules, label graphs, and authorizer
	// rules. Every concrete label a connection can start with must be covered.
	Labels []Label
}

// Validate checks the snapshot topology and its authorizer binding.
func (s Snapshot) Validate() error {
	if s.Auth == nil {
		return fmt.Errorf("snapshot has no authorizer")
	}
	if err := s.Topology.Validate(); err != nil {
		return err
	}
	for _, endpoint := range s.Topology.Endpoints {
		if !s.Auth.IsManaged(endpoint.Tenant) {
			return fmt.Errorf("endpoint %q uses unmanaged tenant %q", endpoint.ID, endpoint.Tenant)
		}
	}
	return nil
}
