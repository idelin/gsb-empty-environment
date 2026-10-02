// Package paxos implements a Multi-Paxos majority-consensus replicated log
// with Prepare/Promise (Phase 1) and Accept/Accepted (Phase 2), leader
// leases that allow the leader to skip Phase 1 for consecutive entries,
// hole repair after leader change, and safe overwrite of dirty minority
// log entries. The network is simulated in-memory with partition support
// so the whole engine is deterministic and testable with go test.
package paxos

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Entry is a single replicated log record.
type Entry struct {
	Term  uint64 // term in which the entry was (last) proposed
	Noop  bool   // hole-filler written by a new leader during repair
	Key   string
	Value string
}

func (e Entry) String() string {
	if e.Noop {
		return fmt.Sprintf("{term=%d noop}", e.Term)
	}
	return fmt.Sprintf("{term=%d %s=%s}", e.Term, e.Key, e.Value)
}

// Slot is one log position on a node.
type Slot struct {
	Entry     Entry
	Accepted  bool // accepted by this node
	Committed bool // known to be chosen by a majority
}

// message counters, exported for tests/diagnostics.
type Stats struct {
	Prepares int
	Accepts  int
	Commits  int
}

// Network simulates the communication fabric with partition support.
// Delivery is synchronous: an RPC handler runs on the caller's goroutine,
// which keeps tests deterministic.
type Network struct {
	mu      sync.Mutex
	n       int
	blocked map[[2]int]bool
	Stats   Stats
}

func NewNetwork(n int) *Network {
	return &Network{n: n, blocked: make(map[[2]int]bool)}
}

// Partition cuts all traffic between group a and group b (both directions).
func (net *Network) Partition(a, b []int) {
	net.mu.Lock()
	defer net.mu.Unlock()
	for _, x := range a {
		for _, y := range b {
			net.blocked[[2]int{x, y}] = true
			net.blocked[[2]int{y, x}] = true
		}
	}
}

// Heal removes all partitions.
func (net *Network) Heal() {
	net.mu.Lock()
	defer net.mu.Unlock()
	net.blocked = make(map[[2]int]bool)
}

func (net *Network) reachable(from, to int) bool {
	if from == to {
		return true
	}
	net.mu.Lock()
	defer net.mu.Unlock()
	return !net.blocked[[2]int{from, to}]
}

func (net *Network) countPrepare() { net.mu.Lock(); net.Stats.Prepares++; net.mu.Unlock() }
func (net *Network) countAccept()  { net.mu.Lock(); net.Stats.Accepts++; net.mu.Unlock() }
func (net *Network) countCommit()  { net.mu.Lock(); net.Stats.Commits++; net.mu.Unlock() }

// ---- RPC payloads ----

type prepareReq struct {
	term     uint64
	fromID   int
	fromSlot int // sender only cares about slots >= fromSlot
}

type promise struct {
	ok       bool
	fromID   int
	promised uint64
	accepted map[int]Slot // accepted slots >= fromSlot known to the responder
}

type acceptReq struct {
	term  uint64
	slot  int
	entry Entry
}

type accepted struct {
	ok       bool
	fromID   int
	promised uint64 // responder's current promised term (for step-down)
}

type commitReq struct {
	term  uint64
	slot  int
	entry Entry
}

// Node is a single Paxos replica.
type Node struct {
	mu      sync.Mutex
	id      int
	cluster []*Node
	net     *Network
	lease   time.Duration
	now     func() time.Time // injectable clock for deterministic tests

	// persistent state
	promised    uint64
	log         map[int]*Slot
	commitIndex int // highest contiguous committed slot (no holes below it)

	// leader state
	isLeader   bool
	term       uint64
	leaseUntil time.Time
	nextSlot   int
}

func newNode(id int, net *Network, lease time.Duration, now func() time.Time) *Node {
	return &Node{
		id:       id,
		net:      net,
		lease:    lease,
		now:      now,
		log:      make(map[int]*Slot),
		nextSlot: 1,
	}
}

// Cluster is a fixed set of nodes sharing one network.
type Cluster struct {
	Nodes []*Node
	Net   *Network
}

// NewCluster builds n nodes. lease is the leader lease duration; now may be
// nil to use the wall clock.
func NewCluster(n int, lease time.Duration, now func() time.Time) *Cluster {
	if now == nil {
		now = time.Now
	}
	net := NewNetwork(n)
	c := &Cluster{Net: net}
	for i := 0; i < n; i++ {
		c.Nodes = append(c.Nodes, newNode(i, net, lease, now))
	}
	for _, nd := range c.Nodes {
		nd.cluster = c.Nodes
	}
	return c
}

func (c *Cluster) majority() int { return len(c.Nodes)/2 + 1 }

// ID returns the node's id.
func (nd *Node) ID() int { return nd.id }

// IsLeader reports whether the node currently believes it is leader.
func (nd *Node) IsLeader() bool {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	return nd.isLeader
}

// Term returns the node's current term.
func (nd *Node) Term() uint64 {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	return nd.term
}

// CommitIndex returns the highest contiguous committed slot.
func (nd *Node) CommitIndex() int {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	return nd.commitIndex
}

// Committed returns a snapshot of the committed prefix (slots 1..commitIndex).
func (nd *Node) Committed() []Entry {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	out := make([]Entry, 0, nd.commitIndex)
	for i := 1; i <= nd.commitIndex; i++ {
		if s, ok := nd.log[i]; ok && s.Committed {
			out = append(out, s.Entry)
		}
	}
	return out
}

// SlotState exposes a slot for inspection (tests).
func (nd *Node) SlotState(slot int) (Slot, bool) {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	s, ok := nd.log[slot]
	if !ok {
		return Slot{}, false
	}
	return *s, true
}

// ---- Phase 1 ----

func (nd *Node) handlePrepare(req prepareReq) promise {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	if req.term <= nd.promised {
		return promise{ok: false, fromID: nd.id, promised: nd.promised}
	}
	nd.promised = req.term
	if nd.isLeader && req.term > nd.term {
		nd.isLeader = false // a higher term exists: step down
	}
	p := promise{ok: true, fromID: nd.id, promised: nd.promised, accepted: make(map[int]Slot)}
	for slot, s := range nd.log {
		if slot >= req.fromSlot && s.Accepted {
			p.accepted[slot] = *s
		}
	}
	return p
}

// Elect runs Phase 1 with the given term and, on success, becomes leader,
// repairs all holes/uncommitted slots learned from the majority, and starts
// a lease during which Phase 1 is skipped for new entries.
func (nd *Node) Elect(term uint64) bool {
	nd.mu.Lock()
	if term <= nd.promised {
		nd.mu.Unlock()
		return false
	}
	nd.term = term
	nd.promised = term
	fromSlot := nd.commitIndex + 1
	nd.mu.Unlock()

	votes := 1 // self
	accepted := make(map[int]Slot)
	nd.mu.Lock()
	for slot, s := range nd.log {
		if slot >= fromSlot && s.Accepted {
			accepted[slot] = *s
		}
	}
	nd.mu.Unlock()

	for _, peer := range nd.cluster {
		if peer.id == nd.id || !nd.net.reachable(nd.id, peer.id) {
			continue
		}
		nd.net.countPrepare()
		p := peer.handlePrepare(prepareReq{term: term, fromID: nd.id, fromSlot: fromSlot})
		if !p.ok {
			continue
		}
		votes++
		for slot, s := range p.accepted {
			cur, ok := accepted[slot]
			if !ok || s.Entry.Term > cur.Entry.Term {
				accepted[slot] = s
			}
		}
	}
	if votes < nd.majority() {
		return false
	}

	nd.mu.Lock()
	nd.isLeader = true
	nd.leaseUntil = nd.now().Add(nd.lease)
	nd.mu.Unlock()

	// Hole repair: every slot between commitIndex+1 and the highest slot any
	// acceptor has seen must be chosen before new client traffic, so the
	// committed prefix never develops holes. Slots nobody accepted get a
	// no-op; slots with accepted values get the highest-term value re-chosen.
	maxSlot := fromSlot - 1
	for slot := range accepted {
		if slot > maxSlot {
			maxSlot = slot
		}
	}
	for slot := fromSlot; slot <= maxSlot; slot++ {
		e, ok := accepted[slot]
		var entry Entry
		if ok {
			entry = e.Entry
		} else {
			entry = Entry{Noop: true}
		}
		if !nd.proposeAt(slot, entry) {
			nd.mu.Lock()
			nd.isLeader = false
			nd.mu.Unlock()
			return false
		}
	}
	nd.mu.Lock()
	if nd.nextSlot < maxSlot+1 {
		nd.nextSlot = maxSlot + 1
	}
	nd.mu.Unlock()
	return true
}

// ---- Phase 2 ----

func (nd *Node) handleAccept(req acceptReq) accepted {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	if req.term < nd.promised {
		return accepted{ok: false, fromID: nd.id, promised: nd.promised}
	}
	nd.promised = req.term
	if nd.isLeader && req.term > nd.term {
		nd.isLeader = false
	}
	s, ok := nd.log[req.slot]
	if !ok {
		s = &Slot{}
		nd.log[req.slot] = s
	}
	// Safe overwrite: only a higher-or-equal term can replace an accepted
	// value; committed values are protected by majority intersection.
	s.Entry = req.entry
	s.Accepted = true
	return accepted{ok: true, fromID: nd.id, promised: nd.promised}
}

func (nd *Node) handleCommit(req commitReq) {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	if req.term < nd.promised {
		return
	}
	nd.promised = req.term
	s, ok := nd.log[req.slot]
	if !ok {
		s = &Slot{}
		nd.log[req.slot] = s
	}
	s.Entry = req.entry
	s.Accepted = true
	s.Committed = true
	nd.advanceLocked()
}

// advanceLocked grows the contiguous committed prefix.
func (nd *Node) advanceLocked() {
	for {
		next := nd.commitIndex + 1
		s, ok := nd.log[next]
		if !ok || !s.Committed {
			return
		}
		nd.commitIndex = next
	}
}

// proposeAt runs Phase 2 for a specific slot and commits it if a majority
// accepts. Used for hole repair and for normal client proposals.
func (nd *Node) proposeAt(slot int, entry Entry) bool {
	nd.mu.Lock()
	term := nd.term
	entry.Term = term
	nd.mu.Unlock()

	// self-accept
	nd.mu.Lock()
	s, ok := nd.log[slot]
	if !ok {
		s = &Slot{}
		nd.log[slot] = s
	}
	s.Entry = entry
	s.Accepted = true
	nd.mu.Unlock()

	votes := 1
	need := nd.majority()
	for _, peer := range nd.cluster {
		if peer.id == nd.id || !nd.net.reachable(nd.id, peer.id) {
			continue
		}
		nd.net.countAccept()
		a := peer.handleAccept(acceptReq{term: term, slot: slot, entry: entry})
		if !a.ok {
			if a.promised > term {
				nd.mu.Lock()
				if a.promised > nd.promised {
					nd.promised = a.promised
				}
				nd.isLeader = false
				nd.mu.Unlock()
				return false
			}
			continue
		}
		votes++
	}
	if votes < need {
		return false
	}

	nd.mu.Lock()
	s = nd.log[slot]
	s.Committed = true
	nd.advanceLocked()
	nd.mu.Unlock()

	// best-effort commit broadcast
	for _, peer := range nd.cluster {
		if peer.id == nd.id || !nd.net.reachable(nd.id, peer.id) {
			continue
		}
		nd.net.countCommit()
		peer.handleCommit(commitReq{term: term, slot: slot, entry: entry})
	}
	return true
}

// Propose appends a client entry under the leader lease, skipping Phase 1.
// Returns the assigned slot and whether the entry was committed.
func (nd *Node) Propose(key, value string) (int, bool) {
	nd.mu.Lock()
	if !nd.isLeader || nd.now().After(nd.leaseUntil) {
		nd.mu.Unlock()
		return 0, false
	}
	slot := nd.nextSlot
	nd.nextSlot++
	nd.mu.Unlock()
	ok := nd.proposeAt(slot, Entry{Key: key, Value: value})
	return slot, ok
}

// SyncFollowers pushes all of the leader's accepted/committed slots to every
// reachable peer. After a partition heals this converges lagging or dirty
// minority nodes: their uncommitted entries are overwritten by the current
// term's chosen values.
func (nd *Node) SyncFollowers() {
	nd.mu.Lock()
	if !nd.isLeader {
		nd.mu.Unlock()
		return
	}
	term := nd.term
	slots := make(map[int]Slot)
	maxSlot := 0
	for slot, s := range nd.log {
		if s.Accepted || s.Committed {
			slots[slot] = *s
			if slot > maxSlot {
				maxSlot = slot
			}
		}
	}
	nd.mu.Unlock()

	for _, peer := range nd.cluster {
		if peer.id == nd.id || !nd.net.reachable(nd.id, peer.id) {
			continue
		}
		for slot := 1; slot <= maxSlot; slot++ {
			s, ok := slots[slot]
			if !ok {
				continue
			}
			nd.net.countAccept()
			a := peer.handleAccept(acceptReq{term: term, slot: slot, entry: s.Entry})
			if !a.ok {
				continue
			}
			if s.Committed {
				nd.net.countCommit()
				peer.handleCommit(commitReq{term: term, slot: slot, entry: s.Entry})
			}
		}
	}
}

func (nd *Node) majority() int { return len(nd.cluster)/2 + 1 }

// CheckConsistency verifies that all nodes agree on the committed prefix and
// that the prefix has no holes. Returns an error describing the first
// divergence found.
func (c *Cluster) CheckConsistency() error {
	var ref []Entry
	for i, nd := range c.Nodes {
		com := nd.Committed()
		if i == 0 {
			ref = com
			continue
		}
		// committed prefixes must be identical up to the shorter length
		shorter := len(ref)
		if len(com) < shorter {
			shorter = len(com)
		}
		for j := 0; j < shorter; j++ {
			if ref[j] != com[j] {
				return fmt.Errorf("node %d diverges at slot %d: %v vs %v", nd.id, j+1, ref[j], com[j])
			}
		}
	}
	return nil
}

// SortedSlots is a test helper listing slot numbers a node knows.
func (nd *Node) SortedSlots() []int {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	out := make([]int, 0, len(nd.log))
	for slot := range nd.log {
		out = append(out, slot)
	}
	sort.Ints(out)
	return out
}
