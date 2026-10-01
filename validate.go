package main

import (
	"fmt"
	"sort"
	"strings"
)

const eps = 1e-9

type direction int

const (
	dirFwd direction = iota
	dirRev
)

// usageKey identifies one capacity budget. Shared-mode links collapse both
// directions into a single key; separate-mode links keep one key per direction.
func usageKey(l *Link, d direction) string {
	switch l.Mode {
	case ModeShared:
		return l.ID + "|shared"
	case ModeSeparate:
		if d == dirRev {
			return l.ID + "|rev"
		}
		return l.ID + "|fwd"
	default:
		return l.ID + "|fwd"
	}
}

func usageLabel(l *Link, d direction) string {
	switch l.Mode {
	case ModeShared:
		return fmt.Sprintf("%s [%s<->%s shared]", l.ID, l.From, l.To)
	case ModeSeparate:
		if d == dirRev {
			return fmt.Sprintf("%s [%s->%s separate]", l.ID, l.To, l.From)
		}
		return fmt.Sprintf("%s [%s->%s separate]", l.ID, l.From, l.To)
	default:
		return fmt.Sprintf("%s [%s->%s]", l.ID, l.From, l.To)
	}
}

type hopRef struct {
	link *Link
	dir  direction
}

type resolvedFlow struct {
	flow        *Flow
	hops        []hopRef
	unreachable bool
	reason      string
}

// topology indexes links by node pair and adjacency, excluding failed links.
type topology struct {
	byPair map[string]*Link
	adj    map[string][]*Link
}

func pairKey(a, b string) string { return a + "\x00" + b }

func buildTopology(sc *Scenario, failed map[string]bool) *topology {
	t := &topology{byPair: map[string]*Link{}, adj: map[string][]*Link{}}
	for i := range sc.Links {
		l := &sc.Links[i]
		if failed[l.ID] {
			continue
		}
		t.byPair[pairKey(l.From, l.To)] = l
		t.adj[l.From] = append(t.adj[l.From], l)
		if l.Mode != ModeUnidirectional {
			t.byPair[pairKey(l.To, l.From)] = l
			t.adj[l.To] = append(t.adj[l.To], l)
		}
	}
	return t
}

// resolveFlows maps every flow's fixed route onto links. A flow whose route
// needs a missing or failed link is unreachable; it is never treated as a
// zero-load success.
func resolveFlows(sc *Scenario, t *topology) []resolvedFlow {
	out := make([]resolvedFlow, 0, len(sc.Flows))
	for i := range sc.Flows {
		f := &sc.Flows[i]
		rf := resolvedFlow{flow: f}
		for h := 0; h+1 < len(f.Route); h++ {
			a, b := f.Route[h], f.Route[h+1]
			l, ok := t.byPair[pairKey(a, b)]
			if !ok {
				rf.unreachable = true
				rf.reason = fmt.Sprintf("no usable link for hop %s->%s", a, b)
				break
			}
			d := dirFwd
			if a == l.To && b == l.From {
				d = dirRev
			}
			rf.hops = append(rf.hops, hopRef{link: l, dir: d})
		}
		out = append(out, rf)
	}
	return out
}

// usageLoad aggregates all flows traversing one capacity budget.
type usageLoad struct {
	key      string
	link     *Link
	dir      direction
	capacity float64
	load     float64
	flows    []int // indices into []resolvedFlow
}

func (u *usageLoad) overloaded() bool { return u.load > u.capacity+eps }

func computeLoads(resolved []resolvedFlow) map[string]*usageLoad {
	loads := map[string]*usageLoad{}
	for i, rf := range resolved {
		if rf.unreachable {
			continue
		}
		for _, h := range rf.hops {
			k := usageKey(h.link, h.dir)
			u, ok := loads[k]
			if !ok {
				u = &usageLoad{key: k, link: h.link, dir: h.dir, capacity: h.link.Capacity}
				loads[k] = u
			}
			u.load += rf.flow.Demand
			u.flows = append(u.flows, i)
		}
	}
	return loads
}

// minimalRemovalSet finds the smallest set of flows (by count, then by total
// removed demand) whose removal from the link brings load back within
// capacity. This is the minimal flow set responsible for the overload.
func minimalRemovalSet(resolved []resolvedFlow, u *usageLoad) []string {
	excess := u.load - u.capacity
	if excess <= eps {
		return nil
	}
	n := len(u.flows)
	bestSize := n + 1
	bestSum := 0.0
	var best []string
	// Enumerate subsets in increasing size; n per link is small in practice.
	var walk func(start, size int, sum float64, chosen []string)
	walk = func(start, size int, sum float64, chosen []string) {
		if size > 0 && sum+eps >= excess {
			if size < bestSize || (size == bestSize && sum < bestSum-eps) {
				bestSize = size
				bestSum = sum
				best = append([]string(nil), chosen...)
			}
			return
		}
		if size+1 > bestSize || size+1 > n {
			return
		}
		for j := start; j < n; j++ {
			f := resolved[u.flows[j]].flow
			walk(j+1, size+1, sum+f.Demand, append(chosen, f.ID))
		}
	}
	walk(0, 0, 0, nil)
	sort.Strings(best)
	return best
}

// findAlternatePath BFS-searches the baseline topology for a src->dst path
// that avoids all links of the flow's primary route (link-disjoint backup).
func findAlternatePath(t *topology, src, dst string, exclude map[string]bool) []string {
	if src == dst {
		return nil
	}
	prev := map[string]string{src: ""} // node -> node it was reached from
	queue := []string{src}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, l := range t.adj[n] {
			if exclude[l.ID] {
				continue
			}
			next := l.To
			if l.To == n {
				next = l.From
			}
			if _, seen := prev[next]; seen {
				continue
			}
			prev[next] = n
			if next == dst {
				path := []string{dst}
				for cur := dst; cur != src; {
					cur = prev[cur]
					path = append([]string{cur}, path...)
				}
				return path
			}
			queue = append(queue, next)
		}
	}
	return nil
}

// scenarioReport holds everything computed for one failure scenario.
type scenarioReport struct {
	name       string
	resolved   []resolvedFlow
	loads      map[string]*usageLoad
	removals   map[string][]string // usage key -> minimal removal set
	redundancy map[string]string   // flow id -> human readable status (baseline only)
	sim        *simResult
}

func analyzeScenario(sc *Scenario, name string, failed map[string]bool, ticks int, baselineTopo *topology) *scenarioReport {
	t := buildTopology(sc, failed)
	resolved := resolveFlows(sc, t)
	loads := computeLoads(resolved)
	rep := &scenarioReport{
		name:       name,
		resolved:   resolved,
		loads:      loads,
		removals:   map[string][]string{},
		redundancy: map[string]string{},
	}
	for k, u := range loads {
		if u.overloaded() {
			rep.removals[k] = minimalRemovalSet(resolved, u)
		}
	}
	if baselineTopo != nil {
		for _, rf := range resolved {
			primary := map[string]bool{}
			for _, h := range rf.hops {
				primary[h.link.ID] = true
			}
			if rf.unreachable {
				rep.redundancy[rf.flow.ID] = "n/a (flow unreachable in this scenario)"
				continue
			}
			alt := findAlternatePath(baselineTopo, rf.flow.Src, rf.flow.Dst, primary)
			if alt == nil {
				rep.redundancy[rf.flow.ID] = "NO link-disjoint backup path"
			} else {
				rep.redundancy[rf.flow.ID] = "backup available: " + strings.Join(alt, "->")
			}
		}
	}
	if ticks > 0 {
		rep.sim = simulate(resolved, ticks)
	}
	return rep
}

func sortedUsageKeys(m map[string]*usageLoad) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func renderReport(rep *scenarioReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== Scenario: %s ===\n", rep.name)

	// Unreachable flows first: they are failures, not zero-load successes.
	unreachable := 0
	for _, rf := range rep.resolved {
		if rf.unreachable {
			fmt.Fprintf(&b, "  UNREACHABLE flow %s (%s->%s demand=%.2f): %s\n",
				rf.flow.ID, rf.flow.Src, rf.flow.Dst, rf.flow.Demand, rf.reason)
			unreachable++
		}
	}
	if unreachable == 0 {
		b.WriteString("  Unreachable flows: none\n")
	}

	fmt.Fprintf(&b, "  Link loads (%d capacity budgets):\n", len(rep.loads))
	for _, k := range sortedUsageKeys(rep.loads) {
		u := rep.loads[k]
		status := "ok"
		if u.overloaded() {
			status = fmt.Sprintf("OVERLOAD excess=%.2f", u.load-u.capacity)
		}
		fmt.Fprintf(&b, "    %-28s cap=%6.2f load=%6.2f  %s\n",
			usageLabel(u.link, u.dir), u.capacity, u.load, status)
		parts := make([]string, 0, len(u.flows))
		for _, fi := range u.flows {
			f := rep.resolved[fi].flow
			parts = append(parts, fmt.Sprintf("%s(%.2f)", f.ID, f.Demand))
		}
		fmt.Fprintf(&b, "      flows: %s\n", strings.Join(parts, " "))
		if rm, ok := rep.removals[k]; ok {
			fmt.Fprintf(&b, "      minimal overload flow set (remove to fix): {%s}\n",
				strings.Join(rm, ", "))
		}
	}

	if len(rep.redundancy) > 0 {
		b.WriteString("  Redundancy (link-disjoint backup path):\n")
		ids := make([]string, 0, len(rep.redundancy))
		for id := range rep.redundancy {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(&b, "    %s: %s\n", id, rep.redundancy[id])
		}
	}

	if rep.sim != nil {
		b.WriteString(renderSim(rep.sim, rep.resolved))
	}
	return b.String()
}

// Validate runs the baseline plus every failure scenario and renders a report.
func Validate(sc *Scenario) string {
	baselineTopo := buildTopology(sc, nil)
	var b strings.Builder
	b.WriteString(renderReport(analyzeScenario(sc, "baseline", nil, sc.SimTicks, baselineTopo)))
	for _, fl := range sc.Failures {
		failed := map[string]bool{}
		for _, id := range fl.Links {
			failed[id] = true
		}
		name := "failure: " + fl.Name + " (links down: " + strings.Join(fl.Links, ", ") + ")"
		// Redundancy is a baseline-topology property; only computed once above.
		b.WriteString(renderReport(analyzeScenario(sc, name, failed, sc.SimTicks, nil)))
	}
	return b.String()
}
