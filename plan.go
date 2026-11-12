package netsec

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Sentinel errors reported by the planner.
var (
	// ErrPlanRejected indicates that at least one required object was missing
	// or unauthorized. The returned Plan is diagnostic and must not be
	// published.
	ErrPlanRejected = errors.New("recovery plan rejected")
	// ErrRevoked indicates that a planned object lost its authorization
	// between planning and the final verification.
	ErrRevoked = errors.New("authorization revoked before publish")
	// ErrNotPlanned indicates that Publish was called on a plan that was never
	// successfully computed.
	ErrNotPlanned = errors.New("plan is not ready")
	// ErrNoRoots indicates that a request carried no recovery roots.
	ErrNoRoots = errors.New("no recovery roots requested")
)

// Status is the outcome of planning.
type Status string

const (
	// StatusReady means every required object was authorized and the plan can
	// be published subject to a final verification.
	StatusReady Status = "ready"
	// StatusRejected means a required object could not be authorized. The
	// recovery must not proceed.
	StatusRejected Status = "rejected"
)

// HoleStatus records what happened to an optional dependency.
type HoleStatus string

const (
	// HoleLeftEmpty means the optional target was not explicitly requested and
	// was therefore deliberately not restored.
	HoleLeftEmpty HoleStatus = "left_empty"
	// HoleCovered means the optional target is already part of the closure
	// through a required path, so no separate pull is needed.
	HoleCovered HoleStatus = "covered"
)

// MissingGrant describes a required object that could not be authorized.
type MissingGrant struct {
	// ID is the object that failed authorization.
	ID ObjectID
	// Reason is the authorization backend's explanation.
	Reason string
	// Source is the object referencing the missing target. It is empty for a
	// root.
	Source ObjectID
	// Edge is the kind of reference that required the object.
	Edge EdgeKind
}

// OptionalHole records an optional reference and how it was handled.
type OptionalHole struct {
	From   ObjectID
	To     ObjectID
	Status HoleStatus
	// Reason explains why the hole was left empty.
	Reason string
}

// Request is the input to PlanRestore.
type Request struct {
	// Roots are the objects the caller wants to restore. Roots are always
	// treated as required.
	Roots []ObjectID
	// IncludeOptional explicitly opts in to specific optional targets. An
	// opted-in target is pulled into the closure and must itself be authorized;
	// optional targets not listed here are left empty.
	IncludeOptional map[ObjectID]bool
}

// Plan is an authorization-checked recovery plan.
type Plan struct {
	roots  []ObjectID
	status Status

	// restore is the deterministic set of objects that would be materialized.
	restore []ObjectID
	// holes records every optional reference encountered and its disposition.
	holes []OptionalHole
	// missing lists the required objects that failed authorization.
	missing []MissingGrant

	// snapshotGrant freezes the authorization reason observed at plan time for
	// each object in the closure.
	snapshotGrant map[ObjectID]string
}

// Status reports the planning outcome.
func (p Plan) Status() Status { return p.status }

// Roots returns the recovery roots the plan was requested for.
func (p Plan) Roots() []ObjectID { return append([]ObjectID(nil), p.roots...) }

// Restore returns the sorted set of objects the plan would materialize. It is
// empty unless the plan is ready.
func (p Plan) Restore() []ObjectID {
	if p.status != StatusReady {
		return nil
	}
	return append([]ObjectID(nil), p.restore...)
}

// Holes returns the optional references and their disposition.
func (p Plan) Holes() []OptionalHole {
	return append([]OptionalHole(nil), p.holes...)
}

// Missing returns the required objects that failed authorization.
func (p Plan) Missing() []MissingGrant {
	return append([]MissingGrant(nil), p.missing...)
}

// Snapshot returns the plan-time authorization reason for each closure object.
func (p Plan) Snapshot() map[ObjectID]string {
	out := make(map[ObjectID]string, len(p.snapshotGrant))
	for id, reason := range p.snapshotGrant {
		out[id] = reason
	}
	return out
}

// PlanRestore computes the recoverable closure for req under g and auth.
//
// Required references are followed transitively and every reachable object
// must be present and authorized; otherwise the whole plan is rejected
// (fail-closed) and a non-nil error wrapping ErrPlanRejected is returned.
// Optional references are never followed implicitly: they become explicit
// empty holes unless the target is listed in IncludeOptional, in which case it
// is pulled in and subjected to the same per-object authorization.
func PlanRestore(ctx context.Context, g *Graph, auth Authorizer, req Request) (Plan, error) {
	if len(req.Roots) == 0 {
		return Plan{status: StatusRejected}, ErrNoRoots
	}

	plan := Plan{
		roots:         append([]ObjectID(nil), req.Roots...),
		status:        StatusReady,
		snapshotGrant: make(map[ObjectID]string),
	}

	seen := make(map[ObjectID]bool)
	holeSeen := make(map[[2]ObjectID]bool)
	queue := make([]ObjectID, 0, len(req.Roots))

	// enqueue authorizes id and, on success, schedules its references for
	// traversal. A failure records a missing grant and does not descend, since
	// the object cannot be restored anyway.
	enqueue := func(id, source ObjectID, edge EdgeKind) {
		if seen[id] {
			return
		}
		seen[id] = true

		if !g.Has(id) {
			plan.missing = append(plan.missing, MissingGrant{
				ID: id, Source: source, Edge: edge, Reason: "object not present in reference graph",
			})
			return
		}

		allowed, reason, err := auth.Allowed(ctx, id)
		if err != nil {
			allowed = false
			reason = "authorization backend error: " + err.Error()
		}
		if !allowed {
			plan.missing = append(plan.missing, MissingGrant{
				ID: id, Source: source, Edge: edge, Reason: reason,
			})
			return
		}

		plan.snapshotGrant[id] = reason
		queue = append(queue, id)
	}

	// Roots are required.
	for _, root := range req.Roots {
		enqueue(root, "", EdgeRequired)
	}

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		for _, ref := range g.refsFrom(node) {
			switch ref.Kind {
			case EdgeRequired:
				enqueue(ref.To, node, EdgeRequired)
			case EdgeOptional:
				if req.IncludeOptional != nil && req.IncludeOptional[ref.To] {
					enqueue(ref.To, node, EdgeOptional)
					continue
				}
				key := [2]ObjectID{node, ref.To}
				if holeSeen[key] {
					continue
				}
				holeSeen[key] = true

				hole := OptionalHole{From: node, To: ref.To}
				if seen[ref.To] {
					hole.Status = HoleCovered
					hole.Reason = "target already included via a required path"
				} else {
					hole.Status = HoleLeftEmpty
					hole.Reason = "optional dependency not explicitly requested"
				}
				plan.holes = append(plan.holes, hole)
			default:
				// Unknown edge kinds are treated as required and therefore
				// fail closed rather than being silently ignored.
				enqueue(ref.To, node, EdgeRequired)
			}
		}
	}

	// Build the deterministic restore set from the authorized closure.
	restore := make([]ObjectID, 0, len(plan.snapshotGrant))
	for id := range plan.snapshotGrant {
		restore = append(restore, id)
	}
	sort.Slice(restore, func(i, j int) bool { return restore[i] < restore[j] })
	plan.restore = restore

	sort.Slice(plan.holes, func(i, j int) bool {
		if plan.holes[i].From != plan.holes[j].From {
			return plan.holes[i].From < plan.holes[j].From
		}
		return plan.holes[i].To < plan.holes[j].To
	})

	sort.Slice(plan.missing, func(i, j int) bool {
		if plan.missing[i].ID != plan.missing[j].ID {
			return plan.missing[i].ID < plan.missing[j].ID
		}
		return plan.missing[i].Source < plan.missing[j].Source
	})

	if len(plan.missing) > 0 {
		plan.status = StatusRejected
		plan.restore = nil
		return plan, plan.rejectError()
	}

	return plan, nil
}

func (p Plan) rejectError() error {
	errs := make([]error, 0, len(p.missing))
	for _, miss := range p.missing {
		errs = append(errs, fmt.Errorf("%w: object %q required by %q: %s",
			ErrPlanRejected, miss.ID, rootOrSource(miss), miss.Reason))
	}
	return errors.Join(errs...)
}

func rootOrSource(miss MissingGrant) ObjectID {
	if miss.Source == "" {
		return "<root>"
	}
	return miss.Source
}

// Reverify re-checks every object in the closure against the live authorizer.
// It returns an error wrapping ErrRevoked if any grant observed at plan time
// has since been revoked, or if the backend fails. A ready plan whose grants
// are unchanged returns nil.
func (p Plan) Reverify(ctx context.Context, auth Authorizer) error {
	if p.status != StatusReady {
		return ErrNotPlanned
	}

	errs := make([]error, 0)
	for _, id := range p.restore {
		allowed, reason, err := auth.Allowed(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: object %q: backend error: %v", ErrRevoked, id, err))
			continue
		}
		if !allowed {
			errs = append(errs, fmt.Errorf("%w: object %q: %s", ErrRevoked, id, reason))
		}
	}
	return errors.Join(errs...)
}

// Publish performs the final verification and, only if every object is still
// authorized, returns the set of objects to materialize. A revoked grant
// causes the whole recovery to be refused.
func (p Plan) Publish(ctx context.Context, auth Authorizer) ([]ObjectID, error) {
	if err := p.Reverify(ctx, auth); err != nil {
		return nil, err
	}
	return p.Restore(), nil
}
