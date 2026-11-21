package admission

import "fmt"

func (a *Analyzer) validateNodes() error {
	m := a.model
	participant := map[Identity]*Participant{}
	for i := range m.Participants {
		participant[m.Participants[i].Subject] = &m.Participants[i]
	}
	categories := map[string]bool{}
	for cat := range m.Ideal {
		categories[cat] = true
	}
	reachable := map[string]bool{m.Root: true}
	for _, id := range a.order {
		n := a.nodes[id]
		if !reachable[id] {
			return fmt.Errorf("admission: node %q is unreachable from root", id)
		}
		for _, e := range n.Out {
			reachable[e.To] = true
		}
		switch n.Kind {
		case KindEntry:
			if err := a.validateEntry(n); err != nil {
				return err
			}
		case KindDraw:
			if err := a.validateDraw(n); err != nil {
				return err
			}
		case KindDecision:
			if err := a.validateDecision(n, participant); err != nil {
				return err
			}
		case KindTerminal:
			if err := a.validateTerminal(n, categories); err != nil {
				return err
			}
		default:
			return fmt.Errorf("admission: node %q has unknown kind %d", n.ID, n.Kind)
		}
	}
	// A model whose only terminals are failures is admissible and simply has
	// a completion probability of zero; Enumerate then reports the trivial
	// deviation bound. Every node is still required to be reachable above.
	return nil
}

func (a *Analyzer) validateEntry(n *Node) error {
	if n.Draw != nil || n.Decision != nil || n.Terminal != nil {
		return fmt.Errorf("admission: entry node %q must carry no spec", n.ID)
	}
	if len(n.Out) != 1 {
		return fmt.Errorf("admission: entry node %q must have exactly one edge", n.ID)
	}
	if n.Out[0].Retry {
		return fmt.Errorf("admission: entry node %q cannot emit a retry edge", n.ID)
	}
	return nil
}

func (a *Analyzer) validateDraw(n *Node) error {
	if n.Draw == nil || n.Decision != nil || n.Terminal != nil {
		return fmt.Errorf("admission: draw node %q has inconsistent spec", n.ID)
	}
	if len(n.Out) != len(n.Draw.Table) {
		return fmt.Errorf("admission: draw node %q needs one edge per table outcome", n.ID)
	}
	seenOutcome := map[string]bool{}
	for _, e := range n.Out {
		if e.OnAction != "" || e.Retry {
			return fmt.Errorf("admission: draw node %q edges use outcomes only", n.ID)
		}
		if n.Draw.Table[e.OnOutcome] == nil || seenOutcome[e.OnOutcome] {
			return fmt.Errorf("admission: draw node %q has invalid/duplicate outcome edge %q", n.ID, e.OnOutcome)
		}
		seenOutcome[e.OnOutcome] = true
	}
	return nil
}

func (a *Analyzer) validateDecision(n *Node, participant map[Identity]*Participant) error {
	d := n.Decision
	if d == nil || n.Draw != nil || n.Terminal != nil {
		return fmt.Errorf("admission: decision node %q has inconsistent spec", n.ID)
	}
	p := participant[d.Subject]
	if p == nil {
		return fmt.Errorf("admission: decision node %q references unknown subject %q", n.ID, d.Subject)
	}
	visible := map[MessageType]bool{}
	for _, msg := range p.VisibleMessages {
		visible[msg] = true
	}
	for _, msg := range d.ObservedMessages {
		if !visible[msg] {
			return fmt.Errorf("admission: subject %q cannot observe %q at node %q", d.Subject, msg, n.ID)
		}
	}
	if len(d.Actions) < 2 {
		return fmt.Errorf("admission: decision node %q needs at least two actions", n.ID)
	}
	abstainOK := false
	seenAction := map[string]bool{}
	for _, act := range d.Actions {
		if act == "" || seenAction[act] {
			return fmt.Errorf("admission: decision node %q has empty/duplicate action", n.ID)
		}
		seenAction[act] = true
		if act == d.AbstainAction {
			abstainOK = true
		}
	}
	if !abstainOK {
		return fmt.Errorf("admission: abstain action %q on node %q is not an action", d.AbstainAction, n.ID)
	}
	if len(n.Out) != len(d.Actions) {
		return fmt.Errorf("admission: decision node %q needs one edge per action", n.ID)
	}
	for _, e := range n.Out {
		if e.OnOutcome != "" || !seenAction[e.OnAction] {
			return fmt.Errorf("admission: decision node %q has invalid action edge", n.ID)
		}
		if e.Retry && e.OnAction != d.AbstainAction {
			return fmt.Errorf("admission: node %q marks a non-abstain action edge as retry", n.ID)
		}
	}
	return nil
}

func (a *Analyzer) validateTerminal(n *Node, categories map[string]bool) error {
	if n.Terminal == nil || n.Draw != nil || n.Decision != nil {
		return fmt.Errorf("admission: terminal node %q has inconsistent spec", n.ID)
	}
	if len(n.Out) != 0 {
		return fmt.Errorf("admission: terminal node %q must have no outgoing edges", n.ID)
	}
	cat := n.Terminal.Category
	if cat == "" {
		return fmt.Errorf("admission: terminal node %q has empty category", n.ID)
	}
	if cat != FailureCategory && !categories[cat] {
		return fmt.Errorf("admission: terminal node %q uses category %q absent from Ideal", n.ID, cat)
	}
	return nil
}
