package netsec

import (
	"fmt"
)

// Deny reasons recorded on the earliest deny evidence. They are stable strings
// so persisted snapshots keep decoding across engine versions.
const (
	// ReasonPolicyDeny: an identity or resource policy explicit deny matched.
	ReasonPolicyDeny = "denied_by_policy"
	// ReasonBoundaryDeny: an organization layer explicit deny matched.
	ReasonBoundaryDeny = "denied_by_boundary"
	// ReasonEmptyAllow: no allow covered the resource, either after the
	// policy union or after an allow boundary intersected it away.
	ReasonEmptyAllow = "no_matching_allow"
)

// Engine evaluates authorization queries against a fixed, finite action set
// and a bounded resource-id universe.
//
// An Engine is safe for concurrent use: it holds only immutable configuration.
type Engine struct {
	actions  []Action
	known    map[Action]struct{}
	universe IntervalSet
}

// NewEngine builds an Engine. The universe bounds every decision; pass an
// explicit set (for example NewIntervalSet(Interval{0, 1_000_000})) rather than
// relying on host-wide defaults so evaluations stay reproducible.
func NewEngine(actions []Action, universe IntervalSet) (*Engine, error) {
	if len(actions) == 0 {
		return nil, errEmptyActionSet
	}
	known := make(map[Action]struct{}, len(actions))
	ordered := make([]Action, 0, len(actions))
	for _, action := range actions {
		if action == "" {
			return nil, fmt.Errorf("netsec: action names must be non-empty")
		}
		if _, dup := known[action]; dup {
			return nil, fmt.Errorf("netsec: action %q registered more than once", action)
		}
		known[action] = struct{}{}
		ordered = append(ordered, action)
	}
	if universe.Empty() {
		return nil, fmt.Errorf("netsec: resource universe must not be empty")
	}
	return &Engine{actions: ordered, known: known, universe: universe}, nil
}

// Actions returns the finite action set in registration order.
func (e *Engine) Actions() []Action {
	out := make([]Action, len(e.actions))
	copy(out, e.actions)
	return out
}

// Universe returns the configured resource-id universe.
func (e *Engine) Universe() IntervalSet {
	return e.universe
}

// EvalInput is one authorization query.
//
// IdentityPolicy is the policy attached to the caller (user/role), ResourcePolicy
// is the policy attached to the target resource. Their allows are unioned and
// their denies are applied before the organization Layers are walked from the
// tree root toward the owning account.
type EvalInput struct {
	Action         Action  `json:"action"`
	Resource       int64   `json:"resource"`
	IdentityPolicy Policy  `json:"identity_policy"`
	ResourcePolicy Policy  `json:"resource_policy"`
	Layers         []Layer `json:"layers"`
}

// LayerContribution records what one organization layer did to the query.
//
// AllowedAfter is the running authorization interval set after applying this
// layer, so reading the slice in order shows the boundary narrowing step by
// step. PointInside reports whether the queried resource survives the layer.
type LayerContribution struct {
	Depth        int         `json:"depth"`
	NodeID       string      `json:"node_id"`
	Effect       Effect      `json:"effect"`
	Boundary     IntervalSet `json:"boundary"`
	AllowedAfter IntervalSet `json:"allowed_after"`
	PointInside  bool        `json:"point_inside"`
}

// DenyEvidence is the earliest reason the queried resource was refused.
type DenyEvidence struct {
	Reason   string `json:"reason"`
	Source   string `json:"source"`
	Depth    int    `json:"depth"`
	Effect   Effect `json:"effect"`
	Resource int64  `json:"resource"`
	Action   Action `json:"action"`
}

// Decision is the fully traced result of an authorization query.
type Decision struct {
	Action            Action              `json:"action"`
	Resource          int64               `json:"resource"`
	Allowed           bool                `json:"allowed"`
	PolicyAllow       IntervalSet         `json:"policy_allow"`
	EffectiveAllow    IntervalSet         `json:"effective_allow"`
	AllowContributors []string            `json:"allow_contributors"`
	Layers            []LayerContribution `json:"layers"`
	EarliestDeny      *DenyEvidence       `json:"earliest_deny"`
}

// Evaluate runs the full interval-based evaluation and returns every layer
// contribution plus the earliest deny evidence for the queried resource.
func (e *Engine) Evaluate(in EvalInput) (*Decision, error) {
	if err := e.validateInput(in); err != nil {
		return nil, err
	}

	identity, err := compilePolicy(in.IdentityPolicy, e.known)
	if err != nil {
		return nil, err
	}
	resource, err := compilePolicy(in.ResourcePolicy, e.known)
	if err != nil {
		return nil, err
	}
	if err := validateLayers(in.Layers); err != nil {
		return nil, err
	}

	decision := &Decision{
		Action:   in.Action,
		Resource: in.Resource,
		Layers:   make([]LayerContribution, 0, len(in.Layers)),
	}

	// 1. Union the allow statements of the identity and resource policies.
	identityAllow, identityContrib := identity.allowsFor(in.Action)
	resourceAllow, resourceContrib := resource.allowsFor(in.Action)
	policyAllow := identityAllow.Union(resourceAllow).Intersect(e.universe)
	decision.PolicyAllow = policyAllow
	decision.AllowContributors = append(decision.AllowContributors, identityContrib...)
	decision.AllowContributors = append(decision.AllowContributors, resourceContrib...)

	// 2. Explicit policy denies win before any boundary is considered.
	// Identity policy is evaluated first, matching the order the host consults
	// it; the first matching statement in input order is reported.
	if st, matched := identity.firstDenyStatement(in.Action, in.Resource); matched {
		decision.EarliestDeny = e.evidence(ReasonPolicyDeny, st.source, -1, st.effect, in)
	} else if st, matched := resource.firstDenyStatement(in.Action, in.Resource); matched {
		decision.EarliestDeny = e.evidence(ReasonPolicyDeny, st.source, -1, st.effect, in)
	}

	// 3. Remove policy-denied resources, then walk the organization path from
	// the root toward the account, intersecting allow boundaries and
	// subtracting deny boundaries layer by layer.
	policyDenied := identity.deniesFor(in.Action).Union(resource.deniesFor(in.Action))
	running := policyAllow.Subtract(policyDenied)
	if decision.EarliestDeny == nil && !running.Contains(in.Resource) {
		decision.EarliestDeny = e.evidence(ReasonEmptyAllow, "policy-union", -1, EffectDeny, in)
	}

	for depth, layer := range in.Layers {
		boundary := layer.Resources
		switch layer.Effect {
		case EffectAllow:
			running = running.Intersect(boundary)
			if decision.EarliestDeny == nil && !running.Contains(in.Resource) {
				decision.EarliestDeny = e.evidence(
					ReasonEmptyAllow, layer.NodeID, depth, EffectAllow, in)
			}
		case EffectDeny:
			running = running.Subtract(boundary)
			if decision.EarliestDeny == nil && boundary.Contains(in.Resource) {
				decision.EarliestDeny = e.evidence(
					ReasonBoundaryDeny, layer.NodeID, depth, EffectDeny, in)
			}
		}
		decision.Layers = append(decision.Layers, LayerContribution{
			Depth:        depth,
			NodeID:       layer.NodeID,
			Effect:       layer.Effect,
			Boundary:     boundary,
			AllowedAfter: running,
			PointInside:  running.Contains(in.Resource),
		})
	}

	decision.EffectiveAllow = running
	decision.Allowed = decision.EarliestDeny == nil && running.Contains(in.Resource)
	return decision, nil
}

// Check is the convenience point-decision form used by the authorization host
// on each request. It returns only the allow/deny verdict.
func (e *Engine) Check(in EvalInput) (bool, error) {
	decision, err := e.Evaluate(in)
	if err != nil {
		return false, err
	}
	return decision.Allowed, nil
}

func (e *Engine) validateInput(in EvalInput) error {
	if _, ok := e.known[in.Action]; !ok {
		return fmt.Errorf("netsec: unknown action %q", in.Action)
	}
	if !e.universe.Contains(in.Resource) {
		return fmt.Errorf("netsec: resource %d is outside the configured universe", in.Resource)
	}
	return nil
}

func (e *Engine) evidence(reason, source string, depth int, effect Effect, in EvalInput) *DenyEvidence {
	return &DenyEvidence{
		Reason:   reason,
		Source:   source,
		Depth:    depth,
		Effect:   effect,
		Resource: in.Resource,
		Action:   in.Action,
	}
}
