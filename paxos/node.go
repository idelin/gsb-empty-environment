package paxos

import (
	"errors"
	"math/rand"
	"sort"
	"sync"
	"time"
)

var (
	// ErrNoMajority is returned when a quorum cannot be reached, e.g.
	// because the node is on the minority side of a partition.
	ErrNoMajority = errors.New("paxos: no majority reachable")
	// ErrNotLeader is returned when the node lost leadership mid-flight
	// because a higher ballot was promised elsewhere.
	ErrNotLeader = errors.New("paxos: leadership superseded by higher ballot")
	// ErrTooManyAttempts is returned when a proposal keeps losing
	// arbitration to concurrent proposers.
	ErrTooManyAttempts = errors.New("paxos: too many failed attempts")
)

const (
	// DefaultLeaseDuration is how long a successful Phase 1 authorizes a
	// leader to run Phase 2 directly.
	DefaultLeaseDuration = 2 * time.Second
	maxProposeAttempts   = 8
)

// Node is a single Multi-Paxos replica combining proposer, acceptor and
// learner roles. All mutable state is guarded by mu; network calls are
// never made while mu is held, which keeps the lock graph acyclic.
type Node struct {
	id  string
	net *Network

	leaseDuration time.Duration

	// proposeMu serializes client-facing proposals on this node so a
	// single node never races itself for slot assignment.
	proposeMu sync.Mutex

	mu           sync.Mutex
	promised     Ballot // highest ballot promised to any proposer
	maxBallotNum int    // highest ballot number ever seen, for liveness
	log          map[int]*Entry
	commitIndex  int // highest contiguous committed slot
	nextSlot     int // next slot this node will try to propose at

	// Leader state: valid while ballot == promised and the lease holds.
	ballot      Ballot
	leaseExpiry time.Time
}

// Option customizes a Node.
type Option func(*Node)

// WithLeaseDuration overrides the leader lease duration.
func WithLeaseDuration(d time.Duration) Option {
	return func(n *Node) { n.leaseDuration = d }
}

func NewNode(id string, net *Network, opts ...Option) *Node {
	n := &Node{
		id:            id,
		net:           net,
		leaseDuration: DefaultLeaseDuration,
		log:           make(map[int]*Entry),
		nextSlot:      1,
	}
	for _, opt := range opts {
		opt(n)
	}
	net.Register(n)
	return n
}

func (n *Node) ID() string { return n.id }

// --------------------------------------------------------------------
// Acceptor / learner handlers. Each only takes n.mu and never calls back
// into the network, so concurrent delivery cannot deadlock.
// --------------------------------------------------------------------

// Handle dispatches an inbound message. It is invoked synchronously by
// the transport.
func (n *Node) Handle(msg Message) Message {
	switch m := msg.(type) {
	case Prepare:
		return n.handlePrepare(m)
	case Accept:
		return n.handleAccept(m)
	case Commit:
		n.handleCommit(m)
		return nil
	}
	return nil
}

func (n *Node) handlePrepare(p Prepare) Promise {
	n.mu.Lock()
	defer n.mu.Unlock()
	if p.Ballot.Compare(n.promised) <= 0 {
		return Promise{Ballot: n.promised, From: n.id, OK: false}
	}
	n.promised = p.Ballot
	if p.Ballot.Num > n.maxBallotNum {
		n.maxBallotNum = p.Ballot.Num
	}
	entries := make(map[int]Entry, len(n.log))
	for slot, e := range n.log {
		entries[slot] = *e
	}
	return Promise{Ballot: p.Ballot, From: n.id, OK: true, Entries: entries}
}

func (n *Node) handleAccept(a Accept) Accepted {
	n.mu.Lock()
	defer n.mu.Unlock()
	if a.Ballot.Compare(n.promised) < 0 {
		return Accepted{Slot: a.Slot, Ballot: n.promised, From: n.id, OK: false}
	}
	if a.Ballot.Num > n.maxBallotNum {
		n.maxBallotNum = a.Ballot.Num
	}
	e := n.log[a.Slot]
	if e == nil || a.Ballot.Compare(e.Ballot) >= 0 {
		n.log[a.Slot] = &Entry{Slot: a.Slot, Value: a.Value, Ballot: a.Ballot}
	}
	return Accepted{Slot: a.Slot, Ballot: a.Ballot, From: n.id, OK: true}
}

func (n *Node) handleCommit(c Commit) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e := n.log[c.Slot]
	switch {
	case e == nil:
		// We were outside the accepting quorum: learn the entry.
		n.log[c.Slot] = &Entry{Slot: c.Slot, Value: c.Value, Ballot: c.Ballot, Committed: true}
	case e.Value == c.Value:
		e.Committed = true
	default:
		// Conflicting value for a committed slot: impossible under Paxos
		// safety; refuse to corrupt local state.
		return
	}
	n.advanceCommitLocked()
}

// advanceCommitLocked raises commitIndex while the next slot is committed,
// guaranteeing the committed prefix never has holes.
func (n *Node) advanceCommitLocked() {
	for {
		e := n.log[n.commitIndex+1]
		if e == nil || !e.Committed {
			return
		}
		n.commitIndex++
	}
}

// --------------------------------------------------------------------
// Proposer path.
// --------------------------------------------------------------------

// Propose replicates value and returns the slot at which it was
// committed. It returns only after a majority has accepted the entry, so
// a successful Propose is linearizable: the value will survive any later
// leader change. If a previous attempt already committed the same value,
// Propose is idempotent and returns the original slot.
func (n *Node) Propose(value string) (int, error) {
	n.proposeMu.Lock()
	defer n.proposeMu.Unlock()

	for attempt := 0; attempt < maxProposeAttempts; attempt++ {
		if slot, ok := n.findCommitted(value); ok {
			return slot, nil
		}
		slot, err := n.tryPropose(value)
		if err == nil {
			return slot, nil
		}
		if !errors.Is(err, ErrNotLeader) && !errors.Is(err, ErrNoMajority) {
			return -1, err
		}
		n.backoff(attempt)
	}
	return -1, ErrTooManyAttempts
}

func (n *Node) backoff(attempt int) {
	shift := uint(attempt)
	if shift > 5 {
		shift = 5
	}
	jitter := time.Duration(rand.Intn(8)) * time.Millisecond
	time.Sleep(time.Duration(1<<shift)*time.Millisecond + jitter)
}

func (n *Node) findCommitted(value string) (int, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for slot, e := range n.log {
		if e.Committed && e.Value == value {
			return slot, true
		}
	}
	return 0, false
}

// isLeaderLocked reports whether this node holds a live lease: it won
// Phase 1 with n.ballot, no higher ballot has been promised since, and
// the lease has not expired.
func (n *Node) isLeaderLocked() bool {
	return n.ballot.Node == n.id &&
		n.ballot.Compare(n.promised) == 0 &&
		time.Now().Before(n.leaseExpiry)
}

func (n *Node) tryPropose(value string) (int, error) {
	n.mu.Lock()
	if !n.isLeaderLocked() {
		n.mu.Unlock()
		if err := n.prepare(); err != nil {
			return -1, err
		}
		// The repair pass may have committed this very value from an
		// earlier failed attempt; return its slot instead of duplicating.
		if slot, ok := n.findCommitted(value); ok {
			return slot, nil
		}
		n.mu.Lock()
		if !n.isLeaderLocked() {
			n.mu.Unlock()
			return -1, ErrNotLeader
		}
	}
	// Lease fast path: skip Phase 1 entirely and go straight to Phase 2.
	slot := n.nextSlot
	n.nextSlot++
	ballot := n.ballot
	n.mu.Unlock()

	if !n.acceptPhase(ballot, slot, value) {
		n.invalidateLease()
		return -1, ErrNotLeader
	}
	n.markCommitted(slot, value)
	n.broadcastCommit(ballot, slot, value)
	return slot, nil
}

// prepare runs Phase 1 with a fresh, strictly higher ballot and, on
// success, repairs every slot the quorum knows about before the lease
// starts.
func (n *Node) prepare() error {
	n.mu.Lock()
	n.maxBallotNum++
	b := Ballot{Num: n.maxBallotNum, Node: n.id}
	n.mu.Unlock()

	members := n.net.Members()
	quorum := len(members)/2 + 1

	promises := make([]Promise, 0, len(members))
	var highest Ballot
	for _, peer := range members {
		rep, ok := n.net.Send(n.id, peer, Prepare{Ballot: b, From: n.id})
		if !ok {
			continue
		}
		p, ok := rep.(Promise)
		if !ok {
			continue
		}
		if p.Ballot.Compare(highest) > 0 {
			highest = p.Ballot
		}
		if !p.OK {
			continue
		}
		promises = append(promises, p)
	}
	// Learn from rejections: acceptors report the ballot they promised,
	// so the next attempt starts strictly above it. Without this a node
	// that inflated its promise while partitioned could fence off a
	// well-formed leader forever.
	n.observeBallot(highest)
	if len(promises) < quorum {
		return ErrNoMajority
	}

	n.mu.Lock()
	if b.Compare(n.promised) < 0 {
		// A concurrent proposer promised a higher ballot while we were
		// collecting promises.
		n.mu.Unlock()
		return ErrNotLeader
	}
	n.ballot = b
	n.leaseExpiry = time.Now().Add(n.leaseDuration)
	n.mu.Unlock()

	if err := n.repair(b, promises); err != nil {
		n.invalidateLease()
		return err
	}
	return nil
}

// invalidateLease drops the leadership lease, forcing the next proposal
// to run Phase 1 again with a fresh ballot.
func (n *Node) invalidateLease() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ballot = Ballot{}
	n.leaseExpiry = time.Time{}
}

// observeBallot raises the local ballot counter so future prepares
// outrank any ballot seen on the wire.
func (n *Node) observeBallot(b Ballot) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if b.Num > n.maxBallotNum {
		n.maxBallotNum = b.Num
	}
}

// repair fills every slot in [1, maxSlot] seen by the promising quorum.
// For each slot it re-proposes the highest-ballot accepted value (which
// preserves any value that may have been committed) or a no-op when the
// slot is a hole. This is what lets a new leader safely overwrite dirty
// minority logs while never losing committed values.
func (n *Node) repair(b Ballot, promises []Promise) error {
	chosen := make(map[int]Entry)
	maxSlot := 0
	for _, p := range promises {
		for slot, e := range p.Entries {
			if cur, ok := chosen[slot]; !ok || e.Ballot.Compare(cur.Ballot) > 0 {
				chosen[slot] = e
			}
			if slot > maxSlot {
				maxSlot = slot
			}
		}
	}
	// A slot conflicts when some acceptor in the quorum holds a different
	// value than the chosen one (e.g. a dirty minority write). Conflicted
	// slots must be re-accepted even if they are committed locally, so
	// the straggler is overwritten with the chosen value.
	conflict := make(map[int]bool)
	for _, p := range promises {
		for slot, e := range p.Entries {
			if c, ok := chosen[slot]; ok && e.Value != c.Value {
				conflict[slot] = true
			}
		}
	}

	for slot := 1; slot <= maxSlot; slot++ {
		value := NoOpValue
		if e, ok := chosen[slot]; ok {
			value = e.Value
		}
		n.mu.Lock()
		local := n.log[slot]
		alreadySafe := local != nil && local.Committed && local.Value == value
		n.mu.Unlock()
		if alreadySafe && !conflict[slot] {
			// Committed on a quorum already; re-accepting is
			// unnecessary, but re-broadcast the commit so lagging
			// followers learn the entry.
			n.broadcastCommit(b, slot, value)
			continue
		}
		if !n.acceptPhase(b, slot, value) {
			return ErrNotLeader
		}
		n.markCommitted(slot, value)
		n.broadcastCommit(b, slot, value)
	}

	n.mu.Lock()
	if maxSlot+1 > n.nextSlot {
		n.nextSlot = maxSlot + 1
	}
	n.mu.Unlock()
	return nil
}

// acceptPhase runs Phase 2 for one slot. It returns true once a quorum
// has accepted, at which point the (slot, value) pair is committed and
// can never be overwritten by any future leader.
//
// If any acceptor rejects with a higher promised ballot, the round is
// treated as failed even when a quorum accepted: the proposer retries
// with a ballot above every promise in the cluster, which both restores
// a clean quorum and converges stragglers that inflated their promises
// while partitioned. The already-accepted value stays committed and is
// re-proposed by the retry's repair pass.
func (n *Node) acceptPhase(b Ballot, slot int, value string) bool {
	members := n.net.Members()
	quorum := len(members)/2 + 1
	accepted := 0
	superseded := false
	for _, peer := range members {
		rep, ok := n.net.Send(n.id, peer, Accept{Slot: slot, Ballot: b, Value: value, From: n.id})
		if !ok {
			continue
		}
		a, ok := rep.(Accepted)
		if !ok {
			continue
		}
		if !a.OK {
			n.observeBallot(a.Ballot)
			if a.Ballot.Compare(b) > 0 {
				superseded = true
			}
			continue
		}
		accepted++
	}
	return accepted >= quorum && !superseded
}

func (n *Node) markCommitted(slot int, value string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e := n.log[slot]
	if e == nil || e.Value != value {
		return
	}
	e.Committed = true
	n.advanceCommitLocked()
}

func (n *Node) broadcastCommit(b Ballot, slot int, value string) {
	for _, peer := range n.net.Members() {
		if peer == n.id {
			continue
		}
		n.net.Send(n.id, peer, Commit{Slot: slot, Ballot: b, Value: value, From: n.id})
	}
}

// --------------------------------------------------------------------
// Read-only accessors (used by tests, the demo and clients).
// --------------------------------------------------------------------

// CommitIndex returns the highest contiguous committed slot.
func (n *Node) CommitIndex() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// IsLeader reports whether this node currently holds a live lease.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeaderLocked()
}

// LogEntry returns a copy of the entry at slot.
func (n *Node) LogEntry(slot int) (Entry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.log[slot]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Log returns a slot-ordered snapshot of the local log.
func (n *Node) Log() []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	slots := make([]int, 0, len(n.log))
	for slot := range n.log {
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	out := make([]Entry, 0, len(slots))
	for _, slot := range slots {
		out = append(out, *n.log[slot])
	}
	return out
}
