package changeaccept

import (
	"fmt"
	"sort"
)

// TenantID identifies a managed tenant. Tenant identifiers are never inferred
// from a label and are part of every connection-tracking cache key.
type TenantID string

// EndpointID identifies a node in the directed topology.
type EndpointID string

// Label is the complete immutable policy label carried by a packet at a hop.
// A deployment that needs a label tuple can serialize the tuple into one Label.
type Label string

// GroupID is the stable, externally assigned identifier of an atomic switch
// group. IDs are also used for deterministic tie breaking.
type GroupID int

// Generation identifies one policy snapshot. Cache entries may only be reused
// for the exact generation under which they were verified.
type Generation uint64

// Plane identifies the enforcement point replaced by a policy change.
type Plane uint8

const (
	PlaneIngress Plane = iota
	PlaneEgress
)

func (p Plane) String() string {
	switch p {
	case PlaneIngress:
		return "ingress"
	case PlaneEgress:
		return "egress"
	default:
		return fmt.Sprintf("plane(%d)", p)
	}
}

func (p Plane) valid() bool {
	return p == PlaneIngress || p == PlaneEgress
}

// Action is the result of the first matching rule in an ordered policy.
type Action uint8

const (
	ActionDeny Action = iota
	ActionAllow
)

func (a Action) valid() bool {
	return a == ActionDeny || a == ActionAllow
}

func (a Action) String() string {
	if a == ActionAllow {
		return "allow"
	}
	return "deny"
}

// Predicate matches a set of labels. A nil or empty Labels slice is a wildcard.
// A policy that needs "match nothing" should simply omit the rule.
type Predicate struct {
	Labels []Label
}

// AnyLabel returns a predicate that matches every label.
func AnyLabel() Predicate {
	return Predicate{}
}

// Labels returns a predicate that matches exactly the supplied labels.
func Labels(labels ...Label) Predicate {
	return Predicate{Labels: normalizeLabels(labels)}
}

// Matches reports whether label satisfies the predicate.
func (p Predicate) Matches(label Label) bool {
	if len(p.Labels) == 0 {
		return true
	}
	for _, candidate := range p.Labels {
		if candidate == label {
			return true
		}
	}
	return false
}

// Rule is one entry in an ordered policy.
type Rule struct {
	Match  Predicate
	Action Action
}

// Policy is evaluated from first rule to last. The first matching rule decides.
// If no rule matches, the policy denies.
type Policy []Rule

// Decision is the result of evaluating an ordered policy.
type Decision struct {
	Action Action
	Rule   int // zero-based rule index, or -1 for default deny
}

func (d Decision) allowed() bool {
	return d.Action == ActionAllow
}

// Decide evaluates the policy in order. The zero value and an empty policy deny.
func (p Policy) Decide(label Label) Decision {
	for index, rule := range p {
		if rule.Match.Matches(label) {
			if !rule.Action.valid() {
				return Decision{Action: ActionDeny, Rule: -1}
			}
			return Decision{Action: rule.Action, Rule: index}
		}
	}
	return Decision{Action: ActionDeny, Rule: -1}
}

func (p Policy) validate(field string) error {
	for index, rule := range p {
		if !rule.Action.valid() {
			return fmt.Errorf("%s[%d]: invalid action %d", field, index, rule.Action)
		}
	}
	return nil
}

func (p Policy) clone() Policy {
	if p == nil {
		return nil
	}
	return append(Policy(nil), p...)
}

func equalPolicy(left, right Policy) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Action != right[index].Action {
			return false
		}
		if !equalLabels(left[index].Match.Labels, right[index].Match.Labels) {
			return false
		}
	}
	return true
}

func equalLabels(left, right []Label) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// LabelGraph describes the label rewrites permitted at one endpoint.
//
// An endpoint's label passes through unchanged when Edges is nil. A non-nil
// map is exact: the only rewrites available from label L are Edges[L]. This
// distinction makes it possible to represent both an unchanged forwarding
// plane and an endpoint that intentionally has no outgoing label.
type LabelGraph struct {
	Edges map[Label][]Label
}

// Passthrough returns a graph that leaves every label unchanged.
func Passthrough() LabelGraph {
	return LabelGraph{}
}

// ExactGraph returns a strict label-change graph. Missing source labels have no
// successors, including no implicit identity edge.
func ExactGraph(edges map[Label][]Label) LabelGraph {
	result := LabelGraph{Edges: make(map[Label][]Label, len(edges))}
	for from, targets := range edges {
		result.Edges[from] = normalizeLabels(targets)
	}
	return result
}

// Next returns the possible labels after the endpoint's label change. The
// result is sorted and contains no duplicates, making traversal deterministic.
func (g LabelGraph) Next(label Label) []Label {
	if g.Edges == nil {
		return []Label{label}
	}
	return append([]Label(nil), normalizeLabels(g.Edges[label])...)
}

func (g LabelGraph) clone() LabelGraph {
	if g.Edges == nil {
		return LabelGraph{}
	}
	result := LabelGraph{Edges: make(map[Label][]Label, len(g.Edges))}
	for from, targets := range g.Edges {
		result.Edges[from] = append([]Label(nil), targets...)
	}
	return result
}

func (g LabelGraph) labels() []Label {
	seen := make(map[Label]struct{})
	for from, targets := range g.Edges {
		seen[from] = struct{}{}
		for _, target := range targets {
			seen[target] = struct{}{}
		}
	}
	return sortedLabelSet(seen)
}

// Endpoint binds a topology node to its tenant, ordered ingress/egress policies,
// and allowed label rewrites.
type Endpoint struct {
	ID      EndpointID
	Tenant  TenantID
	Ingress Policy
	Egress  Policy
	Changes LabelGraph
}

func (e Endpoint) policy(plane Plane) Policy {
	if plane == PlaneIngress {
		return e.Ingress
	}
	return e.Egress
}

func (e *Endpoint) setPolicy(plane Plane, policy Policy) {
	if plane == PlaneIngress {
		e.Ingress = policy
		return
	}
	e.Egress = policy
}

func (e Endpoint) clone() Endpoint {
	e.Ingress = e.Ingress.clone()
	e.Egress = e.Egress.clone()
	e.Changes = e.Changes.clone()
	return e
}

// Edge is one directed forwarding adjacency.
type Edge struct {
	From EndpointID
	To   EndpointID
}

// Topology is a directed graph. Cycles are deliberately permitted.
type Topology struct {
	Endpoints []Endpoint
	Edges     []Edge
}

// TenantPair is the end-to-end authorization scope. Source and destination are
// tenant identities, not endpoint identities.
type TenantPair struct {
	Source      TenantID
	Destination TenantID
}

func normalizeLabels(labels []Label) []Label {
	if len(labels) == 0 {
		return nil
	}
	seen := make(map[Label]struct{}, len(labels))
	for _, label := range labels {
		seen[label] = struct{}{}
	}
	return sortedLabelSet(seen)
}

func sortedLabelSet(set map[Label]struct{}) []Label {
	result := make([]Label, 0, len(set))
	for label := range set {
		result = append(result, label)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
