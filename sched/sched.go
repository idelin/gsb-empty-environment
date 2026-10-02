// Package sched builds on the allen package to solve scheduling
// problems: tasks with durations, Allen interval-algebra constraints
// between them, and absolute time constraints. It propagates the
// temporal network, converts the result into a simple temporal problem
// (STP) over interval endpoints, and computes the earliest/latest
// feasible time window of every task plus the project-level window and
// critical path.
package sched

import (
	"fmt"
	"math"
	"sort"

	"temporal/allen"
)

// Task is a schedulable activity with a duration range (in abstract
// time units, e.g. days).
type Task struct {
	Name        string
	MinDuration float64
	MaxDuration float64 // use math.Inf(1) for unbounded
}

// Window is a feasible time interval for a single time point.
// Exclusive flags mark open bounds (e.g. "must start before day 5"
// yields Latest=5 with LatestExclusive=true).
type Window struct {
	Earliest          float64
	Latest            float64
	EarliestExclusive bool
	LatestExclusive   bool
}

// TaskWindow holds the feasible windows of one task's start and end.
type TaskWindow struct {
	Name       string
	Start, End Window
}

// Result is the outcome of a successful Solve.
type Result struct {
	Tasks        []TaskWindow
	Project      Window   // window of the project end point
	CriticalPath []string // task names on the longest chain, in order
}

type constraintKind int

const (
	startBefore constraintKind = iota
	startBy
	endBefore
	endBy
)

type pointConstraint struct {
	kind  constraintKind
	task  int
	value float64
}

type relConstraint struct {
	i, j int
	r    allen.Relation
}

// constraint is x[to] - x[from] <= c (strictly < c when strict).
type constraint struct {
	from, to int
	c        float64
	strict   bool
	tasks    []int // tasks involved, for critical-path attribution
}

// Solver accumulates tasks and constraints and solves them together.
type Solver struct {
	tasks       []Task
	relations   []relConstraint
	points      []pointConstraint
	deadline    float64
	hasDeadline bool
	net         *allen.Network
}

func NewSolver() *Solver { return &Solver{} }

// AddTask registers a task and returns its index.
func (s *Solver) AddTask(name string, minDuration, maxDuration float64) int {
	s.tasks = append(s.tasks, Task{
		Name:        name,
		MinDuration: minDuration,
		MaxDuration: maxDuration,
	})
	return len(s.tasks) - 1
}

// AddRelation constrains the Allen relation between tasks i and j,
// e.g. AddRelation(a, b, allen.Before) for "a must end before b".
func (s *Solver) AddRelation(i, j int, r allen.Relation) {
	s.relations = append(s.relations, relConstraint{i: i, j: j, r: r})
}

// MustStartBefore constrains the task to start strictly before time t.
func (s *Solver) MustStartBefore(task int, t float64) {
	s.points = append(s.points, pointConstraint{kind: startBefore, task: task, value: t})
}

// MustStartBy constrains the task to start at or before time t.
func (s *Solver) MustStartBy(task int, t float64) {
	s.points = append(s.points, pointConstraint{kind: startBy, task: task, value: t})
}

// MustEndBefore constrains the task to end strictly before time t.
func (s *Solver) MustEndBefore(task int, t float64) {
	s.points = append(s.points, pointConstraint{kind: endBefore, task: task, value: t})
}

// MustEndBy constrains the task to end at or before time t.
func (s *Solver) MustEndBy(task int, t float64) {
	s.points = append(s.points, pointConstraint{kind: endBy, task: task, value: t})
}

// SetDeadline constrains the whole project to end at or before time t.
func (s *Solver) SetDeadline(t float64) {
	s.deadline = t
	s.hasDeadline = true
}

// Network returns the propagated Allen constraint network. Valid only
// after a successful Solve.
func (s *Solver) Network() *allen.Network { return s.net }

// point ids: 0 is the zero reference (project start, time 0),
// then two points per task, then the project end point.
func (s *Solver) zero() int       { return 0 }
func (s *Solver) start(i int) int { return 1 + 2*i }
func (s *Solver) end(i int) int   { return 2 + 2*i }
func (s *Solver) proj() int       { return 1 + 2*len(s.tasks) }

// bound is an upper bound value with a strictness flag; inf marks the
// absence of a bound.
type bound struct {
	val    float64
	strict bool
	inf    bool
}

var infBound = bound{inf: true}

func (b bound) less(o bound) bool {
	if b.inf {
		return false
	}
	if o.inf {
		return true
	}
	if b.val != o.val {
		return b.val < o.val
	}
	return b.strict && !o.strict
}

func (b bound) add(o bound) bound {
	if b.inf || o.inf {
		return infBound
	}
	return bound{val: b.val + o.val, strict: b.strict || o.strict}
}

// Solve propagates the Allen network, derives the STP over endpoints,
// and computes all feasible windows. It returns an error if the
// constraints are inconsistent at either level.
func (s *Solver) Solve() (*Result, error) {
	n := len(s.tasks)
	if n == 0 {
		return nil, fmt.Errorf("no tasks")
	}

	// Level 1: Allen interval-algebra propagation (transitive closure).
	net := allen.NewNetwork(n)
	for _, rc := range s.relations {
		net.Constrain(rc.i, rc.j, rc.r)
	}
	if !net.Propagate() {
		return nil, fmt.Errorf("inconsistent Allen relation network")
	}
	s.net = net

	// Level 2: STP over endpoints.
	var cons []constraint
	add := func(from, to int, c float64, strict bool, tasks ...int) {
		cons = append(cons, constraint{from: from, to: to, c: c, strict: strict, tasks: tasks})
	}
	zero, proj := s.zero(), s.proj()

	for i, t := range s.tasks {
		// Duration: min <= end - start <= max.
		add(s.end(i), s.start(i), -t.MinDuration, false, i)
		if math.IsInf(t.MaxDuration, 1) {
			add(s.end(i), s.start(i), 0, true, i) // intervals are non-empty
		} else {
			add(s.start(i), s.end(i), t.MaxDuration, false, i)
		}
		add(s.start(i), zero, 0, false, i) // start >= 0
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			for _, ec := range allen.EndpointConstraints(net.Relation(i, j)) {
				add(s.mapEndpoint(ec.From, i, j), s.mapEndpoint(ec.To, i, j),
					0, ec.Strict, i, j)
			}
		}
	}
	for _, pc := range s.points {
		switch pc.kind {
		case startBefore:
			add(zero, s.start(pc.task), pc.value, true, pc.task)
		case startBy:
			add(zero, s.start(pc.task), pc.value, false, pc.task)
		case endBefore:
			add(zero, s.end(pc.task), pc.value, true, pc.task)
		case endBy:
			add(zero, s.end(pc.task), pc.value, false, pc.task)
		}
	}
	// Project end point: at or after every task end.
	for i := range s.tasks {
		add(proj, s.end(i), 0, false)
	}
	add(proj, zero, 0, false)
	if s.hasDeadline {
		add(zero, proj, s.deadline, false)
	}

	// Floyd-Warshall over (value, strict) bounds.
	np := proj + 1
	d := make([][]bound, np)
	for i := range d {
		d[i] = make([]bound, np)
		for j := range d[i] {
			d[i][j] = infBound
		}
	}
	for _, c := range cons {
		b := bound{val: c.c, strict: c.strict}
		if b.less(d[c.from][c.to]) {
			d[c.from][c.to] = b
		}
	}
	for k := 0; k < np; k++ {
		for i := 0; i < np; i++ {
			if d[i][k].inf {
				continue
			}
			for j := 0; j < np; j++ {
				if t := d[i][k].add(d[k][j]); t.less(d[i][j]) {
					d[i][j] = t
				}
			}
		}
	}
	for i := 0; i < np; i++ {
		if d[i][i].less(bound{}) {
			return nil, fmt.Errorf("inconsistent temporal constraints (negative cycle)")
		}
	}

	window := func(p int) Window {
		w := Window{Earliest: math.Inf(-1), Latest: math.Inf(1)}
		if lb := d[p][zero]; !lb.inf {
			w.Earliest = -lb.val
			w.EarliestExclusive = lb.strict
		}
		if ub := d[zero][p]; !ub.inf {
			w.Latest = ub.val
			w.LatestExclusive = ub.strict
		}
		return w
	}

	res := &Result{Project: window(proj)}
	for i, t := range s.tasks {
		res.Tasks = append(res.Tasks, TaskWindow{
			Name:  t.Name,
			Start: window(s.start(i)),
			End:   window(s.end(i)),
		})
	}
	res.CriticalPath = s.criticalPath(cons, np, zero, proj)
	return res, nil
}

func (s *Solver) mapEndpoint(local, i, j int) int {
	switch local {
	case allen.StartX:
		return s.start(i)
	case allen.EndX:
		return s.end(i)
	case allen.StartY:
		return s.start(j)
	default: // allen.EndY
		return s.end(j)
	}
}

// criticalPath finds the tasks lying on a longest chain of tight
// constraints from the project start to the project end.
func (s *Solver) criticalPath(cons []constraint, np, zero, proj int) []string {
	type edge struct {
		from, to int
		w        float64
	}
	edges := make([]edge, 0, len(cons))
	for _, c := range cons {
		// x[to] - x[from] <= c  <=>  x[from] >= x[to] - c
		edges = append(edges, edge{from: c.to, to: c.from, w: -c.c})
	}
	longest := func(source int, es []edge) []float64 {
		dist := make([]float64, np)
		for i := range dist {
			dist[i] = math.Inf(-1)
		}
		dist[source] = 0
		for iter := 0; iter < np; iter++ {
			updated := false
			for _, e := range es {
				if math.IsInf(dist[e.from], -1) {
					continue
				}
				if v := dist[e.from] + e.w; v > dist[e.to]+1e-9 {
					dist[e.to] = v
					updated = true
				}
			}
			if !updated {
				break
			}
		}
		return dist
	}
	est := longest(zero, edges)
	rev := make([]edge, len(edges))
	for i, e := range edges {
		rev[i] = edge{from: e.to, to: e.from, w: e.w}
	}
	toEnd := longest(proj, rev)

	makespan := est[proj]
	if math.IsInf(makespan, -1) {
		return nil
	}
	criticalPoint := func(p int) bool {
		if math.IsInf(est[p], -1) || math.IsInf(toEnd[p], -1) {
			return false
		}
		return math.Abs(est[p]+toEnd[p]-makespan) <= 1e-6
	}
	type critTask struct {
		idx      int
		est, eet float64
		name     string
	}
	var crit []critTask
	for i, t := range s.tasks {
		if criticalPoint(s.start(i)) || criticalPoint(s.end(i)) {
			crit = append(crit, critTask{
				idx:  i,
				est:  est[s.start(i)],
				eet:  est[s.end(i)],
				name: t.Name,
			})
		}
	}
	sort.Slice(crit, func(a, b int) bool {
		if crit[a].est != crit[b].est {
			return crit[a].est < crit[b].est
		}
		if crit[a].eet != crit[b].eet {
			return crit[a].eet < crit[b].eet
		}
		return crit[a].name < crit[b].name
	})
	var path []string
	for _, c := range crit {
		path = append(path, c.name)
	}
	return path
}
