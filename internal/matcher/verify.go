package matcher

import "fmt"

// exhaustiveLimit caps the (E+1)^T leaf count for the independent
// exhaustive re-computation inside Verify. 7x7 gives 8^7 = 2097152.
const exhaustiveLimit = 3_000_000

// VerifyInstance validates the instance and independently checks a
// claimed result. It returns the names of the checks that passed.
func VerifyInstance(inst *Instance, res *Result) ([]string, error) {
	p, err := Validate(inst)
	if err != nil {
		return nil, err
	}
	return p.verify(res)
}

// verify independently checks every component of a claimed result:
//
//	structure    - assignment references real edges, employees used once
//	objectives   - cardinality and profit match the assignment
//	vertex cover - covers every edge and has size == cardinality, which
//	               by Kőnig's theorem proves the cardinality is maximal
//	duals        - potentials satisfy pi[u]+c >= pi[v] on every residual
//	               edge, proving profit optimality at max cardinality
//	exhaustive   - full re-enumeration of all matchings (small inputs)
func (p *problem) verify(res *Result) ([]string, error) {
	T, E := len(p.tasks), len(p.emps)
	var checks []string

	if !equalStrings(res.Tasks, p.tasks) {
		return nil, fmt.Errorf("task list mismatch")
	}
	if !equalStrings(res.Employees, p.emps) {
		return nil, fmt.Errorf("employee list mismatch")
	}
	if len(res.Assignment) != T {
		return nil, fmt.Errorf("assignment has %d entries, want %d", len(res.Assignment), T)
	}
	eidx := map[string]int{}
	for j, id := range p.emps {
		eidx[id] = j
	}
	tidx := map[string]int{}
	for i, id := range p.tasks {
		tidx[id] = i
	}
	match := make([]int, T)
	for i := range match {
		match[i] = E
	}
	used := make([]bool, E)
	card := 0
	var profit int64
	for i, a := range res.Assignment {
		if a == "" {
			continue
		}
		j, ok := eidx[a]
		if !ok {
			return nil, fmt.Errorf("assignment uses unknown employee %q", a)
		}
		pf, ok := p.profit[[2]int{i, j}]
		if !ok {
			return nil, fmt.Errorf("assignment uses non-edge %q/%q", p.tasks[i], a)
		}
		if used[j] {
			return nil, fmt.Errorf("employee %q assigned twice", a)
		}
		used[j] = true
		match[i] = j
		card++
		profit += pf
	}
	if len(res.Matches) != card {
		return nil, fmt.Errorf("matches list has %d entries, assignment has %d", len(res.Matches), card)
	}
	for _, m := range res.Matches {
		ti, ok := tidx[m.Task]
		if !ok {
			return nil, fmt.Errorf("match references unknown task %q", m.Task)
		}
		ej, ok := eidx[m.Employee]
		if !ok {
			return nil, fmt.Errorf("match references unknown employee %q", m.Employee)
		}
		if res.Assignment[ti] != m.Employee {
			return nil, fmt.Errorf("match %q/%q contradicts assignment", m.Task, m.Employee)
		}
		if p.profit[[2]int{ti, ej}] != m.Profit {
			return nil, fmt.Errorf("match %q/%q reports profit %d, input says %d",
				m.Task, m.Employee, m.Profit, p.profit[[2]int{ti, ej}])
		}
	}
	if res.Cardinality != card {
		return nil, fmt.Errorf("claimed cardinality %d, assignment has %d", res.Cardinality, card)
	}
	if res.TotalProfit != profit {
		return nil, fmt.Errorf("claimed profit %d, assignment sums to %d", res.TotalProfit, profit)
	}
	checks = append(checks, "structure", "objectives")

	// Vertex-cover certificate: a cover of size == |matching| proves
	// the matching is maximum, since every matching needs a distinct
	// cover vertex per edge.
	if len(res.CoverTasks)+len(res.CoverEmps) != card {
		return nil, fmt.Errorf("vertex cover has size %d, matching has size %d",
			len(res.CoverTasks)+len(res.CoverEmps), card)
	}
	covT := make([]bool, T)
	for _, id := range res.CoverTasks {
		i, ok := tidx[id]
		if !ok {
			return nil, fmt.Errorf("cover references unknown task %q", id)
		}
		covT[i] = true
	}
	covE := make([]bool, E)
	for _, id := range res.CoverEmps {
		j, ok := eidx[id]
		if !ok {
			return nil, fmt.Errorf("cover references unknown employee %q", id)
		}
		covE[j] = true
	}
	for key := range p.profit {
		if !covT[key[0]] && !covE[key[1]] {
			return nil, fmt.Errorf("edge %q/%q not covered", p.tasks[key[0]], p.emps[key[1]])
		}
	}
	checks = append(checks, "min-vertex-cover")

	// Dual certificate: no negative-cost residual cycle.
	if len(res.Duals.Tasks) != T || len(res.Duals.Employees) != E {
		return nil, fmt.Errorf("dual potentials have wrong dimensions")
	}
	pot := make([]int64, T+E+2)
	pot[0] = res.Duals.Source
	for i := 0; i < T; i++ {
		pot[1+i] = res.Duals.Tasks[i]
	}
	for j := 0; j < E; j++ {
		pot[1+T+j] = res.Duals.Employees[j]
	}
	pot[T+E+1] = res.Duals.Sink
	for _, e := range p.residualEdges(match) {
		if pot[e.u]+e.c < pot[e.v] {
			return nil, fmt.Errorf("dual violation: pi[%d]+%d < pi[%d]", e.u, e.c, e.v)
		}
	}
	checks = append(checks, "dual-potentials")

	// Independent exact re-computation for small instances.
	if exhaustiveWays(T, E, exhaustiveLimit) <= exhaustiveLimit {
		bc, bp, bm := p.bruteForce()
		if bc != card || bp != profit {
			return nil, fmt.Errorf("exhaustive reference: got (%d,%d), claimed (%d,%d)",
				bc, bp, card, profit)
		}
		for i := range bm {
			if bm[i] != match[i] {
				return nil, fmt.Errorf("exhaustive reference: task %q should map to %q, claimed %q",
					p.tasks[i], p.empName(bm[i]), p.empName(match[i]))
			}
		}
		checks = append(checks, "exhaustive")
	} else {
		checks = append(checks, "exhaustive(skipped: instance too large)")
	}
	return checks, nil
}

func (p *problem) empName(j int) string {
	if j >= len(p.emps) {
		return "<unassigned>"
	}
	return p.emps[j]
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
