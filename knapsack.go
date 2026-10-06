// Package knapbb solves bounded integer knapsack problems with a
// deterministic branch-and-bound search that supports checkpoints.
//
// Node priority: higher upper bound first; ties broken by the
// lexicographically smaller path encoding. A checkpoint serializes the
// entire frontier plus the incumbent, so resuming explores exactly the
// same nodes in exactly the same order as an uninterrupted run.
package knapbb

import (
	"container/heap"
	"encoding/json"
	"io"
	"sort"
)

// Item is one bounded integer knapsack item: up to Bound copies may be
// taken, each contributing Value and consuming Weight of capacity.
type Item struct {
	Value  int `json:"value"`
	Weight int `json:"weight"`
	Bound  int `json:"bound"`
}

// Instance is a knapsack problem with items sorted by value/weight
// ratio descending (zero-weight positive-value items first).
type Instance struct {
	Items    []Item `json:"items"`
	Capacity int    `json:"capacity"`
}

// NewInstance returns an Instance with items sorted for the LP bound.
func NewInstance(items []Item, capacity int) Instance {
	sorted := make([]Item, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		return higherRatio(sorted[i], sorted[j])
	})
	return Instance{Items: sorted, Capacity: capacity}
}

func higherRatio(a, b Item) bool {
	infA := a.Weight == 0 && a.Value > 0
	infB := b.Weight == 0 && b.Value > 0
	if infA != infB {
		return infA
	}
	if a.Weight == 0 || b.Weight == 0 {
		return a.Value > b.Value
	}
	return a.Value*b.Weight > b.Value*a.Weight
}

// Node is one search-tree node. Path holds the chosen copy counts for
// items 0..len(Path)-1 and doubles as the node's priority tie-breaker.
type Node struct {
	Path   []int `json:"path"`
	Weight int   `json:"weight"`
	Value  int   `json:"value"`
	UB     int   `json:"ub"`
}

// upperBound computes the fractional-knapsack relaxation over items
// depth..n-1, floored to an integer (valid because values are integral).
func upperBound(inst *Instance, depth, weight, value int) int {
	rem := inst.Capacity - weight
	ub := value
	for i := depth; i < len(inst.Items) && rem >= 0; i++ {
		it := inst.Items[i]
		if it.Value <= 0 {
			continue
		}
		if it.Weight == 0 {
			ub += it.Bound * it.Value
			continue
		}
		take := rem / it.Weight
		if take > it.Bound {
			take = it.Bound
		}
		ub += take * it.Value
		rem -= take * it.Weight
		if take < it.Bound {
			ub += rem * it.Value / it.Weight
			break
		}
	}
	return ub
}

// comparePath orders path encodings lexicographically; a strict prefix
// sorts before its extension.
func comparePath(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// nodeHeap is a max-heap by (UB desc, path asc).
type nodeHeap []Node

func (h nodeHeap) Len() int { return len(h) }
func (h nodeHeap) Less(i, j int) bool {
	if h[i].UB != h[j].UB {
		return h[i].UB > h[j].UB
	}
	return comparePath(h[i].Path, h[j].Path) < 0
}
func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x interface{}) { *h = append(*h, x.(Node)) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// Solver is a resumable branch-and-bound search.
type Solver struct {
	inst      Instance
	frontier  nodeHeap
	bestValue int
	bestPath  []int
	explored  int

	// Trace, if set, is called with a copy of each popped node's path,
	// in pop order. Identical traces across runs prove identical search.
	Trace func(path []int)
}

// NewSolver creates a Solver holding only the root node.
func NewSolver(inst Instance) *Solver {
	s := &Solver{inst: inst, bestPath: make([]int, len(inst.Items))}
	root := Node{Path: []int{}}
	root.UB = upperBound(&s.inst, 0, 0, 0)
	s.frontier = nodeHeap{root}
	heap.Init(&s.frontier)
	return s
}

// Result reports the state after a Run call.
type Result struct {
	Done       bool  // frontier exhausted: BestValue is provably optimal
	Explored   int   // total nodes popped across all runs of this search
	BestValue  int   // incumbent value
	BestCounts []int // copy counts achieving BestValue
	LowerBound int   // best known achievable value (== BestValue)
	UpperBound int   // max frontier UB; equals LowerBound when Done
}

// Run pops at most budget nodes, then returns. When the frontier is
// empty the incumbent is optimal; otherwise LowerBound/UpperBound
// bracket the optimum.
func (s *Solver) Run(budget int) Result {
	for budget > 0 && len(s.frontier) > 0 {
		nd := heap.Pop(&s.frontier).(Node)
		budget--
		s.explored++
		if s.Trace != nil {
			p := make([]int, len(nd.Path))
			copy(p, nd.Path)
			s.Trace(p)
		}
		if nd.UB <= s.bestValue {
			continue // cannot beat the incumbent
		}
		depth := len(nd.Path)
		if depth == len(s.inst.Items) {
			s.bestValue = nd.Value
			s.bestPath = append([]int(nil), nd.Path...)
			continue
		}
		it := s.inst.Items[depth]
		for c := 0; c <= it.Bound; c++ {
			w := nd.Weight + c*it.Weight
			if w > s.inst.Capacity {
				break
			}
			child := Node{
				Path:   append(append([]int(nil), nd.Path...), c),
				Weight: w,
				Value:  nd.Value + c*it.Value,
			}
			child.UB = upperBound(&s.inst, depth+1, w, child.Value)
			if child.UB <= s.bestValue {
				continue
			}
			heap.Push(&s.frontier, child)
		}
	}
	res := Result{
		Done:       len(s.frontier) == 0,
		Explored:   s.explored,
		BestValue:  s.bestValue,
		BestCounts: append([]int(nil), s.bestPath...),
		LowerBound: s.bestValue,
		UpperBound: s.bestValue,
	}
	for _, nd := range s.frontier {
		if nd.UB > res.UpperBound {
			res.UpperBound = nd.UB
		}
	}
	return res
}

// Checkpoint is the serializable search state: the whole frontier plus
// the incumbent, so a resumed run can neither repeat nor skip a node.
type Checkpoint struct {
	Instance  Instance `json:"instance"`
	BestValue int      `json:"best_value"`
	BestPath  []int    `json:"best_path"`
	Explored  int      `json:"explored"`
	Frontier  []Node   `json:"frontier"`
}

// Save writes the complete search state as JSON.
func (s *Solver) Save(w io.Writer) error {
	cp := Checkpoint{
		Instance:  s.inst,
		BestValue: s.bestValue,
		BestPath:  append([]int(nil), s.bestPath...),
		Explored:  s.explored,
		Frontier:  append([]Node(nil), s.frontier...),
	}
	return json.NewEncoder(w).Encode(cp)
}

// Load restores a Solver from a checkpoint. The frontier slice is
// restored verbatim and re-heapified deterministically, so the pop
// order matches the uninterrupted run exactly.
func Load(r io.Reader) (*Solver, error) {
	var cp Checkpoint
	if err := json.NewDecoder(r).Decode(&cp); err != nil {
		return nil, err
	}
	s := &Solver{
		inst:      cp.Instance,
		frontier:  nodeHeap(cp.Frontier),
		bestValue: cp.BestValue,
		bestPath:  cp.BestPath,
		explored:  cp.Explored,
	}
	heap.Init(&s.frontier)
	return s, nil
}
