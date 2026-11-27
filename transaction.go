package systemd

import (
	"fmt"
	"sort"
	"strings"
)

// Solver is a reusable transaction solver over a graph snapshot. The graph is
// read-only during Solve, so one Solver may be used concurrently as long as
// the graph is not mutated.
type Solver struct {
	Graph *Graph
}

// NewSolver wraps a graph in a Solver.
func NewSolver(graph *Graph) *Solver { return &Solver{Graph: graph} }

// NewSolverFromUnits constructs a graph and wraps it.
func NewSolverFromUnits(units ...Unit) *Solver { return NewSolver(NewGraph(units...)) }

// Solve expands and resolves a set of requests. All non-empty modes must
// agree. Multiple requests are useful for testing anchor-vs-anchor conflicts.
func (s *Solver) Solve(requests ...Request) (*Plan, error) {
	if s == nil || s.Graph == nil {
		return nil, transactionError(ErrorInvalidRequest, "", "", "nil graph")
	}
	return solve(s.Graph, requests...)
}

// Solve is the package-level single-request convenience function.
func Solve(graph *Graph, request Request) (*Plan, error) {
	return solve(graph, request)
}

// SolveAll is the package-level multi-request convenience function.
func SolveAll(graph *Graph, requests ...Request) (*Plan, error) {
	return solve(graph, requests...)
}

// Solve solves one request against this graph.
func (g *Graph) Solve(request Request) (*Plan, error) {
	return solve(g, request)
}

// SolveAll solves a set of requests against this graph.
func (g *Graph) SolveAll(requests ...Request) (*Plan, error) {
	return solve(g, requests...)
}

// Start starts a unit.
func (g *Graph) Start(unit string, mode ...TransactionMode) (*Plan, error) {
	return solve(g, Start(unit, mode...))
}

// Stop stops a unit.
func (g *Graph) Stop(unit string, mode ...TransactionMode) (*Plan, error) {
	return solve(g, Stop(unit, mode...))
}

// Restart restarts a unit.
func (g *Graph) Restart(unit string, mode ...TransactionMode) (*Plan, error) {
	return solve(g, Restart(unit, mode...))
}

// Isolate starts the target's dependency closure and stops every other active
// unit unless it is marked IgnoreOnIsolate.
func (g *Graph) Isolate(unit string, mode ...TransactionMode) (*Plan, error) {
	return solve(g, Isolate(unit, mode...))
}

type startOptions struct {
	anchor   bool
	core     bool
	optional bool
	origin   string
	reason   string
}

type stopOptions struct {
	anchor   bool
	core     bool
	conflict bool
	force    bool
	pin      bool
	origin   string
	reason   string
}

type restartOptions struct {
	anchor bool
	core   bool
	origin string
	reason string
}

type jobState struct {
	job Job

	origins       map[string]struct{}
	originsPinned bool
	hardNeeds     map[string]struct{}
	softNeeds     map[string]struct{}
	reasons       map[string]struct{}

	startExpanded    bool
	stopExpanded     bool
	restartExpanded  bool
	expandingStart   bool
	expandingStop    bool
	expandingRestart bool
}

type dependencyKey struct {
	from string
	to   string
	kind DependencyKind
}

type solver struct {
	graph *Graph
	mode  TransactionMode

	jobs map[string]*jobState

	dependencies []Dependency
	depSeen      map[dependencyKey]struct{}
	removed      []Job
	removedSeen  map[string]struct{}
	warnings     []string
	warningSeen  map[string]struct{}

	conflicts map[string][]string

	isolateProtected map[string]bool
}

func solve(graph *Graph, requests ...Request) (*Plan, error) {
	if graph == nil {
		return nil, transactionError(ErrorInvalidRequest, "", "", "nil graph")
	}
	if len(requests) == 0 {
		return nil, transactionError(ErrorInvalidRequest, "", "", "no request supplied")
	}

	normalized := make([]Request, 0, len(requests))
	mode := TransactionMode("")
	for _, request := range requests {
		request = request.normalized()
		if request.Unit == "" {
			return nil, transactionError(ErrorInvalidRequest, "", "", "request unit must not be empty")
		}
		if request.Action == "" {
			request.Action = ActionStart
			request.Type = ActionStart
		}
		switch request.Action {
		case ActionStart, ActionStop, ActionRestart, ActionIsolate:
		default:
			return nil, transactionError(ErrorInvalidRequest, request.Unit, "", "unknown action %q", request.Action)
		}
		switch request.Mode {
		case ModeFail, ModeReplace, ModeReplaceIrreversibly:
		default:
			return nil, transactionError(ErrorInvalidRequest, request.Unit, "", "unknown transaction mode %q", request.Mode)
		}
		if mode == "" {
			mode = request.Mode
		} else if mode != request.Mode {
			return nil, transactionError(ErrorInvalidRequest, request.Unit, "", "requests use different transaction modes")
		}
		normalized = append(normalized, request)
	}

	// Sorting requests makes conflict arbitration independent of caller order.
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].Unit != normalized[j].Unit {
			return normalized[i].Unit < normalized[j].Unit
		}
		return normalized[i].Action < normalized[j].Action
	})

	s := &solver{
		graph:       graph,
		mode:        mode,
		jobs:        make(map[string]*jobState),
		depSeen:     make(map[dependencyKey]struct{}),
		removedSeen: make(map[string]struct{}),
		warningSeen: make(map[string]struct{}),
		conflicts:   buildConflictIndex(graph),
	}

	isolateRequests := 0
	for _, request := range normalized {
		if request.Action == ActionIsolate {
			isolateRequests++
		}
	}
	if isolateRequests > 0 && len(normalized) != 1 {
		return nil, transactionError(ErrorInvalidRequest, normalized[0].Unit, "", "isolate must be the only request in its transaction")
	}

	for _, request := range normalized {
		switch request.Action {
		case ActionStart:
			if _, err := s.addStart(request.Unit, startOptions{
				anchor: true,
				core:   true,
				reason: "requested start",
			}); err != nil {
				return nil, err
			}
		case ActionStop:
			if _, err := s.addStop(request.Unit, stopOptions{
				anchor: true,
				core:   true,
				force:  true,
				reason: "requested stop",
			}); err != nil {
				return nil, err
			}
		case ActionRestart:
			if _, err := s.addRestart(request.Unit, restartOptions{
				anchor: true,
				core:   true,
				reason: "requested restart",
			}); err != nil {
				return nil, err
			}
		case ActionIsolate:
			if err := s.isolate(request.Unit); err != nil {
				return nil, err
			}
		}
	}

	return s.plan(normalized)
}

func buildConflictIndex(graph *Graph) map[string][]string {
	index := make(map[string][]string)
	for _, unit := range sortedUnits(graph) {
		for _, other := range sortedUnique(unit.Conflicts) {
			index[unit.Name] = append(index[unit.Name], other)
			index[other] = append(index[other], unit.Name)
		}
	}
	for name := range index {
		index[name] = sortedUnique(index[name])
	}
	return index
}

func (s *solver) addStart(name string, opts startOptions) (bool, error) {
	unit, ok := s.graph.Units[name]
	if !ok || unit == nil {
		if opts.optional {
			s.warnf("optional unit %q not found", name)
			return false, nil
		}
		return false, transactionError(ErrorMissingUnit, name, opts.origin, "required start target is missing")
	}

	existing := s.jobs[name]
	if existing == nil && unit.IsActive() && !opts.anchor {
		return true, nil
	}

	candidate := &jobState{
		job: Job{
			Unit:   name,
			Type:   JobStart,
			Anchor: opts.anchor,
			Core:   opts.core,
			Reason: opts.reason,
		},
		origins:   make(map[string]struct{}),
		hardNeeds: make(map[string]struct{}),
		softNeeds: make(map[string]struct{}),
		reasons:   make(map[string]struct{}),
	}
	addOrigin(candidate, opts.origin)
	addReason(candidate, opts.reason)

	state, accepted, err := s.install(candidate)
	if err != nil {
		return false, err
	}
	if !accepted || state == nil {
		return false, nil
	}
	if !isStartish(state.job.Type) {
		return false, nil
	}
	if err := s.expandStart(state); err != nil {
		return false, err
	}
	current := s.jobs[name]
	if current == nil || !isStartish(current.job.Type) {
		return false, nil
	}
	return true, nil
}

func (s *solver) addStop(name string, opts stopOptions) (bool, error) {
	if s.isolateProtected[name] {
		return true, nil
	}

	unit, ok := s.graph.Units[name]
	if !ok || unit == nil {
		if opts.conflict {
			s.warnf("conflicting unit %q not found", name)
			return false, nil
		}
		if opts.anchor || opts.core {
			return false, transactionError(ErrorMissingUnit, name, opts.origin, "required stop target is missing")
		}
		s.warnf("stop target %q not found", name)
		return false, nil
	}

	existing := s.jobs[name]
	if existing == nil && !unit.IsActive() && !opts.anchor && !opts.force {
		return true, nil
	}

	candidate := &jobState{
		job: Job{
			Unit:   name,
			Type:   JobStop,
			Anchor: opts.anchor,
			Core:   opts.core,
			Reason: opts.reason,
		},
		origins:       make(map[string]struct{}),
		originsPinned: opts.pin,
		hardNeeds:     make(map[string]struct{}),
		softNeeds:     make(map[string]struct{}),
		reasons:       make(map[string]struct{}),
	}
	addOrigin(candidate, opts.origin)
	addReason(candidate, opts.reason)

	state, accepted, err := s.install(candidate)
	if err != nil {
		return false, err
	}
	if !accepted || state == nil {
		return false, nil
	}
	if !isStopish(state.job.Type) {
		return false, nil
	}
	if err := s.expandStop(state); err != nil {
		return false, err
	}
	current := s.jobs[name]
	if current == nil || !isStopish(current.job.Type) {
		return false, nil
	}
	return true, nil
}

func (s *solver) addRestart(name string, opts restartOptions) (bool, error) {
	unit, ok := s.graph.Units[name]
	if !ok || unit == nil {
		return false, transactionError(ErrorMissingUnit, name, opts.origin, "required restart target is missing")
	}

	candidate := &jobState{
		job: Job{
			Unit:   name,
			Type:   JobRestart,
			Anchor: opts.anchor,
			Core:   opts.core,
			Reason: opts.reason,
		},
		origins:   make(map[string]struct{}),
		hardNeeds: make(map[string]struct{}),
		softNeeds: make(map[string]struct{}),
		reasons:   make(map[string]struct{}),
	}
	addOrigin(candidate, opts.origin)
	addReason(candidate, opts.reason)

	state, accepted, err := s.install(candidate)
	if err != nil {
		return false, err
	}
	if !accepted || state == nil {
		return false, nil
	}
	if state.job.Type == JobStop {
		if err := s.expandStop(state); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := s.expandStart(state); err != nil {
		return false, err
	}
	if err := s.expandRestart(state); err != nil {
		return false, err
	}
	current := s.jobs[name]
	return current != nil, nil
}

func (s *solver) install(candidate *jobState) (*jobState, bool, error) {
	name := candidate.job.Unit
	existing := s.jobs[name]
	if existing == nil {
		s.jobs[name] = candidate
		return candidate, true, nil
	}

	mergedType, compatible := mergeJobTypes(existing.job.Type, candidate.job.Type)
	if compatible {
		existing.job.Type = mergedType
		mergeJobState(existing, candidate)
		return existing, true, nil
	}

	keepExisting, err := s.arbitrateConflict(existing, candidate)
	if err != nil {
		return nil, false, err
	}
	if keepExisting {
		s.recordRemoved(candidate.job)
		return existing, false, nil
	}

	old := existing.job
	oldOrigins := copyStringSet(existing.origins)
	oldPinned := existing.originsPinned
	if err := s.removeJob(name, "replaced by conflicting job"); err != nil {
		return nil, false, err
	}

	for origin := range oldOrigins {
		addOrigin(candidate, origin)
	}
	candidate.originsPinned = candidate.originsPinned || oldPinned
	s.jobs[name] = candidate
	s.recordRemoved(old)
	return candidate, true, nil
}

func (s *solver) arbitrateConflict(existing, incoming *jobState) (bool, error) {
	if s.mode == ModeFail {
		return false, transactionError(
			ErrorTransactionConflict,
			existing.job.Unit,
			incoming.job.Unit,
			"conflicting %s and %s jobs in fail mode",
			existing.job.Type,
			incoming.job.Type,
		)
	}

	// Anchors and irreversible jobs are protected asymmetrically: a protected
	// job may replace an unprotected one, but two equal protected jobs cannot
	// both survive.
	if existing.job.Anchor && incoming.job.Anchor {
		return false, transactionError(
			ErrorTransactionConflict,
			existing.job.Unit,
			incoming.job.Unit,
			"both conflicting jobs are anchors",
		)
	}
	if existing.job.Anchor != incoming.job.Anchor {
		return existing.job.Anchor, nil
	}

	if existing.job.Irreversible && incoming.job.Irreversible {
		return false, transactionError(
			ErrorTransactionConflict,
			existing.job.Unit,
			incoming.job.Unit,
			"both conflicting jobs are irreversible",
		)
	}
	if existing.job.Irreversible != incoming.job.Irreversible {
		return existing.job.Irreversible, nil
	}

	if existing.job.Core && incoming.job.Core {
		return false, transactionError(
			ErrorTransactionConflict,
			existing.job.Unit,
			incoming.job.Unit,
			"both conflicting jobs are irreducible core requirements",
		)
	}
	if existing.job.Core != incoming.job.Core {
		return existing.job.Core, nil
	}

	// Both sides are non-anchor, non-core and non-irreversible. replace mode
	// lets the newly expanded dependency win.
	return false, nil
}

func (s *solver) expandStart(state *jobState) error {
	if state == nil || !isStartish(state.job.Type) {
		return nil
	}
	if state.startExpanded {
		return nil
	}
	if state.expandingStart {
		return nil
	}
	state.expandingStart = true
	defer func() { state.expandingStart = false }()
	state.startExpanded = true

	name := state.job.Unit
	unit := s.graph.Units[name]
	if unit == nil {
		return transactionError(ErrorMissingUnit, name, "", "unit disappeared from graph")
	}

	for _, requisite := range sortedUnique(unit.Requisite) {
		dep, ok := s.graph.Units[requisite]
		if !ok || dep == nil {
			return transactionError(ErrorMissingUnit, requisite, name, "Requisite target is missing")
		}
		if !dep.IsActive() {
			return transactionError(
				ErrorRequisiteInactive,
				requisite,
				name,
				"Requisite target is not active; it must not be started implicitly",
			)
		}
		s.recordDependency(name, requisite, DependencyRequisite, true, true)
	}

	for _, required := range sortedUnique(append(append([]string(nil), unit.Requires...), unit.BindsTo...)) {
		kind := DependencyRequires
		if containsName(unit.BindsTo, required) {
			kind = DependencyBindsTo
		}
		state.hardNeeds[required] = struct{}{}
		s.recordDependency(name, required, kind, true, unitIsActive(s.graph, required))
		accepted, err := s.addStart(required, startOptions{
			core:   true,
			origin: name,
			reason: string(kind),
		})
		if err != nil {
			return err
		}
		if !accepted {
			if s.jobs[name] == state {
				if state.job.Anchor {
					return transactionError(
						ErrorTransactionConflict,
						name,
						required,
						"anchor lost an unavailable required start job",
					)
				}
				if err := s.removeJob(name, "hard requirement "+required+" could not be started"); err != nil {
					return err
				}
			}
			return nil
		}
		if s.jobs[name] != state {
			return nil
		}
	}

	for _, wanted := range sortedUnique(unit.Wants) {
		state.softNeeds[wanted] = struct{}{}
		s.recordDependency(name, wanted, DependencyWants, false, unitIsActive(s.graph, wanted))
		if _, err := s.addStart(wanted, startOptions{
			optional: true,
			origin:   name,
			reason:   string(DependencyWants),
		}); err != nil {
			return err
		}
		if s.jobs[name] != state {
			return nil
		}
	}

	if !isStartish(state.job.Type) {
		return nil
	}

	for _, conflicting := range s.conflicts[name] {
		target, ok := s.graph.Units[conflicting]
		if !ok || target == nil {
			s.warnf("conflicting unit %q not found", conflicting)
			continue
		}
		targetJob := s.jobs[conflicting]
		if !target.IsActive() && (targetJob == nil || !isStartish(targetJob.job.Type)) {
			continue
		}
		s.recordDependency(name, conflicting, DependencyConflict, true, target.IsActive())
		if _, err := s.addStop(conflicting, stopOptions{
			conflict: true,
			force:    true,
			origin:   name,
			reason:   "Conflicts=" + name,
		}); err != nil {
			return err
		}
		if s.jobs[name] != state {
			return nil
		}
	}

	return nil
}

func (s *solver) expandStop(state *jobState) error {
	if state == nil || !isStopish(state.job.Type) {
		return nil
	}
	if state.stopExpanded {
		return nil
	}
	if state.expandingStop {
		return nil
	}
	state.expandingStop = true
	defer func() { state.expandingStop = false }()
	state.stopExpanded = true

	name := state.job.Unit
	for _, other := range sortedUnits(s.graph) {
		if other == nil || other.Name == name {
			continue
		}

		if containsName(other.PartOf, name) {
			s.recordDependency(other.Name, name, DependencyPartOf, false, other.IsActive())
			accepted, err := s.addStop(other.Name, stopOptions{
				origin: name,
				reason: "PartOf propagation",
			})
			if err != nil {
				return err
			}
			if !accepted && hasStartJob(s.jobs, other.Name) {
				return transactionError(
					ErrorTransactionConflict,
					other.Name,
					name,
					"cannot stop PartOf dependent while its start job remains",
				)
			}
			if s.jobs[name] != state {
				return nil
			}
		}

		if containsName(other.Requires, name) || containsName(other.Requisite, name) || containsName(other.BindsTo, name) {
			kind := DependencyRequires
			switch {
			case containsName(other.Requisite, name):
				kind = DependencyRequisite
			case containsName(other.BindsTo, name):
				kind = DependencyBindsTo
			}
			s.recordDependency(other.Name, name, kind, true, other.IsActive())
			accepted, err := s.addStop(other.Name, stopOptions{
				core:   true,
				origin: name,
				reason: "reverse " + string(kind) + " propagation",
			})
			if err != nil {
				return err
			}
			if !accepted && hasStartJob(s.jobs, other.Name) {
				return transactionError(
					ErrorTransactionConflict,
					other.Name,
					name,
					"cannot stop dependent while its start job remains",
				)
			}
			if s.jobs[name] != state {
				return nil
			}
		}
	}
	return nil
}

func (s *solver) expandRestart(state *jobState) error {
	if state == nil || state.job.Type != JobRestart {
		return nil
	}
	if state.restartExpanded {
		return nil
	}
	if state.expandingRestart {
		return nil
	}
	state.expandingRestart = true
	defer func() { state.expandingRestart = false }()
	state.restartExpanded = true

	name := state.job.Unit
	for _, other := range sortedUnits(s.graph) {
		if other == nil || other.Name == name {
			continue
		}
		if !containsName(other.PartOf, name) {
			continue
		}
		if _, err := s.addRestart(other.Name, restartOptions{
			origin: name,
			reason: "PartOf restart propagation",
		}); err != nil {
			return err
		}
		if s.jobs[name] != state {
			return nil
		}
	}
	return nil
}

func (s *solver) isolate(target string) error {
	unit, ok := s.graph.Units[target]
	if !ok || unit == nil {
		return transactionError(ErrorMissingUnit, target, "", "isolate target is missing")
	}

	protected := make(map[string]bool)
	protected[target] = true
	for name := range s.dependencyClosure(target) {
		protected[name] = true
	}
	for _, other := range sortedUnits(s.graph) {
		if other.IgnoreOnIsolate {
			protected[other.Name] = true
		}
	}
	s.isolateProtected = protected

	if !unit.IsActive() {
		if _, err := s.addStart(target, startOptions{
			anchor: true,
			core:   true,
			reason: "isolate target",
		}); err != nil {
			return err
		}
	}

	for _, other := range sortedUnits(s.graph) {
		if !other.IsActive() || protected[other.Name] {
			continue
		}
		if _, err := s.addStop(other.Name, stopOptions{
			core:   true,
			force:  true,
			pin:    true,
			reason: "isolate",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *solver) dependencyClosure(target string) map[string]bool {
	seen := make(map[string]bool)
	var visit func(string)
	visit = func(name string) {
		unit := s.graph.Units[name]
		if unit == nil {
			return
		}
		for _, dep := range sortedUnique(append(append(append(
			append([]string(nil), unit.Requires...),
			unit.Requisite...),
			unit.Wants...),
			unit.BindsTo...)) {
			if seen[dep] {
				continue
			}
			seen[dep] = true
			visit(dep)
		}
	}
	visit(target)
	return seen
}

func (s *solver) removeJob(name, reason string) error {
	state := s.jobs[name]
	if state == nil {
		return nil
	}
	delete(s.jobs, name)
	s.recordRemoved(state.job)

	if !isStartish(state.job.Type) {
		return nil
	}

	for _, otherName := range sortedJobNames(s.jobs) {
		other := s.jobs[otherName]
		if other == nil {
			continue
		}
		if _, required := other.hardNeeds[name]; !required {
			continue
		}
		if unitIsActive(s.graph, name) || hasStartJob(s.jobs, name) {
			continue
		}
		if other.job.Anchor {
			return transactionError(
				ErrorTransactionConflict,
				otherName,
				name,
				"anchor lost a required start job (%s)",
				reason,
			)
		}
		if err := s.removeJob(otherName, "required unit "+name+" was removed"); err != nil {
			return err
		}
	}
	return nil
}

func (s *solver) recordDependency(from, to string, kind DependencyKind, hard, active bool) {
	key := dependencyKey{from: from, to: to, kind: kind}
	if _, exists := s.depSeen[key]; exists {
		return
	}
	s.depSeen[key] = struct{}{}
	s.dependencies = append(s.dependencies, Dependency{
		From:              from,
		To:                to,
		Kind:              kind,
		Hard:              hard,
		SatisfiedByActive: active,
	})
}

func (s *solver) warnf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if _, exists := s.warningSeen[message]; exists {
		return
	}
	s.warningSeen[message] = struct{}{}
	s.warnings = append(s.warnings, message)
}

func (s *solver) recordRemoved(job Job) {
	key := fmt.Sprintf("%s/%s/%t/%t", job.Unit, job.Type, job.Anchor, job.Core)
	if _, exists := s.removedSeen[key]; exists {
		return
	}
	s.removedSeen[key] = struct{}{}
	s.removed = append(s.removed, job)
}

func mergeJobTypes(existing, incoming JobType) (JobType, bool) {
	if existing == incoming {
		return existing, true
	}

	// Restart overlaps both phases. A stop request wins over restart, while a
	// start request is promoted to restart.
	if existing == JobRestart && incoming == JobStop {
		return JobStop, true
	}
	if existing == JobStop && incoming == JobRestart {
		return JobStop, true
	}
	if isStartish(existing) && isStartish(incoming) {
		return JobRestart, true
	}
	if isStopish(existing) && isStopish(incoming) {
		return JobStop, true
	}
	return existing, false
}

func mergeJobState(dst, src *jobState) {
	dst.job.Anchor = dst.job.Anchor || src.job.Anchor
	dst.job.Core = dst.job.Core || src.job.Core
	dst.job.Irreversible = dst.job.Irreversible || src.job.Irreversible
	dst.originsPinned = dst.originsPinned || src.originsPinned

	for origin := range src.origins {
		addOrigin(dst, origin)
	}
	for need := range src.hardNeeds {
		if dst.hardNeeds == nil {
			dst.hardNeeds = make(map[string]struct{})
		}
		dst.hardNeeds[need] = struct{}{}
	}
	for need := range src.softNeeds {
		if dst.softNeeds == nil {
			dst.softNeeds = make(map[string]struct{})
		}
		dst.softNeeds[need] = struct{}{}
	}
	for reason := range src.reasons {
		addReason(dst, reason)
	}
	if dst.reasons == nil {
		dst.reasons = make(map[string]struct{})
	}
	if dst.job.Reason == "" {
		dst.job.Reason = src.job.Reason
	}
}

func addOrigin(state *jobState, origin string) {
	if origin == "" {
		return
	}
	if state.origins == nil {
		state.origins = make(map[string]struct{})
	}
	state.origins[origin] = struct{}{}
}

func addReason(state *jobState, reason string) {
	if reason == "" {
		return
	}
	if state.reasons == nil {
		state.reasons = make(map[string]struct{})
	}
	state.reasons[reason] = struct{}{}
	if state.job.Reason == "" {
		state.job.Reason = reason
	}
}

func copyStringSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for value := range in {
		out[value] = struct{}{}
	}
	return out
}

func isStartish(jobType JobType) bool {
	return jobType == JobStart || jobType == JobRestart
}

func isStopish(jobType JobType) bool {
	return jobType == JobStop || jobType == JobRestart
}

func unitIsActive(graph *Graph, name string) bool {
	if graph == nil {
		return false
	}
	unit := graph.Units[name]
	return unit != nil && unit.IsActive()
}

func hasStartJob(jobs map[string]*jobState, name string) bool {
	state := jobs[name]
	return state != nil && isStartish(state.job.Type)
}

func sortedJobNames(jobs map[string]*jobState) []string {
	names := make([]string, 0, len(jobs))
	for name := range jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *solver) jobNamesSorted() []string {
	return sortedJobNames(s.jobs)
}

func joinReasons(reasons map[string]struct{}) []string {
	out := make([]string, 0, len(reasons))
	for reason := range reasons {
		out = append(out, reason)
	}
	sort.Strings(out)
	return out
}

func trimReasonList(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	if len(reasons) == 1 {
		return reasons[0]
	}
	return strings.Join(reasons, "; ")
}
