package systemd

import (
	"sort"
	"strings"
)

func (s *solver) plan(requests []Request) (*Plan, error) {
	if err := s.resolveStartConflicts(); err != nil {
		return nil, err
	}
	s.pruneOrphans()
	if err := s.resolveStartConflicts(); err != nil {
		return nil, err
	}

	jobs := s.finalJobs()
	jobMap := make(map[string]Job, len(jobs))
	for _, job := range jobs {
		jobMap[job.Unit] = job
	}

	edges := s.buildOrderEdges(jobMap, edgeModeCombined)
	retainedEdges, cycles, cuts := breakCycles(jobNamesFromJobs(jobs), edges)

	startEdges := s.buildOrderEdges(jobMap, edgeModeStart)
	retainedStart, startCycles, startCuts := breakCycles(jobNamesForPhase(jobs, PhaseStart), startEdges)

	stopEdges := s.buildOrderEdges(jobMap, edgeModeStop)
	retainedStop, stopCycles, stopCuts := breakCycles(jobNamesForPhase(jobs, PhaseStop), stopEdges)

	cycles = append(cycles, startCycles...)
	cycles = append(cycles, stopCycles...)
	cycles = dedupeCycles(cycles)

	cuts = append(cuts, startCuts...)
	cuts = append(cuts, stopCuts...)
	cuts = dedupeOrderEdges(cuts)

	plan := &Plan{
		Request:      requests[0],
		Requests:     append([]Request(nil), requests...),
		Mode:         s.mode,
		Jobs:         jobs,
		Edges:        retainedEdges,
		StartEdges:   retainedStart,
		StopEdges:    retainedStop,
		Dependencies: s.sortedDependencies(),
		Cycles:       cycles,
		CutEdges:     cuts,
		RemovedJobs:  s.sortedRemoved(),
		Warnings:     append([]string(nil), s.warnings...),
	}
	sort.Strings(plan.Warnings)

	plan.Order = topologicalOrder(jobNamesFromJobs(jobs), retainedEdges)
	plan.StartOrder = topologicalOrder(jobNamesForPhase(jobs, PhaseStart), retainedStart)
	plan.StopOrder = topologicalOrder(jobNamesForPhase(jobs, PhaseStop), retainedStop)
	plan.Steps = buildExecutionSteps(plan.StopOrder, plan.StartOrder)

	return plan, nil
}

func (s *solver) resolveStartConflicts() error {
	for {
		left, right, ok := s.firstStartConflict()
		if !ok {
			return nil
		}

		if s.mode == ModeFail {
			return transactionError(
				ErrorTransactionConflict,
				left,
				right,
				"both units have retained start jobs but declare Conflicts=",
			)
		}

		loser, err := s.startConflictLoser(left, right)
		if err != nil {
			return err
		}
		if err := s.removeJob(loser, "lost Conflicts= arbitration"); err != nil {
			return err
		}
	}
}

func (s *solver) firstStartConflict() (string, string, bool) {
	for _, unit := range s.jobNamesSorted() {
		if !hasStartJob(s.jobs, unit) {
			continue
		}
		for _, other := range s.conflicts[unit] {
			if other <= unit {
				continue
			}
			if hasStartJob(s.jobs, other) {
				return unit, other, true
			}
		}
	}
	return "", "", false
}

func (s *solver) startConflictLoser(left, right string) (string, error) {
	a := s.jobs[left]
	b := s.jobs[right]
	if a == nil || b == nil {
		return "", transactionError(ErrorUnsatisfiedConflict, left, right, "conflicting job disappeared")
	}

	if a.job.Anchor && b.job.Anchor {
		return "", transactionError(
			ErrorTransactionConflict,
			left,
			right,
			"both conflicting start jobs are anchors",
		)
	}
	if a.job.Anchor != b.job.Anchor {
		if a.job.Anchor {
			return right, nil
		}
		return left, nil
	}

	if a.job.Irreversible && b.job.Irreversible {
		return "", transactionError(
			ErrorTransactionConflict,
			left,
			right,
			"both conflicting start jobs are irreversible",
		)
	}
	if a.job.Irreversible != b.job.Irreversible {
		if a.job.Irreversible {
			return right, nil
		}
		return left, nil
	}

	if a.job.Core && b.job.Core {
		return "", transactionError(
			ErrorTransactionConflict,
			left,
			right,
			"both conflicting start jobs are irreducible core requirements",
		)
	}
	if a.job.Core != b.job.Core {
		if a.job.Core {
			return right, nil
		}
		return left, nil
	}

	// Both sides are removable non-core dependencies. Use the unit name for a
	// stable, input-order-independent choice.
	if left > right {
		return left, nil
	}
	return right, nil
}

func (s *solver) pruneOrphans() {
	for {
		changed := false
		for _, name := range s.jobNamesSorted() {
			state := s.jobs[name]
			if state == nil || state.job.Anchor || state.originsPinned || len(state.origins) == 0 {
				continue
			}

			needed := false
			for origin := range state.origins {
				parent := s.jobs[origin]
				if parent == nil {
					continue
				}
				if isStartish(state.job.Type) {
					_, hardNeed := parent.hardNeeds[name]
					_, softNeed := parent.softNeeds[name]
					if isStartish(parent.job.Type) && (hardNeed || softNeed) {
						needed = true
					}
					// Restart propagates through PartOf. Keep that propagated
					// restart job even though PartOf is not a start requirement.
					if parent.job.Type == JobRestart && state.job.Type == JobRestart {
						needed = true
					}
				} else if isStartish(parent.job.Type) || isStopish(parent.job.Type) {
					needed = true
				}
				if needed {
					break
				}
			}
			if needed {
				continue
			}

			delete(s.jobs, name)
			s.recordRemoved(state.job)
			changed = true
		}
		if !changed {
			return
		}
	}
}

func (s *solver) finalJobs() []Job {
	jobs := make([]Job, 0, len(s.jobs))
	for _, name := range s.jobNamesSorted() {
		state := s.jobs[name]
		if state == nil {
			continue
		}
		job := state.job
		job.Reasons = joinReasons(state.reasons)
		if job.Reason == "" {
			job.Reason = trimReasonList(job.Reasons)
		}
		job.Origins = make([]string, 0, len(state.origins))
		for origin := range state.origins {
			job.Origins = append(job.Origins, origin)
		}
		sort.Strings(job.Origins)
		if s.mode == ModeReplaceIrreversibly {
			job.Irreversible = true
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func (s *solver) sortedDependencies() []Dependency {
	deps := append([]Dependency(nil), s.dependencies...)
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].From != deps[j].From {
			return deps[i].From < deps[j].From
		}
		if deps[i].To != deps[j].To {
			return deps[i].To < deps[j].To
		}
		return deps[i].Kind < deps[j].Kind
	})
	return deps
}

func (s *solver) sortedRemoved() []Job {
	jobs := append([]Job(nil), s.removed...)
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Unit != jobs[j].Unit {
			return jobs[i].Unit < jobs[j].Unit
		}
		if jobs[i].Type != jobs[j].Type {
			return jobs[i].Type < jobs[j].Type
		}
		return jobs[i].Reason < jobs[j].Reason
	})
	return jobs
}

type edgeMode int

const (
	edgeModeCombined edgeMode = iota
	edgeModeStart
	edgeModeStop
)

func (s *solver) buildOrderEdges(jobs map[string]Job, mode edgeMode) []OrderEdge {
	edges := make([]OrderEdge, 0)
	for _, name := range sortedJobMapNames(jobs) {
		unit := s.graph.Units[name]
		if unit == nil {
			continue
		}
		job := jobs[name]
		for _, other := range sortedUnique(unit.After) {
			otherJob, ok := jobs[other]
			if !ok || other == name {
				continue
			}
			edge, ok := orderingEdge(job, otherJob, RelationAfter, name, other, mode)
			if ok {
				edges = append(edges, edge)
			}
		}
		for _, other := range sortedUnique(unit.Before) {
			otherJob, ok := jobs[other]
			if !ok || other == name {
				continue
			}
			edge, ok := orderingEdge(job, otherJob, RelationBefore, name, other, mode)
			if ok {
				edges = append(edges, edge)
			}
		}
	}
	return dedupeOrderEdges(edges)
}

func orderingEdge(job, other Job, relation OrderRelation, name, otherName string, mode edgeMode) (OrderEdge, bool) {
	switch mode {
	case edgeModeStart:
		if !isStartish(job.Type) || !isStartish(other.Type) {
			return OrderEdge{}, false
		}
		if relation == RelationAfter {
			return OrderEdge{
				From:     otherName,
				To:       name,
				Phase:    PhaseStart,
				Relation: relation,
				Source:   name + ".After=" + otherName,
			}, true
		}
		return OrderEdge{
			From:     name,
			To:       otherName,
			Phase:    PhaseStart,
			Relation: relation,
			Source:   name + ".Before=" + otherName,
		}, true

	case edgeModeStop:
		if !isStopish(job.Type) || !isStopish(other.Type) {
			return OrderEdge{}, false
		}
		if relation == RelationAfter {
			return OrderEdge{
				From:     name,
				To:       otherName,
				Phase:    PhaseStop,
				Relation: relation,
				Source:   name + ".After=" + otherName,
			}, true
		}
		return OrderEdge{
			From:     otherName,
			To:       name,
			Phase:    PhaseStop,
			Relation: relation,
			Source:   name + ".Before=" + otherName,
		}, true

	default:
		reverse := stopReverseOrder(job.Type, other.Type)
		phase := PhaseStart
		if reverse {
			phase = PhaseStop
		}
		if relation == RelationAfter {
			if reverse {
				return OrderEdge{
					From:     name,
					To:       otherName,
					Phase:    phase,
					Relation: relation,
					Source:   name + ".After=" + otherName,
				}, true
			}
			return OrderEdge{
				From:     otherName,
				To:       name,
				Phase:    phase,
				Relation: relation,
				Source:   name + ".After=" + otherName,
			}, true
		}
		if reverse {
			return OrderEdge{
				From:     otherName,
				To:       name,
				Phase:    phase,
				Relation: relation,
				Source:   name + ".Before=" + otherName,
			}, true
		}
		return OrderEdge{
			From:     name,
			To:       otherName,
			Phase:    phase,
			Relation: relation,
			Source:   name + ".Before=" + otherName,
		}, true
	}
}

func stopReverseOrder(a, b JobType) bool {
	if a == JobStop && b == JobStop {
		return true
	}
	if (a == JobStop && b == JobRestart) || (a == JobRestart && b == JobStop) {
		return true
	}
	return false
}

func dedupeOrderEdges(edges []OrderEdge) []OrderEdge {
	if len(edges) == 0 {
		return nil
	}
	byKey := make(map[string]OrderEdge, len(edges))
	for _, edge := range edges {
		key := edge.key()
		existing, ok := byKey[key]
		if !ok || edgeLess(edge, existing) {
			byKey[key] = edge
		}
	}
	out := make([]OrderEdge, 0, len(byKey))
	for _, edge := range byKey {
		out = append(out, edge)
	}
	sort.Slice(out, func(i, j int) bool { return edgeLess(out[i], out[j]) })
	return out
}

func edgeLess(a, b OrderEdge) bool {
	if a.From != b.From {
		return a.From < b.From
	}
	if a.To != b.To {
		return a.To < b.To
	}
	if a.Phase != b.Phase {
		return a.Phase < b.Phase
	}
	if a.Relation != b.Relation {
		return a.Relation < b.Relation
	}
	return a.Source < b.Source
}

func breakCycles(nodes []string, edges []OrderEdge) ([]OrderEdge, []Cycle, []OrderEdge) {
	nodes = sortedUnique(nodes)
	kept := dedupeOrderEdges(edges)
	var cycles []Cycle
	var cuts []OrderEdge

	for {
		adj := adjacencyFor(kept)
		cycleNodes := canonicalCycle(nodes, adj)
		if len(cycleNodes) == 0 {
			break
		}

		cycleEdges := make([]OrderEdge, 0, len(cycleNodes))
		for i, from := range cycleNodes {
			to := cycleNodes[(i+1)%len(cycleNodes)]
			edge, ok := firstEdgeBetween(kept, from, to)
			if !ok {
				// canonicalCycle only traverses existing edges. If an
				// invariant is ever violated, stop rather than looping or
				// panicking in a library call.
				return kept, cycles, dedupeOrderEdges(cuts)
			}
			cycleEdges = append(cycleEdges, edge)
		}

		cut := selectCycleCut(cycleEdges)
		var removed []OrderEdge
		next := make([]OrderEdge, 0, len(kept))
		for _, edge := range kept {
			if edge.From == cut.From && edge.To == cut.To && edge.Phase == cut.Phase {
				removed = append(removed, edge)
			} else {
				next = append(next, edge)
			}
		}
		if len(removed) == 0 {
			return kept, cycles, dedupeOrderEdges(cuts)
		}
		kept = next
		sort.Slice(removed, func(i, j int) bool { return edgeLess(removed[i], removed[j]) })
		cuts = append(cuts, removed...)
		cycles = append(cycles, Cycle{
			Phase:    cut.Phase,
			Nodes:    append([]string(nil), cycleNodes...),
			Edges:    cycleEdges,
			CutEdges: removed,
		})
	}

	return kept, cycles, dedupeOrderEdges(cuts)
}

func adjacencyFor(edges []OrderEdge) map[string][]string {
	sets := make(map[string]map[string]bool)
	for _, edge := range edges {
		if sets[edge.From] == nil {
			sets[edge.From] = make(map[string]bool)
		}
		sets[edge.From][edge.To] = true
	}
	adj := make(map[string][]string, len(sets))
	for from, targets := range sets {
		for to := range targets {
			adj[from] = append(adj[from], to)
		}
		sort.Strings(adj[from])
	}
	return adj
}

func canonicalCycle(nodes []string, adj map[string][]string) []string {
	var best []string
	for _, source := range nodes {
		candidate := shortestCycleThrough(source, adj)
		if len(candidate) == 0 {
			continue
		}
		if betterCycle(candidate, best) {
			best = candidate
		}
	}
	return best
}

func shortestCycleThrough(source string, adj map[string][]string) []string {
	dist := map[string]int{source: 0}
	parent := make(map[string]string)
	queue := []string{source}
	var best []string

	for head := 0; head < len(queue); head++ {
		current := queue[head]
		for _, next := range adj[current] {
			if next == source {
				if dist[current] == 0 {
					continue
				}
				path := reconstructPath(parent, source, current)
				if betterCycle(path, best) {
					best = append([]string(nil), path...)
				}
				continue
			}
			if _, seen := dist[next]; seen {
				continue
			}
			dist[next] = dist[current] + 1
			parent[next] = current
			queue = append(queue, next)
		}
	}
	return best
}

func reconstructPath(parent map[string]string, source, end string) []string {
	reversed := make([]string, 0)
	for current := end; current != source; current = parent[current] {
		reversed = append(reversed, current)
		if _, ok := parent[current]; !ok {
			return nil
		}
	}
	reversed = append(reversed, source)
	out := make([]string, len(reversed))
	for i := range reversed {
		out[i] = reversed[len(reversed)-1-i]
	}
	return out
}

func betterCycle(candidate, current []string) bool {
	if len(candidate) == 0 {
		return false
	}
	if len(current) == 0 || len(candidate) < len(current) {
		return true
	}
	if len(candidate) > len(current) {
		return false
	}
	for i := range candidate {
		if candidate[i] != current[i] {
			return candidate[i] < current[i]
		}
	}
	return false
}

func firstEdgeBetween(edges []OrderEdge, from, to string) (OrderEdge, bool) {
	for _, edge := range edges {
		if edge.From == from && edge.To == to {
			return edge, true
		}
	}
	return OrderEdge{}, false
}

func selectCycleCut(edges []OrderEdge) OrderEdge {
	cut := edges[0]
	for _, edge := range edges[1:] {
		if edgeLess(cut, edge) {
			cut = edge
		}
	}
	return cut
}

func topologicalOrder(nodes []string, edges []OrderEdge) []string {
	nodes = sortedUnique(nodes)
	allowed := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		allowed[node] = true
	}

	adjSets := make(map[string]map[string]bool)
	indegree := make(map[string]int, len(nodes))
	for _, node := range nodes {
		indegree[node] = 0
	}
	for _, edge := range edges {
		if !allowed[edge.From] || !allowed[edge.To] {
			continue
		}
		if adjSets[edge.From] == nil {
			adjSets[edge.From] = make(map[string]bool)
		}
		if adjSets[edge.From][edge.To] {
			continue
		}
		adjSets[edge.From][edge.To] = true
		indegree[edge.To]++
	}

	available := make([]string, 0)
	for _, node := range nodes {
		if indegree[node] == 0 {
			available = append(available, node)
		}
	}
	sort.Strings(available)

	out := make([]string, 0, len(nodes))
	for len(available) > 0 {
		current := available[0]
		available = available[1:]
		out = append(out, current)

		nexts := make([]string, 0, len(adjSets[current]))
		for next := range adjSets[current] {
			nexts = append(nexts, next)
		}
		sort.Strings(nexts)
		for _, next := range nexts {
			indegree[next]--
			if indegree[next] == 0 {
				index := sort.SearchStrings(available, next)
				available = append(available, "")
				copy(available[index+1:], available[index:])
				available[index] = next
			}
		}
	}

	// Cycle breaking should guarantee this, but never silently drop jobs if a
	// future edge source violates that invariant.
	if len(out) != len(nodes) {
		seen := make(map[string]bool, len(out))
		for _, node := range out {
			seen[node] = true
		}
		for _, node := range nodes {
			if !seen[node] {
				out = append(out, node)
			}
		}
	}
	return out
}

func buildExecutionSteps(stopOrder, startOrder []string) []ExecutionStep {
	steps := make([]ExecutionStep, 0, len(stopOrder)+len(startOrder))
	for _, name := range stopOrder {
		steps = append(steps, ExecutionStep{Unit: name, Operation: JobStop, Phase: PhaseStop})
	}
	for _, name := range startOrder {
		steps = append(steps, ExecutionStep{Unit: name, Operation: JobStart, Phase: PhaseStart})
	}
	return steps
}

func jobNamesFromJobs(jobs []Job) []string {
	names := make([]string, 0, len(jobs))
	for _, job := range jobs {
		names = append(names, job.Unit)
	}
	return names
}

func jobNamesForPhase(jobs []Job, phase JobPhase) []string {
	names := make([]string, 0, len(jobs))
	for _, job := range jobs {
		switch phase {
		case PhaseStart:
			if isStartish(job.Type) {
				names = append(names, job.Unit)
			}
		case PhaseStop:
			if isStopish(job.Type) {
				names = append(names, job.Unit)
			}
		}
	}
	return names
}

func sortedJobMapNames(jobs map[string]Job) []string {
	names := make([]string, 0, len(jobs))
	for name := range jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func dedupeCycles(cycles []Cycle) []Cycle {
	if len(cycles) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	out := make([]Cycle, 0, len(cycles))
	for _, cycle := range cycles {
		key := cycleKey(cycle)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cycle)
	}
	return out
}

func cycleKey(cycle Cycle) string {
	return string(cycle.Phase) + "\x00" + strings.Join(cycle.Nodes, "\x00")
}
