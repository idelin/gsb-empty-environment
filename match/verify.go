package match

import "fmt"

// Verify independently checks a Result against a raw instance. It
// never calls the solver; it only validates the assignment and the
// two certificates:
//
//  1. the vertex cover touches every edge and its size equals the
//     number of assignments, so no matching can be larger (any
//     matching needs a distinct cover vertex per edge);
//  2. the potentials make every residual arc's reduced cost
//     non-negative in the residual network of the reported matching,
//     so no negative-cost residual cycle exists and the profit is
//     optimal among all matchings of the same cardinality.
//
// The lexicographic tie-break is not covered by a compact
// certificate; it is checked independently by BruteSolve.
func Verify(tasks, employees []int64, edges []Edge, res *Result) error {
	s := NewSolver(tasks, employees, edges)

	if res.Sentinel != s.Sentinel() {
		return fmt.Errorf("sentinel: got %d, want %d", res.Sentinel, s.Sentinel())
	}
	if len(res.Tasks) != len(s.tasks) {
		return fmt.Errorf("tasks list length mismatch")
	}
	for i, t := range s.tasks {
		if res.Tasks[i] != t {
			return fmt.Errorf("tasks list mismatch at %d", i)
		}
	}
	if len(res.EmployeeByTask) != len(s.tasks) {
		return fmt.Errorf("employeeByTask length %d, want %d", len(res.EmployeeByTask), len(s.tasks))
	}
	if res.NumAssigned != len(res.Assignment) {
		return fmt.Errorf("numAssigned=%d but %d assignment entries", res.NumAssigned, len(res.Assignment))
	}

	// Assignment validity: real edges, distinct endpoints, sorted,
	// consistent with employeeByTask, profits sum to totalProfit.
	usedT := make(map[int64]bool)
	usedE := make(map[int64]bool)
	var profitSum int64
	for i, a := range res.Assignment {
		p, ok := s.profits[[2]int64{a.Task, a.Employee}]
		if !ok {
			return fmt.Errorf("assignment (%d,%d) is not an allowed edge", a.Task, a.Employee)
		}
		if a.Profit != p {
			return fmt.Errorf("assignment (%d,%d) profit %d, want %d", a.Task, a.Employee, a.Profit, p)
		}
		if usedT[a.Task] || usedE[a.Employee] {
			return fmt.Errorf("assignment reuses task %d or employee %d", a.Task, a.Employee)
		}
		usedT[a.Task] = true
		usedE[a.Employee] = true
		if i > 0 && res.Assignment[i-1].Task >= a.Task {
			return fmt.Errorf("assignment not sorted by task ID")
		}
		profitSum += p
	}
	if profitSum != res.TotalProfit {
		return fmt.Errorf("totalProfit=%d but assignment sums to %d", res.TotalProfit, profitSum)
	}
	for i, t := range s.tasks {
		want := res.Sentinel
		if e, ok := matchedEmployee(res.Assignment, t); ok {
			want = e
		}
		if res.EmployeeByTask[i] != want {
			return fmt.Errorf("employeeByTask[%d]=%d, want %d", i, res.EmployeeByTask[i], want)
		}
	}

	// Certificate 1: minimum vertex cover => cardinality is maximal.
	c := &res.Certificate
	if len(c.VertexCoverTasks)+len(c.VertexCoverEmployees) != res.NumAssigned {
		return fmt.Errorf("vertex cover size %d != numAssigned %d",
			len(c.VertexCoverTasks)+len(c.VertexCoverEmployees), res.NumAssigned)
	}
	inCovT := make(map[int64]bool, len(c.VertexCoverTasks))
	for _, t := range c.VertexCoverTasks {
		inCovT[t] = true
	}
	inCovE := make(map[int64]bool, len(c.VertexCoverEmployees))
	for _, e := range c.VertexCoverEmployees {
		inCovE[e] = true
	}
	for k := range s.profits {
		if !inCovT[k[0]] && !inCovE[k[1]] {
			return fmt.Errorf("edge (%d,%d) not covered", k[0], k[1])
		}
	}

	// Certificate 2: potentials => no negative-cost residual cycle.
	type vnode struct {
		kind byte // 's' source, 'k' sink, 't' task, 'e' employee
		id   int64
	}
	pot := map[vnode]int64{
		{'s', 0}: c.SourcePotential,
		{'k', 0}: c.SinkPotential,
	}
	for _, p := range c.TaskPotentials {
		pot[vnode{'t', p.ID}] = p.Value
	}
	for _, p := range c.EmployeePotentials {
		pot[vnode{'e', p.ID}] = p.Value
	}
	check := func(u, v vnode, cost int64) error {
		pu, ok1 := pot[u]
		pv, ok2 := pot[v]
		if !ok1 || !ok2 {
			return fmt.Errorf("missing potential for node %+v or %+v", u, v)
		}
		if pv > pu+cost {
			return fmt.Errorf("negative reduced cost on residual arc %+v -> %+v", u, v)
		}
		return nil
	}
	src, sink := vnode{'s', 0}, vnode{'k', 0}
	for _, t := range s.tasks {
		tn := vnode{'t', t}
		var err error
		if usedT[t] {
			err = check(tn, src, 0) // saturated source arc, backward residual
		} else {
			err = check(src, tn, 0)
		}
		if err != nil {
			return err
		}
	}
	for _, e := range s.employees {
		en := vnode{'e', e}
		var err error
		if usedE[e] {
			err = check(sink, en, 0)
		} else {
			err = check(en, sink, 0)
		}
		if err != nil {
			return err
		}
	}
	for k, p := range s.profits {
		tn, en := vnode{'t', k[0]}, vnode{'e', k[1]}
		var err error
		if usedT[k[0]] && matchedTo(res.Assignment, k[0]) == k[1] {
			err = check(en, tn, p) // matched edge, backward residual
		} else {
			err = check(tn, en, -p)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func matchedEmployee(assignment []Edge, task int64) (int64, bool) {
	for _, a := range assignment {
		if a.Task == task {
			return a.Employee, true
		}
	}
	return 0, false
}

func matchedTo(assignment []Edge, task int64) int64 {
	e, _ := matchedEmployee(assignment, task)
	return e
}
