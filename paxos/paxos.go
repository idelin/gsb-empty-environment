// Package paxos implements Multi-Paxos majority consensus with a gap-free
// log commit engine.
//
// Features:
//   - Prepare/Promise (phase 1) and Accept/Accepted (phase 2).
//   - Leader lease: while the lease is valid the leader skips phase 1 and
//     commits consecutive log entries directly through phase 2.
//   - Gap repair: a new leader re-runs phase 1, adopts the highest-ballot
//     value found in any quorum for every open slot, fills holes with
//     no-op entries and safely overwrites dirty minority logs.
//
// The package spawns no goroutines; all progress is driven by explicit
// calls (Propose, BroadcastHeartbeat), which makes it deterministic and
// leak-free by construction.
package paxos

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	// ErrNoQuorum is returned when a majority cannot be reached.
	ErrNoQuorum = errors.New("paxos: no quorum reachable")
	// ErrNotLeader is returned when the node lost leadership mid-proposal.
	ErrNotLeader = errors.New("paxos: leadership lost")
)

// Ballot is a totally ordered Paxos proposal number.
type Ballot struct {
	N  uint64
	ID int
}

// Compare returns -1, 0 or 1.
func (b Ballot) Compare(o Ballot) int {
	switch {
	case b.N != o.N:
		if b.N < o.N {
			return -1
		}
		return 1
	case b.ID != o.ID:
		if b.ID < o.ID {
			return -1
		}
		return 1
	}
	return 0
}

func (b Ballot) Greater(o Ballot) bool   { return b.Compare(o) > 0 }
func (b Ballot) GreaterEq(o Ballot) bool { return b.Compare(o) >= 0 }

// Entry is a single log slot.
type Entry struct {
	Index  uint64
	Ballot Ballot
	Value  []byte
	Noop   bool // true for hole-filling entries written during repair
}

// Promise is the phase-1b response.
type Promise struct {
	OK       bool
	Promised Ballot
	Accepted []Entry // acceptor's uncommitted accepted entries
}

// AcceptReply is the phase-2b response.
type AcceptReply struct {
	ID       int
	OK       bool
	Promised Ballot
}

// CommitMsg carries newly committed entries from leader to follower.
type CommitMsg struct {
	Ballot      Ballot
	LeaderID    int
	Entries     []Entry
	CommitIndex uint64
}

// HeartbeatMsg grants a lease and synchronises committed entries.
type HeartbeatMsg struct {
	Ballot      Ballot
	LeaderID    int
	Entries     []Entry
	CommitIndex uint64
}

// Node is a single Paxos replica.
type Node struct {
	mu    sync.Mutex
	id    int
	size  int
	net   *Network
	now   func() time.Time
	lease time.Duration

	// Acceptor state.
	promised Ballot
	accepted map[uint64]Entry

	// Gap-free commit engine: committed[1..commitIndex] is always
	// contiguous; commitIndex only advances over consecutive slots.
	committed   map[uint64]Entry
	commitIndex uint64

	// Leader state.
	leader     bool
	ballot     Ballot
	leaseUntil time.Time
	acks       map[uint64]map[int]bool

	// Follower lease view.
	leaseHolder int
	followLease time.Time

	propMu sync.Mutex // serialises Propose/Elect per node
}

func newNode(id, size int, net *Network, now func() time.Time, lease time.Duration) *Node {
	return &Node{
		id:        id,
		size:      size,
		net:       net,
		now:       now,
		lease:     lease,
		accepted:  make(map[uint64]Entry),
		committed: make(map[uint64]Entry),
		acks:      make(map[uint64]map[int]bool),
	}
}

// ID returns the node identifier.
func (n *Node) ID() int { return n.id }

func (n *Node) quorum() int { return n.size/2 + 1 }

// IsLeader reports whether the node currently holds leadership.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.leader
}

// CommitIndex is the highest contiguous committed slot (0 = none).
func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// Log returns the committed, gap-free prefix of the log (slots 1..CommitIndex).
func (n *Node) Log() []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Entry, 0, n.commitIndex)
	for i := uint64(1); i <= n.commitIndex; i++ {
		out = append(out, n.committed[i])
	}
	return out
}

// AcceptedAt exposes the acceptor state for slot idx (used by tests and repair).
func (n *Node) AcceptedAt(idx uint64) (Entry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.accepted[idx]
	return e, ok
}

// BallotN returns the highest promised ballot.
func (n *Node) BallotN() Ballot {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.promised
}

func (n *Node) followLeaseActiveLocked() bool {
	return n.leaseHolder >= 0 && n.now().Before(n.followLease)
}

func (n *Node) leaderWithLease() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.leader && n.now().Before(n.leaseUntil)
}

// ---------------------------------------------------------------------------
// Acceptor handlers (invoked through Network).
// ---------------------------------------------------------------------------

func (n *Node) handlePrepare(b Ballot, fromIndex uint64) Promise {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Lease protection: while a valid lease is held by another node,
	// reject prepares from anyone else (phase-1 bypass guarantee).
	if n.followLeaseActiveLocked() && n.leaseHolder != b.ID {
		return Promise{OK: false, Promised: n.promised}
	}
	if !b.Greater(n.promised) {
		return Promise{OK: false, Promised: n.promised}
	}
	n.promised = b
	n.leader = false
	var acc []Entry
	for idx, e := range n.accepted {
		if idx > fromIndex {
			acc = append(acc, e)
		}
	}
	sort.Slice(acc, func(i, j int) bool { return acc[i].Index < acc[j].Index })
	return Promise{OK: true, Promised: b, Accepted: acc}
}

func (n *Node) handleAccept(e Entry) AcceptReply {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.followLeaseActiveLocked() && n.leaseHolder != e.Ballot.ID {
		return AcceptReply{ID: n.id, OK: false, Promised: n.promised}
	}
	if !e.Ballot.GreaterEq(n.promised) {
		return AcceptReply{ID: n.id, OK: false, Promised: n.promised}
	}
	n.promised = e.Ballot
	if n.leader && e.Ballot.Greater(n.ballot) {
		n.leader = false
	}
	// Never touch already committed slots; overwrite dirty ones freely.
	if e.Index > n.commitIndex {
		n.accepted[e.Index] = e
	}
	return AcceptReply{ID: n.id, OK: true, Promised: n.promised}
}

func (n *Node) handleCommit(m CommitMsg) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if m.Ballot.Greater(n.promised) {
		n.promised = m.Ballot
	}
	if m.LeaderID != n.id && m.Ballot.GreaterEq(n.ballot) {
		n.leader = false
	}
	n.applyCommittedLocked(m.Entries)
}

func (n *Node) handleHeartbeat(h HeartbeatMsg) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !h.Ballot.GreaterEq(n.promised) {
		return
	}
	n.promised = h.Ballot
	if h.LeaderID != n.id {
		n.leader = false
	}
	n.leaseHolder = h.LeaderID
	n.followLease = n.now().Add(n.lease)
	n.applyCommittedLocked(h.Entries)
}

// applyCommittedLocked installs committed entries and advances the
// gap-free commit index. Committed slots are never overwritten.
func (n *Node) applyCommittedLocked(entries []Entry) {
	for _, e := range entries {
		if e.Index <= n.commitIndex {
			continue
		}
		if _, ok := n.committed[e.Index]; !ok {
			n.committed[e.Index] = e
		}
		delete(n.accepted, e.Index)
	}
	for {
		next := n.commitIndex + 1
		if _, ok := n.committed[next]; !ok {
			return
		}
		n.commitIndex = next
	}
}

// committedSinceLocked returns committed entries in (from, commitIndex].
func (n *Node) committedSince(from uint64) []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []Entry
	for i := from + 1; i <= n.commitIndex; i++ {
		out = append(out, n.committed[i])
	}
	return out
}

// ---------------------------------------------------------------------------
// Leader logic.
// ---------------------------------------------------------------------------

// Propose appends value to the replicated log and returns its slot.
// Under a valid leader lease phase 1 is skipped entirely.
func (n *Node) Propose(value []byte) (uint64, error) {
	n.propMu.Lock()
	defer n.propMu.Unlock()
	for attempt := 0; attempt < 8; attempt++ {
		if !n.leaderWithLease() {
			if err := n.electLocked(); err != nil {
				return 0, err
			}
		}
		idx, err := n.phase2(value)
		if err == nil {
			return idx, nil
		}
		if err == ErrNotLeader {
			continue
		}
		return 0, err
	}
	return 0, ErrNotLeader
}

// Elect forces a phase-1 election round (used by tests and the demo).
func (n *Node) Elect() error {
	n.propMu.Lock()
	defer n.propMu.Unlock()
	return n.electLocked()
}

// electLocked runs Prepare/Promise, merges accepted state, repairs every
// open slot and establishes the lease.
func (n *Node) electLocked() error {
	n.mu.Lock()
	b := Ballot{N: n.promised.N + 1, ID: n.id}
	fromIndex := n.commitIndex
	// Self promise.
	if n.followLeaseActiveLocked() && n.leaseHolder != n.id {
		n.mu.Unlock()
		return ErrNoQuorum
	}
	if !b.Greater(n.promised) {
		n.mu.Unlock()
		return ErrNotLeader
	}
	n.promised = b
	// The proposer's own accepted entries take part in the merge.
	var selfAccepted []Entry
	for idx, e := range n.accepted {
		if idx > fromIndex {
			selfAccepted = append(selfAccepted, e)
		}
	}
	n.mu.Unlock()

	promises := n.net.prepare(n.id, b, fromIndex)

	ok := 1 // self
	accepted := selfAccepted
	var maxSeen Ballot
	for _, p := range promises {
		if p.OK {
			ok++
			accepted = append(accepted, p.Accepted...)
		} else if p.Promised.Greater(maxSeen) {
			maxSeen = p.Promised
		}
	}
	if ok < n.quorum() {
		// Jump ahead of any higher promise we heard about so the next
		// attempt does not waste rounds.
		n.mu.Lock()
		if maxSeen.Greater(n.promised) {
			n.promised = maxSeen
		}
		n.mu.Unlock()
		return ErrNoQuorum
	}

	// Merge: per slot adopt the value with the highest ballot.
	merged := make(map[uint64]Entry)
	maxIdx := fromIndex
	for _, e := range accepted {
		if cur, seen := merged[e.Index]; !seen || e.Ballot.Greater(cur.Ballot) {
			merged[e.Index] = e
		}
		if e.Index > maxIdx {
			maxIdx = e.Index
		}
	}

	n.mu.Lock()
	if !b.GreaterEq(n.promised) {
		n.mu.Unlock()
		return ErrNotLeader
	}
	n.leader = true
	n.ballot = b
	n.leaseUntil = n.now().Add(n.lease)
	n.acks = make(map[uint64]map[int]bool)
	n.mu.Unlock()

	// Gap repair: rewrite every open slot [fromIndex+1, maxIdx] with the
	// merged value (or a no-op for holes) under the new ballot. This both
	// completes possibly-chosen values and overwrites dirty minority logs.
	for idx := fromIndex + 1; idx <= maxIdx; idx++ {
		e, seen := merged[idx]
		if !seen {
			e = Entry{Index: idx, Noop: true}
		}
		e.Ballot = b
		if err := n.phase2entry(e); err != nil {
			return err
		}
	}

	n.BroadcastHeartbeat()
	return nil
}

// phase2 assigns the next free slot and runs Accept/Accepted.
func (n *Node) phase2(value []byte) (uint64, error) {
	n.mu.Lock()
	if !n.leader {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	e := Entry{Index: n.commitIndex + 1, Ballot: n.ballot, Value: value}
	n.mu.Unlock()
	if err := n.phase2entry(e); err != nil {
		return 0, err
	}
	return e.Index, nil
}

// phase2entry replicates one entry and advances the commit index once a
// majority has accepted it.
func (n *Node) phase2entry(e Entry) error {
	n.mu.Lock()
	if !n.leader || e.Ballot != n.ballot {
		n.mu.Unlock()
		return ErrNotLeader
	}
	if e.Index > n.commitIndex {
		n.accepted[e.Index] = e
	}
	n.mu.Unlock()

	replies := n.net.accept(n.id, e)

	n.mu.Lock()
	if !n.leader || e.Ballot != n.ballot {
		n.mu.Unlock()
		return ErrNotLeader
	}
	acks := n.acks[e.Index]
	if acks == nil {
		acks = make(map[int]bool)
		n.acks[e.Index] = acks
	}
	acks[n.id] = true
	for _, r := range replies {
		if r.OK {
			acks[r.ID] = true
		} else if r.Promised.Greater(n.ballot) {
			n.promised = r.Promised
			n.leader = false
			n.mu.Unlock()
			return ErrNotLeader
		}
	}
	if len(acks) < n.quorum() {
		n.mu.Unlock()
		return ErrNoQuorum
	}
	// Advance the gap-free commit index over contiguous majority-acked slots.
	for {
		next := n.commitIndex + 1
		a, seen := n.acks[next]
		if !seen || len(a) < n.quorum() {
			break
		}
		ent, seen := n.accepted[next]
		if !seen {
			break
		}
		n.committed[next] = ent
		delete(n.accepted, next)
		n.commitIndex = next
	}
	n.mu.Unlock()

	n.broadcastCommit()
	return nil
}

// broadcastCommit pushes missing committed entries to every reachable peer.
func (n *Node) broadcastCommit() {
	n.mu.Lock()
	if !n.leader {
		n.mu.Unlock()
		return
	}
	ballot := n.ballot
	n.mu.Unlock()
	n.net.commit(n.id, ballot)
}

// BroadcastHeartbeat extends the lease on followers and syncs their logs.
// It also refreshes the leader's own lease when a quorum is reachable.
func (n *Node) BroadcastHeartbeat() {
	n.mu.Lock()
	if !n.leader {
		n.mu.Unlock()
		return
	}
	ballot := n.ballot
	n.mu.Unlock()
	reached := n.net.heartbeat(n.id, ballot)
	if reached+1 >= n.quorum() {
		n.mu.Lock()
		if n.leader && n.ballot == ballot {
			n.leaseUntil = n.now().Add(n.lease)
		}
		n.mu.Unlock()
	}
}
