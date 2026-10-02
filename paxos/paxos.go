// Package paxos implements Multi-Paxos majority consensus with a
// hole-free committed log.
//
// Protocol summary:
//   - Phase 1 (Prepare/Promise): a candidate leader picks a ballot higher
//     than any it has seen and collects promises from a majority. Promises
//     carry each acceptor's accepted entries and decided values, letting the
//     new leader repair holes and safely overwrite dirty minority writes.
//   - Phase 2 (Accept/Accepted): the leader replicates chosen values to a
//     majority; once a majority accepts a slot it is committed.
//   - Lease fast path: while the leader's lease is valid and it remains
//     prepared, it skips Phase 1 and commits consecutive log entries with a
//     single round trip. Safety never depends on the lease — acceptors still
//     enforce ballot monotonicity — it is purely a latency optimization.
package paxos

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNotLeader is returned when a proposer loses its majority (e.g. it is
// partitioned away) and cannot commit.
var ErrNotLeader = errors.New("paxos: lost majority, not leader")

// ErrUnreachable is returned by the transport when a link is partitioned.
var ErrUnreachable = errors.New("paxos: peer unreachable")

// Ballot is a totally ordered proposal number: (N, ID).
type Ballot struct {
	N  uint64
	ID int
}

// Compare returns -1, 0 or 1 as b is less than, equal to or greater than o.
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

func (b Ballot) String() string { return fmt.Sprintf("(%d,%d)", b.N, b.ID) }

// LogEntry is one replicated log slot.
type LogEntry struct {
	Slot   uint64
	Ballot Ballot
	Value  []byte
	Noop   bool
}

// ---------------------------------------------------------------------------
// Messages (delivered synchronously through the Transport)
// ---------------------------------------------------------------------------

// PrepareReq is Phase 1a.
type PrepareReq struct{ Ballot Ballot }

// Promise is Phase 1b. Entries holds accepted-but-not-decided slots;
// Decided holds committed slots (immutable).
type Promise struct {
	OK       bool
	Promised Ballot
	Entries  map[uint64]LogEntry
	Decided  map[uint64]LogEntry
}

// AcceptReq is Phase 2a.
type AcceptReq struct{ Entry LogEntry }

// Accepted is Phase 2b.
type Accepted struct {
	OK       bool
	Promised Ballot
}

// CommitReq notifies an acceptor that a slot is decided.
type CommitReq struct{ Entry LogEntry }

// ---------------------------------------------------------------------------
// Node: combined acceptor + learner state
// ---------------------------------------------------------------------------

type acceptorEntry struct {
	ballot Ballot
	value  []byte
	noop   bool
}

// Node is a single replica. All handler methods are safe for concurrent use.
type Node struct {
	id        int
	mu        sync.Mutex
	promised  Ballot
	accepted  map[uint64]*acceptorEntry
	decided   map[uint64]LogEntry
	commitTop uint64 // highest contiguous committed slot
}

// NewNode creates a replica with the given id.
func NewNode(id int) *Node {
	return &Node{
		id:       id,
		accepted: make(map[uint64]*acceptorEntry),
		decided:  make(map[uint64]LogEntry),
	}
}

// ID returns the node id.
func (n *Node) ID() int { return n.id }

// Handle dispatches an inbound message and returns its reply.
func (n *Node) Handle(msg any) any {
	switch m := msg.(type) {
	case PrepareReq:
		return n.handlePrepare(m)
	case AcceptReq:
		return n.handleAccept(m)
	case CommitReq:
		n.handleCommit(m)
		return nil
	}
	panic(fmt.Sprintf("paxos: unknown message %T", msg))
}

func (n *Node) handlePrepare(req PrepareReq) Promise {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Ballot.Compare(n.promised) <= 0 {
		return Promise{OK: false, Promised: n.promised}
	}
	n.promised = req.Ballot
	p := Promise{
		OK:       true,
		Promised: n.promised,
		Entries:  make(map[uint64]LogEntry, len(n.accepted)),
		Decided:  make(map[uint64]LogEntry, len(n.decided)),
	}
	for slot, e := range n.accepted {
		v := make([]byte, len(e.value))
		copy(v, e.value)
		p.Entries[slot] = LogEntry{Slot: slot, Ballot: e.ballot, Value: v, Noop: e.noop}
	}
	for slot, e := range n.decided {
		v := make([]byte, len(e.Value))
		copy(v, e.Value)
		p.Decided[slot] = LogEntry{Slot: slot, Ballot: e.Ballot, Value: v, Noop: e.Noop}
	}
	return p
}

func (n *Node) handleAccept(req AcceptReq) Accepted {
	n.mu.Lock()
	defer n.mu.Unlock()
	e := req.Entry
	if e.Ballot.Compare(n.promised) < 0 {
		return Accepted{OK: false, Promised: n.promised}
	}
	n.promised = e.Ballot
	if _, ok := n.decided[e.Slot]; ok {
		// Already decided: ignore re-accepts of the slot.
		return Accepted{OK: true, Promised: n.promised}
	}
	v := make([]byte, len(e.Value))
	copy(v, e.Value)
	n.accepted[e.Slot] = &acceptorEntry{ballot: e.Ballot, value: v, noop: e.Noop}
	return Accepted{OK: true, Promised: n.promised}
}

func (n *Node) handleCommit(req CommitReq) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e := req.Entry
	if _, ok := n.decided[e.Slot]; ok {
		return
	}
	v := make([]byte, len(e.Value))
	copy(v, e.Value)
	n.decided[e.Slot] = LogEntry{Slot: e.Slot, Ballot: e.Ballot, Value: v, Noop: e.Noop}
	delete(n.accepted, e.Slot)
	for n.commitTop++; ; n.commitTop++ {
		if _, ok := n.decided[n.commitTop+1]; !ok {
			break
		}
	}
}

// Decided returns a copy of the decided value for slot, if any.
func (n *Node) Decided(slot uint64) (LogEntry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.decided[slot]
	if !ok {
		return LogEntry{}, false
	}
	v := make([]byte, len(e.Value))
	copy(v, e.Value)
	e.Value = v
	return e, true
}

// DecidedSlots returns all decided slot numbers (unordered).
func (n *Node) DecidedSlots() []uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]uint64, 0, len(n.decided))
	for s := range n.decided {
		out = append(out, s)
	}
	return out
}

// AcceptedEntry exposes the accepted (not yet committed) entry for a slot;
// used by tests to inspect dirty state.
func (n *Node) AcceptedEntry(slot uint64) (LogEntry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.accepted[slot]
	if !ok {
		return LogEntry{}, false
	}
	v := make([]byte, len(e.value))
	copy(v, e.value)
	return LogEntry{Slot: slot, Ballot: e.ballot, Value: v, Noop: e.noop}, true
}

// Promised returns the highest promised ballot.
func (n *Node) Promised() Ballot {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.promised
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

// Transport delivers a message from one node to another synchronously.
type Transport interface {
	Send(from, to int, msg any) (any, error)
}

// ---------------------------------------------------------------------------
// Leader (proposer)
// ---------------------------------------------------------------------------

// Leader proposes values on behalf of one node. It is not thread-safe: a
// single goroutine should drive Propose.
type Leader struct {
	self   *Node
	peers  []int
	tr     Transport
	quorum int
	lease  time.Duration
	now    func() time.Time // injectable clock for tests

	ballot     Ballot
	prepared   bool
	leaseUntil time.Time
	nextSlot   uint64
}

// NewLeader creates a proposer for self talking to peers over tr. quorum is
// the majority size (e.g. 3 for a 5-node cluster).
func NewLeader(self *Node, peers []int, tr Transport, quorum int, lease time.Duration) *Leader {
	return &Leader{
		self:   self,
		peers:  peers,
		tr:     tr,
		quorum: quorum,
		lease:  lease,
		now:    time.Now,
		ballot: Ballot{N: 0, ID: self.id},
	}
}

// SetClock overrides the time source (tests).
func (l *Leader) SetClock(now func() time.Time) { l.now = now }

// BumpBallot raises the ballot counter to at least n. It models a leadership
// handoff in which the new leader starts strictly above the previous one
// (in a real deployment this comes from election / persisted state).
func (l *Leader) BumpBallot(n uint64) {
	if l.ballot.N < n {
		l.ballot.N = n
	}
}

// Refresh forces the next Propose to re-run Phase 1, re-establishing the
// lease and repairing/learning any slots missed while partitioned.
func (l *Leader) Refresh() { l.prepared = false }

// Ballot returns the leader's current ballot.
func (l *Leader) Ballot() Ballot { return l.ballot }

// leaseValid reports whether the Phase-1 skip fast path may be used.
func (l *Leader) leaseValid() bool {
	return l.prepared && l.now().Before(l.leaseUntil)
}

// Propose replicates value and returns its committed slot. On the fast path
// (valid lease) it costs one majority round trip; otherwise it first runs
// Phase 1, repairing any holes it finds.
func (l *Leader) Propose(value []byte) (uint64, error) {
	if !l.leaseValid() {
		if err := l.runPhase1(); err != nil {
			return 0, err
		}
	}
	slot := l.nextSlot
	e := LogEntry{Slot: slot, Ballot: l.ballot, Value: value}
	if err := l.acceptToMajority(e); err != nil {
		l.prepared = false // someone promised higher; force re-prepare
		return 0, err
	}
	l.commit(e)
	l.nextSlot++
	return slot, nil
}

// runPhase1 executes Prepare/Promise against a majority, then repairs every
// uncommitted slot up to the maximum seen: slots with an accepted value keep
// the highest-ballot value (or the decided value, which is immutable), and
// holes are filled with no-ops so the log commits contiguously.
func (l *Leader) runPhase1() error {
	for {
		l.ballot.N++
		promises := 0
		maxSeen := l.ballot
		merged := make(map[uint64]LogEntry)  // highest-ballot accepted per slot
		decided := make(map[uint64]LogEntry) // committed values (immutable)
		var maxSlot uint64

		// Self-promise.
		if p := l.self.handlePrepare(PrepareReq{Ballot: l.ballot}); p.OK {
			promises++
			mergePromise(p, merged, decided, &maxSlot)
		}
		for _, peer := range l.peers {
			resp, err := l.tr.Send(l.self.id, peer, PrepareReq{Ballot: l.ballot})
			if err != nil {
				continue
			}
			p := resp.(Promise)
			if p.Promised.Compare(maxSeen) > 0 {
				maxSeen = p.Promised
			}
			if !p.OK {
				continue
			}
			promises++
			mergePromise(p, merged, decided, &maxSlot)
		}
		if promises < l.quorum {
			return ErrNotLeader
		}
		if maxSeen.Compare(l.ballot) > 0 {
			// Someone promised higher; adopt and retry with a higher N.
			l.ballot.N = maxSeen.N
			continue
		}

		// Repair phase: rewrite every undecided slot in [1, maxSlot].
		for slot := uint64(1); slot <= maxSlot; slot++ {
			if _, ok := decided[slot]; ok {
				continue
			}
			e, ok := merged[slot]
			if !ok {
				e = LogEntry{Slot: slot, Noop: true} // hole -> no-op
			}
			e.Slot = slot
			e.Ballot = l.ballot
			if err := l.acceptToMajority(e); err != nil {
				l.ballot.N++
				continue
			}
			l.commit(e)
		}
		// Learn decided values discovered from peers.
		for slot, e := range decided {
			l.commit(LogEntry{Slot: slot, Ballot: e.Ballot, Value: e.Value, Noop: e.Noop})
		}
		l.prepared = true
		l.leaseUntil = l.now().Add(l.lease)
		l.nextSlot = maxSlot + 1
		return nil
	}
}

func mergePromise(p Promise, merged, decided map[uint64]LogEntry, maxSlot *uint64) {
	for slot, e := range p.Entries {
		if slot > *maxSlot {
			*maxSlot = slot
		}
		if cur, ok := merged[slot]; !ok || e.Ballot.Compare(cur.Ballot) > 0 {
			merged[slot] = e
		}
	}
	for slot, e := range p.Decided {
		if slot > *maxSlot {
			*maxSlot = slot
		}
		decided[slot] = e
	}
}

// acceptToMajority sends Phase 2a and requires quorum accepted replies
// (including the local acceptor).
func (l *Leader) acceptToMajority(e LogEntry) error {
	accepted := 0
	if r := l.self.handleAccept(AcceptReq{Entry: e}); r.OK {
		accepted++
	}
	for _, peer := range l.peers {
		resp, err := l.tr.Send(l.self.id, peer, AcceptReq{Entry: e})
		if err != nil {
			continue
		}
		a := resp.(Accepted)
		if a.Promised.Compare(l.ballot) > 0 {
			l.ballot.N = a.Promised.N // learn higher ballot for next attempt
		}
		if a.OK {
			accepted++
		}
	}
	if accepted < l.quorum {
		return ErrNotLeader
	}
	return nil
}

// commit records the decision locally and broadcasts it best-effort.
func (l *Leader) commit(e LogEntry) {
	l.self.handleCommit(CommitReq{Entry: e})
	for _, peer := range l.peers {
		_, _ = l.tr.Send(l.self.id, peer, CommitReq{Entry: e})
	}
}
