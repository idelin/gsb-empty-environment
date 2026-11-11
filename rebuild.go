package netsec

import "sort"

// RebuildReport is the result of a full rebuild cross-check. The derived
// engine state is never replaced: rebuilt ground facts are compared against
// the incrementally maintained indexes and every disagreement is listed.
type RebuildReport struct {
	// Consistent is true when rebuilt and incremental derived state match.
	Consistent bool
	// Cycles lists every reference cycle in the rule graph, each entry as a
	// closed group path whose first and last elements are equal.
	Cycles [][]GroupID
	// EmptyGroups lists known groups that currently have no members.
	EmptyGroups []GroupID
	// SharedAddresses lists addresses that belong to more than one group.
	SharedAddresses []Address
	// Mismatches describes differences between rebuild and incremental state.
	Mismatches []string
}

// CheckRebuild recomputes every derived relation from the committed ground
// state and compares it with the incrementally maintained indexes. It also
// audits cyclic group references, empty groups and addresses shared across
// groups. Queries are blocked while the check runs, but no state is mutated.
func (e *Engine) CheckRebuild() RebuildReport {
	e.mu.RLock()
	defer e.mu.RUnlock()

	report := RebuildReport{
		Cycles:          e.referenceCyclesLocked(),
		EmptyGroups:     e.emptyGroupsLocked(),
		SharedAddresses: e.sharedAddressesLocked(),
		Mismatches:      e.rebuildMismatchesLocked(),
	}
	report.Consistent = len(report.Mismatches) == 0
	return report
}

// ReferenceCycles returns every distinct group reference cycle produced by
// rules, including self loops. Each cycle is a closed path of group ids whose
// last element repeats the first.
func (e *Engine) ReferenceCycles() [][]GroupID {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.referenceCyclesLocked()
}

// rebuildMismatchesLocked independently re-derives all indexes from ground
// facts and diffs them against the live incremental structures.
func (e *Engine) rebuildMismatchesLocked() []string {
	var mismatches []string

	ruleCount := make(map[RuleID]int)
	for id, rule := range e.rules {
		if got := e.srcRules[rule.SourceGroup][id]; got != rule {
			mismatches = append(mismatches, "source-group index missing rule "+string(id))
		}
		ruleCount[id]++
	}

	indexedRules := make(map[RuleID]int)
	for _, set := range e.srcRules {
		for id, rule := range set {
			committed, ok := e.rules[id]
			if !ok || committed != rule {
				mismatches = append(mismatches, "source-group index has stale rule "+string(id))
			}
			indexedRules[id]++
		}
	}
	for id, count := range ruleCount {
		if indexedRules[id] != count {
			mismatches = append(mismatches, "source-group index count mismatch for rule "+string(id))
		}
	}
	for id := range indexedRules {
		if _, ok := ruleCount[id]; !ok {
			mismatches = append(mismatches, "source-group index has unknown rule "+string(id))
		}
	}

	wantRuleEv := make(map[RuleID]map[AllowKey]Evidence)
	wantAllows := make(map[AllowKey]map[RuleID]Evidence)
	for id, rule := range e.rules {
		ev := e.ruleEvidence(rule)
		if len(ev) > 0 {
			wantRuleEv[id] = ev
			for key, item := range ev {
				set := wantAllows[key]
				if set == nil {
					set = make(map[RuleID]Evidence)
					wantAllows[key] = set
				}
				set[id] = item
			}
		}
	}

	if len(wantRuleEv) != len(e.ruleEv) {
		mismatches = append(mismatches, "per-rule evidence size differs")
	}
	for id, want := range wantRuleEv {
		got := e.ruleEv[id]
		if !sameEvidenceSet(want, got) {
			mismatches = append(mismatches, "per-rule evidence differs for rule "+string(id))
		}
	}
	for id, got := range e.ruleEv {
		if _, ok := wantRuleEv[id]; !ok {
			mismatches = append(mismatches, "stale per-rule evidence for rule "+string(id))
		}
		_ = got
	}

	if len(wantAllows) != len(e.allows) {
		mismatches = append(mismatches, "allow index size differs")
	}
	for key, want := range wantAllows {
		got := e.allows[key]
		if !sameEvidenceById(want, got) {
			mismatches = append(mismatches, "allow evidence differs for an allow key")
		}
	}
	for key, got := range e.allows {
		if _, ok := wantAllows[key]; !ok {
			mismatches = append(mismatches, "stale allow key in incremental index")
		}
		_ = got
	}

	sort.Strings(mismatches)
	return mismatches
}

func sameEvidenceSet(want, got map[AllowKey]Evidence) bool {
	if len(want) != len(got) {
		return false
	}
	for key, wantEv := range want {
		gotEv, ok := got[key]
		if !ok || gotEv != wantEv {
			return false
		}
	}
	return true
}

func sameEvidenceById(want, got map[RuleID]Evidence) bool {
	if len(want) != len(got) {
		return false
	}
	for id, wantEv := range want {
		gotEv, ok := got[id]
		if !ok || gotEv != wantEv {
			return false
		}
	}
	return true
}

func (e *Engine) emptyGroupsLocked() []GroupID {
	var out []GroupID
	for group := range e.known {
		if len(e.members[group]) == 0 {
			out = append(out, group)
		}
	}
	sortGroupIDs(out)
	return out
}

func (e *Engine) sharedAddressesLocked() []Address {
	groupsPerAddr := make(map[Address]map[GroupID]struct{})
	for group, set := range e.members {
		for addr := range set {
			groups := groupsPerAddr[addr]
			if groups == nil {
				groups = make(map[GroupID]struct{})
				groupsPerAddr[addr] = groups
			}
			groups[group] = struct{}{}
		}
	}
	var out []Address
	for addr, groups := range groupsPerAddr {
		if len(groups) > 1 {
			out = append(out, addr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// referenceCyclesLocked runs an iterative DFS over the rule reference graph
// whose edge group -> source group means "a rule attached to this group
// references that source group". Back edges are recorded as closed paths and
// canonicalized by rotating each path to start at its smallest group id.
func (e *Engine) referenceCyclesLocked() [][]GroupID {
	edges := make(map[GroupID]map[GroupID]struct{})
	for _, rule := range e.rules {
		set := edges[rule.TargetGroup]
		if set == nil {
			set = make(map[GroupID]struct{})
			edges[rule.TargetGroup] = set
		}
		set[rule.SourceGroup] = struct{}{}
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[GroupID]int)
	seen := make(map[string]struct{})
	var cycles [][]GroupID

	roots := make([]GroupID, 0, len(edges))
	for group := range edges {
		roots = append(roots, group)
	}
	sortGroupIDs(roots)

	for _, root := range roots {
		if color[root] != white {
			continue
		}
		type frame struct {
			group GroupID
			next  []GroupID
			idx   int
		}
		stack := []frame{{group: root, next: sortedNeighbors(edges, root)}}
		color[root] = gray

		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.idx >= len(top.next) {
				color[top.group] = black
				stack = stack[:len(stack)-1]
				continue
			}
			neighbor := top.next[top.idx]
			top.idx++

			switch color[neighbor] {
			case white:
				color[neighbor] = gray
				stack = append(stack, frame{group: neighbor, next: sortedNeighbors(edges, neighbor)})
			case gray:
				// Close the back edge: neighbor (already on the stack),
				// every later frame including the edge source, then neighbor
				// again. A self loop therefore yields [g, g].
				path := make([]GroupID, 0, len(stack)+1)
				path = append(path, neighbor)
				if neighbor == top.group {
					path = append(path, neighbor)
				} else {
					for i := 0; i < len(stack); i++ {
						if stack[i].group == neighbor {
							for j := i + 1; j < len(stack); j++ {
								path = append(path, stack[j].group)
							}
							break
						}
					}
					path = append(path, neighbor)
				}
				path = canonicalCycle(path)
				key := cycleKey(path)
				if _, ok := seen[key]; !ok {
					seen[key] = struct{}{}
					cycles = append(cycles, path)
				}
			}
		}
	}

	sort.Slice(cycles, func(i, j int) bool { return cycleKey(cycles[i]) < cycleKey(cycles[j]) })
	return cycles
}

func sortedNeighbors(edges map[GroupID]map[GroupID]struct{}, group GroupID) []GroupID {
	var out []GroupID
	for neighbor := range edges[group] {
		out = append(out, neighbor)
	}
	sortGroupIDs(out)
	return out
}

// canonicalCycle normalizes a closed path g0..gk (g0 == gk) by dropping the
// repeated start, rotating so the smallest group id leads, and choosing the
// lexicographically smaller of the two traversal directions. The returned
// path is closed again by repeating the leading group.
func canonicalCycle(path []GroupID) []GroupID {
	loop := path[:len(path)-1]
	rotations := [][]GroupID{rotateMin(loop), rotateMin(reversed(loop))}
	best := rotations[0]
	if groupPathLess(rotations[1], best) {
		best = rotations[1]
	}
	return append(best, best[0])
}

func rotateMin(loop []GroupID) []GroupID {
	minIdx := 0
	for i := 1; i < len(loop); i++ {
		if loop[i] < loop[minIdx] {
			minIdx = i
		}
	}
	out := make([]GroupID, len(loop))
	for i := range loop {
		out[i] = loop[(minIdx+i)%len(loop)]
	}
	return out
}

func reversed(in []GroupID) []GroupID {
	out := make([]GroupID, len(in))
	for i := range in {
		out[i] = in[len(in)-1-i]
	}
	return out
}

func groupPathLess(a, b []GroupID) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func cycleKey(path []GroupID) string {
	key := ""
	for _, group := range path {
		key += string(group) + "\x00"
	}
	return key
}

func sortGroupIDs(groups []GroupID) {
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
}
