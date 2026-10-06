package main

import (
	"fmt"
	"math/big"
	"strings"
)

// Scenario is one demand path with a rational probability Num/Den.
type Scenario struct {
	Num, Den int64
	Demand   []int
}

func (s Scenario) prob() *big.Rat {
	return big.NewRat(s.Num, s.Den)
}

// Node is a vertex of the scenario tree. A node at depth t+1 represents
// the event "demands on days 0..t revealed as (path to this node)";
// the production decision for day t is taken here, after observing
// Demand. Scenarios sharing a demand history share nodes, which enforces
// non-anticipativity: identical history => identical decision.
type Node struct {
	ID       int
	Day      int // day whose demand is revealed at this node (-1 for root)
	Demand   int
	Prob     *big.Rat // unconditional probability of reaching this node
	Children []*Node
	Parent   *Node
}

// History returns the revealed demand path 0..Day leading to the node.
func (n *Node) History() []int {
	var rev []int
	for cur := n; cur != nil && cur.Day >= 0; cur = cur.Parent {
		rev = append(rev, cur.Demand)
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

func (n *Node) String() string {
	if n.Day < 0 {
		return "root"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "day %d demand=%d p=%s hist=[", n.Day, n.Demand, n.Prob.RatString())
	for i, d := range n.History() {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d", d)
	}
	b.WriteByte(']')
	return b.String()
}

// BuildTree merges scenarios into a prefix tree. All scenarios must
// have length T and probabilities summing to 1.
func BuildTree(p Params, scens []Scenario) (*Node, error) {
	root := &Node{ID: 0, Day: -1, Prob: big.NewRat(1, 1)}
	nextID := 1
	total := big.NewRat(0, 1)
	for i, sc := range scens {
		if len(sc.Demand) != p.T {
			return nil, fmt.Errorf("scenario %d: demand length %d, want %d", i, len(sc.Demand), p.T)
		}
		pr := sc.prob()
		total.Add(total, pr)
		cur := root
		for t := 0; t < p.T; t++ {
			var child *Node
			for _, c := range cur.Children {
				if c.Demand == sc.Demand[t] {
					child = c
					break
				}
			}
			if child == nil {
				child = &Node{ID: nextID, Day: t, Demand: sc.Demand[t],
					Prob: big.NewRat(0, 1), Parent: cur}
				nextID++
				cur.Children = append(cur.Children, child)
			}
			child.Prob.Add(child.Prob, pr)
			cur = child
		}
	}
	if total.Cmp(big.NewRat(1, 1)) != 0 {
		return nil, fmt.Errorf("scenario probabilities sum to %s, want 1", total.RatString())
	}
	return root, nil
}

// Nodes returns all decision nodes (every node except the root).
func Nodes(root *Node) []*Node {
	var out []*Node
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.Children {
			out = append(out, c)
			walk(c)
		}
	}
	walk(root)
	return out
}

// StochSolver computes a minimum expected cost non-anticipative policy
// by DP over (node, inventory age vector) states. Costs are exact
// rationals.
type StochSolver struct {
	P      Params
	Root   *Node
	memo   map[string]*big.Rat
	choice map[string]int
	states int
}

func NewStochSolver(p Params, root *Node) *StochSolver {
	return &StochSolver{P: p, Root: root, memo: map[string]*big.Rat{}, choice: map[string]int{}}
}

// dp returns the minimum expected cost of the subtree at node n
// (including node n's own day costs), conditional on reaching n,
// given start-of-day inventory inv. Returns nil if infeasible.
func (s *StochSolver) dp(n *Node, inv []int) *big.Rat {
	key := fmt.Sprintf("%d|%s", n.ID, invKey(inv))
	if v, ok := s.memo[key]; ok {
		return v
	}
	s.states++

	onHand := 0
	for _, v := range inv {
		onHand += v
	}
	lo := n.Demand - onHand
	if lo < 0 {
		lo = 0
	}
	// Upper bound: never produce more than the maximum remaining demand
	// along any single descendant path can absorb.
	hi := s.P.Cap
	if r := pathRemDemand(n) - onHand; r < hi {
		hi = r
	}

	var best *big.Rat
	bestX := -1
	for x := lo; x <= hi; x++ {
		next, res := stepDay(s.P, inv, x, n.Demand)
		if !res.OK {
			continue
		}
		total := new(big.Rat).SetInt64(int64(res.DayCost))
		ok := true
		for _, c := range n.Children {
			sub := s.dp(c, next)
			if sub == nil {
				ok = false
				break
			}
			cond := new(big.Rat).Quo(c.Prob, n.Prob)
			total.Add(total, cond.Mul(cond, sub))
		}
		if !ok {
			continue
		}
		if best == nil || total.Cmp(best) < 0 {
			best, bestX = total, x
		}
	}
	s.memo[key] = best
	s.choice[key] = bestX
	return best
}

// pathRemDemand returns, for each node, the maximum over descendant
// paths of the sum of demands from that node downward.
func pathRemDemand(n *Node) int {
	if len(n.Children) == 0 {
		return n.Demand
	}
	best := 0
	for _, c := range n.Children {
		if v := pathRemDemand(c); v > best {
			best = v
		}
	}
	return n.Demand + best
}

// Solve returns the minimum expected total cost (exact rational) and
// ok=false if some scenario path cannot be served without stockout.
func (s *StochSolver) Solve() (*big.Rat, bool) {
	inv := make([]int, s.P.Shelf-1)
	total := big.NewRat(0, 1)
	for _, c := range s.Root.Children {
		sub := s.dp(c, inv)
		if sub == nil {
			return nil, false
		}
		total.Add(total, new(big.Rat).Mul(c.Prob, sub))
	}
	return total, true
}

// Decision returns the policy's production quantity at node n when
// reached with start-of-day inventory inv.
func (s *StochSolver) Decision(n *Node, inv []int) int {
	key := fmt.Sprintf("%d|%s", n.ID, invKey(inv))
	if x, ok := s.choice[key]; ok {
		return x
	}
	return -1
}

// PathPlan simulates the policy along one scenario's demand path and
// returns the per-day production quantities. Since a node fixes the
// history, the inventory at each node is path-determined, so the policy
// yields exactly one quantity per node.
func (s *StochSolver) PathPlan(demand []int) ([]int, bool) {
	inv := make([]int, s.P.Shelf-1)
	plan := make([]int, s.P.T)
	cur := s.Root
	for t := 0; t < s.P.T; t++ {
		var next *Node
		for _, c := range cur.Children {
			if c.Demand == demand[t] {
				next = c
				break
			}
		}
		if next == nil {
			return nil, false
		}
		x := s.Decision(next, inv)
		if x < 0 {
			return nil, false
		}
		plan[t] = x
		ninv, res := stepDay(s.P, inv, x, demand[t])
		if !res.OK {
			return nil, false
		}
		inv = ninv
		cur = next
	}
	return plan, true
}
