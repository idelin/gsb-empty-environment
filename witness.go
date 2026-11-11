package netsec

import "fmt"

// HopTrace is one independently replayable step of a witness packet.
type HopTrace struct {
	Node          int
	In            Packet
	Out           Packet
	Outcome       StepOutcome
	FilterRule    string
	TranslateRule string
}

// Witness is a concrete packet plus the node path it traverses and a full
// per-hop trace. Replay re-executes the concrete semantics from scratch, so a
// witness is a real replayable artifact rather than an abstract claim.
type Witness struct {
	Entry  int
	Exit   int
	Packet Packet
	Path   []int
	Traces []HopTrace
}

// Replay re-runs the witness through the graph using only the concrete
// transfer function and checks every hop against the recorded trace. It
// returns the final packet on success and an error on the first divergence.
func (w *Witness) Replay(g *Graph) (Packet, error) {
	if len(w.Path) == 0 {
		return Packet{}, fmt.Errorf("netsec: witness has empty path")
	}
	if w.Path[0] != w.Entry || w.Path[len(w.Path)-1] != w.Exit {
		return Packet{}, fmt.Errorf("netsec: witness endpoints do not match path")
	}
	cur := w.Packet
	for i, nodeID := range w.Path {
		node, ok := g.Nodes[nodeID]
		if !ok {
			return Packet{}, fmt.Errorf("%w: %d", ErrUnknownNode, nodeID)
		}
		next, outcome, rule := stepPacket(node, cur)
		trace := w.Traces[i]
		if trace.Node != nodeID || trace.In != cur || trace.Outcome != outcome {
			return Packet{}, fmt.Errorf("netsec: replay mismatch at node %d", nodeID)
		}
		if outcome == OutcomePassed {
			if trace.Out != next {
				return Packet{}, fmt.Errorf("netsec: replay output mismatch at node %d", nodeID)
			}
			if trace.TranslateRule != rule {
				return Packet{}, fmt.Errorf("netsec: replay translation rule mismatch at node %d", nodeID)
			}
		}
		cur = next
		if outcome != OutcomePassed {
			return Packet{}, fmt.Errorf("netsec: witness packet did not survive node %d: %s", nodeID, outcome)
		}
	}
	return cur, nil
}

// replayPath executes a concrete packet along an explicit node path and
// builds a full trace. It fails on the first filter/overflow outcome.
func replayPath(g *Graph, entry int, p Packet, path []int) (*Witness, error) {
	if len(path) == 0 || path[0] != entry {
		return nil, fmt.Errorf("netsec: invalid exemplar path")
	}
	cur := p
	traces := make([]HopTrace, 0, len(path))
	for _, nodeID := range path {
		node, ok := g.Nodes[nodeID]
		if !ok {
			return nil, fmt.Errorf("%w: %d", ErrUnknownNode, nodeID)
		}
		in := cur
		next, outcome, rule := stepPacket(node, cur)
		trace := HopTrace{
			Node:    nodeID,
			In:      in,
			Outcome: outcome,
		}
		switch outcome {
		case OutcomeFiltered:
			trace.FilterRule = rule
			traces = append(traces, trace)
			return &Witness{Entry: entry, Exit: nodeID, Packet: p, Path: clonePath(path), Traces: traces}, ErrFiltered
		case OutcomeOverflow:
			trace.TranslateRule = rule
			traces = append(traces, trace)
			return &Witness{Entry: entry, Exit: nodeID, Packet: p, Path: clonePath(path), Traces: traces}, ErrOverflow
		default:
			trace.Out = next
			trace.TranslateRule = rule
			traces = append(traces, trace)
			cur = next
		}
	}
	return &Witness{
		Entry:  entry,
		Exit:   path[len(path)-1],
		Packet: p,
		Path:   clonePath(path),
		Traces: traces,
	}, nil
}

// satisfiesConstraints is a concrete check of ingress/egress constraints.
func packetSatisfies(p Packet, cs []Constraint) bool {
	for _, c := range cs {
		v := p.Src
		if c.Field == Dst {
			v = p.Dst
		}
		if !c.Range.Contains(v) {
			return false
		}
	}
	return true
}
