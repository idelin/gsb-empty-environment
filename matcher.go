// Package matcher implements a maximum-weight matching for complete weighted
// bipartite graphs using the Kuhn-Munkres (Hungarian) algorithm.
//
// It supports dynamic updates: single-edge weight changes, masking
// (taking offline) of workers or tasks, and incremental re-matching that
// reuses the current vertex labels (potentials) and only re-runs alternating
// augmenting paths for the nodes affected by a mask event.
package matcher

import "math"

// forbidden marks edges that must never be matched (masked endpoints or
// padding rows/columns of the internal square matrix).
const forbidden = -1e15

// Match is one matched worker-task pair of a Result.
type Match struct {
	Worker int
	Task   int
	Weight float64
}

// Unmatched describes a node left out of the matching together with its
// opportunity cost: the weight of the best edge it could still have used.
type Unmatched struct {
	Side            string // "worker" or "task"
	Index           int
	OpportunityCost float64
}

// Result is the outcome of Solve.
type Result struct {
	Matches            []Match
	Total              float64
	Unmatched          []Unmatched
	MinOpportunityCost float64 // smallest opportunity cost among unmatched nodes; 0 when none
}

// Matcher solves max-weight matching on a complete bipartite graph of
// workers x tasks. Edge weights default to 0 and are set with SetWeight.
type Matcher struct {
	workers int
	tasks   int
	weight  [][]float64
	maskedW []bool
	maskedT []bool

	n      int // size of the internal square matrix
	a      [][]float64
	lx     []float64 // left (worker) labels
	ly     []float64 // right (task) labels
	matchL []int
	matchR []int
	built  bool
	dirty  bool // labels/matching must be rebuilt from scratch
}

// New creates a Matcher for a complete bipartite graph with the given
// numbers of workers and tasks. All edge weights start at 0.
func New(workers, tasks int) *Matcher {
	if workers < 0 || tasks < 0 {
		panic("matcher: negative dimension")
	}
	w := make([][]float64, workers)
	for i := range w {
		w[i] = make([]float64, tasks)
	}
	return &Matcher{
		workers: workers,
		tasks:   tasks,
		weight:  w,
		maskedW: make([]bool, workers),
		maskedT: make([]bool, tasks),
		dirty:   true,
	}
}

// SetWeight changes the weight of the edge (worker, task). It invalidates
// the current labels, so the next Solve re-optimizes from scratch.
func (m *Matcher) SetWeight(worker, task int, weight float64) {
	m.checkWorker(worker)
	m.checkTask(task)
	m.weight[worker][task] = weight
	m.dirty = true
}

// Weight returns the current weight of the edge (worker, task).
func (m *Matcher) Weight(worker, task int) float64 {
	m.checkWorker(worker)
	m.checkTask(task)
	return m.weight[worker][task]
}

// MaskWorker takes a worker offline (or back online when masked is false).
// Masking keeps the current labels feasible, so the next Solve only
// re-augments the pairs that were torn down; unmasking forces a full reset.
func (m *Matcher) MaskWorker(worker int, masked bool) {
	m.checkWorker(worker)
	if m.maskedW[worker] == masked {
		return
	}
	m.maskedW[worker] = masked
	if !masked {
		m.dirty = true
	}
}

// MaskTask takes a task offline (or back online when masked is false).
func (m *Matcher) MaskTask(task int, masked bool) {
	m.checkTask(task)
	if m.maskedT[task] == masked {
		return
	}
	m.maskedT[task] = masked
	if !masked {
		m.dirty = true
	}
}

// ResetLabels recomputes feasible vertex labels from the current weights
// (lx[i] = max incident edge weight, ly[j] = 0) and drops the current
// matching, so the next Solve rebuilds everything via augmenting paths.
func (m *Matcher) ResetLabels() {
	m.buildMatrix()
	m.lx = make([]float64, m.n)
	m.ly = make([]float64, m.n)
	m.matchL = make([]int, m.n)
	m.matchR = make([]int, m.n)
	for i := 0; i < m.n; i++ {
		best := math.Inf(-1)
		for j := 0; j < m.n; j++ {
			if m.a[i][j] > best {
				best = m.a[i][j]
			}
		}
		m.lx[i] = best
		m.ly[i] = 0
		m.matchL[i] = -1
		m.matchR[i] = -1
	}
	m.built = true
	m.dirty = false
}

// Labels returns copies of the current vertex labels (potentials).
func (m *Matcher) Labels() (lx, ly []float64) {
	return append([]float64(nil), m.lx...), append([]float64(nil), m.ly...)
}

// Solve computes a maximum-weight matching over the unmasked nodes and
// returns it together with the unmatched nodes and their opportunity costs.
func (m *Matcher) Solve() Result {
	m.buildMatrix()
	if m.dirty || !m.built {
		m.ResetLabels()
	} else {
		m.unmatchMasked()
	}
	for i := 0; i < m.n; i++ {
		if m.matchL[i] == -1 {
			m.augment(i)
		}
	}
	return m.extract()
}

func (m *Matcher) checkWorker(i int) {
	if i < 0 || i >= m.workers {
		panic("matcher: worker index out of range")
	}
}

func (m *Matcher) checkTask(j int) {
	if j < 0 || j >= m.tasks {
		panic("matcher: task index out of range")
	}
}

func (m *Matcher) buildMatrix() {
	n := m.workers
	if m.tasks > n {
		n = m.tasks
	}
	a := make([][]float64, n)
	for i := 0; i < n; i++ {
		a[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			switch {
			case i >= m.workers || j >= m.tasks:
				a[i][j] = 0 // padding dummy
			case m.maskedW[i] || m.maskedT[j]:
				a[i][j] = forbidden
			default:
				a[i][j] = m.weight[i][j]
			}
		}
	}
	m.n = n
	m.a = a
}

// unmatchMasked tears down only the matched pairs that touch a masked node,
// keeping every other pair and all labels intact for a fast local re-solve.
func (m *Matcher) unmatchMasked() {
	for i := 0; i < m.workers; i++ {
		if m.maskedW[i] && m.matchL[i] != -1 {
			m.matchR[m.matchL[i]] = -1
			m.matchL[i] = -1
		}
	}
	for j := 0; j < m.tasks; j++ {
		if m.maskedT[j] && m.matchR[j] != -1 {
			m.matchL[m.matchR[j]] = -1
			m.matchR[j] = -1
		}
	}
}

// augment grows an alternating tree from the free left node start over the
// equality subgraph, tightening labels by the minimum slack when stuck,
// until an exposed right node is reached and the path is flipped.
func (m *Matcher) augment(start int) {
	n := m.n
	slack := make([]float64, n)
	prev := make([]int, n)
	inTreeL := make([]bool, n)
	inTreeR := make([]bool, n)
	for j := 0; j < n; j++ {
		slack[j] = math.Inf(1)
	}
	relax := func(x int) {
		for y := 0; y < n; y++ {
			if inTreeR[y] {
				continue
			}
			if s := m.lx[x] + m.ly[y] - m.a[x][y]; s < slack[y] {
				slack[y] = s
				prev[y] = x
			}
		}
	}
	inTreeL[start] = true
	relax(start)
	for {
		delta := math.Inf(1)
		yMin := -1
		for y := 0; y < n; y++ {
			if !inTreeR[y] && slack[y] < delta {
				delta = slack[y]
				yMin = y
			}
		}
		if delta > 0 {
			for x := 0; x < n; x++ {
				if inTreeL[x] {
					m.lx[x] -= delta
				}
			}
			for y := 0; y < n; y++ {
				if inTreeR[y] {
					m.ly[y] += delta
				} else {
					slack[y] -= delta
				}
			}
		}
		y := yMin
		if m.matchR[y] == -1 {
			for {
				x := prev[y]
				old := m.matchL[x]
				m.matchL[x] = y
				m.matchR[y] = x
				if x == start {
					break
				}
				y = old
			}
			return
		}
		x := m.matchR[y]
		inTreeR[y] = true
		inTreeL[x] = true
		relax(x)
	}
}

func (m *Matcher) extract() Result {
	res := Result{}
	matchedW := make([]bool, m.workers)
	matchedT := make([]bool, m.tasks)
	for i := 0; i < m.workers; i++ {
		j := m.matchL[i]
		if j >= 0 && j < m.tasks && !m.maskedW[i] && !m.maskedT[j] {
			res.Matches = append(res.Matches, Match{Worker: i, Task: j, Weight: m.weight[i][j]})
			res.Total += m.weight[i][j]
			matchedW[i] = true
			matchedT[j] = true
		}
	}
	minCost := math.Inf(1)
	for i := 0; i < m.workers; i++ {
		if matchedW[i] || m.maskedW[i] {
			continue
		}
		cost := math.Inf(-1)
		for j := 0; j < m.tasks; j++ {
			if !m.maskedT[j] && m.weight[i][j] > cost {
				cost = m.weight[i][j]
			}
		}
		if cost == math.Inf(-1) {
			cost = 0
		}
		res.Unmatched = append(res.Unmatched, Unmatched{Side: "worker", Index: i, OpportunityCost: cost})
		if cost < minCost {
			minCost = cost
		}
	}
	for j := 0; j < m.tasks; j++ {
		if matchedT[j] || m.maskedT[j] {
			continue
		}
		cost := math.Inf(-1)
		for i := 0; i < m.workers; i++ {
			if !m.maskedW[i] && m.weight[i][j] > cost {
				cost = m.weight[i][j]
			}
		}
		if cost == math.Inf(-1) {
			cost = 0
		}
		res.Unmatched = append(res.Unmatched, Unmatched{Side: "task", Index: j, OpportunityCost: cost})
		if cost < minCost {
			minCost = cost
		}
	}
	if len(res.Unmatched) > 0 {
		res.MinOpportunityCost = minCost
	}
	return res
}
