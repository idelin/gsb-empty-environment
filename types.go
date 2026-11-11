package netsec

import (
	"errors"
	"fmt"
)

// Field identifies which half of a packet a rule inspects or rewrites.
type Field uint8

const (
	// Src is the source-address field.
	Src Field = iota
	// Dst is the destination-address field.
	Dst
)

func (f Field) String() string {
	switch f {
	case Src:
		return "src"
	case Dst:
		return "dst"
	default:
		return fmt.Sprintf("field(%d)", int(f))
	}
}

// Interval is a closed integer interval [Lo, Hi] over the uint32 address
// space. The zero value is not a valid interval; use NewInterval.
type Interval struct {
	Lo uint32
	Hi uint32
}

// NewInterval constructs a closed interval and rejects inverted ranges.
func NewInterval(lo, hi uint32) (Interval, error) {
	if lo > hi {
		return Interval{}, fmt.Errorf("netsec: inverted interval [%d, %d]", lo, hi)
	}
	return Interval{Lo: lo, Hi: hi}, nil
}

// Contains reports whether v lies inside the closed interval.
func (i Interval) Contains(v uint32) bool {
	return i.Lo <= v && v <= i.Hi
}

// Size returns the number of integers in the interval. It returns the full
// 64-bit magnitude so that a range covering the whole uint32 space is exact.
func (i Interval) Size() uint64 {
	return uint64(i.Hi) - uint64(i.Lo) + 1
}

// FilterRule is one entry of a node's ordered filter list. Filters are
// evaluated first-match in list order; a match decides Allow or Drop and
// terminates filter evaluation for the packet (or symbolic region).
type FilterRule struct {
	Name  string
	Field Field
	Match Interval
	Allow bool
}

// TranslateRule is one entry of a node's ordered translation list. Packets
// whose Target field lies inside Match (and, when GuardActive is set, whose
// guard field lies inside Guard) have Delta added to the target field.
// Translation rules are first-match as well; a region that does not match any
// rule passes through unchanged.
//
// Delta is signed. An addition that does not fit into a signed 64-bit
// pre-check, or whose result falls outside the uint32 post-check range, is an
// integer overflow: the affected concrete packets (or symbolic sub-region)
// are dropped.
type TranslateRule struct {
	Name        string
	Target      Field
	Match       Interval
	GuardField  Field
	GuardActive bool
	Guard       Interval
	Delta       int64
}

// Node is one device in the network graph. Every packet reaching the node is
// first run through Filters (ordered, first-match), then through Translates
// (ordered, first-match), exactly in that order.
type Node struct {
	ID         int
	Filters    []FilterRule
	Translates []TranslateRule
}

// Edge is a directed connection from Src to Dst in the network graph.
type Edge struct {
	Src int
	Dst int
}

// Graph is a finite directed network of nodes with adjacency lists.
type Graph struct {
	Nodes map[int]*Node
	Edges map[int][]int
}

// NewGraph validates that every edge references an existing node.
func NewGraph(nodes []Node, edges []Edge) (*Graph, error) {
	g := &Graph{
		Nodes: make(map[int]*Node, len(nodes)),
		Edges: make(map[int][]int),
	}
	for idx := range nodes {
		n := nodes[idx]
		if _, dup := g.Nodes[n.ID]; dup {
			return nil, fmt.Errorf("netsec: duplicate node id %d", n.ID)
		}
		g.Nodes[n.ID] = &n
	}
	for _, e := range edges {
		if _, ok := g.Nodes[e.Src]; !ok {
			return nil, fmt.Errorf("netsec: edge references unknown source node %d", e.Src)
		}
		if _, ok := g.Nodes[e.Dst]; !ok {
			return nil, fmt.Errorf("netsec: edge references unknown destination node %d", e.Dst)
		}
		g.Edges[e.Src] = append(g.Edges[e.Src], e.Dst)
	}
	return g, nil
}

// Constraint restricts one field of a packet to a closed interval.
type Constraint struct {
	Field Field
	Range Interval
}

// Packet is a concrete (source, destination) pair in uint32 space.
type Packet struct {
	Src uint32
	Dst uint32
}

// Errors returned by the analysis pipeline.
var (
	// ErrOverflow is reported when a translation delta cannot be represented
	// or the translated value leaves the uint32 address space.
	ErrOverflow = errors.New("netsec: integer overflow during address translation")
	// ErrFiltered is reported when a node's filter list drops a packet.
	ErrFiltered = errors.New("netsec: filter dropped packet")
	// ErrUnknownNode is reported when an ID does not identify a graph node.
	ErrUnknownNode = errors.New("netsec: unknown node")
)
