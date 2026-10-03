package paxos

import (
	"errors"
	"sync"
)

// ErrNotLeader is returned by Propose when the node is not a lease-holding
// leader and therefore cannot accept client commands.
var ErrNotLeader = errors.New("paxos: not a lease-holding leader")

// Config tunes the deterministic tick-driven timers of a Node.
type Config struct {
	ID                int
	ClusterSize       int
	ElectionTimeout   int // ticks without leader contact before starting Phase 1
	HeartbeatInterval int // ticks between leader heartbeats
	LeaseDuration     int // ticks a majority-backed lease remains valid
}

// Node is a single Multi-Paxos participant. All state transitions happen
// under n.mu; outbound messages are collected while the lock is held and
// dispatched after it is released, so no lock is ever held while another
// node's lock is acquired.
type Node struct {
	mu  sync.Mutex
	id  int
	cfg Config
	net *Network

	// Persistent protocol state.
	ballot Ballot // highest ballot this node has promised to
	log    []Entry

	// Commit / apply state. The committed prefix is always hole-free.
	commitIndex int
	applied     [][]byte

	// Deterministic clock state (tick units).
	now               int
	lastLeaderContact int
	lastHeartbeat     int
	leaseExpiry       int

	role Role

	// Candidate state (Phase 1).
	promises map[int]Message

	// Leader state (Phase 2 + lease).
	accepted      map[int]map[int]bool // log index -> acceptors
	heartbeatAcks map[int]bool
}

func newNode(cfg Config, net *Network) *Node {
	return &Node{
		id:            cfg.ID,
		cfg:           cfg,
		net:           net,
		role:          Follower,
		promises:      make(map[int]Message),
		accepted:      make(map[int]map[int]bool),
		heartbeatAcks: make(map[int]bool),
	}
}

func (n *Node) majority() int { return n.cfg.ClusterSize/2 + 1 }

// ---------------------------------------------------------------------------
// Deterministic clock
// ---------------------------------------------------------------------------

// Tick advances the node's deterministic clock by one tick and fires any
// due timers (election timeout, heartbeat broadcast, lease expiry).
func (n *Node) Tick() {
	n.mu.Lock()
	n.now++
	var out []Message
	switch n.role {
	case Leader:
		if n.now >= n.leaseExpiry {
			// Lease not renewed by a majority: step down.
			n.stepDownLocked()
		} else if n.now-n.lastHeartbeat >= n.cfg.HeartbeatInterval {
			out = n.broadcastHeartbeatLocked()
		}
	default:
		if n.now-n.lastLeaderContact >= n.cfg.ElectionTimeout {
			out = n.startElectionLocked()
		}
	}
	n.mu.Unlock()
	n.net.sendAll(out)
}

// ---------------------------------------------------------------------------
// Client API
// ---------------------------------------------------------------------------

// Propose appends cmd to the log and replicates it with Phase 2 only,
// bypassing Phase 1, which is legal while the leader lease is valid.
// It returns the assigned log index.
func (n *Node) Propose(cmd []byte) (int, error) {
	n.mu.Lock()
	if n.role != Leader || n.now >= n.leaseExpiry {
		n.mu.Unlock()
		return -1, ErrNotLeader
	}
	idx := len(n.log)
	n.log = append(n.log, Entry{})
	out := n.proposeAtLocked(idx, cmd)
	n.mu.Unlock()
	n.net.sendAll(out)
	return idx, nil
}

// ---------------------------------------------------------------------------
// Phase 1: Prepare / Promise
// ---------------------------------------------------------------------------

func (n *Node) startElectionLocked() []Message {
	n.role = Candidate
	n.ballot = Ballot{N: n.ballot.N + 1, ID: n.id}
	n.lastLeaderContact = n.now
	// A candidate always votes for itself.
	n.promises = map[int]Message{
		n.id: {Kind: MsgPromise, From: n.id, Ballot: n.ballot, Log: cloneLog(n.log), CommitIndex: n.commitIndex},
	}
	if len(n.promises) >= n.majority() {
		return n.becomeLeaderLocked()
	}
	return n.broadcastLocked(Message{Kind: MsgPrepare, Ballot: n.ballot})
}

func (n *Node) handlePrepareLocked(m Message) []Message {
	if m.Ballot.Greater(n.ballot) {
		n.ballot = m.Ballot
		n.stepDownLocked()
	}
	// Always answer so the proposer learns the highest promised ballot.
	return []Message{{
		Kind:        MsgPromise,
		From:        n.id,
		To:          m.From,
		Ballot:      n.ballot,
		Log:         cloneLog(n.log),
		CommitIndex: n.commitIndex,
	}}
}

func (n *Node) handlePromiseLocked(m Message) []Message {
	if n.role != Candidate || m.Ballot != n.ballot {
		return nil
	}
	n.promises[m.From] = m
	if len(n.promises) < n.majority() {
		return nil
	}
	return n.becomeLeaderLocked()
}

// becomeLeaderLocked finishes Phase 1: it merges the highest-ballot accepted
// entry per slot over the majority's promises, adopts the committed prefix,
// and re-proposes every uncommitted slot (gap repair) in its own ballot.
func (n *Node) becomeLeaderLocked() []Message {
	n.role = Leader

	maxLen := 0
	maxCommit := 0
	for _, p := range n.promises {
		if len(p.Log) > maxLen {
			maxLen = len(p.Log)
		}
		if p.CommitIndex > maxCommit {
			maxCommit = p.CommitIndex
		}
	}

	merged := make([]Entry, maxLen)
	for i := range merged {
		best := Entry{}
		for _, p := range n.promises {
			if i < len(p.Log) && p.Log[i].Ballot.Greater(best.Ballot) {
				best = p.Log[i]
			}
		}
		merged[i] = best // zero entry => hole, repaired with a no-op below
	}
	n.log = merged

	// Adopt the committed prefix observed in the majority.
	for n.commitIndex < maxCommit && n.commitIndex < len(n.log) && !n.log[n.commitIndex].isHole() {
		n.applyLocked(n.log[n.commitIndex])
		n.commitIndex++
	}

	n.accepted = make(map[int]map[int]bool)
	n.heartbeatAcks = map[int]bool{n.id: true}
	n.leaseExpiry = n.now + n.cfg.LeaseDuration
	n.lastHeartbeat = n.now

	// Gap repair: re-propose every uncommitted slot in the current ballot.
	// Holes become committed no-ops so the prefix stays contiguous.
	var out []Message
	for i := n.commitIndex; i < len(n.log); i++ {
		out = append(out, n.proposeAtLocked(i, n.log[i].Cmd)...)
	}
	// Publish commitIndex and reset follower election timers immediately.
	out = append(out, n.broadcastHeartbeatLocked()...)
	return out
}

// ---------------------------------------------------------------------------
// Phase 2: Accept / Accepted
// ---------------------------------------------------------------------------

// proposeAtLocked assigns the current ballot to the slot, self-accepts and
// broadcasts Accept. Caller must ensure index < len(n.log).
func (n *Node) proposeAtLocked(index int, cmd []byte) []Message {
	n.log[index] = Entry{Ballot: n.ballot, Cmd: cmd}
	n.trackAcceptLocked(index, n.id)
	out := n.broadcastLocked(Message{Kind: MsgAccept, Index: index, Entry: n.log[index]})
	out = append(out, n.tryCommitLocked()...)
	return out
}

func (n *Node) handleAcceptLocked(m Message) []Message {
	if m.Ballot.Less(n.ballot) {
		// Nack so the stale leader learns about the higher ballot.
		return []Message{{Kind: MsgAccepted, From: n.id, To: m.From, Ballot: n.ballot, Index: m.Index}}
	}
	n.ballot = m.Ballot
	n.role = Follower
	n.lastLeaderContact = n.now
	for len(n.log) <= m.Index {
		n.log = append(n.log, Entry{}) // placeholder hole, repaired later
	}
	if m.Index >= n.commitIndex {
		// Committed slots are immutable; uncommitted slots may be
		// overwritten by a higher-ballot entry (dirty-log cleanup).
		n.log[m.Index] = m.Entry
	}
	return []Message{{Kind: MsgAccepted, From: n.id, To: m.From, Ballot: n.ballot, Index: m.Index}}
}

func (n *Node) handleAcceptedLocked(m Message) []Message {
	if m.Ballot.Greater(n.ballot) {
		n.ballot = m.Ballot
		n.stepDownLocked()
		return nil
	}
	if n.role != Leader || m.Ballot != n.ballot || m.Index < n.commitIndex {
		return nil
	}
	n.trackAcceptLocked(m.Index, m.From)
	return n.tryCommitLocked()
}

func (n *Node) trackAcceptLocked(index, from int) {
	set := n.accepted[index]
	if set == nil {
		set = make(map[int]bool)
		n.accepted[index] = set
	}
	set[from] = true
}

// tryCommitLocked advances the commit index over the contiguous prefix of
// slots that a majority accepted in the leader's current ballot. It never
// skips a slot: that is the no-hole commit invariant.
func (n *Node) tryCommitLocked() []Message {
	advanced := false
	for n.commitIndex < len(n.log) {
		e := n.log[n.commitIndex]
		if e.isHole() || e.Ballot != n.ballot {
			break
		}
		if len(n.accepted[n.commitIndex]) < n.majority() {
			break
		}
		n.applyLocked(e)
		n.commitIndex++
		advanced = true
	}
	if !advanced {
		return nil
	}
	return n.broadcastHeartbeatLocked()
}

// ---------------------------------------------------------------------------
// Heartbeats, lease and log reconciliation
// ---------------------------------------------------------------------------

func (n *Node) broadcastHeartbeatLocked() []Message {
	n.heartbeatAcks = map[int]bool{n.id: true}
	n.lastHeartbeat = n.now
	return n.broadcastLocked(Message{
		Kind:        MsgHeartbeat,
		Ballot:      n.ballot,
		CommitIndex: n.commitIndex,
		Log:         cloneLog(n.log),
	})
}

func (n *Node) handleHeartbeatLocked(m Message) []Message {
	if m.Ballot.Less(n.ballot) {
		return []Message{{Kind: MsgHeartbeatAck, From: n.id, To: m.From, Ballot: n.ballot}}
	}
	n.ballot = m.Ballot
	n.role = Follower
	n.lastLeaderContact = n.now

	// Reconcile: adopt the leader's entries for every uncommitted slot and
	// drop any uncommitted dirty tail the majority never saw.
	for i := 0; i < len(m.Log); i++ {
		if i < n.commitIndex {
			continue
		}
		if i >= len(n.log) {
			n.log = append(n.log, m.Log[i])
		} else if n.log[i].Ballot != m.Log[i].Ballot {
			n.log[i] = m.Log[i]
		}
	}
	if len(n.log) > len(m.Log) && len(m.Log) >= n.commitIndex {
		n.log = n.log[:len(m.Log)]
	}

	// Advance the local commit index over the contiguous, present prefix.
	for n.commitIndex < m.CommitIndex && n.commitIndex < len(n.log) && !n.log[n.commitIndex].isHole() {
		n.applyLocked(n.log[n.commitIndex])
		n.commitIndex++
	}
	return []Message{{Kind: MsgHeartbeatAck, From: n.id, To: m.From, Ballot: n.ballot}}
}

func (n *Node) handleHeartbeatAckLocked(m Message) []Message {
	if m.Ballot.Greater(n.ballot) {
		n.ballot = m.Ballot
		n.stepDownLocked()
		return nil
	}
	if n.role != Leader || m.Ballot != n.ballot {
		return nil
	}
	n.heartbeatAcks[m.From] = true
	if len(n.heartbeatAcks) >= n.majority() {
		// Majority-confirmed lease renewal.
		n.leaseExpiry = n.now + n.cfg.LeaseDuration
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func (n *Node) stepDownLocked() {
	n.role = Follower
	n.lastLeaderContact = n.now
	n.promises = make(map[int]Message)
	n.accepted = make(map[int]map[int]bool)
	n.heartbeatAcks = make(map[int]bool)
}

func (n *Node) applyLocked(e Entry) {
	cmd := make([]byte, len(e.Cmd))
	copy(cmd, e.Cmd)
	n.applied = append(n.applied, cmd)
}

func (n *Node) broadcastLocked(m Message) []Message {
	m.From = n.id
	out := make([]Message, 0, n.cfg.ClusterSize-1)
	for to := 0; to < n.cfg.ClusterSize; to++ {
		if to == n.id {
			continue
		}
		m.To = to
		out = append(out, m)
	}
	return out
}

// HandleMessage runs one inbound message through the state machine and
// dispatches the resulting outbound messages.
func (n *Node) HandleMessage(m Message) {
	n.mu.Lock()
	var out []Message
	switch m.Kind {
	case MsgPrepare:
		out = n.handlePrepareLocked(m)
	case MsgPromise:
		out = n.handlePromiseLocked(m)
	case MsgAccept:
		out = n.handleAcceptLocked(m)
	case MsgAccepted:
		out = n.handleAcceptedLocked(m)
	case MsgHeartbeat:
		out = n.handleHeartbeatLocked(m)
	case MsgHeartbeatAck:
		out = n.handleHeartbeatAckLocked(m)
	}
	n.mu.Unlock()
	n.net.sendAll(out)
}

// ---------------------------------------------------------------------------
// Read-only accessors (used by tests and the demo)
// ---------------------------------------------------------------------------

// Status is a consistent snapshot of a node's observable state.
type Status struct {
	ID          int
	Role        Role
	Ballot      Ballot
	Now         int
	CommitIndex int
	Log         []Entry
	Applied     [][]byte
	LeaseExpiry int
}

// Snapshot returns a copy of the node's observable state.
func (n *Node) Snapshot() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	applied := make([][]byte, len(n.applied))
	copy(applied, n.applied)
	return Status{
		ID:          n.id,
		Role:        n.role,
		Ballot:      n.ballot,
		Now:         n.now,
		CommitIndex: n.commitIndex,
		Log:         cloneLog(n.log),
		Applied:     applied,
		LeaseExpiry: n.leaseExpiry,
	}
}

// LeaseValid reports whether the node is a leader inside its majority lease.
func (n *Node) LeaseValid() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.role == Leader && n.now < n.leaseExpiry
}
