// Package dvsim simulates distance-vector routing with split horizon and
// poison reverse over a discrete-event model. Time advances in integer
// ticks; all packets, queues and links are events inside the simulator.
// No real network traffic is generated.
package dvsim

import (
	"container/heap"
	"math/rand"
	"sort"
)

// Infinity is the metric ceiling. Any metric >= Infinity means
// "destination unreachable", which bounds count-to-infinity.
const Infinity = 16

type event struct {
	tick  int
	order int
	run   func()
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].order < h[j].order
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Link is a bidirectional edge between two nodes.
type Link struct {
	A, B int
	Cost int
	Up   bool
}

// Sim is the discrete-event simulator.
type Sim struct {
	Now        int
	MaxLatency int // max per-message delivery latency in ticks (>=1)
	events     eventHeap
	order      int
	rng        *rand.Rand
	nodes      map[int]*Node
	links      []*Link
}

// NewSim creates a simulator. seed makes message latencies reproducible.
func NewSim(seed int64) *Sim {
	return &Sim{
		nodes:      make(map[int]*Node),
		rng:        rand.New(rand.NewSource(seed)),
		MaxLatency: 5,
	}
}

// Schedule runs fn at tick Now+delay.
func (s *Sim) Schedule(delay int, fn func()) {
	heap.Push(&s.events, event{tick: s.Now + delay, order: s.order, run: fn})
	s.order++
}

// Run processes events until the queue is empty or maxTick is exceeded.
// Because updates are triggered (not periodic), an empty queue means the
// network has converged: no node has anything left to say.
func (s *Sim) Run(maxTick int) {
	for len(s.events) > 0 {
		e := heap.Pop(&s.events).(event)
		if e.tick > maxTick {
			heap.Push(&s.events, e)
			return
		}
		s.Now = e.tick
		e.run()
	}
}

// Pending reports how many events are still queued. Zero means converged.
func (s *Sim) Pending() int { return len(s.events) }

// AddNode adds a router.
func (s *Sim) AddNode(id int) *Node {
	n := &Node{
		ID:      id,
		sim:     s,
		table:   map[int]Route{id: {Metric: 0, NextHop: id}},
		links:   make(map[int]*Link),
		lastSeq: make(map[int]int),
	}
	s.nodes[id] = n
	return n
}

// Node returns the node with the given id, or nil.
func (s *Sim) Node(id int) *Node { return s.nodes[id] }

// NodeIDs returns sorted node ids.
func (s *Sim) NodeIDs() []int {
	ids := make([]int, 0, len(s.nodes))
	for id := range s.nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// AddLink creates an up link between two existing nodes.
func (s *Sim) AddLink(a, b, cost int) *Link {
	l := &Link{A: a, B: b, Cost: cost, Up: true}
	s.links = append(s.links, l)
	s.nodes[a].links[b] = l
	s.nodes[b].links[a] = l
	return l
}

// Links returns all links in creation order.
func (s *Sim) Links() []*Link { return s.links }

// SetLinkUp brings a link up or down and notifies both endpoints.
func (s *Sim) SetLinkUp(l *Link, up bool) {
	if l.Up == up {
		return
	}
	l.Up = up
	s.nodes[l.A].onLinkChange(l.B)
	s.nodes[l.B].onLinkChange(l.A)
}

// Start makes every node advertise its initial table.
func (s *Sim) Start() {
	for _, id := range s.NodeIDs() {
		s.nodes[id].trigger()
	}
}
