package main

import (
	"fmt"
	"sort"
	"strings"
)

// Packet is a single message. Born is the tick at which it becomes
// eligible for injection at its source node.
type Packet struct {
	ID   int
	Src  int
	Dst  int
	Born int
}

// Edge is a directed link with a bounded in-transit queue (Cap packets).
type Edge struct {
	From int
	To   int
	Cap  int
}

// Config fully describes a simulation: topology, routing and traffic.
type Config struct {
	Nodes     int
	BufferCap int     // per-node buffer capacity (packets held at a node)
	Edges     []Edge  // directed edges with bounded queues
	NextHop   [][]int // NextHop[at][dst] = neighbor node id, -1 if unreachable
	Traffic   []Packet
}

// Sim is a deterministic tick-based store-and-forward simulator.
//
// Tick semantics (identical for every run, independent of node order):
//  1. inject:   due packets enter source-node buffers while room remains.
//  2. receive:  every edge delivers its head packet to the destination
//               node if the node buffer has a free slot (else it stays).
//  3. commit:   every buffered packet is routed; all send intents are
//               collected first, then committed in packet-ID order onto
//               edges that still have free capacity. Blocked sends stay
//               in the node buffer (blocking, never dropped).
type Sim struct {
	cfg     Config
	edgeIdx [][]int // edgeIdx[from][to] = edge index or -1
	pkts    []Packet

	tick      int
	buffers   [][]int // packet ids held at each node
	queues    [][]int // packet ids in transit on each edge (FIFO)
	pending   [][]int // packet ids awaiting injection per node
	delivered []int   // delivered packet ids in delivery order
	latency   []int   // latency[id], -1 until delivered
	progress  bool
}

// NewSim validates the config and returns an initialized simulator.
func NewSim(cfg Config) (*Sim, error) {
	s := &Sim{cfg: cfg}
	s.edgeIdx = make([][]int, cfg.Nodes)
	for i := range s.edgeIdx {
		s.edgeIdx[i] = make([]int, cfg.Nodes)
		for j := range s.edgeIdx[i] {
			s.edgeIdx[i][j] = -1
		}
	}
	for ei, e := range cfg.Edges {
		if e.From < 0 || e.From >= cfg.Nodes || e.To < 0 || e.To >= cfg.Nodes {
			return nil, fmt.Errorf("edge %d: endpoint out of range", ei)
		}
		if e.Cap < 1 {
			return nil, fmt.Errorf("edge %d: capacity must be >= 1", ei)
		}
		if s.edgeIdx[e.From][e.To] != -1 {
			return nil, fmt.Errorf("duplicate edge %d -> %d", e.From, e.To)
		}
		s.edgeIdx[e.From][e.To] = ei
	}
	s.pkts = append([]Packet(nil), cfg.Traffic...)
	sort.Slice(s.pkts, func(i, j int) bool { return s.pkts[i].ID < s.pkts[j].ID })
	for i := range s.pkts {
		if s.pkts[i].ID != i {
			return nil, fmt.Errorf("packet IDs must be 0..N-1, missing %d", i)
		}
	}
	for _, p := range s.pkts {
		if p.Src < 0 || p.Src >= cfg.Nodes || p.Dst < 0 || p.Dst >= cfg.Nodes {
			return nil, fmt.Errorf("packet %d: endpoint out of range", p.ID)
		}
		if p.Src != p.Dst {
			if err := checkRoute(cfg, s.edgeIdx, p); err != nil {
				return nil, err
			}
		}
	}
	s.buffers = make([][]int, cfg.Nodes)
	s.queues = make([][]int, len(cfg.Edges))
	s.pending = make([][]int, cfg.Nodes)
	s.latency = make([]int, len(s.pkts))
	for i := range s.latency {
		s.latency[i] = -1
	}
	for _, p := range s.pkts {
		s.pending[p.Src] = append(s.pending[p.Src], p.ID)
	}
	for n := range s.pending {
		sort.Slice(s.pending[n], func(i, j int) bool {
			a, b := s.pkts[s.pending[n][i]], s.pkts[s.pending[n][j]]
			if a.Born != b.Born {
				return a.Born < b.Born
			}
			return a.ID < b.ID
		})
	}
	return s, nil
}

func checkRoute(cfg Config, edgeIdx [][]int, p Packet) error {
	at, seen := p.Src, map[int]bool{p.Src: true}
	for at != p.Dst {
		nxt := cfg.NextHop[at][p.Dst]
		if nxt < 0 || nxt >= cfg.Nodes {
			return fmt.Errorf("packet %d: no route %d -> %d (stuck at %d)", p.ID, p.Src, p.Dst, at)
		}
		if edgeIdx[at][nxt] == -1 {
			return fmt.Errorf("packet %d: route uses missing edge %d -> %d", p.ID, at, nxt)
		}
		if seen[nxt] {
			return fmt.Errorf("packet %d: route to %d loops at %d", p.ID, p.Dst, nxt)
		}
		seen[nxt] = true
		at = nxt
	}
	return nil
}

// Tick returns the current tick (number of completed steps).
func (s *Sim) Tick() int { return s.tick }

// Delivered returns delivered packet ids in delivery order.
func (s *Sim) Delivered() []int { return append([]int(nil), s.delivered...) }

// Latency returns the latency of a delivered packet (-1 if not yet).
func (s *Sim) Latency(id int) int { return s.latency[id] }

// Done reports whether every packet has been delivered.
func (s *Sim) Done() bool { return len(s.delivered) == len(s.pkts) }

// Step advances the simulation by one tick. It reports whether any
// packet moved (injection, receive, delivery or committed send).
func (s *Sim) Step() bool {
	s.progress = false
	s.inject()
	s.receive()
	s.commit()
	s.tick++
	return s.progress
}

func (s *Sim) inject() {
	for n := 0; n < s.cfg.Nodes; n++ {
		for len(s.pending[n]) > 0 {
			p := s.pkts[s.pending[n][0]]
			if p.Born > s.tick || len(s.buffers[n]) >= s.cfg.BufferCap {
				break
			}
			s.pending[n] = s.pending[n][1:]
			s.buffers[n] = append(s.buffers[n], p.ID)
			s.progress = true
		}
	}
}

func (s *Sim) receive() {
	for ei, e := range s.cfg.Edges {
		if len(s.queues[ei]) == 0 {
			continue
		}
		if len(s.buffers[e.To]) >= s.cfg.BufferCap {
			continue // receiver full: packet stays on the edge (blocking)
		}
		p := s.queues[ei][0]
		s.queues[ei] = s.queues[ei][1:]
		s.buffers[e.To] = append(s.buffers[e.To], p)
		s.progress = true
	}
}

type intent struct {
	pkt  int
	edge int
}

func (s *Sim) commit() {
	var intents []intent
	kept := make([][]int, s.cfg.Nodes)
	for n := 0; n < s.cfg.Nodes; n++ {
		for _, pid := range s.buffers[n] {
			p := s.pkts[pid]
			if p.Dst == n {
				s.latency[pid] = s.tick - p.Born
				s.delivered = append(s.delivered, pid)
				s.progress = true
				continue
			}
			nxt := s.cfg.NextHop[n][p.Dst]
			intents = append(intents, intent{pkt: pid, edge: s.edgeIdx[n][nxt]})
			kept[n] = append(kept[n], pid)
		}
	}
	// Uniform commit: intents are sorted by packet id so the result never
	// depends on node iteration order or buffer order.
	sort.Slice(intents, func(i, j int) bool { return intents[i].pkt < intents[j].pkt })
	blocked := make(map[int]bool)
	for _, it := range intents {
		e := s.cfg.Edges[it.edge]
		if len(s.queues[it.edge]) < e.Cap {
			s.queues[it.edge] = append(s.queues[it.edge], it.pkt)
			s.progress = true
		} else {
			blocked[it.pkt] = true // send blocks; packet stays, never dropped
		}
	}
	for n := 0; n < s.cfg.Nodes; n++ {
		out := kept[n][:0]
		for _, pid := range kept[n] {
			if blocked[pid] {
				out = append(out, pid)
			}
		}
		s.buffers[n] = out
	}
}

// RunResult summarizes a finished Run.
type RunResult struct {
	Deadlock  bool
	DeadlockE []WaitEdge // wait-for cycle evidence, when Deadlock
	Ticks     int
}

// Run steps until all packets are delivered, a deadlock fixed point is
// reached, or maxTicks elapses.
func (s *Sim) Run(maxTicks int) RunResult {
	for i := 0; i < maxTicks; i++ {
		if s.Done() {
			return RunResult{Ticks: s.tick}
		}
		if !s.Step() {
			// No progress: fast-forward past future injections, else deadlock.
			next := -1
			for n := range s.pending {
				if len(s.pending[n]) > 0 {
					b := s.pkts[s.pending[n][0]].Born
					if next == -1 || b < next {
						next = b
					}
				}
			}
			if next == -1 {
				return RunResult{
					Deadlock:  true,
					DeadlockE: FindWaitCycle(s.cfg, s.edgeIdx, s.buffers, s.queues, s.pkts),
					Ticks:     s.tick,
				}
			}
			s.tick = next
		}
	}
	return RunResult{Ticks: s.tick} // gave up; neither done nor proven deadlocked
}

// LatencyReport renders per-packet latency plus min/avg/max.
func (s *Sim) LatencyReport() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-4s %-4s %-4s %-5s %s\n", "pkt", "src", "dst", "born", "latency")
	min, max, sum, n := -1, 0, 0, 0
	for _, p := range s.pkts {
		l := s.latency[p.ID]
		fmt.Fprintf(&b, "%-4d %-4d %-4d %-5d %s\n", p.ID, p.Src, p.Dst, p.Born, fmtLat(l))
		if l >= 0 {
			if min == -1 || l < min {
				min = l
			}
			if l > max {
				max = l
			}
			sum += l
			n++
		}
	}
	if n > 0 {
		fmt.Fprintf(&b, "delivered %d/%d  min=%d avg=%.2f max=%d\n",
			n, len(s.pkts), min, float64(sum)/float64(n), max)
	} else {
		fmt.Fprintf(&b, "delivered 0/%d\n", len(s.pkts))
	}
	return b.String()
}

func fmtLat(l int) string {
	if l < 0 {
		return "-"
	}
	return fmt.Sprintf("%d", l)
}

// StateString dumps buffers and edge queues (used in deadlock evidence).
func (s *Sim) StateString() string {
	return FormatState(s.cfg, s.buffers, s.queues, s.pending, s.delivered)
}

// FormatState renders buffers/queues/pending/delivered for evidence output.
func FormatState(cfg Config, buffers, queues, pending [][]int, delivered []int) string {
	var b strings.Builder
	for n := 0; n < cfg.Nodes; n++ {
		fmt.Fprintf(&b, "  node %d buffer(%d/%d): %v\n", n, len(buffers[n]), cfg.BufferCap, buffers[n])
		if len(pending) > 0 && len(pending[n]) > 0 {
			fmt.Fprintf(&b, "  node %d pending injection: %v\n", n, pending[n])
		}
	}
	for ei, e := range cfg.Edges {
		fmt.Fprintf(&b, "  edge %d->%d queue(%d/%d): %v\n", e.From, e.To, len(queues[ei]), e.Cap, queues[ei])
	}
	fmt.Fprintf(&b, "  delivered: %v\n", delivered)
	return b.String()
}
