package netsec

import "fmt"

// wordBits is the width of one bitset backing word.
const wordBits = 64

// bitset is a fixed-width set of node indices backed by packed uint64 words.
type bitset []uint64

func newBitset(n int) bitset {
	return make(bitset, (n+wordBits-1)/wordBits)
}

func (b bitset) set(i int) {
	b[i/wordBits] |= 1 << uint(i%wordBits)
}

func (b bitset) has(i int) bool {
	return b[i/wordBits]&(1<<uint(i%wordBits)) != 0
}

// gate describes a compiled precondition gate using node indices.
type gate struct {
	kind GateKind
	pre  []int
}

// satisfied reports whether the gate holds against the current closure.
//
// An AND gate with no preconditions is vacuously true (an initial fact) and
// an OR gate with no preconditions is never true.
func (g gate) satisfied(closed bitset) bool {
	if g.kind == GateAND {
		for _, p := range g.pre {
			if !closed.has(p) {
				return false
			}
		}
		return true
	}
	for _, p := range g.pre {
		if closed.has(p) {
			return true
		}
	}
	return false
}

// compiled holds the index-encoded representation of a validated problem.
type compiled struct {
	nodeID  []string
	gate    []gate
	target  int
	patches []compiledPatch
}

type compiledPatch struct {
	id     string
	action int
	cost   int
}

// compile validates the problem and translates every ID into a dense index so
// the fixpoint and exhaustive search work on compact integers.
func compile(p *Problem) (*compiled, error) {
	if len(p.Nodes) == 0 {
		return nil, ErrNoNodes
	}
	if len(p.Patches) > 20 {
		return nil, ErrTooManyPatches
	}

	index := make(map[string]int, len(p.Nodes))
	for i, n := range p.Nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("netsec: node at position %d has an empty ID", i)
		}
		if _, dup := index[n.ID]; dup {
			return nil, fmt.Errorf("netsec: duplicate node ID %q", n.ID)
		}
		if n.Gate != GateAND && n.Gate != GateOR {
			return nil, fmt.Errorf("netsec: node %q has unknown gate kind %d", n.ID, n.Gate)
		}
		index[n.ID] = i
	}

	target, ok := index[p.Target]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTarget, p.Target)
	}

	gates := make([]gate, len(p.Nodes))
	for i, n := range p.Nodes {
		g := gate{kind: n.Gate}
		seen := make(map[int]bool, len(n.Preconditions))
		for _, pid := range n.Preconditions {
			j, known := index[pid]
			if !known {
				return nil, fmt.Errorf("netsec: node %q references unknown precondition %q", n.ID, pid)
			}
			if j == i {
				return nil, fmt.Errorf("netsec: node %q lists itself as a precondition", n.ID)
			}
			if seen[j] {
				return nil, fmt.Errorf("netsec: node %q lists precondition %q more than once", n.ID, pid)
			}
			seen[j] = true
			g.pre = append(g.pre, j)
		}
		gates[i] = g
	}

	patchIDs := make(map[string]bool, len(p.Patches))
	cp := make([]compiledPatch, 0, len(p.Patches))
	for i, pc := range p.Patches {
		if pc.ID == "" {
			return nil, fmt.Errorf("netsec: patch at position %d has an empty ID", i)
		}
		if patchIDs[pc.ID] {
			return nil, fmt.Errorf("netsec: duplicate patch ID %q", pc.ID)
		}
		patchIDs[pc.ID] = true
		if pc.Cost < 0 {
			return nil, fmt.Errorf("netsec: patch %q has negative cost %d", pc.ID, pc.Cost)
		}
		action, known := index[pc.Action]
		if !known {
			return nil, fmt.Errorf("netsec: patch %q disables unknown action %q", pc.ID, pc.Action)
		}
		cp = append(cp, compiledPatch{id: pc.ID, action: action, cost: pc.Cost})
	}

	ids := make([]string, len(p.Nodes))
	for i, n := range p.Nodes {
		ids[i] = n.ID
	}
	return &compiled{nodeID: ids, gate: gates, target: target, patches: cp}, nil
}

// simulate runs the least-fixpoint reachability computation.
//
// Nodes in disabled never activate. Every iteration activates every currently
// satisfied gate; activation order is the node index order, which makes the
// resulting witness deterministic. Cycles without an external seed simply
// never activate, matching least-fixpoint semantics. Shared preconditions are
// represented once in the closure and are therefore counted once regardless
// of how many gates depend on them.
//
// When stopAtTarget is true the scan returns as soon as the target activates,
// which the subset search only needs for a yes/no answer.
func (c *compiled) simulate(disabled bitset, stopAtTarget bool) (bitset, []int) {
	n := len(c.gate)
	closed := newBitset(n)
	var order []int

	for {
		progress := false
		for i := 0; i < n; i++ {
			if closed.has(i) || disabled.has(i) {
				continue
			}
			if c.gate[i].satisfied(closed) {
				closed.set(i)
				order = append(order, i)
				progress = true
				if stopAtTarget && i == c.target {
					return closed, order
				}
			}
		}
		if !progress {
			return closed, order
		}
	}
}

// ReachabilityResult reports a standalone reachability check.
type ReachabilityResult struct {
	// Reachable says whether the target can be activated.
	Reachable bool
	// Closure lists every reachable node ID in activation order.
	Closure []string
	// ActivationOrder is the ordered node IDs of a concrete activation
	// sequence; when Reachable is true its last element is the target.
	ActivationOrder []string
}

// Reachability evaluates the graph with a set of disabled actions and returns
// the least-fixpoint closure together with a target activation sequence. The
// disabledActions slice may contain duplicate IDs.
func Reachability(p *Problem, disabledActions []string) (ReachabilityResult, error) {
	c, err := compile(p)
	if err != nil {
		return ReachabilityResult{}, err
	}
	disabled := newBitset(len(c.gate))
	for _, id := range disabledActions {
		idx, ok := c.indexOfAction(id)
		if !ok {
			return ReachabilityResult{}, fmt.Errorf("netsec: unknown disabled action %q", id)
		}
		disabled.set(idx)
	}

	closed, order := c.simulate(disabled, false)
	ids := make([]string, len(order))
	for i, idx := range order {
		ids[i] = c.nodeID[idx]
	}
	res := ReachabilityResult{
		Reachable:       closed.has(c.target),
		Closure:         ids,
		ActivationOrder: c.targetOrder(closed, order),
	}
	return res, nil
}

func (c *compiled) indexOfAction(id string) (int, bool) {
	for i, nodeID := range c.nodeID {
		if nodeID == id {
			return i, true
		}
	}
	return 0, false
}

// targetOrder trims an activation sequence down to the prefix ending at the
// target, returning nil when the target is not reachable.
func (c *compiled) targetOrder(closed bitset, order []int) []string {
	if !closed.has(c.target) {
		return nil
	}
	for i, idx := range order {
		if idx == c.target {
			out := make([]string, i+1)
			for j := 0; j <= i; j++ {
				out[j] = c.nodeID[order[j]]
			}
			return out
		}
	}
	return nil
}
