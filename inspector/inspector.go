// Package inspector is an offline integrity-inspection and invariant
// assertion engine. It takes a snapshot of decoded records (a graph of
// IDs, references and weights) and formally checks structural
// constraints: dangling references, orphan cycles, unreachable nodes,
// duplicate IDs, self loops, negative weights and data skew.
//
// Inspect is a pure function over its input snapshot and is safe for
// concurrent use; it allocates no shared mutable state.
package inspector

import (
	"fmt"
	"sort"
)

// Kind classifies a detected integrity violation.
type Kind string

const (
	KindDanglingRef    Kind = "dangling_reference"
	KindOrphanCycle    Kind = "orphan_cycle"
	KindReachableCycle Kind = "reachable_cycle"
	KindUnreachable    Kind = "unreachable_node"
	KindDuplicateID    Kind = "duplicate_id"
	KindSelfLoop       Kind = "self_loop"
	KindNegativeWeight Kind = "negative_weight"
	KindDataSkew       Kind = "data_skew"
)

// Severity grades an issue.
type Severity string

const (
	Fatal Severity = "FATAL"
	Warn  Severity = "WARN"
)

// Issue is a single detected violation.
type Issue struct {
	Kind     Kind
	Severity Severity
	Detail   string
}

func (i Issue) String() string {
	return fmt.Sprintf("[%s] %s: %s", i.Severity, i.Kind, i.Detail)
}

// Record is one node of the inspected snapshot graph.
type Record struct {
	ID     uint64
	Refs   []uint64 // outgoing references to other record IDs
	Weight int64    // payload size / load metric used for skew analysis
	Root   bool     // entry point for reachability analysis
}

// Options tunes the inspection thresholds.
type Options struct {
	// SkewThreshold is the max/mean weight ratio above which data skew is
	// reported. Zero selects the default of 3.0.
	SkewThreshold float64
}

// DefaultSkewThreshold applies when Options.SkewThreshold is zero.
const DefaultSkewThreshold = 3.0

// Report aggregates the inspection outcome.
type Report struct {
	Issues       []Issue
	Nodes        int
	Edges        int
	Reachable    int
	CyclicGroups int
	SkewRatio    float64 // max/mean weight, 0 when no positive weights
}

// OK reports whether no FATAL issue was found.
func (r *Report) OK() bool {
	for _, i := range r.Issues {
		if i.Severity == Fatal {
			return false
		}
	}
	return true
}

// IssuesOf filters issues by kind.
func (r *Report) IssuesOf(k Kind) []Issue {
	var out []Issue
	for _, i := range r.Issues {
		if i.Kind == k {
			out = append(out, i)
		}
	}
	return out
}

// Inspect runs the full formal constraint sweep over the snapshot.
func Inspect(records []Record, opts Options) *Report {
	threshold := opts.SkewThreshold
	if threshold <= 0 {
		threshold = DefaultSkewThreshold
	}
	rep := &Report{Nodes: len(records)}

	index := make(map[uint64]int, len(records))
	for i, r := range records {
		if _, dup := index[r.ID]; dup {
			rep.Issues = append(rep.Issues, Issue{KindDuplicateID, Fatal,
				fmt.Sprintf("record id %d defined more than once", r.ID)})
			continue
		}
		index[r.ID] = i
		if r.Weight < 0 {
			rep.Issues = append(rep.Issues, Issue{KindNegativeWeight, Fatal,
				fmt.Sprintf("record %d has negative weight %d", r.ID, r.Weight)})
		}
	}

	// Adjacency + dangling reference detection.
	adj := make([][]int, len(records))
	for i, r := range records {
		for _, ref := range r.Refs {
			rep.Edges++
			if ref == r.ID {
				rep.Issues = append(rep.Issues, Issue{KindSelfLoop, Warn,
					fmt.Sprintf("record %d references itself", r.ID)})
			}
			j, ok := index[ref]
			if !ok {
				rep.Issues = append(rep.Issues, Issue{KindDanglingRef, Fatal,
					fmt.Sprintf("record %d references missing record %d", r.ID, ref)})
				continue
			}
			adj[i] = append(adj[i], j)
		}
	}

	// Reachability from roots (iterative BFS, no recursion limits).
	reachable := make([]bool, len(records))
	var queue []int
	for i, r := range records {
		if r.Root && !reachable[i] {
			reachable[i] = true
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nxt := range adj[cur] {
			if !reachable[nxt] {
				reachable[nxt] = true
				queue = append(queue, nxt)
			}
		}
	}
	for i := range records {
		if reachable[i] {
			rep.Reachable++
		}
	}

	// Cycle detection via iterative Tarjan SCC.
	sccs := tarjanSCC(adj)
	for _, scc := range sccs {
		cyclic := len(scc) > 1
		if !cyclic { // possible self loop
			n := scc[0]
			for _, m := range adj[n] {
				if m == n {
					cyclic = true
					break
				}
			}
		}
		if !cyclic {
			continue
		}
		rep.CyclicGroups++
		orphan := true
		for _, n := range scc {
			if reachable[n] {
				orphan = false
				break
			}
		}
		ids := make([]uint64, len(scc))
		for k, n := range scc {
			ids[k] = records[n].ID
		}
		sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
		if orphan {
			rep.Issues = append(rep.Issues, Issue{KindOrphanCycle, Fatal,
				fmt.Sprintf("orphan cycle unreachable from any root: %v", ids)})
		} else {
			rep.Issues = append(rep.Issues, Issue{KindReachableCycle, Warn,
				fmt.Sprintf("cycle reachable from root: %v", ids)})
		}
	}

	// Unreachable non-cyclic nodes (informational).
	for i, r := range records {
		if !reachable[i] && !inAnyCycle(i, sccs, adj) && len(records) > 0 && hasRoot(records) {
			rep.Issues = append(rep.Issues, Issue{KindUnreachable, Warn,
				fmt.Sprintf("record %d is unreachable from any root", r.ID)})
		}
	}

	// Data skew: max/mean weight ratio over positive weights.
	var total, maxW int64
	var positive int
	for _, r := range records {
		if r.Weight > 0 {
			total += r.Weight
			positive++
			if r.Weight > maxW {
				maxW = r.Weight
			}
		}
	}
	if positive > 0 && total > 0 {
		mean := float64(total) / float64(positive)
		rep.SkewRatio = float64(maxW) / mean
		if rep.SkewRatio > threshold {
			rep.Issues = append(rep.Issues, Issue{KindDataSkew, Warn,
				fmt.Sprintf("weight skew ratio %.2f exceeds threshold %.2f (max=%d mean=%.2f over %d nodes)",
					rep.SkewRatio, threshold, maxW, mean, positive)})
		}
	}
	return rep
}

func hasRoot(records []Record) bool {
	for _, r := range records {
		if r.Root {
			return true
		}
	}
	return false
}

func inAnyCycle(node int, sccs [][]int, adj [][]int) bool {
	for _, scc := range sccs {
		if len(scc) > 1 {
			for _, n := range scc {
				if n == node {
					return true
				}
			}
			continue
		}
		if scc[0] == node {
			for _, m := range adj[node] {
				if m == node {
					return true
				}
			}
		}
	}
	return false
}

// tarjanSCC computes strongly connected components iteratively (explicit
// stack, safe for very deep graphs).
func tarjanSCC(adj [][]int) [][]int {
	n := len(adj)
	indexOf := make([]int, n)
	lowlink := make([]int, n)
	onStack := make([]bool, n)
	for i := range indexOf {
		indexOf[i] = -1
	}
	var sccs [][]int
	var stack []int
	counter := 0

	type frame struct {
		node, child int
	}
	for start := 0; start < n; start++ {
		if indexOf[start] != -1 {
			continue
		}
		callStack := []frame{{node: start, child: 0}}
		for len(callStack) > 0 {
			top := &callStack[len(callStack)-1]
			if top.child == 0 {
				indexOf[top.node] = counter
				lowlink[top.node] = counter
				counter++
				stack = append(stack, top.node)
				onStack[top.node] = true
			}
			if top.child < len(adj[top.node]) {
				succ := adj[top.node][top.child]
				top.child++
				if indexOf[succ] == -1 {
					callStack = append(callStack, frame{node: succ, child: 0})
				} else if onStack[succ] && indexOf[succ] < lowlink[top.node] {
					lowlink[top.node] = indexOf[succ]
				}
				continue
			}
			// All successors processed: pop frame, propagate lowlink.
			callStack = callStack[:len(callStack)-1]
			if len(callStack) > 0 && lowlink[top.node] < lowlink[callStack[len(callStack)-1].node] {
				lowlink[callStack[len(callStack)-1].node] = lowlink[top.node]
			}
			if lowlink[top.node] == indexOf[top.node] {
				var scc []int
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[w] = false
					scc = append(scc, w)
					if w == top.node {
						break
					}
				}
				sccs = append(sccs, scc)
			}
		}
	}
	return sccs
}
