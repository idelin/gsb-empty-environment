package tempsched

import "math"

// Task is a scheduled activity with a duration range (in project days).
type Task struct {
	Name        string
	MinDuration float64
	MaxDuration float64
}

// Window is the feasible time window of one task, relative to project time 0.
type Window struct {
	EarliestStart float64
	LatestStart   float64
	EarliestEnd   float64
	LatestEnd     float64
}

// Result is the outcome of Solve.
type Result struct {
	// Consistent is false when the constraints admit no feasible schedule.
	Consistent bool
	// Windows holds the feasible time window of every task.
	Windows map[string]Window
	// EarliestFinish / LatestFinish bound the project makespan, i.e. the
	// global critical-path window of the whole schedule.
	EarliestFinish float64
	LatestFinish   float64
	// Network is the Allen constraint network after path-consistency
	// propagation; use Network.Relation to inspect inferred relations.
	Network *Network
}

type bound struct {
	task  int
	start bool // true: constrain start point, false: end point
	lo    float64
	hi    float64
}

// Solver combines an Allen relation network with numeric duration and
// deadline constraints.
type Solver struct {
	tasks  []Task
	index  map[string]int
	net    *Network
	bounds []bound
}

// NewSolver creates an empty solver.
func NewSolver() *Solver {
	return &Solver{index: map[string]int{}, net: NewNetwork()}
}

// AddTask registers a task with a duration in [minDuration, maxDuration] days.
func (s *Solver) AddTask(name string, minDuration, maxDuration float64) {
	if _, ok := s.index[name]; ok {
		return
	}
	s.index[name] = len(s.tasks)
	s.tasks = append(s.tasks, Task{Name: name, MinDuration: minDuration, MaxDuration: maxDuration})
	s.net.AddTask(name)
}

// Constrain asserts an Allen relation between two tasks, e.g.
// Constrain("A", "B", Before) means task A must end before task B starts.
// Relations are interpreted non-strictly (<=) at the numeric layer, which is
// the standard convention for scheduling.
func (s *Solver) Constrain(x, y string, r Relation) {
	s.net.Constrain(x, y, r)
}

func (s *Solver) addBound(task string, start bool, lo, hi float64) {
	i := s.index[task]
	for k := range s.bounds {
		b := &s.bounds[k]
		if b.task == i && b.start == start {
			b.lo = math.Max(b.lo, lo)
			b.hi = math.Min(b.hi, hi)
			return
		}
	}
	s.bounds = append(s.bounds, bound{task: i, start: start, lo: lo, hi: hi})
}

// ConstrainStartBefore requires the task to start no later than day t.
func (s *Solver) ConstrainStartBefore(task string, t float64) {
	s.addBound(task, true, math.Inf(-1), t)
}

// ConstrainStartAfter requires the task to start no earlier than day t.
func (s *Solver) ConstrainStartAfter(task string, t float64) {
	s.addBound(task, true, t, math.Inf(1))
}

// ConstrainEndBefore requires the task to end no later than day t.
func (s *Solver) ConstrainEndBefore(task string, t float64) {
	s.addBound(task, false, math.Inf(-1), t)
}

// ConstrainEndAfter requires the task to end no earlier than day t.
func (s *Solver) ConstrainEndAfter(task string, t float64) {
	s.addBound(task, false, t, math.Inf(1))
}

// Solve propagates the temporal relations (path consistency / transitive
// closure on the Allen network) and then computes the tightest feasible
// time window of every task via an all-pairs shortest-paths STN solve.
func (s *Solver) Solve() *Result {
	res := &Result{Windows: map[string]Window{}, Network: s.net}
	if !s.net.PathConsistent() {
		return res
	}

	n := len(s.tasks)
	const ref = 0
	start := func(i int) int { return 1 + 2*i }
	end := func(i int) int { return 2 + 2*i }

	var edges []edge
	add := func(from, to int, w float64) { edges = append(edges, edge{from, to, w}) }

	for i, t := range s.tasks {
		add(start(i), end(i), t.MaxDuration)  // e - s <= maxDur
		add(end(i), start(i), -t.MinDuration) // s - e <= -minDur
		add(start(i), ref, 0)                 // s >= 0
		add(end(i), ref, 0)                   // e >= 0
	}

	// Numeric edges for every pair whose propagated relation is definite.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			if r := s.net.Relation(s.tasks[i].Name, s.tasks[j].Name); IsAtomic(r) {
				edges = append(edges, relationEdges(r, start(i), end(i), start(j), end(j))...)
			}
		}
	}

	for _, b := range s.bounds {
		p := end(b.task)
		if b.start {
			p = start(b.task)
		}
		if !math.IsInf(b.hi, 1) {
			add(ref, p, b.hi) // x_p <= hi
		}
		if !math.IsInf(b.lo, -1) {
			add(p, ref, -b.lo) // x_p >= lo
		}
	}

	d, ok := floydWarshall(1+2*n, edges)
	if !ok {
		return res
	}

	res.Consistent = true
	norm := func(v float64) float64 {
		if v == 0 {
			return 0 // normalize -0
		}
		return v
	}
	for i, t := range s.tasks {
		w := Window{
			EarliestStart: norm(-d[start(i)][ref]),
			LatestStart:   d[ref][start(i)],
			EarliestEnd:   norm(-d[end(i)][ref]),
			LatestEnd:     d[ref][end(i)],
		}
		res.Windows[t.Name] = w
		if w.EarliestEnd > res.EarliestFinish || i == 0 {
			res.EarliestFinish = w.EarliestEnd
		}
		if w.LatestEnd > res.LatestFinish || i == 0 {
			res.LatestFinish = w.LatestEnd
		}
	}
	return res
}

// relationEdges converts an atomic Allen relation Ra(X,Y) into STN edges over
// the endpoint variables (sx, ex, sy, ey), using non-strict semantics.
func relationEdges(r Relation, sx, ex, sy, ey int) []edge {
	le := func(from, to int) edge { return edge{from, to, 0} } // x[to] <= x[from]
	eq := func(a, b int) []edge { return []edge{le(a, b), le(b, a)} }
	switch r {
	case Before:
		return []edge{le(sy, ex)}
	case Meets:
		return eq(sy, ex)
	case Overlaps:
		return []edge{le(sy, sx), le(ex, sy), le(ey, ex)}
	case Starts:
		return append(eq(sx, sy), le(ey, ex))
	case During:
		return []edge{le(sx, sy), le(ey, ex)}
	case Finishes:
		return append(eq(ex, ey), le(sx, sy))
	case Equal:
		return append(eq(sx, sy), eq(ex, ey)...)
	default: // inverse relations: swap the roles of X and Y
		return relationEdges(Inverse(r), sy, ey, sx, ex)
	}
}
