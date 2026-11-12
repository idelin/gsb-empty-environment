// Package netsec computes minimal program slices for vulnerability triage.
//
// A vulnerability report ships a recorded execution trace together with
// explicit data and control dependencies. The report usually contains many
// instructions that are irrelevant to the bug. This package finds the
// smallest set of trace nodes that must be kept so that replaying the
// pruned trace still reaches the same dangerous read of the same object at
// the same offset. Minimality is proven by exhaustively enumerating every
// candidate subset; the dependency closure is capped at MaxCandidates nodes
// so the exhaustive search stays feasible.
package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// MaxCandidates bounds the dependency closure handed to the exhaustive
// minimizer. With at most 20 candidate nodes the subset space is 2^20.
const MaxCandidates = 20

var (
	// ErrNoTarget is returned when the target event is out of range or is
	// not a dangerous read.
	ErrNoTarget = errors.New("netsec: target event is not a dangerous read")
	// ErrTooManyCandidates is returned when the dependency closure of the
	// target exceeds MaxCandidates nodes.
	ErrTooManyCandidates = errors.New("netsec: dependency closure exceeds candidate limit")
	// ErrNotReproducible is returned when no subset of the candidate
	// closure can replay the target dangerous read.
	ErrNotReproducible = errors.New("netsec: dangerous read is not reproducible from the given dependencies")
)

// NodeID identifies a program point that appears in the execution trace.
type NodeID int

// Deps lists the explicit dependencies of one node. Data are nodes whose
// produced values the node consumes; Control are nodes that decide whether
// the node executes.
type Deps struct {
	Data    []NodeID
	Control []NodeID
}

// Event is one recorded step of the execution trace. Dangerous marks a
// dangerous read (a crash candidate); Object and Offset describe what it
// reads and are only meaningful for dangerous events.
type Event struct {
	Node      NodeID
	Dangerous bool
	Object    string
	Offset    int64
}

// Trace is a recorded execution plus the explicit dependency facts.
type Trace struct {
	Events []Event
	Deps   map[NodeID]Deps
}

// Slice is the result of minimization: the smallest set of nodes that must
// be kept for the trace to replay the target dangerous read.
type Slice struct {
	// Target is the index of the dangerous read in Trace.Events.
	Target int
	// Nodes is the minimal kept set, sorted ascending. It always contains
	// the target event's node.
	Nodes []NodeID
}

// Size returns the number of kept nodes.
func (s *Slice) Size() int { return len(s.Nodes) }

// nodeSet returns the set of nodes that occur at least once in the trace.
func (t *Trace) nodeSet() map[NodeID]bool {
	present := make(map[NodeID]bool, len(t.Events))
	for _, ev := range t.Events {
		present[ev.Node] = true
	}
	return present
}

// Replay simulates the trace keeping only the given nodes and returns the
// indices of all dangerous events that fire, in trace order.
//
// An event fires when its node is kept and every dependency that itself
// appears in the trace has already fired. Dependencies on nodes absent from
// the trace are treated as external inputs and are always satisfied. A
// dropped node never fires, so everything transitively depending on it is
// dropped as well.
func (t *Trace) Replay(keep map[NodeID]bool) []int {
	present := t.nodeSet()
	executed := make(map[NodeID]bool, len(keep))
	var fired []int
	for i, ev := range t.Events {
		if !keep[ev.Node] {
			continue
		}
		if !depsSatisfied(t.Deps[ev.Node], present, executed) {
			continue
		}
		executed[ev.Node] = true
		if ev.Dangerous {
			fired = append(fired, i)
		}
	}
	return fired
}

func depsSatisfied(deps Deps, present, executed map[NodeID]bool) bool {
	for _, d := range deps.Data {
		if present[d] && !executed[d] {
			return false
		}
	}
	for _, d := range deps.Control {
		if present[d] && !executed[d] {
			return false
		}
	}
	return true
}

// ReplaysTarget reports whether the kept set replays the target dangerous
// read itself — the same event, hence the same object and offset. Any other
// crash firing is not accepted as a substitute for the original bug.
func (t *Trace) ReplaysTarget(keep map[NodeID]bool, target int) bool {
	for _, i := range t.Replay(keep) {
		if i == target {
			return true
		}
	}
	return false
}

// Candidates computes the shared dependency closure of the target event:
// the target node plus every node reachable through data and control
// dependencies, restricted to nodes that actually occur in the trace. The
// result is sorted and capped at MaxCandidates nodes.
func (t *Trace) Candidates(target int) ([]NodeID, error) {
	if target < 0 || target >= len(t.Events) || !t.Events[target].Dangerous {
		return nil, ErrNoTarget
	}
	present := t.nodeSet()
	seen := map[NodeID]bool{t.Events[target].Node: true}
	queue := []NodeID{t.Events[target].Node}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		deps := t.Deps[n]
		for _, d := range deps.Data {
			if present[d] && !seen[d] {
				seen[d] = true
				queue = append(queue, d)
			}
		}
		for _, d := range deps.Control {
			if present[d] && !seen[d] {
				seen[d] = true
				queue = append(queue, d)
			}
		}
	}
	if len(seen) > MaxCandidates {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooManyCandidates, len(seen), MaxCandidates)
	}
	cands := make([]NodeID, 0, len(seen))
	for n := range seen {
		cands = append(cands, n)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i] < cands[j] })
	return cands, nil
}

// Minimize finds the smallest subset of the candidate closure that still
// replays the target dangerous read. Subsets are enumerated exhaustively in
// increasing size order, so the first match is globally minimal: no smaller
// subset — in particular no proper subset of the result — can replay the
// target. Nodes outside the closure cannot influence the target event and
// are never part of a minimal slice.
func (t *Trace) Minimize(target int) (*Slice, error) {
	cands, err := t.Candidates(target)
	if err != nil {
		return nil, err
	}
	targetNode := t.Events[target].Node
	optional := make([]NodeID, 0, len(cands)-1)
	for _, n := range cands {
		if n != targetNode {
			optional = append(optional, n)
		}
	}
	for size := 0; size <= len(optional); size++ {
		var found []int
		combinations(len(optional), size, func(idxs []int) bool {
			keep := map[NodeID]bool{targetNode: true}
			for _, i := range idxs {
				keep[optional[i]] = true
			}
			if t.ReplaysTarget(keep, target) {
				found = append([]int(nil), idxs...)
				return false
			}
			return true
		})
		if found != nil {
			nodes := []NodeID{targetNode}
			for _, i := range found {
				nodes = append(nodes, optional[i])
			}
			sort.Slice(nodes, func(a, b int) bool { return nodes[a] < nodes[b] })
			return &Slice{Target: target, Nodes: nodes}, nil
		}
	}
	return nil, ErrNotReproducible
}

// Verify checks a computed slice: the kept nodes must replay the target
// dangerous read, and dropping any single kept node must break the replay.
// Because Minimize enumerates subsets in increasing size, a returned slice
// is already globally minimal; Verify provides a cheap independent check
// for callers that constructed or modified a slice by hand.
func (t *Trace) Verify(s *Slice) error {
	if s == nil || len(s.Nodes) == 0 {
		return errors.New("netsec: empty slice")
	}
	keep := make(map[NodeID]bool, len(s.Nodes))
	for _, n := range s.Nodes {
		keep[n] = true
	}
	if !t.ReplaysTarget(keep, s.Target) {
		return errors.New("netsec: slice does not replay the target dangerous read")
	}
	for _, n := range s.Nodes {
		pruned := make(map[NodeID]bool, len(keep)-1)
		for m := range keep {
			if m != n {
				pruned[m] = true
			}
		}
		if t.ReplaysTarget(pruned, s.Target) {
			return fmt.Errorf("netsec: node %d is redundant, slice is not minimal", n)
		}
	}
	return nil
}

// combinations calls fn for every size-k subset of {0..n-1}, in
// lexicographic order, until fn returns false.
func combinations(n, k int, fn func(idxs []int) bool) {
	if k < 0 || k > n {
		return
	}
	idxs := make([]int, k)
	for i := range idxs {
		idxs[i] = i
	}
	for {
		if !fn(idxs) {
			return
		}
		i := k - 1
		for i >= 0 && idxs[i] == n-k+i {
			i--
		}
		if i < 0 {
			return
		}
		idxs[i]++
		for j := i + 1; j < k; j++ {
			idxs[j] = idxs[j-1] + 1
		}
	}
}
