// Package tasksched provides deterministic topological scheduling with
// precedence constraints and an incremental, progress-locking executor.
package tasksched

import "container/heap"

// Edge is a precedence constraint: From must run before To.
// Tasks are identified by their index in the original task list.
type Edge struct {
	From, To int
}

// Result is the outcome of a deterministic topological sort.
//
// Order holds tasks in the unique greedy order: at every step the
// executable task with the smallest original position is chosen.
// When the constraints contain a cycle, Order is the maximal
// still-executable prefix and Cycle holds one concrete closed loop
// (consecutive tasks, including last->first, are real constraints).
type Result struct {
	Order []int
	Cycle []int
}

// Sort topologically sorts n tasks under the given constraints.
// Duplicate constraints are deduplicated and never affect the result.
func Sort(n int, edges []Edge) Result {
	adj := make([]map[int]bool, n)
	indeg := make([]int, n)
	for i := range adj {
		adj[i] = make(map[int]bool)
	}
	for _, e := range edges {
		if !adj[e.From][e.To] {
			adj[e.From][e.To] = true
			indeg[e.To]++
		}
	}

	ready := &intHeap{}
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			heap.Push(ready, i)
		}
	}
	done := make([]bool, n)
	var order []int
	for ready.Len() > 0 {
		v := heap.Pop(ready).(int)
		order = append(order, v)
		done[v] = true
		for _, w := range sortedKeys(adj[v]) {
			indeg[w]--
			if indeg[w] == 0 {
				heap.Push(ready, w)
			}
		}
	}
	if len(order) == n {
		return Result{Order: order}
	}
	return Result{Order: order, Cycle: findCycle(adj, done)}
}

// findCycle returns one concrete closed loop inside the subgraph of
// unfinished tasks. Such a loop always exists there: every unfinished
// task still has an unfinished predecessor.
func findCycle(adj []map[int]bool, done []bool) []int {
	const (
		white = iota
		gray
		black
	)
	color := make([]int, len(adj))
	var stack []int
	var cycle []int
	var visit func(v int) bool
	visit = func(v int) bool {
		color[v] = gray
		stack = append(stack, v)
		for _, w := range sortedKeys(adj[v]) {
			if done[w] {
				continue
			}
			if color[w] == gray {
				for i, u := range stack {
					if u == w {
						cycle = append([]int(nil), stack[i:]...)
						return true
					}
				}
			}
			if color[w] == white && visit(w) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[v] = black
		return false
	}
	for v := range adj {
		if !done[v] && color[v] == white && visit(v) {
			return cycle
		}
	}
	return nil
}

func sortedKeys(m map[int]bool) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// intHeap is a min-heap of task indices (original positions).
type intHeap []int

func (h intHeap) Len() int            { return len(h) }
func (h intHeap) Less(i, j int) bool  { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *intHeap) Push(x interface{}) { *h = append(*h, x.(int)) }
func (h *intHeap) Pop() interface{} {
	old := *h
	v := old[len(old)-1]
	*h = old[:len(old)-1]
	return v
}
