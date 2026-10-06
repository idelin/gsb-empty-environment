package rewrite

// Status is the outcome of a search.
type Status int

const (
	// Unknown means the node budget was exhausted before the reachable
	// graph was fully explored. Call Run again with more budget.
	Unknown Status = iota
	// FoundCycle means a reachable cycle exists; Result holds the
	// shortest entry path and cycle evidence.
	FoundCycle
	// NoCycle means the reachable graph is a DAG; Result.Longest holds
	// the longest derivation length from the initial state.
	NoCycle
)

func (s Status) String() string {
	switch s {
	case FoundCycle:
		return "FoundCycle"
	case NoCycle:
		return "NoCycle"
	default:
		return "Unknown"
	}
}

// Result is the outcome of a (possibly final) Run.
type Result struct {
	Status Status
	// Entry is the shortest sequence of steps from the initial state to
	// the cycle start. Set only for FoundCycle.
	Entry []Step
	// Cycle is the sequence of steps that returns to the cycle start
	// state (string, control state and resources all equal). Set only
	// for FoundCycle.
	Cycle []Step
	// Longest is the longest derivation length (in steps) from the
	// initial state. Set only for NoCycle.
	Longest int
	// Explored is the number of states expanded so far.
	Explored int
}

type node struct {
	state    State
	depth    int
	parent   int
	via      Step
	succ     []int
	succStep []Step
}

// Searcher incrementally explores the full reachable state graph with
// breadth-first order. It supports pause/resume: Run expands at most a
// budget of new states per call and can be called repeatedly.
type Searcher struct {
	cfg      Config
	nodes    []node
	index    map[string]int
	queue    []int
	explored int
	result   *Result
}

// NewSearcher validates cfg and returns a Searcher positioned at the
// initial state.
func NewSearcher(cfg Config) (*Searcher, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &Searcher{cfg: cfg, index: make(map[string]int)}
	s.index[cfg.Init.key()] = 0
	s.nodes = append(s.nodes, node{state: cfg.Init, parent: -1})
	s.queue = append(s.queue, 0)
	return s, nil
}

// Search is a one-shot convenience wrapper around NewSearcher and Run.
func Search(cfg Config, budget int) (Result, error) {
	s, err := NewSearcher(cfg)
	if err != nil {
		return Result{}, err
	}
	return s.Run(budget), nil
}

// Pending returns the number of discovered states not yet expanded.
func (s *Searcher) Pending() int { return len(s.queue) }

// Explored returns the number of states expanded so far.
func (s *Searcher) Explored() int { return s.explored }

// Run expands at most budget further states. It returns Unknown while the
// frontier is non-empty and the budget is spent; once the reachable graph
// is fully explored it returns the final FoundCycle or NoCycle result.
// A final result is cached, so extra calls are cheap and idempotent.
func (s *Searcher) Run(budget int) Result {
	if s.result != nil {
		return *s.result
	}
	for len(s.queue) > 0 {
		if budget <= 0 {
			return Result{Status: Unknown, Explored: s.explored}
		}
		u := s.queue[0]
		s.queue = s.queue[1:]
		s.expand(u)
		s.explored++
		budget--
	}
	res := s.analyze()
	s.result = &res
	return res
}

func (s *Searcher) expand(u int) {
	type out struct {
		st   State
		step Step
	}
	var outs []out
	s.cfg.successors(s.nodes[u].state, func(ns State, st Step) {
		outs = append(outs, out{ns, st})
	})
	for _, o := range outs {
		key := o.st.key()
		v, ok := s.index[key]
		if !ok {
			v = len(s.nodes)
			s.index[key] = v
			s.nodes = append(s.nodes, node{
				state:  o.st,
				depth:  s.nodes[u].depth + 1,
				parent: u,
				via:    o.step,
			})
			s.queue = append(s.queue, v)
		}
		s.nodes[u].succ = append(s.nodes[u].succ, v)
		s.nodes[u].succStep = append(s.nodes[u].succStep, o.step)
	}
}

// analyze runs on the fully explored graph: it looks for a cycle with the
// shortest entry path, or computes the longest derivation in the DAG.
func (s *Searcher) analyze() Result {
	cyc := s.minDepthCycleNode()
	if cyc < 0 {
		return Result{Status: NoCycle, Longest: s.longest(), Explored: s.explored}
	}
	return Result{
		Status:   FoundCycle,
		Entry:    s.pathTo(cyc),
		Cycle:    s.cycleThrough(cyc),
		Explored: s.explored,
	}
}

// minDepthCycleNode returns the shallowest node that lies on a cycle
// (member of a non-trivial strongly connected component), or -1.
func (s *Searcher) minDepthCycleNode() int {
	n := len(s.nodes)
	idx := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range idx {
		idx[i] = -1
	}
	var stack []int
	counter := 0
	best := -1
	consider := func(v int) {
		if best < 0 || s.nodes[v].depth < s.nodes[best].depth {
			best = v
		}
	}
	var connect func(v int)
	connect = func(v int) {
		idx[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range s.nodes[v].succ {
			if idx[w] < 0 {
				connect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && idx[w] < low[v] {
				low[v] = idx[w]
			}
		}
		if low[v] == idx[v] {
			var comp []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			if len(comp) > 1 {
				for _, w := range comp {
					consider(w)
				}
			} else {
				// singleton: cyclic only via a self-loop
				v := comp[0]
				for _, w := range s.nodes[v].succ {
					if w == v {
						consider(v)
						break
					}
				}
			}
		}
	}
	for v := 0; v < n; v++ {
		if idx[v] < 0 {
			connect(v)
		}
	}
	return best
}

// cycleThrough returns the shortest sequence of steps that leads from node
// m back to m. m must lie on a cycle.
func (s *Searcher) cycleThrough(m int) []Step {
	prev := make([]int, len(s.nodes))
	prevStep := make([]Step, len(s.nodes))
	seen := make([]bool, len(s.nodes))
	var q []int
	for i, w := range s.nodes[m].succ {
		if w == m {
			return []Step{s.nodes[m].succStep[i]}
		}
		if !seen[w] {
			seen[w] = true
			prev[w] = m
			prevStep[w] = s.nodes[m].succStep[i]
			q = append(q, w)
		}
	}
	for len(q) > 0 {
		x := q[0]
		q = q[1:]
		for i, w := range s.nodes[x].succ {
			if w == m {
				rev := []Step{s.nodes[x].succStep[i]}
				for y := x; y != m; y = prev[y] {
					rev = append(rev, prevStep[y])
				}
				out := make([]Step, len(rev))
				for j := range rev {
					out[len(rev)-1-j] = rev[j]
				}
				return out
			}
			if !seen[w] {
				seen[w] = true
				prev[w] = x
				prevStep[w] = s.nodes[x].succStep[i]
				q = append(q, w)
			}
		}
	}
	return nil
}

// pathTo returns the shortest (BFS) step sequence from the initial state
// to node x.
func (s *Searcher) pathTo(x int) []Step {
	var rev []Step
	for x != 0 {
		rev = append(rev, s.nodes[x].via)
		x = s.nodes[x].parent
	}
	out := make([]Step, len(rev))
	for i := range rev {
		out[len(rev)-1-i] = rev[i]
	}
	return out
}

// longest returns the longest path length (in edges) from the initial
// node. It must only be called on an acyclic explored graph.
func (s *Searcher) longest() int {
	memo := make([]int, len(s.nodes))
	done := make([]bool, len(s.nodes))
	var dfs func(x int) int
	dfs = func(x int) int {
		if done[x] {
			return memo[x]
		}
		done[x] = true
		best := 0
		for _, w := range s.nodes[x].succ {
			if d := dfs(w) + 1; d > best {
				best = d
			}
		}
		memo[x] = best
		return best
	}
	return dfs(0)
}

// Edge is one labeled transition of the explored graph.
type Edge struct {
	From int
	To   int
	Step Step
}

// Snapshot returns the full explored reachable graph: every discovered
// state and every expanded transition. It allows callers to audit
// overlapping matches, empty right-hand sides and discarded successors.
func (s *Searcher) Snapshot() ([]State, []Edge) {
	states := make([]State, len(s.nodes))
	var edges []Edge
	for i, n := range s.nodes {
		states[i] = n.state
		for j, w := range n.succ {
			edges = append(edges, Edge{From: i, To: w, Step: n.succStep[j]})
		}
	}
	return states, edges
}
