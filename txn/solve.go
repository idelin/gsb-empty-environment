package txn

import (
	"fmt"
	"sort"
)

// Solve expands a manager request into an executable, acyclic job plan.
//
// The request's anchor job is always protected from replacement. On any
// unrecoverable error (unsatisfied Requisite, unresolvable conflict, unknown
// unit) the whole transaction is rejected and no Plan is returned.
func Solve(g *Graph, snap Snapshot, req Request) (*Plan, error) {
	if err := validateRequest(g, req); err != nil {
		return nil, err
	}
	if err := validateReferences(g); err != nil {
		return nil, err
	}

	idx := buildIndex(g)
	engine := newEngine(g, idx, snap, req.Mode, req.Unit)

	anchorType := req.Job
	if err := engine.run(anchorType); err != nil {
		return nil, err
	}
	if req.Isolate {
		if err := engine.applyIsolate(); err != nil {
			return nil, err
		}
	}

	names := make([]string, 0, len(engine.jobs))
	for name := range engine.jobs {
		names = append(names, name)
	}
	sort.Strings(names)

	orderEdges := buildOrderEdges(engine.jobs, idx)
	orderEdges, cuts := breakCycles(names, orderEdges, engine.jobs)
	layers := executionLayers(names, orderEdges)

	plan := &Plan{
		Request: req,
		Jobs:    make([]PlannedJob, 0, len(names)),
		Edges:   make([]OrderEdge, 0, len(orderEdges)),
		Layers:  layers,
		Cuts:    cuts,
	}
	for _, name := range names {
		j := engine.jobs[name]
		plan.Jobs = append(plan.Jobs, PlannedJob{
			Unit:        name,
			Type:        j.typ,
			Anchor:      j.anchor,
			Irreducible: j.irreducible,
		})
	}
	for _, e := range orderEdges {
		plan.Edges = append(plan.Edges, OrderEdge{
			From:     e.from,
			To:       e.to,
			Reversed: e.reversed,
			Reason:   e.reason,
		})
	}
	return plan, nil
}

func validateRequest(g *Graph, req Request) error {
	if g == nil || g.Units == nil {
		return fmt.Errorf("%w: nil graph", ErrInvalidRequest)
	}
	if g.Units[req.Unit] == nil {
		return fmt.Errorf("%w: anchor unit %q not in graph", ErrInvalidRequest, req.Unit)
	}
	switch req.Job {
	case JobStart, JobStop, JobRestart:
	default:
		return fmt.Errorf("%w: unsupported anchor job %s", ErrInvalidRequest, req.Job)
	}
	if req.Isolate && req.Job != JobStart {
		return fmt.Errorf("%w: isolate is only valid with a start request", ErrInvalidRequest)
	}
	switch req.Mode {
	case ModeFail, ModeReplace, ModeReplaceIrreversibly:
	default:
		return fmt.Errorf("%w: unknown mode", ErrInvalidRequest)
	}
	return nil
}

// JobType returns the planned job type for a unit, and whether a job exists.
func (p *Plan) JobType(unit string) (JobType, bool) {
	for _, j := range p.Jobs {
		if j.Unit == unit {
			return j.Type, true
		}
	}
	return 0, false
}
