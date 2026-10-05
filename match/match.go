// Package match computes exact weighted bipartite matchings with a
// lexicographically ordered triple objective:
//
//  1. maximize the number of assigned tasks (cardinality),
//  2. among those, maximize the total profit (profits are signed
//     integers, negative edges are never dropped a priori),
//  3. among those, pick the assignment whose list of employee IDs,
//     ordered by increasing task ID, is lexicographically minimal
//     (unassigned tasks are represented by a sentinel larger than
//     every ID).
//
// The result carries a machine-checkable optimality certificate:
// a minimum vertex cover (proves cardinality, Kőnig's theorem) and
// feasible potentials of the residual min-cost-flow network (prove
// profit optimality). See Verify.
package match

import "sort"

// Edge is an allowed (task, employee) pair with a signed profit.
type Edge struct {
	Task     int64 `json:"task"`
	Employee int64 `json:"employee"`
	Profit   int64 `json:"profit"`
}

// Update adds or removes a single edge. Op is "add" or "remove".
type Update struct {
	Op       string `json:"op"`
	Task     int64  `json:"task"`
	Employee int64  `json:"employee"`
	Profit   int64  `json:"profit,omitempty"`
}

// Instance is the JSON input document for the CLI.
type Instance struct {
	Tasks     []int64  `json:"tasks"`
	Employees []int64  `json:"employees"`
	Edges     []Edge   `json:"edges"`
	Updates   []Update `json:"updates,omitempty"`
}

// PotentialEntry is one node's dual value in the certificate.
type PotentialEntry struct {
	ID    int64 `json:"id"`
	Value int64 `json:"value"`
}

// Certificate is the checkable optimality evidence.
//
// VertexCoverTasks + VertexCoverEmployees form a minimum vertex cover
// whose size equals the number of assigned tasks, proving no larger
// matching exists (Kőnig). The potentials satisfy, for every residual
// arc (u,v) of the min-cost-flow network of the reported matching,
// pot[v] <= pot[u] + cost(u,v), proving no negative-cost residual
// cycle exists, i.e. the profit is optimal at this cardinality.
type Certificate struct {
	VertexCoverTasks     []int64          `json:"vertexCoverTasks"`
	VertexCoverEmployees []int64          `json:"vertexCoverEmployees"`
	SourcePotential      int64            `json:"sourcePotential"`
	SinkPotential        int64            `json:"sinkPotential"`
	TaskPotentials       []PotentialEntry `json:"taskPotentials"`
	EmployeePotentials   []PotentialEntry `json:"employeePotentials"`
}

// Result is the solver output.
type Result struct {
	Tasks          []int64     `json:"tasks"` // sorted task IDs, parallel to EmployeeByTask
	Sentinel       int64       `json:"sentinel"`
	NumAssigned    int         `json:"numAssigned"`
	TotalProfit    int64       `json:"totalProfit"`
	EmployeeByTask []int64     `json:"employeeByTask"` // sentinel means unassigned
	Assignment     []Edge      `json:"assignment"`     // matched pairs, sorted by task
	Certificate    Certificate `json:"certificate"`
}

// Solver holds a canonicalized instance and supports edge updates.
// Duplicate edges between the same pair are merged keeping the
// maximum profit. Edges referencing unknown IDs are ignored.
type Solver struct {
	tasks     []int64 // sorted, deduplicated
	employees []int64 // sorted, deduplicated
	profits   map[[2]int64]int64
}

// NewSolver builds a solver from raw (possibly duplicated) input.
func NewSolver(tasks, employees []int64, edges []Edge) *Solver {
	s := &Solver{
		tasks:     dedupeSorted(tasks),
		employees: dedupeSorted(employees),
		profits:   make(map[[2]int64]int64),
	}
	for _, e := range edges {
		s.AddEdge(e.Task, e.Employee, e.Profit)
	}
	return s
}

func dedupeSorted(ids []int64) []int64 {
	cp := append([]int64(nil), ids...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	out := cp[:0]
	for i, v := range cp {
		if i == 0 || v != cp[i-1] {
			out = append(out, v)
		}
	}
	return append([]int64(nil), out...)
}

func contains(sorted []int64, v int64) bool {
	i := sort.Search(len(sorted), func(i int) bool { return sorted[i] >= v })
	return i < len(sorted) && sorted[i] == v
}

// AddEdge inserts an edge; if the pair already exists the maximum
// profit is kept (same policy as input deduplication).
func (s *Solver) AddEdge(task, employee, profit int64) {
	if !contains(s.tasks, task) || !contains(s.employees, employee) {
		return
	}
	k := [2]int64{task, employee}
	if old, ok := s.profits[k]; !ok || profit > old {
		s.profits[k] = profit
	}
}

// RemoveEdge deletes a single edge; it is a no-op if absent.
func (s *Solver) RemoveEdge(task, employee int64) {
	delete(s.profits, [2]int64{task, employee})
}

// Edges returns the current canonical edge set, sorted.
func (s *Solver) Edges() []Edge {
	out := make([]Edge, 0, len(s.profits))
	for k, p := range s.profits {
		out = append(out, Edge{Task: k[0], Employee: k[1], Profit: p})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Task != out[j].Task {
			return out[i].Task < out[j].Task
		}
		return out[i].Employee < out[j].Employee
	})
	return out
}

// Sentinel returns an ID strictly greater than every task/employee ID.
func (s *Solver) Sentinel() int64 {
	var m int64
	empty := true
	if n := len(s.tasks); n > 0 {
		m, empty = s.tasks[n-1], false
	}
	if n := len(s.employees); n > 0 && (empty || s.employees[n-1] > m) {
		m = s.employees[n-1]
	}
	if empty {
		return 0
	}
	return m + 1
}

// adjacency maps each task to its employees in increasing ID order.
func (s *Solver) adjacency() map[int64][]int64 {
	adj := make(map[int64][]int64)
	for k := range s.profits {
		adj[k[0]] = append(adj[k[0]], k[1])
	}
	for t := range adj {
		sort.Slice(adj[t], func(i, j int) bool { return adj[t][i] < adj[t][j] })
	}
	return adj
}

// Solve computes the optimal assignment together with its certificate.
func (s *Solver) Solve() Result {
	adj := s.adjacency()

	// Phase 1: maximum cardinality (profits ignored, so negative-profit
	// edges are used whenever they increase the number of assignments).
	matchT, matchE := maxCardinality(s.tasks, adj)
	covT, covE := vertexCover(s.tasks, s.employees, adj, matchT, matchE)
	card := len(matchT)

	// Phase 2: minimum-cost flow of exactly `card` units; cost of an
	// edge is -profit, so this maximizes profit at max cardinality.
	net, src, sink := s.buildNet(s.tasks, s.employees)
	_, cost, pot := minCostFlow(net, src, sink, card)
	profit := -cost

	// Phase 3: lexicographically smallest employee list among all
	// (card, profit)-optimal matchings, tasks in increasing ID order.
	fixed := s.lexMin(adj, card, profit)

	sentinel := s.Sentinel()
	res := Result{
		Tasks:       append([]int64(nil), s.tasks...),
		Sentinel:    sentinel,
		NumAssigned: card,
		TotalProfit: profit,
	}
	for _, t := range s.tasks {
		if e, ok := fixed[t]; ok {
			res.EmployeeByTask = append(res.EmployeeByTask, e)
			res.Assignment = append(res.Assignment, Edge{Task: t, Employee: e, Profit: s.profits[[2]int64{t, e}]})
		} else {
			res.EmployeeByTask = append(res.EmployeeByTask, sentinel)
		}
	}

	cert := Certificate{
		VertexCoverTasks:     covT,
		VertexCoverEmployees: covE,
		SourcePotential:      pot[src],
		SinkPotential:        pot[sink],
	}
	for i, t := range s.tasks {
		cert.TaskPotentials = append(cert.TaskPotentials, PotentialEntry{ID: t, Value: pot[1+i]})
	}
	for j, e := range s.employees {
		cert.EmployeePotentials = append(cert.EmployeePotentials, PotentialEntry{ID: e, Value: pot[1+len(s.tasks)+j]})
	}
	res.Certificate = cert
	return res
}

// maxCardinality is Kuhn's augmenting-path algorithm; deterministic
// because tasks and adjacency lists are processed in sorted order.
func maxCardinality(tasks []int64, adj map[int64][]int64) (matchT, matchE map[int64]int64) {
	matchT = make(map[int64]int64)
	matchE = make(map[int64]int64)
	for _, t0 := range tasks {
		seen := make(map[int64]bool)
		var aug func(t int64) bool
		aug = func(t int64) bool {
			for _, e := range adj[t] {
				if seen[e] {
					continue
				}
				seen[e] = true
				if old, ok := matchE[e]; !ok || aug(old) {
					matchT[t] = e
					matchE[e] = t
					return true
				}
			}
			return false
		}
		aug(t0)
	}
	return matchT, matchE
}

// vertexCover applies Kőnig's theorem: Z = vertices reachable from
// unmatched tasks via alternating paths (non-matching arcs go
// task->employee, matching arcs go employee->task); the minimum
// vertex cover is (Tasks \ Z) u (Employees n Z).
func vertexCover(tasks, employees []int64, adj map[int64][]int64, matchT, matchE map[int64]int64) (covT, covE []int64) {
	inZT := make(map[int64]bool)
	inZE := make(map[int64]bool)
	type node struct {
		isTask bool
		id     int64
	}
	var q []node
	for _, t := range tasks {
		if _, ok := matchT[t]; !ok {
			inZT[t] = true
			q = append(q, node{true, t})
		}
	}
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		if n.isTask {
			for _, e := range adj[n.id] {
				if m, ok := matchT[n.id]; ok && m == e {
					continue // matching arc is only traversed right-to-left
				}
				if !inZE[e] {
					inZE[e] = true
					q = append(q, node{false, e})
				}
			}
		} else if t, ok := matchE[n.id]; ok && !inZT[t] {
			inZT[t] = true
			q = append(q, node{true, t})
		}
	}
	for _, t := range tasks {
		if !inZT[t] {
			covT = append(covT, t)
		}
	}
	for _, e := range employees {
		if inZE[e] {
			covE = append(covE, e)
		}
	}
	return covT, covE
}

// lexMin fixes tasks one by one in increasing ID order, choosing for
// each the smallest employee ID that still admits a (card, profit)-
// optimal completion; "left unassigned" acts as the sentinel, which
// is larger than every ID and therefore tried last.
func (s *Solver) lexMin(adj map[int64][]int64, card int, profit int64) map[int64]int64 {
	fixed := make(map[int64]int64)
	used := make(map[int64]bool)
	var fixedProfit int64
	for _, t := range s.tasks {
		for _, e := range adj[t] {
			if used[e] {
				continue
			}
			p := s.profits[[2]int64{t, e}]
			fixed[t] = e
			used[e] = true
			fixedProfit += p
			if s.feasible(card, profit, fixed, used, fixedProfit) {
				break
			}
			delete(fixed, t)
			delete(used, e)
			fixedProfit -= p
		}
		// If no employee fits, t stays unassigned; feasibility is
		// guaranteed because the previous fixes admit an optimum.
	}
	return fixed
}

// feasible reports whether the fixed partial assignment extends to a
// matching of size `card` and total profit `profit`.
func (s *Solver) feasible(card int, profit int64, fixed map[int64]int64, used map[int64]bool, fixedProfit int64) bool {
	need := card - len(fixed)
	targetCost := -(profit - fixedProfit)
	tasks := make([]int64, 0, len(s.tasks)-len(fixed))
	for _, t := range s.tasks {
		if _, ok := fixed[t]; !ok {
			tasks = append(tasks, t)
		}
	}
	emps := make([]int64, 0, len(s.employees)-len(used))
	for _, e := range s.employees {
		if !used[e] {
			emps = append(emps, e)
		}
	}
	net, src, sink := s.buildNet(tasks, emps)
	flow, cost, _ := minCostFlow(net, src, sink, need)
	// cost < targetCost is impossible (it would contradict the
	// optimality of `profit`), so equality decides feasibility.
	return flow == need && cost == targetCost
}
