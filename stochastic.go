package main

import (
	"math/big"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Stochastic perishable lot-sizing over a scenario tree.
//
// Demand of day t is revealed at the START of day t; the production decision
// x_t is made right after observing it. Non-anticipativity: two scenarios
// that share the same observed demand history (days 1..t) must use the same
// production decision on day t. Hence decisions live on prefix nodes of the
// scenario tree. All scenarios sharing a node also share the same inventory
// (same history -> same decisions -> same age vector), so the perishable
// stock account is a shared constraint per node.
//
// Probabilities are rational; the expected cost is computed exactly with
// math/big.Rat. No stockout is allowed on any scenario path.
// ---------------------------------------------------------------------------

type Scenario struct {
	Demand []int
	Num    int64
	Den    int64
	prob   *big.Rat
}

type StochParams struct {
	T         int
	Shelf     int
	Cap       int
	Setup     int
	Unit      int
	Hold      int
	Scenarios []Scenario
}

func (p StochParams) normalize() {
	for i := range p.Scenarios {
		p.Scenarios[i].prob = big.NewRat(p.Scenarios[i].Num, p.Scenarios[i].Den)
	}
}

// node is one decision point of the scenario tree: all scenarios that share
// the demand history prefix (days 1..level) pass through it.
type node struct {
	id       int
	level    int // day index 1..T
	prefix   []int
	prob     *big.Rat // total probability mass of scenarios through this node
	maxRem   int      // max over those scenarios of demand from this day on
	children []*node
}

func prefixKey(dem []int) string {
	parts := make([]string, len(dem))
	for i, v := range dem {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

func buildTree(p StochParams) [][]*node {
	levels := make([][]*node, p.T+1)
	byKey := map[string]*node{}
	nextID := 0
	get := func(prefix []int) *node {
		k := prefixKey(prefix)
		n, ok := byKey[k]
		if !ok {
			n = &node{
				id:     nextID,
				level:  len(prefix),
				prefix: append([]int{}, prefix...),
				prob:   new(big.Rat),
			}
			nextID++
			byKey[k] = n
			levels[len(prefix)] = append(levels[len(prefix)], n)
			if len(prefix) > 1 {
				parent := byKey[prefixKey(prefix[:len(prefix)-1])]
				parent.children = append(parent.children, n)
			}
		}
		return n
	}
	for si := range p.Scenarios {
		sc := &p.Scenarios[si]
		for t := 1; t <= p.T; t++ {
			n := get(sc.Demand[:t])
			n.prob.Add(n.prob, sc.prob)
			if rem := sumInts(sc.Demand[t-1:]); rem > n.maxRem {
				n.maxRem = rem
			}
		}
	}
	return levels
}

type stochSolver struct {
	p      StochParams
	levels [][]*node
	memo   map[string]*big.Rat
	policy map[string]int
}

func (s *stochSolver) stateKey(n *node, inv []int) string {
	return strconv.Itoa(n.id) + "|" + invKey(inv)
}

// value returns the minimal expected cost-to-go at node n with start-of-day
// inventory inv, or nil if no feasible (stockout-free on every path) policy
// exists from here.
func (s *stochSolver) value(n *node, inv []int) *big.Rat {
	k := s.stateKey(n, inv)
	if v, ok := s.memo[k]; ok {
		return v
	}
	p := s.p
	dem := n.prefix[n.level-1]
	invSum := sumInts(inv)
	lo := maxInt(0, dem-invSum)
	hi := minInt(p.Cap, maxInt(0, n.maxRem-invSum))
	var best *big.Rat
	bestX := -1
	for x := lo; x <= hi; x++ {
		next, _, _, feasible := simulateDay(inv, x, dem, p.Shelf)
		if !feasible {
			continue
		}
		imm := p.Unit*x + p.Hold*sumInts(next)
		if x > 0 {
			imm += p.Setup
		}
		total := big.NewRat(int64(imm), 1)
		ok := true
		for _, c := range n.children {
			sub := s.value(c, next)
			if sub == nil {
				ok = false
				break
			}
			w := new(big.Rat).Quo(c.prob, n.prob)
			total.Add(total, w.Mul(w, sub))
		}
		if !ok {
			continue
		}
		if best == nil || total.Cmp(best) < 0 {
			best = total
			bestX = x
		}
	}
	if best == nil {
		return nil
	}
	s.memo[k] = best
	s.policy[k] = bestX
	return best
}

// solveStochastic returns the minimal expected cost (exact rational).
func solveStochastic(p StochParams) (*big.Rat, *stochSolver, bool) {
	p.normalize()
	s := &stochSolver{
		p:      p,
		levels: buildTree(p),
		memo:   map[string]*big.Rat{},
		policy: map[string]int{},
	}
	total := new(big.Rat)
	empty := make([]int, p.Shelf-1)
	for _, n := range s.levels[1] {
		v := s.value(n, empty)
		if v == nil {
			return nil, nil, false
		}
		total.Add(total, new(big.Rat).Mul(n.prob, v))
	}
	return total, s, true
}

// decisionAt returns the stored optimal production quantity at node n for
// the given start-of-day inventory.
func (s *stochSolver) decisionAt(n *node, inv []int) int {
	return s.policy[s.stateKey(n, inv)]
}

// bruteStochastic enumerates every non-anticipative policy (one production
// quantity per tree node) and evaluates it with the independent batch-list
// simulator. Returns the minimal expected cost over all feasible policies.
func bruteStochastic(p StochParams) (*big.Rat, bool) {
	p.normalize()
	levels := buildTree(p)
	var nodes []*node
	for t := 1; t <= p.T; t++ {
		nodes = append(nodes, levels[t]...)
	}
	nodeByPrefix := map[string]*node{}
	for _, n := range nodes {
		nodeByPrefix[prefixKey(n.prefix)] = n
	}
	assign := map[int]int{}
	var best *big.Rat
	var rec func(i int)
	rec = func(i int) {
		if i == len(nodes) {
			if c, ok := evalPolicy(p, nodeByPrefix, assign); ok {
				if best == nil || c.Cmp(best) < 0 {
					best = c
				}
			}
			return
		}
		for x := 0; x <= p.Cap; x++ {
			assign[nodes[i].id] = x
			rec(i + 1)
		}
	}
	rec(0)
	return best, best != nil
}

// evalPolicy simulates every scenario path under a fixed policy using the
// batch-list simulator and returns the exact expected cost.
func evalPolicy(p StochParams, nodeByPrefix map[string]*node, assign map[int]int) (*big.Rat, bool) {
	total := new(big.Rat)
	for si := range p.Scenarios {
		sc := &p.Scenarios[si]
		batches := []Batch{}
		cost := 0
		for t := 1; t <= p.T; t++ {
			n := nodeByPrefix[prefixKey(sc.Demand[:t])]
			x := assign[n.id]
			nb, _, feasible := simulateDayBatches(batches, x, sc.Demand[t-1], p.Shelf)
			if !feasible {
				return nil, false
			}
			cost += p.Unit*x + p.Hold*sumBatches(nb)
			if x > 0 {
				cost += p.Setup
			}
			batches = nb
		}
		term := new(big.Rat).Mul(sc.prob, big.NewRat(int64(cost), 1))
		total.Add(total, term)
	}
	return total, true
}
