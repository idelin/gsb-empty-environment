package txn

import "fmt"

// job is the internal representation of a transaction job.
//
// anchor marks the job created directly by the caller request. irreducible
// (systemd's "matters") marks hard requirements that cannot simply be pruned:
// anchor jobs, Requires/BindsTo pulls and Requisite gates.
type job struct {
	unit        string
	typ         JobType
	anchor      bool
	irreducible bool
}

// engine performs fixed-point transaction expansion.
type engine struct {
	g      *Graph
	idx    *depIndex
	snap   Snapshot
	mode   TransactionMode
	jobs   map[string]*job
	pend   map[string][]*job
	edges  map[string]map[string]struct{}
	anchor string
	roots  map[string]struct{}
}

func newEngine(g *Graph, idx *depIndex, snap Snapshot, mode TransactionMode, anchor string) *engine {
	return &engine{
		g:      g,
		idx:    idx,
		snap:   snap,
		mode:   mode,
		jobs:   map[string]*job{},
		pend:   map[string][]*job{},
		edges:  map[string]map[string]struct{}{},
		anchor: anchor,
		roots:  map[string]struct{}{anchor: struct{}{}},
	}
}

// run seeds the anchor job and expands until a fixed point is reached.
func (e *engine) run(anchorType JobType) error {
	e.enqueue(e.anchor, anchorType, true, true)
	return e.drain()
}

func (e *engine) enqueue(name string, typ JobType, anchor, irreducible bool) {
	e.pend[name] = append(e.pend[name], &job{
		unit:        name,
		typ:         typ,
		anchor:      anchor,
		irreducible: irreducible,
	})
}

// drain processes pending jobs in deterministic (unit-name) order.
func (e *engine) drain() error {
	for len(e.pend) > 0 {
		name := minPendingUnit(e.pend)
		queue := e.pend[name]
		next := queue[0]
		if len(queue) == 1 {
			delete(e.pend, name)
		} else {
			e.pend[name] = queue[1:]
		}
		if err := e.commit(next); err != nil {
			return err
		}
	}
	return nil
}

func minPendingUnit(pend map[string][]*job) string {
	min := ""
	for name := range pend {
		if min == "" || name < min {
			min = name
		}
	}
	return min
}

// commit merges an incoming job with any existing job on the same unit, or
// arbitrates the start/stop collision according to the transaction mode.
func (e *engine) commit(in *job) error {
	existing := e.jobs[in.unit]
	if existing == nil {
		e.jobs[in.unit] = in
		return e.expand(in)
	}

	if merged, ok := mergeType(existing.typ, in.typ); ok {
		old := existing.typ
		existing.typ = merged
		existing.anchor = existing.anchor || in.anchor
		existing.irreducible = existing.irreducible || in.irreducible
		if merged != old {
			// A changed polarity may pull additional dependencies.
			return e.expand(existing)
		}
		return nil
	}
	return e.arbitrate(existing, in)
}

// mergeType collapses two compatible jobs on the same unit. Incompatible
// combinations always contain one stop job and are handled by arbitrate.
func mergeType(a, b JobType) (JobType, bool) {
	switch {
	case a == b:
		return a, true
	case (a == JobStart && b == JobRestart) || (a == JobRestart && b == JobStart):
		return JobRestart, true
	case a == JobVerifyActive && b == JobStart:
		return JobStart, true
	case a == JobStart && b == JobVerifyActive:
		return JobStart, true
	case a == JobVerifyActive && b == JobRestart:
		return JobRestart, true
	case a == JobRestart && b == JobVerifyActive:
		return JobRestart, true
	default:
		return 0, false
	}
}

// arbitrate resolves a start-like vs stop collision. systemd always lets the
// stop side win when replacement is permitted; the protected (anchor /
// irreducible) status of the existing job decides whether the transaction must
// fail. This is independent of job enqueue order.
func (e *engine) arbitrate(existing, in *job) error {
	switch e.mode {
	case ModeFail:
		return conflictError(existing, in)
	case ModeReplace, ModeReplaceIrreversibly:
		// Normalise so that stop is the challenger, matching systemd's
		// stop-outranks-start collision handling.
		stopJob, startJob := in, existing
		if existing.typ == JobStop {
			stopJob, startJob = existing, in
		}
		protected := startJob.anchor ||
			(startJob.irreducible && e.mode == ModeReplace)
		if protected {
			return conflictError(existing, in)
		}
		existing.typ = JobStop
		existing.anchor = existing.anchor || stopJob.anchor
		existing.irreducible = existing.irreducible || stopJob.irreducible
		return e.expand(existing)
	default:
		return fmt.Errorf("%w: unknown mode", ErrInvalidRequest)
	}
}

func conflictError(existing, in *job) error {
	return fmt.Errorf("%w on unit %q: existing %s (anchor=%t irreducible=%t) vs incoming %s (anchor=%t irreducible=%t)",
		ErrConflict, existing.unit, existing.typ, existing.anchor, existing.irreducible,
		in.typ, in.anchor, in.irreducible)
}

// expand pulls all jobs entailed by one committed job.
func (e *engine) expand(j *job) error {
	u := e.g.Units[j.unit]

	switch j.typ {
	case JobStart, JobRestart:
		for _, dep := range u.Requires {
			e.edge(j.unit, dep)
			e.enqueue(dep, JobStart, false, true)
		}
		for _, dep := range u.BindsTo {
			e.edge(j.unit, dep)
			e.enqueue(dep, JobStart, false, true)
		}
		for _, dep := range u.Wants {
			if e.g.Units[dep] == nil {
				continue
			}
			e.edge(j.unit, dep)
			e.enqueue(dep, JobStart, false, false)
		}
		for _, dep := range u.Requisite {
			if !e.snap.Active(dep) {
				return fmt.Errorf("%w: unit %q has Requisite=%q which is not active",
					ErrRequisiteNotActive, j.unit, dep)
			}
			e.edge(j.unit, dep)
			e.enqueue(dep, JobVerifyActive, false, true)
		}
		// Conflicts are symmetric: stop every conflicting unit that is active
		// now or already participating in the transaction.
		for _, other := range e.idx.conflicts[j.unit] {
			if e.snap.Active(other) || e.jobs[other] != nil {
				e.enqueue(other, JobStop, false, false)
			}
		}
		if j.typ == JobRestart {
			for _, child := range e.idx.revPartOf[j.unit] {
				e.edge(j.unit, child)
				e.enqueue(child, JobRestart, false, false)
			}
			for _, child := range e.idx.revBindsTo[j.unit] {
				e.edge(j.unit, child)
				e.enqueue(child, JobStop, false, false)
			}
		}
	case JobStop:
		for _, child := range e.idx.revPartOf[j.unit] {
			e.edge(j.unit, child)
			e.enqueue(child, JobStop, false, false)
		}
		for _, child := range e.idx.revBindsTo[j.unit] {
			e.edge(j.unit, child)
			e.enqueue(child, JobStop, false, false)
		}
	case JobVerifyActive:
		// No-op gate: nothing to pull.
	}
	return nil
}

func (e *engine) edge(from, to string) {
	if e.edges[from] == nil {
		e.edges[from] = map[string]struct{}{}
	}
	e.edges[from][to] = struct{}{}
}

// pruneOrphans removes reducible jobs no longer reachable from any retained
// root through requirement edges. Irreducible (hard) jobs always survive.
func (e *engine) pruneOrphans() {
	reachable := map[string]struct{}{}
	queue := make([]string, 0, len(e.roots))
	for root := range e.roots {
		if _, ok := e.jobs[root]; ok {
			queue = append(queue, root)
			reachable[root] = struct{}{}
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for next := range e.edges[cur] {
			if _, hasJob := e.jobs[next]; !hasJob {
				continue
			}
			if _, seen := reachable[next]; seen {
				continue
			}
			reachable[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	for name, j := range e.jobs {
		if j.irreducible {
			continue
		}
		if _, ok := reachable[name]; !ok {
			delete(e.jobs, name)
		}
	}
}

// applyIsolate stops every active unit outside the anchor's dependency
// closure, except units carrying IgnoreOnIsolate. Units that already hold a
// job are left to conflict arbitration and never double-scheduled.
func (e *engine) applyIsolate() error {
	closure := e.anchorClosure()

	names := make([]string, 0, len(e.snap))
	for name := range e.snap {
		names = append(names, name)
	}
	for _, name := range names {
		if !e.snap.Active(name) {
			continue
		}
		if _, inClosure := closure[name]; inClosure {
			continue
		}
		if e.g.Units[name] == nil || e.g.Units[name].IgnoreOnIsolate {
			continue
		}
		if _, hasJob := e.jobs[name]; hasJob {
			continue
		}
		e.roots[name] = struct{}{}
		e.enqueue(name, JobStop, false, false)
	}
	if err := e.drain(); err != nil {
		return err
	}
	e.pruneOrphans()
	return nil
}

// anchorClosure is the fixed-point requirement/propagation reachability from
// the anchor: every unit systemd would keep reachable during an isolate.
func (e *engine) anchorClosure() map[string]struct{} {
	closure := map[string]struct{}{e.anchor: {}}
	queue := []string{e.anchor}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for next := range e.edges[cur] {
			if _, seen := closure[next]; seen {
				continue
			}
			closure[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return closure
}
