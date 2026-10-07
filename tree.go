package policytree

import (
	"fmt"
	"strings"
)

// String renders the policy tree with one node per line, children indented
// under their probe node and keyed by outcome symbol.
func (n *Node) String(p *Problem) string {
	var b strings.Builder
	n.writeTo(&b, p, "")
	return b.String()
}

func (n *Node) writeTo(b *strings.Builder, p *Problem, indent string) {
	switch n.Kind {
	case ActionNode:
		fmt.Fprintf(b, "%saction %s\n", indent, p.Actions[n.Action].Name)
	case ProbeNode:
		probe := p.Probes[n.Probe]
		fmt.Fprintf(b, "%sprobe %s (cost %d)\n", indent, probe.Name, probe.Cost)
		for _, outcome := range n.Outcomes {
			fmt.Fprintf(b, "%s  [%s] ->\n", indent, outcome)
			n.Children[outcome].writeTo(b, p, indent+"    ")
		}
	}
}

// Step is one probe performed along a world's path through the policy tree.
type Step struct {
	Probe   string
	Outcome string
	Cost    int64
}

// ReplayEntry records how a single world traverses the policy tree: the
// probes performed and observed, the final action, and the total loss
// (probe costs plus action loss).
type ReplayEntry struct {
	World     string
	Prob      string
	Steps     []Step
	Action    string
	Symbol    string
	ProbeCost int64
	Loss      int64
	Total     int64
}

// Replay simulates every world through the policy tree and returns one entry
// per world, in world order.
func Replay(p *Problem, root *Node) ([]ReplayEntry, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	entries := make([]ReplayEntry, 0, p.NumWorlds())
	for w := 0; w < p.NumWorlds(); w++ {
		entry := ReplayEntry{World: p.Worlds[w], Prob: p.Probability(w).RatString()}
		node := root
		depth := 0
		for node.Kind == ProbeNode {
			depth++
			if depth > p.MaxProbes {
				return nil, fmt.Errorf("world %s: policy probes more than MaxProbes=%d times", p.Worlds[w], p.MaxProbes)
			}
			probe := p.Probes[node.Probe]
			outcome := probe.Outcome(w)
			child, ok := node.Children[outcome]
			if !ok {
				return nil, fmt.Errorf("world %s: probe %s outcome %q has no subtree", p.Worlds[w], probe.Name, outcome)
			}
			entry.Steps = append(entry.Steps, Step{Probe: probe.Name, Outcome: outcome, Cost: probe.Cost})
			entry.ProbeCost += probe.Cost
			node = child
		}
		action := p.Actions[node.Action]
		entry.Action = action.Name
		entry.Symbol = action.Symbols[w]
		entry.Loss = action.Losses[w]
		entry.Total = entry.ProbeCost + entry.Loss
		entries = append(entries, entry)
	}
	return entries, nil
}

// Describe renders a replay entry as a single line.
func (e ReplayEntry) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "world %s (p=%s): ", e.World, e.Prob)
	if len(e.Steps) == 0 {
		b.WriteString("no probes; ")
	}
	for _, s := range e.Steps {
		fmt.Fprintf(&b, "probe %s -> %q (cost %d); ", s.Probe, s.Outcome, s.Cost)
	}
	fmt.Fprintf(&b, "action %s (symbol %q, loss %d); total = %d + %d = %d",
		e.Action, e.Symbol, e.Loss, e.ProbeCost, e.Loss, e.Total)
	return b.String()
}

// VerifyNonAnticipation checks that the policy is a well-defined function of
// the observation history: any two worlds that produce identical probe
// outcomes along their paths must receive the same action. It also checks
// that the probe budget is respected on every path.
func VerifyNonAnticipation(p *Problem, root *Node) error {
	if err := p.Validate(); err != nil {
		return err
	}
	actionByHistory := make(map[string]int)
	for w := 0; w < p.NumWorlds(); w++ {
		node := root
		var history strings.Builder
		depth := 0
		for node.Kind == ProbeNode {
			depth++
			if depth > p.MaxProbes {
				return fmt.Errorf("world %s: probe depth %d exceeds MaxProbes=%d", p.Worlds[w], depth, p.MaxProbes)
			}
			probe := p.Probes[node.Probe]
			outcome := probe.Outcome(w)
			child, ok := node.Children[outcome]
			if !ok {
				return fmt.Errorf("world %s: probe %s outcome %q has no subtree", p.Worlds[w], probe.Name, outcome)
			}
			fmt.Fprintf(&history, "/%s=%s", probe.Name, outcome)
			node = child
		}
		key := history.String()
		if prev, ok := actionByHistory[key]; ok && prev != node.Action {
			return fmt.Errorf("history %q maps to both action %s and action %s",
				key, p.Actions[prev].Name, p.Actions[node.Action].Name)
		}
		actionByHistory[key] = node.Action
	}
	return nil
}
