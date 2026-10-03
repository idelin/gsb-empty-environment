// Package paxos implements the core state machine of a Multi-Paxos
// majority-consensus replicated log with a gap-free commit engine.
//
// Design notes:
//
//   - Two-phase protocol: Prepare/Promise (phase 1) and Accept/Accepted
//     (phase 2). A leader that holds a valid lease skips phase 1 and
//     commits consecutive log entries directly with phase 2.
//   - A newly elected leader re-runs phase 1 and repairs every slot
//     reported by the majority: slots with a highest-ballot accepted value
//     are re-proposed with that value, genuine holes are filled with no-op
//     entries, and dirty (never chosen by a majority) minority entries are
//     safely overwritten.
//   - Commitment is gap-free: commitIndex only ever advances over a
//     contiguous prefix of chosen slots. Followers learn the committed
//     prefix from leader heartbeats, which also repairs follower-side
//     holes and dirty entries.
//   - The state machine is deterministic and clock-driven: nodes never
//     read the wall clock, never spawn goroutines and never do I/O. Time
//     advances only via OnTick and messages travel only via the send
//     callback. A per-node mutex makes the state machine safe for
//     concurrent clients (e.g. Propose from many goroutines while another
//     goroutine drives ticks).
package paxos

import "sync"

// Ballot is a totally ordered leadership term. The node id tie-breaker
// guarantees that ballots are unique per proposer.
type Ballot struct {
	N  uint64
	ID int
}

// Less reports whether b is strictly ordered before o.
func (b Ballot) Less(o Ballot) bool {
	return b.N < o.N || (b.N == o.N && b.ID < o.ID)
}

// IsZero reports whether b is the zero ballot (no value accepted yet).
func (b Ballot) IsZero() bool { return b.N == 0 && b.ID == 0 }

// Role is the protocol role of a node.
type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	}
	return "?"
}

// Slot is a single log entry. A zero Ballot marks an empty (hole) slot.
type Slot struct {
	Ballot Ballot // ballot under which the value was accepted
	Value  string
	NoOp   bool
}

// MsgType enumerates the protocol messages.
type MsgType int

const (
	MsgPrepare MsgType = iota
	MsgPromise
	MsgAccept
	MsgAccepted
	MsgHeartbeat
)

func (t MsgType) String() string {
	switch t {
	case MsgPrepare:
		return "Prepare"
	case MsgPromise:
		return "Promise"
	case MsgAccept:
		return "Accept"
	case MsgAccepted:
		return "Accepted"
	case MsgHeartbeat:
		return "Heartbeat"
	}
	return "?"
}

// Message is the single wire format for all protocol traffic.
type Message struct {
	Type   MsgType
	From   int
	To     int
	Ballot Ballot

	Log       []Slot // Promise payload: acceptor's full log
	Slot      int    // Accept payload index; Accepted slot (-1 = lease ack)
	Value     Slot   // Accept payload
	Committed []Slot // Heartbeat payload: leader's committed prefix
}

// Config holds the deterministic timing parameters (in ticks).
type Config struct {
	ElectionBase    int // base election timeout; node i uses base + stagger*i
	ElectionStagger int // per-node election timeout stagger
	LeaseDuration   int // leader lease validity window
	HeartbeatEvery  int // leader heartbeat period
}

// DefaultConfig returns the timing parameters used by the demo and tests.
func DefaultConfig() Config {
	return Config{
		ElectionBase:    20,
		ElectionStagger: 5,
		LeaseDuration:   15,
		HeartbeatEvery:  5,
	}
}

// Node is a single Multi-Paxos replica. All exported methods are safe for
// concurrent use; the protocol itself is driven by Handle and OnTick.
type Node struct {
	mu sync.Mutex

	id   int
	size int
	send func(to int, m Message)

	baseTimeout    int
	backoff        int
	leaseDuration  int
	heartbeatEvery int

	now         int
	role        Role
	promised    Ballot // highest ballot this node has promised
	ballot      Ballot // own candidacy/leadership ballot
	log         []Slot
	commitIndex int // length of the gap-free committed prefix
	leaseUntil  int // follower: election deadline; leader: lease expiry
	lastBeat    int

	promises  map[int]Message // candidate: collected promises
	acks      []map[int]bool  // leader: per-slot acceptor sets
	chosen    []bool          // leader: per-slot chosen flags
	leaseAcks map[int]int     // leader: peer -> last lease-ack tick

	preparesSent int // instrumentation: phase-1 rounds started
}

func newNode(id, size int, cfg Config, send func(to int, m Message)) *Node {
	base := cfg.ElectionBase + cfg.ElectionStagger*id
	return &Node{
		id:             id,
		size:           size,
		send:           send,
		baseTimeout:    base,
		backoff:        base,
		leaseDuration:  cfg.LeaseDuration,
		heartbeatEvery: cfg.HeartbeatEvery,
		role:           Follower,
		leaseUntil:     base,
		leaseAcks:      map[int]int{},
	}
}

func (n *Node) majority() int { return n.size/2 + 1 }

// --- exported accessors (all take the node lock) ---

// ID returns the node id.
func (n *Node) ID() int { return n.id }

// Role returns the current protocol role.
func (n *Node) Role() Role {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.role
}

// Ballot returns the current leadership ballot (meaningful when Leader).
func (n *Node) Ballot() Ballot {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ballot
}

// Promised returns the highest ballot this node has promised.
func (n *Node) Promised() Ballot {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.promised
}

// CommitIndex returns the length of the gap-free committed prefix.
func (n *Node) CommitIndex() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// Log returns a copy of the full local log (including uncommitted slots).
func (n *Node) Log() []Slot {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Slot(nil), n.log...)
}

// Committed returns a copy of the committed prefix log[:commitIndex].
func (n *Node) Committed() []Slot {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Slot(nil), n.log[:n.commitIndex]...)
}

// PreparesSent returns how many phase-1 rounds this node has started.
// Used to verify the lease fast path bypasses phase 1.
func (n *Node) PreparesSent() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.preparesSent
}

// Propose appends a client value to the log. It succeeds only on a leader
// holding a valid lease, in which case the value is sent straight to
// phase 2 (the lease fast path bypasses phase 1).
func (n *Node) Propose(v string) (slot int, ok bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.role != Leader || n.now >= n.leaseUntil {
		return -1, false
	}
	slot = len(n.log)
	n.proposeSlotLocked(slot, Slot{Ballot: n.ballot, Value: v})
	return slot, true
}

// --- deterministic clock and message entry points ---

// OnTick advances the node's deterministic clock by one tick, firing
// election timeouts, lease expiries and heartbeats as needed.
func (n *Node) OnTick() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.now++
	switch n.role {
	case Leader:
		recent := 0
		for _, t := range n.leaseAcks {
			if n.now-t <= n.leaseDuration {
				recent++
			}
		}
		if recent >= n.majority() {
			n.leaseUntil = n.now + n.leaseDuration
		}
		if n.now >= n.leaseUntil {
			// Lost contact with a majority: relinquish leadership.
			n.stepDownLocked()
			return
		}
		if n.now-n.lastBeat >= n.heartbeatEvery {
			n.broadcastHeartbeatLocked()
		}
	default: // Follower or Candidate
		if n.now >= n.leaseUntil {
			n.startElectionLocked()
		}
	}
}

// Handle processes one inbound protocol message.
func (n *Node) Handle(m Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch m.Type {
	case MsgPrepare:
		n.handlePrepareLocked(m)
	case MsgPromise:
		n.handlePromiseLocked(m)
	case MsgAccept:
		n.handleAcceptLocked(m)
	case MsgAccepted:
		n.handleAcceptedLocked(m)
	case MsgHeartbeat:
		n.handleHeartbeatLocked(m)
	}
}

// --- internal state transitions (caller holds n.mu) ---

func (n *Node) stepDownLocked() {
	n.role = Follower
	n.backoff = n.baseTimeout
	n.leaseUntil = n.now + n.baseTimeout
}

func (n *Node) startElectionLocked() {
	n.role = Candidate
	n.ballot = Ballot{N: n.promised.N + 1, ID: n.id}
	n.promised = n.ballot // promise to self
	n.promises = map[int]Message{
		n.id: {Log: append([]Slot(nil), n.log...)},
	}
	n.preparesSent++
	for p := 0; p < n.size; p++ {
		if p != n.id {
			n.send(p, Message{Type: MsgPrepare, From: n.id, To: p, Ballot: n.ballot})
		}
	}
	// Deterministic exponential backoff bounds ballot inflation when a
	// candidate is partitioned away from a majority.
	n.leaseUntil = n.now + n.backoff
	if n.backoff*2 <= 4*n.baseTimeout {
		n.backoff *= 2
	} else {
		n.backoff = 4 * n.baseTimeout
	}
}

func (n *Node) becomeLeaderLocked() {
	n.role = Leader
	n.leaseAcks = map[int]int{n.id: n.now}
	n.leaseUntil = n.now + n.leaseDuration
	n.lastBeat = n.now

	// Phase-1 repair: for every slot known to the majority, re-propose
	// the highest-ballot accepted value; fill genuine holes with no-ops.
	maxLen := len(n.log)
	for _, p := range n.promises {
		if len(p.Log) > maxLen {
			maxLen = len(p.Log)
		}
	}
	for i := 0; i < maxLen; i++ {
		if i < n.commitIndex {
			continue // committed prefix is immutable
		}
		var best Slot
		have := false
		if i < len(n.log) && !n.log[i].Ballot.IsZero() {
			best, have = n.log[i], true
		}
		for _, p := range n.promises {
			if i < len(p.Log) && !p.Log[i].Ballot.IsZero() &&
				(!have || best.Ballot.Less(p.Log[i].Ballot)) {
				best, have = p.Log[i], true
			}
		}
		s := Slot{Ballot: n.ballot, NoOp: true}
		if have {
			s.Value, s.NoOp = best.Value, best.NoOp
		}
		n.proposeSlotLocked(i, s)
	}
}

// proposeSlotLocked writes slot i locally (self-accept) and broadcasts
// phase-2 Accepts. It is used both by repair and by the lease fast path.
func (n *Node) proposeSlotLocked(i int, s Slot) {
	// The log may already extend past i (accepted as a follower), while
	// the leader-side bookkeeping starts empty; align them independently.
	for len(n.log) <= i {
		n.log = append(n.log, Slot{})
	}
	for len(n.acks) <= i {
		n.acks = append(n.acks, nil)
		n.chosen = append(n.chosen, false)
	}
	n.log[i] = s
	n.acks[i] = map[int]bool{n.id: true}
	n.chosen[i] = false
	for p := 0; p < n.size; p++ {
		if p != n.id {
			n.send(p, Message{Type: MsgAccept, From: n.id, To: p, Ballot: n.ballot, Slot: i, Value: s})
		}
	}
}

func (n *Node) broadcastHeartbeatLocked() {
	n.lastBeat = n.now
	committed := append([]Slot(nil), n.log[:n.commitIndex]...)
	for p := 0; p < n.size; p++ {
		if p != n.id {
			n.send(p, Message{Type: MsgHeartbeat, From: n.id, To: p, Ballot: n.ballot, Slot: -1, Committed: committed})
		}
	}
}

func (n *Node) handlePrepareLocked(m Message) {
	if m.Ballot.Less(n.promised) {
		// Nack: inform the proposer of the higher promised ballot.
		n.send(m.From, Message{Type: MsgPromise, From: n.id, To: m.From, Ballot: n.promised})
		return
	}
	n.promised = m.Ballot
	n.role = Follower // a leader/candidate steps down on a higher prepare
	n.backoff = n.baseTimeout
	n.leaseUntil = n.now + n.baseTimeout
	n.send(m.From, Message{
		Type:   MsgPromise,
		From:   n.id,
		To:     m.From,
		Ballot: m.Ballot,
		Log:    append([]Slot(nil), n.log...),
	})
}

func (n *Node) handlePromiseLocked(m Message) {
	if n.ballot.Less(m.Ballot) {
		// Nack (or a promise for a newer ballot): adopt and step down.
		n.promised = m.Ballot
		if n.role != Follower {
			n.stepDownLocked()
		}
		return
	}
	if n.role != Candidate || m.Ballot != n.ballot {
		return
	}
	n.promises[m.From] = m
	if len(n.promises) >= n.majority() {
		n.becomeLeaderLocked()
	}
}

func (n *Node) handleAcceptLocked(m Message) {
	if m.Ballot.Less(n.promised) {
		// Nack so a stale leader learns about the higher ballot.
		n.send(m.From, Message{Type: MsgAccepted, From: n.id, To: m.From, Ballot: n.promised, Slot: m.Slot})
		return
	}
	n.promised = m.Ballot
	n.role = Follower
	n.backoff = n.baseTimeout
	n.leaseUntil = n.now + n.baseTimeout
	for len(n.log) <= m.Slot {
		n.log = append(n.log, Slot{}) // holes stay empty until repaired
	}
	// Overwrite any dirty (never chosen) entry at this slot.
	n.log[m.Slot] = m.Value
	n.send(m.From, Message{Type: MsgAccepted, From: n.id, To: m.From, Ballot: m.Ballot, Slot: m.Slot})
}

func (n *Node) handleAcceptedLocked(m Message) {
	if n.ballot.Less(m.Ballot) {
		n.promised = m.Ballot
		if n.role != Follower {
			n.stepDownLocked()
		}
		return
	}
	if n.role != Leader || m.Ballot != n.ballot {
		return
	}
	n.leaseAcks[m.From] = n.now // lease ack (slot -1 heartbeats included)
	if m.Slot < 0 || m.Slot >= len(n.acks) {
		return
	}
	if n.acks[m.Slot] == nil {
		n.acks[m.Slot] = map[int]bool{}
	}
	n.acks[m.Slot][m.From] = true
	if !n.chosen[m.Slot] && len(n.acks[m.Slot]) >= n.majority() {
		n.chosen[m.Slot] = true
		// Gap-free commit: advance only over a contiguous chosen prefix.
		for n.commitIndex < len(n.log) && n.chosen[n.commitIndex] {
			n.commitIndex++
		}
	}
}

func (n *Node) handleHeartbeatLocked(m Message) {
	if m.Ballot.Less(n.promised) {
		n.send(m.From, Message{Type: MsgAccepted, From: n.id, To: m.From, Ballot: n.promised, Slot: -1})
		return
	}
	n.promised = m.Ballot
	n.role = Follower
	n.backoff = n.baseTimeout
	n.leaseUntil = n.now + n.baseTimeout
	// Apply the leader's committed prefix. This repairs follower-side
	// holes and safely overwrites dirty minority entries: any entry the
	// leader reports as committed was chosen by a majority, so replacing
	// a local unchosen entry can never lose a committed value.
	for i, s := range m.Committed {
		if i < len(n.log) {
			n.log[i] = s
		} else {
			n.log = append(n.log, s)
		}
	}
	if len(m.Committed) > n.commitIndex {
		n.commitIndex = len(m.Committed)
	}
	n.send(m.From, Message{Type: MsgAccepted, From: n.id, To: m.From, Ballot: m.Ballot, Slot: -1})
}
