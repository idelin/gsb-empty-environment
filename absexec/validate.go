package absexec

import (
	"errors"
	"fmt"
)

func (c *CFG) validate() error {
	if c == nil {
		return errors.New("absexec: nil CFG")
	}
	if c.BufferSize <= 0 {
		return fmt.Errorf("absexec: BufferSize must be positive, got %d", c.BufferSize)
	}
	if len(c.Nodes) == 0 {
		return errors.New("absexec: CFG has no nodes")
	}

	index := make(map[int]int, len(c.Nodes))
	for i, n := range c.Nodes {
		if n.ID < 0 {
			return fmt.Errorf("absexec: node #%d has negative ID %d", i, n.ID)
		}
		if _, dup := index[n.ID]; dup {
			return fmt.Errorf("absexec: duplicate node ID %d", n.ID)
		}
		index[n.ID] = i
		if len(n.Succ) > 2 {
			return fmt.Errorf("absexec: node %d has %d successors, at most 2 allowed", n.ID, len(n.Succ))
		}
		if err := n.validate(c.BufferSize); err != nil {
			return err
		}
	}
	if _, ok := index[c.Entry]; !ok {
		return fmt.Errorf("absexec: entry node %d not found", c.Entry)
	}

	for i := range c.BackEdges {
		be := &c.BackEdges[i]
		if be.MaxCount < 0 {
			return fmt.Errorf("absexec: back edge %d -> %d has negative MaxCount", be.From, be.To)
		}
		if _, ok := index[be.From]; !ok {
			return fmt.Errorf("absexec: back edge source node %d not found", be.From)
		}
		if _, ok := index[be.To]; !ok {
			return fmt.Errorf("absexec: back edge target node %d not found", be.To)
		}
	}

	return nil
}

func (n Node) validate(bufSize int) error {
	inRange := func(v int) bool { return v >= 0 && v <= bufSize }
	switch n.Op {
	case OpNoop, OpSend:
		if n.Op == OpSend {
			lo, hi := n.sendRange(bufSize)
			if !inRange(lo) || !inRange(hi) || lo >= hi {
				return fmt.Errorf("absexec: send node %d has invalid interval [%d,%d)", n.ID, lo, hi)
			}
		}
		if len(n.Succ) > 1 {
			return fmt.Errorf("absexec: op %d node %d must have at most one successor", n.Op, n.ID)
		}
	case OpWrite:
		if !inRange(n.Lo) || !inRange(n.Hi) || n.Lo > n.Hi {
			return fmt.Errorf("absexec: write node %d has invalid interval [%d,%d)", n.ID, n.Lo, n.Hi)
		}
		if len(n.Succ) > 1 {
			return fmt.Errorf("absexec: write node %d must have at most one successor", n.ID)
		}
	case OpDynWrite:
		if !(inRange(n.LoMin) && inRange(n.LoMax) && inRange(n.HiMin) && inRange(n.HiMax)) {
			return fmt.Errorf("absexec: dynamic write node %d has out-of-range bounds", n.ID)
		}
		if n.LoMin > n.LoMax || n.HiMin > n.HiMax || n.LoMax > n.HiMin {
			return fmt.Errorf("absexec: dynamic write node %d has inconsistent bounds", n.ID)
		}
		if len(n.Succ) > 1 {
			return fmt.Errorf("absexec: dynamic write node %d must have at most one successor", n.ID)
		}
	case OpBranch:
		if len(n.Succ) != 2 {
			return fmt.Errorf("absexec: branch node %d must have exactly two successors", n.ID)
		}
	default:
		return fmt.Errorf("absexec: node %d has unknown Op %d", n.ID, n.Op)
	}
	for _, s := range n.Succ {
		if s < 0 || s >= 1<<31 {
			return fmt.Errorf("absexec: node %d references invalid successor %d", n.ID, s)
		}
	}
	return nil
}

// sendRange resolves the required interval of a send node, defaulting to the
// whole buffer when Lo == Hi == 0.
func (n Node) sendRange(bufSize int) (int, int) {
	if n.Op == OpSend && (n.Lo != 0 || n.Hi != 0) {
		return n.Lo, n.Hi
	}
	return 0, bufSize
}

// checkReducedAcyclic verifies that removing the declared back edges leaves a
// DAG, so every cycle in the input is governed by an iteration cap.
func (c *CFG) checkReducedAcyclic() error {
	isBack := make(map[[2]int]bool, len(c.BackEdges))
	for _, be := range c.BackEdges {
		isBack[[2]int{be.From, be.To}] = true
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[int]int, len(c.Nodes))
	var visit func(id int) error
	visit = func(id int) error {
		if color[id] == black {
			return nil
		}
		if color[id] == gray {
			return fmt.Errorf("absexec: CFG cycle through node %d is not covered by a back edge", id)
		}
		color[id] = gray
		for _, s := range c.node(id).Succ {
			if isBack[[2]int{id, s}] {
				continue
			}
			if err := visit(s); err != nil {
				return err
			}
		}
		color[id] = black
		return nil
	}
	return visit(c.Entry)
}

func (c *CFG) node(id int) Node {
	for _, n := range c.Nodes {
		if n.ID == id {
			return n
		}
	}
	return Node{}
}
