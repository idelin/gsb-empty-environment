package admission

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

func (m *Model) validateTablesAndIdeal() error {
	if len(m.Nodes) == 0 {
		return errors.New("admission: model has no nodes")
	}
	if len(m.PublicMessages) == 0 {
		return errors.New("admission: PublicMessages is empty")
	}
	seenMsg := map[MessageType]bool{}
	for _, msg := range m.PublicMessages {
		if msg == "" {
			return errors.New("admission: empty public message type")
		}
		if seenMsg[msg] {
			return fmt.Errorf("admission: duplicate public message %q", msg)
		}
		seenMsg[msg] = true
	}
	for i := range m.Nodes {
		n := &m.Nodes[i]
		if n.Kind == KindDraw {
			if n.Draw == nil {
				return fmt.Errorf("admission: draw node %q missing DrawSpec", n.ID)
			}
			if err := validateTable(n.ID, n.Draw.Table); err != nil {
				return err
			}
		}
	}
	if len(m.Ideal) == 0 {
		return errors.New("admission: Ideal allocation is empty")
	}
	idealSum := new(big.Rat)
	for cat, p := range m.Ideal {
		if cat == "" || cat == FailureCategory {
			return fmt.Errorf("admission: ideal category %q is reserved", cat)
		}
		if p == nil || p.Sign() < 0 {
			return fmt.Errorf("admission: ideal share for %q must be non-negative", cat)
		}
		idealSum.Add(idealSum, p)
	}
	if idealSum.Cmp(big.NewRat(1, 1)) != 0 {
		return fmt.Errorf("admission: Ideal shares must sum to 1, got %s", idealSum.RatString())
	}
	return nil
}

func validateTable(nodeID string, table ProbabilityTable) error {
	if len(table) == 0 {
		return fmt.Errorf("admission: draw node %q has empty probability table", nodeID)
	}
	sum := new(big.Rat)
	for outcome, p := range table {
		if outcome == "" {
			return fmt.Errorf("admission: draw node %q has empty outcome label", nodeID)
		}
		if p == nil || p.Sign() <= 0 {
			return fmt.Errorf("admission: probability for %q/%q must be strictly positive", nodeID, outcome)
		}
		sum.Add(sum, p)
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		return fmt.Errorf("admission: probabilities on %q sum to %s, want 1", nodeID, sum.RatString())
	}
	return nil
}

func (m *Model) validateParticipants() error {
	if len(m.Participants) == 0 {
		return errors.New("admission: no participants declared")
	}
	seen := map[Identity]bool{}
	for i := range m.Participants {
		p := &m.Participants[i]
		if p.Subject == "" {
			return errors.New("admission: participant with empty identity")
		}
		if seen[p.Subject] {
			return fmt.Errorf("admission: duplicate participant %q", p.Subject)
		}
		seen[p.Subject] = true
		seenMsg := map[MessageType]bool{}
		for _, msg := range p.VisibleMessages {
			if msg == "" {
				return fmt.Errorf("admission: participant %q lists empty message", p.Subject)
			}
			if seenMsg[msg] {
				return fmt.Errorf("admission: participant %q lists duplicate visible message %q", p.Subject, msg)
			}
			seenMsg[msg] = true
		}
	}
	for _, hc := range m.HostConditions {
		if hc.Domain == "" {
			return errors.New("admission: host condition with empty failure domain")
		}
		if hc.Rule == "" {
			return fmt.Errorf("admission: host condition for domain %q has empty Rule", hc.Domain)
		}
		if hc.Action == "" {
			return fmt.Errorf("admission: host condition for domain %q has empty Action", hc.Domain)
		}
		if len(hc.ObservedMessages) == 0 {
			return fmt.Errorf("admission: host condition for domain %q has empty observation", hc.Domain)
		}
	}
	return nil
}

func (a *Analyzer) indexAndTopo() error {
	m := a.model
	indeg := map[string]int{}
	for i := range m.Nodes {
		n := &m.Nodes[i]
		if n.ID == "" {
			return errors.New("admission: node with empty ID")
		}
		if a.nodes[n.ID] != nil {
			return fmt.Errorf("admission: duplicate node ID %q", n.ID)
		}
		a.nodes[n.ID] = n
		indeg[n.ID] = 0
	}
	if a.nodes[m.Root] == nil {
		return fmt.Errorf("admission: root %q is not a known node", m.Root)
	}
	if a.nodes[m.Root].Kind != KindEntry {
		return fmt.Errorf("admission: root %q must be an entry node", m.Root)
	}
	for id, n := range a.nodes {
		for _, e := range n.Out {
			if a.nodes[e.To] == nil {
				return fmt.Errorf("admission: edge %q -> %q targets unknown node", id, e.To)
			}
			indeg[e.To]++
		}
	}
	// Kahn's algorithm; ties broken by node ID for a canonical order.
	queue := make([]string, 0)
	for id, d := range indeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		a.order = append(a.order, id)
		next := []string{}
		for _, e := range a.nodes[id].Out {
			indeg[e.To]--
			if indeg[e.To] == 0 {
				next = append(next, e.To)
			}
		}
		sort.Strings(next)
		queue = append(queue, next...)
		sort.Strings(queue)
	}
	if len(a.order) != len(a.nodes) {
		return errors.New("admission: round graph contains a cycle; it must be a DAG")
	}
	for i, id := range a.order {
		a.idx[id] = i
	}
	return nil
}
