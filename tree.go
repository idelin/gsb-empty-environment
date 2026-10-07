package policy

import (
	"fmt"
	"sort"
	"strings"
)

// Node is a node of a policy tree. An internal node performs Probe and
// branches on the observed symbol; a leaf takes Action. Decisions depend
// only on the observation history, which enforces non-clairvoyance.
type Node struct {
	Probe    int              // probe index at internal nodes, -1 at leaves
	Action   int              // action index at leaves, -1 at internal nodes
	Children map[string]*Node // observation symbol -> subtree (internal nodes)
}

// Leaf returns a terminal node that takes the given action.
func Leaf(action int) *Node {
	return &Node{Probe: -1, Action: action}
}

// IsLeaf reports whether n is a terminal (action) node.
func (n *Node) IsLeaf() bool { return n.Probe < 0 }

// TreeString renders the policy tree with one line per node.
func (p *Problem) TreeString(t *Node) string {
	var b strings.Builder
	var rec func(n *Node, indent string)
	rec = func(n *Node, indent string) {
		if n.IsLeaf() {
			fmt.Fprintf(&b, "%sact A%d\n", indent, n.Action)
			return
		}
		fmt.Fprintf(&b, "%sprobe %s (cost %d)\n", indent, p.probeName(n.Probe), p.Probes[n.Probe].Cost)
		keys := make([]string, 0, len(n.Children))
		for k := range n.Children {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s  [%q]:\n", indent, k)
			rec(n.Children[k], indent+"    ")
		}
	}
	rec(t, "")
	return b.String()
}

// ProbeEvent records one probe outcome along a world's path.
type ProbeEvent struct {
	Probe int
	Obs   string
}

// Replay is the trace of a single world through the policy tree.
type Replay struct {
	World      int
	Events     []ProbeEvent // probes performed, in order, with their outcomes
	Action     int          // terminal action taken
	ActionObs  string       // symbol observed when the action is taken
	ProbeCost  int          // total probe cost paid
	ActionLoss int          // loss of the terminal action
	Total      int          // ProbeCost + ActionLoss
}

// Replay traces world w through the policy tree. It returns an error if
// the tree has no branch for some observation of w.
func (p *Problem) Replay(t *Node, w int) (Replay, error) {
	r := Replay{World: w}
	n := t
	for !n.IsLeaf() {
		if n.Probe < 0 || n.Probe >= len(p.Probes) {
			return r, fmt.Errorf("world %d: invalid probe index %d", w, n.Probe)
		}
		obs := p.Probes[n.Probe].Obs[w]
		r.Events = append(r.Events, ProbeEvent{Probe: n.Probe, Obs: obs})
		r.ProbeCost += p.Probes[n.Probe].Cost
		next, ok := n.Children[obs]
		if !ok {
			return r, fmt.Errorf("world %d: no branch for observation %q of probe %s",
				w, obs, p.probeName(n.Probe))
		}
		n = next
	}
	if n.Action < 0 || n.Action >= p.NumActions {
		return r, fmt.Errorf("world %d: invalid action index %d", w, n.Action)
	}
	r.Action = n.Action
	r.ActionObs = p.ActObs[n.Action][w]
	r.ActionLoss = p.Loss[n.Action][w]
	r.Total = r.ProbeCost + r.ActionLoss
	return r, nil
}

// ReplayAll traces every world through the policy tree.
func (p *Problem) ReplayAll(t *Node) ([]Replay, error) {
	rs := make([]Replay, p.NumWorlds)
	for w := 0; w < p.NumWorlds; w++ {
		r, err := p.Replay(t, w)
		if err != nil {
			return nil, err
		}
		rs[w] = r
	}
	return rs, nil
}

// ReplayString renders one line per world replay.
func (p *Problem) ReplayString(rs []Replay) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "world %2d:", r.World)
		for _, e := range r.Events {
			fmt.Fprintf(&b, " %s=%q", p.probeName(e.Probe), e.Obs)
		}
		fmt.Fprintf(&b, " -> act A%d (obs %q), probe cost %d, loss %d, total %d\n",
			r.Action, r.ActionObs, r.ProbeCost, r.ActionLoss, r.Total)
	}
	return b.String()
}

// historyKey encodes an observation history so that identical histories
// map to identical keys.
func historyKey(events []ProbeEvent) string {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "%d=%q;", e.Probe, e.Obs)
	}
	return b.String()
}

// CheckPolicy verifies that t is a valid, non-clairvoyant policy for p:
//
//   - every world reaches a leaf (all its observations have branches);
//   - no probe is repeated along a path and the probe budget is respected;
//   - worlds with identical observation histories take the same action.
func (p *Problem) CheckPolicy(t *Node) error {
	if t == nil {
		return fmt.Errorf("nil policy tree")
	}
	// Structural checks: budget, no repeated probe, valid child symbols.
	var walk func(n *Node, depth int, used uint64) error
	walk = func(n *Node, depth int, used uint64) error {
		if n.IsLeaf() {
			if n.Action < 0 || n.Action >= p.NumActions {
				return fmt.Errorf("leaf has invalid action %d", n.Action)
			}
			return nil
		}
		if depth >= p.MaxProbes {
			return fmt.Errorf("probe depth exceeds budget %d", p.MaxProbes)
		}
		if n.Probe < 0 || n.Probe >= len(p.Probes) {
			return fmt.Errorf("invalid probe index %d", n.Probe)
		}
		if used>>uint(n.Probe)&1 == 1 {
			return fmt.Errorf("probe %s used twice on one path", p.probeName(n.Probe))
		}
		if len(n.Children) == 0 {
			return fmt.Errorf("probe %s has no branches", p.probeName(n.Probe))
		}
		for k, c := range n.Children {
			if c == nil {
				return fmt.Errorf("probe %s branch %q is nil", p.probeName(n.Probe), k)
			}
			if err := walk(c, depth+1, used|1<<uint(n.Probe)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(t, 0, 0); err != nil {
		return err
	}
	// Coverage and non-clairvoyance via replays.
	actionByHistory := make(map[string]int)
	for w := 0; w < p.NumWorlds; w++ {
		r, err := p.Replay(t, w)
		if err != nil {
			return err
		}
		h := historyKey(r.Events)
		if a, ok := actionByHistory[h]; ok && a != r.Action {
			return fmt.Errorf("clairvoyant policy: observation history %q maps to both A%d and A%d",
				h, a, r.Action)
		}
		actionByHistory[h] = r.Action
	}
	return nil
}
