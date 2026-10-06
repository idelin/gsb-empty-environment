package srs

// ResultKind classifies a search outcome.
type ResultKind int

const (
	// Unknown means the node budget was exhausted before the reachable
	// graph was fully explored; resume the Searcher with more budget.
	Unknown ResultKind = iota
	// Cycle means a reachable cycle was found.
	Cycle
	// NoCycle means the reachable graph is a finite DAG.
	NoCycle
)

// Result is the outcome of a (possibly partial) search.
type Result struct {
	Kind ResultKind
	// Entry is the shortest path from the start state to the cycle
	// (empty when the start state is on the cycle). Cycle only.
	Entry []Step
	// Cycle is a non-empty step sequence that returns the entry state
	// to itself. Cycle only.
	Cycle []Step
	// Longest is the length of the longest derivation from the start
	// state. NoCycle only.
	Longest int
	// LongestPath witnesses Longest. NoCycle only.
	LongestPath []Step
	// Expanded counts expanded nodes; States counts discovered states.
	Expanded int
	States   int
}

type arc struct {
	to   string
	step Step
}

type parentArc struct {
	from string
	step Step
}

// Searcher performs a pausable, resumable exploration of the full
// reachable state graph. Each call to Search spends up to a given node
// budget; when the budget runs out the result is Unknown and the
// Searcher keeps its frontier so the search can be resumed.
type Searcher struct {
	sys      *System
	start    State
	startKey string

	queue    []State // BFS frontier of discovered, unexpanded states
	states   map[string]State
	adj      map[string][]arc
	parent   map[string]parentArc // first-discovery parent (shortest path)
	dist     map[string]int
	expanded int

	done   bool
	result Result
}

// NewSearcher creates a Searcher rooted at start. The start resource
// vector is padded with zeros to NumRes.
func NewSearcher(sys *System, start State) (*Searcher, error) {
	if err := sys.Validate(); err != nil {
		return nil, err
	}
	res := make([]int, sys.NumRes)
	copy(res, start.Res)
	start.Res = res
	key := stateKey(start)
	s := &Searcher{
		sys:      sys,
		start:    start,
		startKey: key,
		states:   map[string]State{key: start},
		adj:      make(map[string][]arc),
		parent:   make(map[string]parentArc),
		dist:     map[string]int{key: 0},
	}
	s.queue = append(s.queue, start)
	return s, nil
}

// Search expands up to nodeBudget more nodes and reports the outcome.
// It returns Unknown while unexplored frontier remains and the budget
// is spent; call Search again with more budget to resume. Once the
// reachable graph is fully explored the final answer (Cycle or
// NoCycle) is computed and cached, so later calls are free. A
// nodeBudget <= 0 checks the current state without expanding.
func (s *Searcher) Search(nodeBudget int) Result {
	if s.done {
		return s.result
	}
	for nodeBudget > 0 && len(s.queue) > 0 {
		st := s.queue[0]
		s.queue = s.queue[1:]
		s.expand(st)
		s.expanded++
		nodeBudget--
	}
	if len(s.queue) > 0 {
		return Result{Kind: Unknown, Expanded: s.expanded, States: len(s.states)}
	}
	s.done = true
	s.result = s.analyze()
	s.result.Expanded = s.expanded
	s.result.States = len(s.states)
	return s.result
}

func (s *Searcher) expand(st State) {
	fromKey := stateKey(st)
	for _, succ := range s.sys.Successors(st) {
		toKey := stateKey(succ.State)
		s.adj[fromKey] = append(s.adj[fromKey], arc{to: toKey, step: succ.Step})
		if _, seen := s.states[toKey]; !seen {
			s.states[toKey] = succ.State
			s.parent[toKey] = parentArc{from: fromKey, step: succ.Step}
			s.dist[toKey] = s.dist[fromKey] + 1
			s.queue = append(s.queue, succ.State)
		}
	}
}

// analyze runs on the fully explored reachable graph and decides
// between Cycle and NoCycle.
func (s *Searcher) analyze() Result {
	sccs := s.sccs()
	cyclic := make(map[string]bool)
	for _, comp := range sccs {
		if len(comp) > 1 {
			for _, k := range comp {
				cyclic[k] = true
			}
		} else if s.hasSelfLoop(comp[0]) {
			cyclic[comp[0]] = true
		}
	}
	if len(cyclic) == 0 {
		longest, path := s.longestDerivation()
		return Result{Kind: NoCycle, Longest: longest, LongestPath: path}
	}
	// Entry node: the closest cyclic state to the start. BFS
	// discovery makes dist minimal, so this yields the shortest
	// possible entry path.
	entry := ""
	for k := range cyclic {
		if entry == "" || s.dist[k] < s.dist[entry] {
			entry = k
		}
	}
	return Result{
		Kind:  Cycle,
		Entry: s.pathTo(entry),
		Cycle: s.cycleThrough(entry, cyclic),
	}
}

func (s *Searcher) hasSelfLoop(key string) bool {
	for _, a := range s.adj[key] {
		if a.to == key {
			return true
		}
	}
	return false
}

// pathTo reconstructs the shortest path from the start state to key
// using first-discovery parents.
func (s *Searcher) pathTo(key string) []Step {
	var rev []Step
	for key != s.startKey {
		p := s.parent[key]
		rev = append(rev, p.step)
		key = p.from
	}
	steps := make([]Step, len(rev))
	for i, st := range rev {
		steps[len(rev)-1-i] = st
	}
	return steps
}

// cycleThrough finds the shortest non-empty cycle from entry back to
// itself, staying inside cyclic states (the entry node's SCC).
func (s *Searcher) cycleThrough(entry string, cyclic map[string]bool) []Step {
	type item struct {
		key  string
		path []Step
	}
	seen := map[string]bool{entry: true}
	var queue []item
	for _, a := range s.adj[entry] {
		if !cyclic[a.to] {
			continue
		}
		if a.to == entry {
			return []Step{a.step}
		}
		if !seen[a.to] {
			seen[a.to] = true
			queue = append(queue, item{a.to, []Step{a.step}})
		}
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		for _, a := range s.adj[it.key] {
			if !cyclic[a.to] {
				continue
			}
			if a.to == entry {
				return append(it.path, a.step)
			}
			if !seen[a.to] {
				seen[a.to] = true
				queue = append(queue, item{a.to, append(append([]Step{}, it.path...), a.step)})
			}
		}
	}
	return nil // unreachable for a truly cyclic SCC
}

// sccs computes the strongly connected components of the reachable
// graph with Tarjan's algorithm (iterative to avoid stack depth
// limits on long derivations).
func (s *Searcher) sccs() [][]string {
	index := make(map[string]int)
	low := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	counter := 0
	var sccs [][]string

	type frame struct {
		key string
		arc int
	}
	for key := range s.states {
		if _, seen := index[key]; seen {
			continue
		}
		call := []frame{{key: key}}
		for len(call) > 0 {
			f := &call[len(call)-1]
			if f.arc == 0 {
				index[f.key] = counter
				low[f.key] = counter
				counter++
				stack = append(stack, f.key)
				onStack[f.key] = true
			}
			if f.arc < len(s.adj[f.key]) {
				to := s.adj[f.key][f.arc].to
				f.arc++
				if _, seen := index[to]; !seen {
					call = append(call, frame{key: to})
				} else if onStack[to] && index[to] < low[f.key] {
					low[f.key] = index[to]
				}
				continue
			}
			call = call[:len(call)-1]
			if low[f.key] == index[f.key] {
				var comp []string
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[w] = false
					comp = append(comp, w)
					if w == f.key {
						break
					}
				}
				sccs = append(sccs, comp)
			}
			if len(call) > 0 {
				p := &call[len(call)-1]
				if low[f.key] < low[p.key] {
					low[p.key] = low[f.key]
				}
			}
		}
	}
	return sccs
}

// longestDerivation returns the longest derivation length from the
// start state and a witness path. Only called on a DAG.
func (s *Searcher) longestDerivation() (int, []Step) {
	memo := make(map[string]int)
	next := make(map[string]arc)
	var solve func(key string) int
	solve = func(key string) int {
		if v, ok := memo[key]; ok {
			return v
		}
		best := 0
		for _, a := range s.adj[key] {
			if v := 1 + solve(a.to); v > best {
				best = v
				next[key] = a
			}
		}
		memo[key] = best
		return best
	}
	longest := solve(s.startKey)
	var path []Step
	for key := s.startKey; ; {
		a, ok := next[key]
		if !ok {
			break
		}
		path = append(path, a.step)
		key = a.to
	}
	return longest, path
}
