package main

import (
	"fmt"
	"sort"
	"strings"
)

// Resource is a renewable resource with a per-slot capacity.
type Resource struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

// Job is a non-preemptive activity. A zero-duration job occupies no time
// slots and consumes no resources, but its precedence relations still hold.
type Job struct {
	ID       string         `json:"id"`
	Duration int            `json:"duration"`
	Preds    []string       `json:"preds"`
	Demand   map[string]int `json:"demand"`
}

// Instance is the full problem input. Horizon is the number of discrete
// slots (0..Horizon-1); every job must complete by Horizon.
type Instance struct {
	Horizon   int        `json:"horizon"`
	Resources []Resource `json:"resources"`
	Jobs      []Job      `json:"jobs"`
}

// model is the indexed internal form of an Instance.
type model struct {
	n      int
	r      int
	h      int
	dur    []int
	pred   [][]int
	succ   [][]int
	dem    [][]int
	cap    []int
	ids    []string
	rnames []string
}

// buildModel validates the instance and converts it to indexed form.
// A non-empty string result is a hard input error (not mere infeasibility).
func buildModel(in *Instance) (*model, string) {
	if in.Horizon <= 0 {
		return nil, "horizon must be positive"
	}
	if len(in.Jobs) == 0 {
		return nil, "instance has no jobs"
	}
	if len(in.Jobs) > 12 {
		return nil, fmt.Sprintf("too many jobs: %d (max 12)", len(in.Jobs))
	}
	m := &model{n: len(in.Jobs), r: len(in.Resources), h: in.Horizon}
	index := make(map[string]int, m.n)
	for i, j := range in.Jobs {
		if j.ID == "" {
			return nil, fmt.Sprintf("job %d has empty id", i)
		}
		if _, dup := index[j.ID]; dup {
			return nil, "duplicate job id: " + j.ID
		}
		if j.Duration < 0 {
			return nil, "job " + j.ID + " has negative duration"
		}
		index[j.ID] = i
		m.ids = append(m.ids, j.ID)
		m.dur = append(m.dur, j.Duration)
	}
	seenRes := make(map[string]bool, m.r)
	for _, rs := range in.Resources {
		if rs.Name == "" {
			return nil, "resource with empty name"
		}
		if seenRes[rs.Name] {
			return nil, "duplicate resource: " + rs.Name
		}
		seenRes[rs.Name] = true
		if rs.Capacity < 0 {
			return nil, "resource " + rs.Name + " has negative capacity"
		}
		m.rnames = append(m.rnames, rs.Name)
		m.cap = append(m.cap, rs.Capacity)
	}
	rindex := make(map[string]int, m.r)
	for i, name := range m.rnames {
		rindex[name] = i
	}
	m.pred = make([][]int, m.n)
	m.succ = make([][]int, m.n)
	m.dem = make([][]int, m.n)
	for i, j := range in.Jobs {
		m.dem[i] = make([]int, m.r)
		for name, amt := range j.Demand {
			ri, ok := rindex[name]
			if !ok {
				return nil, "job " + j.ID + " demands unknown resource " + name
			}
			if amt < 0 {
				return nil, "job " + j.ID + " has negative demand for " + name
			}
			if amt > 0 && j.Duration == 0 {
				return nil, "zero-duration job " + j.ID + " cannot demand resource " + name
			}
			m.dem[i][ri] = amt
		}
		seen := make(map[string]bool)
		for _, p := range j.Preds {
			pi, ok := index[p]
			if !ok {
				return nil, "job " + j.ID + " has unknown predecessor " + p
			}
			if seen[p] {
				continue
			}
			seen[p] = true
			m.pred[i] = append(m.pred[i], pi)
			m.succ[pi] = append(m.succ[pi], i)
		}
	}
	return m, ""
}

// findCycle returns one precedence cycle as a job-index path, or nil.
func (m *model) findCycle() []int {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, m.n)
	var stack []int
	var cycle []int
	var dfs func(u int) bool
	dfs = func(u int) bool {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range m.succ[u] {
			if color[v] == gray {
				k := 0
				for stack[k] != v {
					k++
				}
				cycle = append(cycle, stack[k:]...)
				cycle = append(cycle, v)
				return true
			}
			if color[v] == white && dfs(v) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return false
	}
	for u := 0; u < m.n; u++ {
		if color[u] == white && dfs(u) {
			return cycle
		}
	}
	return nil
}

// cpmBounds returns earliest starts (longest paths from sources) and, for a
// makespan bound bound, latest starts. Valid only on acyclic precedence.
func (m *model) cpmBounds(bound int) (est, lst []int) {
	est = make([]int, m.n)
	lst = make([]int, m.n)
	order := m.topoOrder()
	for _, u := range order {
		for _, p := range m.pred[u] {
			if est[p]+m.dur[p] > est[u] {
				est[u] = est[p] + m.dur[p]
			}
		}
	}
	for i := 0; i < m.n; i++ {
		lst[i] = bound - m.dur[i]
	}
	for k := len(order) - 1; k >= 0; k-- {
		u := order[k]
		for _, v := range m.succ[u] {
			if lst[v]-m.dur[u] < lst[u] {
				lst[u] = lst[v] - m.dur[u]
			}
		}
	}
	return est, lst
}

func (m *model) topoOrder() []int {
	indeg := make([]int, m.n)
	for u := 0; u < m.n; u++ {
		indeg[u] = len(m.pred[u])
	}
	var queue []int
	for u := 0; u < m.n; u++ {
		if indeg[u] == 0 {
			queue = append(queue, u)
		}
	}
	var order []int
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, v := range m.succ[u] {
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	return order
}

// lowerBound is the max of the critical-path bound and per-resource work bounds.
func (m *model) lowerBound() int {
	est, _ := m.cpmBounds(m.h)
	lb := 0
	for u := 0; u < m.n; u++ {
		if est[u]+m.dur[u] > lb {
			lb = est[u] + m.dur[u]
		}
	}
	for ri := 0; ri < m.r; ri++ {
		if m.cap[ri] <= 0 {
			continue
		}
		work := 0
		for u := 0; u < m.n; u++ {
			work += m.dur[u] * m.dem[u][ri]
		}
		if rb := (work + m.cap[ri] - 1) / m.cap[ri]; rb > lb {
			lb = rb
		}
	}
	return lb
}

func (m *model) cycleString(cycle []int) string {
	parts := make([]string, len(cycle))
	for i, u := range cycle {
		parts[i] = m.ids[u]
	}
	return strings.Join(parts, " -> ")
}

// demandOverflow returns jobs whose demand exceeds some resource capacity.
func (m *model) demandOverflow() []string {
	var bad []string
	for u := 0; u < m.n; u++ {
		for ri := 0; ri < m.r; ri++ {
			if m.dem[u][ri] > m.cap[ri] {
				bad = append(bad, fmt.Sprintf("%s needs %d of %s (capacity %d)",
					m.ids[u], m.dem[u][ri], m.rnames[ri], m.cap[ri]))
			}
		}
	}
	sort.Strings(bad)
	return bad
}
