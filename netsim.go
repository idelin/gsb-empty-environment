// Package netsim simulates packet transport across a network of nodes
// connected by capacity-limited, directed edge queues.
//
// Tick semantics (synchronous, order-independent):
//  1. Receive phase: for every edge, a head-of-queue packet whose
//     destination is the edge's endpoint is consumed (delivered).
//  2. Send phase: all forwarding/injection decisions are computed against
//     the post-receive snapshot and committed atomically. Because each
//     directed edge is written only by its source node and read only by
//     its destination node, the node traversal order cannot influence
//     the outcome.
//
// Sends block instead of dropping: a packet that cannot enter a full
// edge queue stays where it is. Capacity freed by a departure in the
// same commit is NOT reusable until the next tick (conservative,
// order-independent rule).
package netsim

import (
	"fmt"
	"sort"
)

// Packet is injected at Src at InjectTick and routed toward Dst.
type Packet struct {
	ID, Src, Dst int
	InjectTick   int
}

// Edge is a directed, capacity-limited FIFO queue from From to To.
// One hop of transport takes one tick.
type Edge struct {
	ID, From, To, Capacity int
}

// Network holds topology and the given (static) routing table.
type Network struct {
	NumNodes int
	Edges    []*Edge
	incoming [][]*Edge
	byPair   map[[2]int]*Edge
	nextHop  map[[2]int]int // (node, dst) -> next node
}

func NewNetwork(numNodes int) *Network {
	return &Network{
		NumNodes: numNodes,
		incoming: make([][]*Edge, numNodes),
		byPair:   make(map[[2]int]*Edge),
		nextHop:  make(map[[2]int]int),
	}
}

func (n *Network) AddEdge(from, to, capacity int) (*Edge, error) {
	if from < 0 || from >= n.NumNodes || to < 0 || to >= n.NumNodes {
		return nil, fmt.Errorf("edge %d->%d: node id out of range", from, to)
	}
	if capacity < 1 {
		return nil, fmt.Errorf("edge %d->%d: capacity must be >= 1", from, to)
	}
	if _, dup := n.byPair[[2]int{from, to}]; dup {
		return nil, fmt.Errorf("edge %d->%d: duplicate edge", from, to)
	}
	e := &Edge{ID: len(n.Edges), From: from, To: to, Capacity: capacity}
	n.Edges = append(n.Edges, e)
	n.incoming[to] = append(n.incoming[to], e)
	n.byPair[[2]int{from, to}] = e
	return e, nil
}

// SetRoute installs one next-hop entry of the given routing table.
func (n *Network) SetRoute(node, dst, next int) error {
	if _, ok := n.byPair[[2]int{node, next}]; !ok {
		return fmt.Errorf("route at %d toward %d via %d: no edge %d->%d", node, dst, next, node, next)
	}
	n.nextHop[[2]int{node, dst}] = next
	return nil
}

// NextEdge returns the outgoing edge a packet at node bound for dst uses.
func (n *Network) NextEdge(node, dst int) *Edge {
	next, ok := n.nextHop[[2]int{node, dst}]
	if !ok {
		return nil
	}
	return n.byPair[[2]int{node, next}]
}

// ValidateRoutes ensures every packet has a complete, loop-free route.
func (n *Network) ValidateRoutes(packets []*Packet) error {
	for _, p := range packets {
		node := p.Src
		for hops := 0; node != p.Dst; hops++ {
			if hops >= n.NumNodes {
				return fmt.Errorf("packet %d: routing loop from %d toward %d", p.ID, p.Src, p.Dst)
			}
			next, ok := n.nextHop[[2]int{node, p.Dst}]
			if !ok {
				return fmt.Errorf("packet %d: no route at node %d toward %d", p.ID, node, p.Dst)
			}
			node = next
		}
	}
	return nil
}

// State is the full, cloneable simulation state.
type State struct {
	Tick      int
	Queues    [][]int     // edge ID -> FIFO queue of packet IDs
	Inject    [][]int     // node -> pending packet IDs, sorted by (InjectTick, ID)
	Delivered map[int]int // packet ID -> delivery tick
}

func (st *State) Clone() *State {
	out := &State{
		Tick:      st.Tick,
		Queues:    make([][]int, len(st.Queues)),
		Inject:    make([][]int, len(st.Inject)),
		Delivered: make(map[int]int, len(st.Delivered)),
	}
	for i, q := range st.Queues {
		out.Queues[i] = append([]int(nil), q...)
	}
	for i, q := range st.Inject {
		out.Inject[i] = append([]int(nil), q...)
	}
	for k, v := range st.Delivered {
		out.Delivered[k] = v
	}
	return out
}

// Sim is an immutable simulator over a network and a packet workload.
type Sim struct {
	Net     *Network
	Packets []*Packet // sorted by ID, IDs must be 0..n-1
}

func NewSim(net *Network, packets []*Packet) (*Sim, error) {
	sorted := make([]*Packet, len(packets))
	copy(sorted, packets)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for i, p := range sorted {
		if p.ID != i {
			return nil, fmt.Errorf("packet IDs must be 0..n-1, found %d at position %d", p.ID, i)
		}
	}
	if err := net.ValidateRoutes(sorted); err != nil {
		return nil, err
	}
	return &Sim{Net: net, Packets: sorted}, nil
}

func (s *Sim) packet(id int) *Packet { return s.Packets[id] }

// Initial builds the tick-0 state with all packets pending injection.
func (s *Sim) Initial() *State {
	st := &State{
		Tick:      0,
		Queues:    make([][]int, len(s.Net.Edges)),
		Inject:    make([][]int, s.Net.NumNodes),
		Delivered: map[int]int{},
	}
	for _, p := range s.Packets {
		st.Inject[p.Src] = append(st.Inject[p.Src], p.ID)
	}
	for v := range st.Inject {
		q := st.Inject[v]
		sort.Slice(q, func(i, j int) bool {
			a, b := s.packet(q[i]), s.packet(q[j])
			if a.InjectTick != b.InjectTick {
				return a.InjectTick < b.InjectTick
			}
			return a.ID < b.ID
		})
	}
	return st
}

// Receive performs the receive phase: delivers head-of-queue packets that
// arrived at their destination. Returns a new state at the same tick.
func (s *Sim) Receive(st *State) *State {
	out := st.Clone()
	for _, e := range s.Net.Edges {
		if q := out.Queues[e.ID]; len(q) > 0 && s.packet(q[0]).Dst == e.To {
			out.Delivered[q[0]] = st.Tick
			out.Queues[e.ID] = q[1:]
		}
	}
	for v := 0; v < s.Net.NumNodes; v++ {
		if q := out.Inject[v]; len(q) > 0 {
			if p := s.packet(q[0]); p.Dst == v && p.InjectTick <= st.Tick {
				out.Delivered[q[0]] = st.Tick
				out.Inject[v] = q[1:]
			}
		}
	}
	return out
}

// Move is a staged send: Packet leaves the head of FromEdge (or the
// injection queue when FromEdge == -1) and enters ToEdge at commit.
type Move struct {
	Node     int
	Packet   int
	FromEdge int // -1 means injection
	ToEdge   int
}

func (s *Sim) room(st *State, e *Edge) int { return e.Capacity - len(st.Queues[e.ID]) }

// Candidates lists every send a node could stage in the send phase:
// the head packet of each incoming edge (if forwardable) and the head
// of each node's injection queue (if due). At most one candidate per
// incoming edge and per injection queue per tick.
func (s *Sim) Candidates(st *State) []Move {
	var moves []Move
	for v := 0; v < s.Net.NumNodes; v++ {
		for _, e := range s.Net.incoming[v] {
			q := st.Queues[e.ID]
			if len(q) == 0 {
				continue
			}
			p := s.packet(q[0])
			if p.Dst == v {
				continue // consumable, handled by Receive
			}
			if f := s.Net.NextEdge(v, p.Dst); f != nil && s.room(st, f) > 0 {
				moves = append(moves, Move{Node: v, Packet: p.ID, FromEdge: e.ID, ToEdge: f.ID})
			}
		}
		if q := st.Inject[v]; len(q) > 0 {
			p := s.packet(q[0])
			if p.Dst != v && p.InjectTick <= st.Tick {
				if f := s.Net.NextEdge(v, p.Dst); f != nil && s.room(st, f) > 0 {
					moves = append(moves, Move{Node: v, Packet: p.ID, FromEdge: -1, ToEdge: f.ID})
				}
			}
		}
	}
	return moves
}

// Compatible reports whether a set of moves can be committed together:
// no packet or source edge used twice, no edge over capacity.
func (s *Sim) Compatible(st *State, moves []Move) bool {
	used := map[int]int{}
	srcUsed := map[int]bool{}
	pktUsed := map[int]bool{}
	for _, m := range moves {
		if pktUsed[m.Packet] || (m.FromEdge >= 0 && srcUsed[m.FromEdge]) {
			return false
		}
		pktUsed[m.Packet] = true
		srcUsed[m.FromEdge] = true
		used[m.ToEdge]++
		if used[m.ToEdge] > s.room(st, s.Net.Edges[m.ToEdge]) {
			return false
		}
	}
	return true
}

// Apply commits a compatible set of moves atomically and advances the tick.
func (s *Sim) Apply(st *State, moves []Move) *State {
	out := st.Clone()
	for _, m := range moves {
		if m.FromEdge < 0 {
			q := out.Inject[m.Node]
			if len(q) == 0 || q[0] != m.Packet {
				panic("netsim: injection move not at queue head")
			}
			out.Inject[m.Node] = q[1:]
		} else {
			q := out.Queues[m.FromEdge]
			if len(q) == 0 || q[0] != m.Packet {
				panic("netsim: forward move not at edge head")
			}
			out.Queues[m.FromEdge] = q[1:]
		}
		out.Queues[m.ToEdge] = append(out.Queues[m.ToEdge], m.Packet)
	}
	out.Tick = st.Tick + 1
	return out
}

// Greedy picks a maximal compatible set of candidates. Nodes are visited
// in nodeOrder; because distinct nodes never share an edge, the result is
// independent of nodeOrder. Within a node, candidates keep edge-ID order.
func (s *Sim) Greedy(st *State, cands []Move, nodeOrder []int) []Move {
	if nodeOrder == nil {
		nodeOrder = make([]int, s.Net.NumNodes)
		for i := range nodeOrder {
			nodeOrder[i] = i
		}
	}
	byNode := map[int][]Move{}
	for _, c := range cands {
		byNode[c.Node] = append(byNode[c.Node], c)
	}
	used := map[int]int{}
	var moves []Move
	for _, v := range nodeOrder {
		for _, c := range byNode[v] {
			if used[c.ToEdge] >= s.room(st, s.Net.Edges[c.ToEdge]) {
				continue
			}
			used[c.ToEdge]++
			moves = append(moves, c)
		}
	}
	return moves
}

func (s *Sim) allDelivered(st *State) bool { return len(st.Delivered) == len(s.Packets) }

// nextInjectTick returns the smallest future injection tick among pending
// packets, so the simulator can fast-forward idle ticks.
func (s *Sim) nextInjectTick(st *State) (int, bool) {
	best := -1
	for v := range st.Inject {
		for _, pid := range st.Inject[v] {
			if t := s.packet(pid).InjectTick; t > st.Tick && (best < 0 || t < best) {
				best = t
			}
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// RunResult summarizes a deterministic run.
type RunResult struct {
	Final    *State
	Done     bool // all packets delivered
	Deadlock bool // undelivered packets remain and no move is possible
}

// Run executes the deterministic greedy simulation. nodeOrder controls
// node traversal order (nil = 0..n-1); the outcome does not depend on it.
func (s *Sim) Run(st *State, maxTicks int, nodeOrder []int) RunResult {
	for i := 0; i < maxTicks; i++ {
		st = s.Receive(st)
		if s.allDelivered(st) {
			return RunResult{Final: st, Done: true}
		}
		cands := s.Candidates(st)
		if len(cands) == 0 {
			if nt, ok := s.nextInjectTick(st); ok {
				st = st.Clone()
				st.Tick = nt
				continue
			}
			return RunResult{Final: st, Deadlock: true}
		}
		st = s.Apply(st, s.Greedy(st, cands, nodeOrder))
	}
	return RunResult{Final: st}
}

// Latency returns the end-to-end latency of a delivered packet.
func (s *Sim) Latency(st *State, packetID int) (int, bool) {
	t, ok := st.Delivered[packetID]
	if !ok {
		return 0, false
	}
	return t - s.packet(packetID).InjectTick, true
}
