package recovery

import "sort"

// actionKind distinguishes the two fixed action types.
type actionKind uint8

const (
	kindCommand actionKind = iota + 1
	kindProbe
)

// action is one entry of the fixed action library, normalized for planning.
type action struct {
	kind actionKind
	name string
	cost int
	cmd  Command
	pr   Probe
}

// Solution is the synthesized minimax strategy plus bookkeeping for auditing.
type Solution struct {
	p        *Problem
	initial  belief
	actions  []action
	nodeKeys []string
	policy   map[string]action
	value    map[string]int
	losing   map[string]bool
	cert     *Certificate
}

// DefaultNodeCap bounds the reachable belief growth. The task limits are small
// (10 transactions, 6 keys, 12 starting sites), so the default is generous.
const DefaultNodeCap = 100000

// Solve synthesizes the minimum worst-case total-cost strategy. When no
// guaranteed strategy exists it returns a checkable impossibility certificate.
func (p *Problem) Solve() (*Solution, error) {
	return p.SolveWithCap(DefaultNodeCap)
}

// SolveWithCap is Solve with an explicit bound on the number of distinct
// reachable belief states explored.
func (p *Problem) SolveWithCap(cap int) (*Solution, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	sol := &Solution{
		p:       p,
		initial: p.initialBelief(),
		policy:  map[string]action{},
		value:   map[string]int{},
		losing:  map[string]bool{},
	}
	for _, c := range p.Commands {
		sol.actions = append(sol.actions, action{kind: kindCommand, name: c.Name, cost: c.Cost, cmd: c})
	}
	for _, pr := range p.Probes {
		sol.actions = append(sol.actions, action{kind: kindProbe, name: pr.Name, cost: pr.Cost, pr: pr})
	}
	sort.SliceStable(sol.actions, func(i, j int) bool { return sol.actions[i].name < sol.actions[j].name })

	nodes, edges, err := sol.explore(cap)
	if err != nil {
		return nil, err
	}
	sol.nodeKeys = nodes
	sol.valueIterate(nodes, edges)
	sol.buildCertificate(nodes, edges)
	if sol.losing[sol.initial.key()] {
		sol.policy = nil
		return sol, nil
	}
	return sol, nil
}

type edge struct {
	cost int
	obs  []string
}

func (s *Solution) explore(cap int) ([]string, map[string]map[string]edge, error) {
	idx := map[string]belief{s.initial.key(): s.initial}
	queue := []string{s.initial.key()}
	edges := map[string]map[string]edge{}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if len(idx) > cap {
			return nil, nil, ErrBeliefCapExceeded
		}
		b := idx[key]
		if s.p.atGoal(b) {
			continue
		}
		edges[key] = map[string]edge{}
		for _, a := range s.actions {
			var obs []observation
			if a.kind == kindCommand {
				obs = s.p.applyCommit(b, a.cmd)
			} else {
				obs = s.p.applyProbe(b, a.pr)
			}
			next := make([]string, 0, len(obs))
			for _, o := range obs {
				nk := o.b.key()
				if _, ok := idx[nk]; !ok {
					idx[nk] = o.b
					queue = append(queue, nk)
				}
				next = append(next, nk)
			}
			edges[key][a.name] = edge{cost: a.cost, obs: next}
		}
	}
	nodes := make([]string, 0, len(idx))
	for k := range idx {
		nodes = append(nodes, k)
	}
	sort.Strings(nodes)
	return nodes, edges, nil
}

// valueIterate solves the finite one-controller minimax stochastic shortest
// path with monotone worklist propagation.
//
// A node becomes solved once some action has every successor solved, taking
// cost + max successor value. A node becomes losing once every action has at
// least one losing successor. Positive costs make goal-free cycles worthless,
// so nodes never reached by either monotone rule are losing at the fixed
// point. Each node changes status only a bounded number of times.
func (s *Solution) valueIterate(nodes []string, edges map[string]map[string]edge) {
	rev := map[string][]string{}
	for k, byAction := range edges {
		for _, e := range byAction {
			for _, nk := range e.obs {
				rev[nk] = append(rev[nk], k)
			}
		}
	}

	type status uint8
	const (
		unknown status = iota
		solved
		losing
	)
	state := map[string]status{}
	var queue []string
	for _, k := range nodes {
		if len(edges[k]) == 0 {
			state[k] = solved
			s.value[k] = 0
			queue = append(queue, k)
		}
	}

	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for _, pred := range rev[k] {
			if state[pred] == solved {
				continue
			}
			best := -1
			var bestAction action
			allBlocked := true
			for _, a := range s.actions {
				e, ok := edges[pred][a.name]
				if !ok {
					continue
				}
				hasLosing := false
				known := true
				worst := 0
				for _, nk := range e.obs {
					switch state[nk] {
					case losing:
						hasLosing = true
					case solved:
						if s.value[nk] > worst {
							worst = s.value[nk]
						}
					default:
						known = false
					}
				}
				if !hasLosing {
					allBlocked = false
				}
				if !hasLosing && known {
					candidate := e.cost + worst
					if best == -1 || candidate < best ||
						(candidate == best && a.name < bestAction.name) {
						best = candidate
						bestAction = a
					}
				}
			}
			next := unknown
			if best != -1 {
				next = solved
			} else if allBlocked {
				next = losing
			}
			if next == unknown || next == state[pred] {
				continue
			}
			state[pred] = next
			if next == solved {
				s.value[pred] = best
				s.policy[pred] = bestAction
				delete(s.losing, pred)
			} else {
				s.losing[pred] = true
			}
			queue = append(queue, pred)
		}
	}
	for _, k := range nodes {
		if state[k] != solved {
			state[k] = losing
			s.losing[k] = true
		}
	}
}

// OptimalCost returns the minimax remaining cost at the initial belief.
func (s *Solution) OptimalCost() (int, bool) {
	v, ok := s.value[s.initial.key()]
	return v, ok
}

// Solvable reports whether a guaranteed minimum-cost strategy exists.
func (s *Solution) Solvable() bool {
	return !s.losing[s.initial.key()]
}

// NodeCount reports how many distinct belief states were explored.
func (s *Solution) NodeCount() int { return len(s.nodeKeys) }
