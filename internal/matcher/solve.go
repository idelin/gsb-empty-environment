package matcher

import "fmt"

// Result is the full solver output, including optimality certificates.
type Result struct {
	Tasks       []string // sorted task IDs
	Employees   []string // sorted employee IDs
	Assignment  []string // employee per task; "" = unassigned sentinel
	Matches     []Edge   // matched pairs with profits, in task order
	Cardinality int
	TotalProfit int64
	CoverTasks  []string // minimum vertex cover (Kőnig certificate)
	CoverEmps   []string
	Duals       Duals // node potentials certifying profit optimality
}

// Duals are potentials pi over the flow network of the final matching.
// For every residual edge (u,v) with cost c they satisfy
// pi[u] + c >= pi[v], which proves the residual graph has no
// negative-cost cycle, i.e. the flow is min-cost (profit-maximal)
// among all flows of the same (maximum) value.
type Duals struct {
	Source    int64
	Sink      int64
	Tasks     []int64 // parallel to Result.Tasks
	Employees []int64 // parallel to Result.Employees
}

// SolveInstance validates the instance and solves it exactly.
func SolveInstance(inst *Instance) (*Result, error) {
	p, err := Validate(inst)
	if err != nil {
		return nil, err
	}
	return p.solve(), nil
}

// fedge is a residual edge in the unit-capacity flow network.
type fedge struct {
	to, rev int
	cap     int
	cost    int64
}

type flowNet struct {
	g [][]fedge
}

func (f *flowNet) addEdge(u, v int, cost int64) {
	f.g[u] = append(f.g[u], fedge{to: v, rev: len(f.g[v]), cap: 1, cost: cost})
	f.g[v] = append(f.g[v], fedge{to: u, rev: len(f.g[u]) - 1, cap: 0, cost: -cost})
}

// solveCore computes a max-cardinality, then max-profit matching under
// constraints: forced[t]=e pins task t to employee e (both are removed
// from the free subproblem), banned[t] forbids matching task t.
// It returns the cardinality, the total profit, and the match array
// (employee index per task, or len(emps) = unassigned sentinel).
func (p *problem) solveCore(forced map[int]int, banned map[int]bool) (int, int64, []int) {
	T, E := len(p.tasks), len(p.emps)
	match := make([]int, T)
	for i := range match {
		match[i] = E
	}
	usedEmp := make([]bool, E)
	card, profit := 0, int64(0)
	for t, e := range forced {
		match[t] = e
		usedEmp[e] = true
		card++
		profit += p.profit[[2]int{t, e}]
	}
	var freeT, freeE []int
	for i := 0; i < T; i++ {
		if _, ok := forced[i]; ok {
			continue
		}
		if banned[i] {
			continue
		}
		freeT = append(freeT, i)
	}
	for j := 0; j < E; j++ {
		if !usedEmp[j] {
			freeE = append(freeE, j)
		}
	}
	// Nodes: 0 = source, then free tasks, then free employees, last = sink.
	tBase := 1
	eBase := tBase + len(freeT)
	sink := eBase + len(freeE)
	net := flowNet{g: make([][]fedge, sink+1)}
	eLocal := make([]int, E)
	for j := range eLocal {
		eLocal[j] = -1
	}
	for b, ej := range freeE {
		eLocal[ej] = b
		net.addEdge(eBase+b, sink, 0)
	}
	for a, ti := range freeT {
		net.addEdge(0, tBase+a, 0)
		for _, ej := range p.adj[ti] {
			if usedEmp[ej] {
				continue
			}
			net.addEdge(tBase+a, eBase+eLocal[ej], -p.profit[[2]int{ti, ej}])
		}
	}
	// Successive shortest augmenting paths. We augment while any
	// s-t path exists, regardless of path cost, so cardinality is
	// maximized first; each augmentation is along a min-cost path,
	// which yields min cost (max profit) among max-cardinality flows.
	const inf = int64(1) << 60
	n := len(net.g)
	for {
		dist := make([]int64, n)
		prevV := make([]int, n)
		prevE := make([]int, n)
		for i := range dist {
			dist[i] = inf
		}
		dist[0] = 0
		for iter := 0; iter < n-1; iter++ {
			updated := false
			for v := 0; v < n; v++ {
				if dist[v] == inf {
					continue
				}
				for ei, e := range net.g[v] {
					if e.cap > 0 && dist[e.to] > dist[v]+e.cost {
						dist[e.to] = dist[v] + e.cost
						prevV[e.to] = v
						prevE[e.to] = ei
						updated = true
					}
				}
			}
			if !updated {
				break
			}
		}
		if dist[sink] == inf {
			break
		}
		for v := sink; v != 0; v = prevV[v] {
			e := &net.g[prevV[v]][prevE[v]]
			e.cap--
			net.g[v][e.rev].cap++
		}
		card++
		profit -= dist[sink] // path cost is the negated edge profit
	}
	for a, ti := range freeT {
		for _, e := range net.g[tBase+a] {
			if e.to >= eBase && e.to < sink && e.cap == 0 {
				match[ti] = freeE[e.to-eBase]
			}
		}
	}
	return card, profit, match
}

// lexMin finds the lexicographically smallest match array achieving
// cardinality K and profit P. Tasks are processed in ID order; each
// task tries its adjacent employees in ascending ID order, and a
// candidate is accepted iff the remaining instance can still reach
// (K, P). Unassigned (sentinel) is the last resort.
func (p *problem) lexMin(K int, P int64) []int {
	T, E := len(p.tasks), len(p.emps)
	forced := map[int]int{}
	banned := map[int]bool{}
	match := make([]int, T)
	for i := range match {
		match[i] = E
	}
	for i := 0; i < T; i++ {
		chosen := E
		for _, e := range p.adj[i] {
			if empUsed(forced, e) {
				continue
			}
			forced[i] = e
			c, pf, _ := p.solveCore(forced, banned)
			if c == K && pf == P {
				chosen = e
				break
			}
			delete(forced, i)
		}
		if chosen == E {
			// Feasible by induction: some optimal solution extends
			// the current prefix, and it must leave task i unassigned.
			banned[i] = true
		} else {
			match[i] = chosen
		}
	}
	return match
}

func empUsed(forced map[int]int, e int) bool {
	for _, v := range forced {
		if v == e {
			return true
		}
	}
	return false
}

// minVertexCover applies Kőnig's theorem: Z is the set of vertices
// reachable from unmatched tasks via alternating paths (non-matching
// edges from tasks to employees, matching edges back); the cover is
// (tasks not in Z) ∪ (employees in Z).
func (p *problem) minVertexCover(match []int) (coverT, coverE []int) {
	T, E := len(p.tasks), len(p.emps)
	empMatch := make([]int, E)
	for j := range empMatch {
		empMatch[j] = -1
	}
	for i := 0; i < T; i++ {
		if match[i] != E {
			empMatch[match[i]] = i
		}
	}
	inZT := make([]bool, T)
	inZE := make([]bool, E)
	var queue []int
	for i := 0; i < T; i++ {
		if match[i] == E {
			inZT[i] = true
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		for _, e := range p.adj[t] {
			if match[t] == e || inZE[e] {
				continue
			}
			inZE[e] = true
			if m := empMatch[e]; m >= 0 && !inZT[m] {
				inZT[m] = true
				queue = append(queue, m)
			}
		}
	}
	for i := 0; i < T; i++ {
		if !inZT[i] {
			coverT = append(coverT, i)
		}
	}
	for j := 0; j < E; j++ {
		if inZE[j] {
			coverE = append(coverE, j)
		}
	}
	return coverT, coverE
}

// residEdge is one residual edge of the flow network of a given
// matching: node 0 = source, 1..T tasks, T+1..T+E employees, last = sink.
type residEdge struct {
	u, v int
	c    int64
}

func (p *problem) residualEdges(match []int) []residEdge {
	T, E := len(p.tasks), len(p.emps)
	sink := T + E + 1
	empMatch := make([]bool, E)
	for i := 0; i < T; i++ {
		if match[i] != E {
			empMatch[match[i]] = true
		}
	}
	var edges []residEdge
	add := func(u, v int, c int64) { edges = append(edges, residEdge{u, v, c}) }
	for i := 0; i < T; i++ {
		if match[i] == E {
			add(0, 1+i, 0)
		} else {
			add(1+i, 0, 0)
		}
	}
	for i := 0; i < T; i++ {
		for _, j := range p.adj[i] {
			c := -p.profit[[2]int{i, j}]
			if match[i] == j {
				add(1+T+j, 1+i, -c) // backward residual edge
			} else {
				add(1+i, 1+T+j, c)
			}
		}
	}
	for j := 0; j < E; j++ {
		if empMatch[j] {
			add(sink, 1+T+j, 0)
		} else {
			add(1+T+j, sink, 0)
		}
	}
	return edges
}

// duals computes node potentials certifying that the matching is
// profit-optimal among max-cardinality matchings: after Bellman-Ford
// from a virtual zero-distance super-source, every residual edge
// satisfies pi[u] + c >= pi[v], so no negative residual cycle exists.
func (p *problem) duals(match []int) (Duals, error) {
	T, E := len(p.tasks), len(p.emps)
	n := T + E + 2
	edges := p.residualEdges(match)
	dist := make([]int64, n) // all zero: virtual super-source
	for iter := 0; iter < n-1; iter++ {
		updated := false
		for _, e := range edges {
			if dist[e.v] > dist[e.u]+e.c {
				dist[e.v] = dist[e.u] + e.c
				updated = true
			}
		}
		if !updated {
			break
		}
	}
	for _, e := range edges {
		if dist[e.v] > dist[e.u]+e.c {
			return Duals{}, fmt.Errorf("internal error: negative residual cycle")
		}
	}
	d := Duals{Source: dist[0], Sink: dist[n-1], Tasks: make([]int64, T), Employees: make([]int64, E)}
	for i := 0; i < T; i++ {
		d.Tasks[i] = dist[1+i]
	}
	for j := 0; j < E; j++ {
		d.Employees[j] = dist[1+T+j]
	}
	return d, nil
}

func (p *problem) solve() *Result {
	T, E := len(p.tasks), len(p.emps)
	K, P, _ := p.solveCore(nil, nil)
	match := p.lexMin(K, P)
	covT, covE := p.minVertexCover(match)
	duals, err := p.duals(match)
	if err != nil {
		panic(err) // an optimal matching cannot have a negative residual cycle
	}
	res := &Result{
		Tasks:       p.tasks,
		Employees:   p.emps,
		Assignment:  make([]string, T),
		Cardinality: K,
		TotalProfit: P,
		Duals:       duals,
	}
	for i := 0; i < T; i++ {
		if match[i] != E {
			res.Assignment[i] = p.emps[match[i]]
			res.Matches = append(res.Matches, Edge{
				Task:     p.tasks[i],
				Employee: p.emps[match[i]],
				Profit:   p.profit[[2]int{i, match[i]}],
			})
		}
	}
	for _, i := range covT {
		res.CoverTasks = append(res.CoverTasks, p.tasks[i])
	}
	for _, j := range covE {
		res.CoverEmps = append(res.CoverEmps, p.emps[j])
	}
	return res
}
