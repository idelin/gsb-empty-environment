// Package multipaxos implements a Multi-Paxos majority-consensus replicated
// log with a hole-free commit engine, together with an offline formal
// invariant inspector.
//
// Protocol summary:
//   - Phase 1 (Prepare/Promise): a candidate leader picks a fresh ballot and
//     collects promises plus all accepted entries from a majority. This both
//     earns leadership and discovers every value that may have been chosen.
//   - Phase 2 (Accept/Accepted): the leader replicates a log entry to a
//     majority; once a majority accepts, the entry is committed and a commit
//     notification carrying the full committed prefix is broadcast.
//   - Leader lease: while the lease is valid the leader skips Phase 1 and
//     commits consecutive LogEntries directly through Phase 2.
//   - Hole repair: a new leader replays Phase 2 for every slot up to the
//     maximum slot seen in promises, filling gaps with no-op entries and
//     safely overwriting minority "dirty" entries that were never chosen.
package multipaxos

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// NoOp is the value used to fill log holes discovered during Phase 1 repair.
const NoOp = "(no-op)"

// Ballot is a totally ordered proposal number: (epoch, node id).
type Ballot struct {
	N    uint64
	Node int
}

// Compare returns -1, 0 or +1 as b is less than, equal to or greater than o.
func (b Ballot) Compare(o Ballot) int {
	if b.N != o.N {
		if b.N < o.N {
			return -1
		}
		return 1
	}
	if b.Node != o.Node {
		if b.Node < o.Node {
			return -1
		}
		return 1
	}
	return 0
}

func (b Ballot) String() string { return fmt.Sprintf("(%d,%d)", b.N, b.Node) }

// Entry is a single replicated log record. Refs declares causal dependencies
// on earlier slots; the inspector validates that committed refs never dangle
// and never form cycles.
type Entry struct {
	Slot   int
	Value  string
	Refs   []int
	Ballot Ballot
}

// PrepareReq is a Phase 1a request.
type PrepareReq struct {
	Ballot Ballot
}

// PrepareResp is a Phase 1b promise carrying every entry the acceptor holds.
type PrepareResp struct {
	Promised bool
	Ballot   Ballot // acceptor's current promised ballot
	Accepted []Entry
}

// AcceptReq is a Phase 2a request.
type AcceptReq struct {
	Entry Entry
}

// AcceptResp is a Phase 2b reply. On rejection Promised tells the leader why.
type AcceptResp struct {
	Accepted bool
	Promised Ballot
}

// CommitReq carries the leader's full committed prefix so lagging followers
// can fill holes and overwrite unchosen entries in one round.
type CommitReq struct {
	Entries []Entry
}

// Node is a single Multi-Paxos replica.
type Node struct {
	mu    sync.Mutex
	id    int
	net   *Network
	lease time.Duration

	promised    Ballot
	lastBallotN uint64
	log         map[int]*Entry
	committed   map[int]bool
	commitIndex int

	isLeader     bool
	leaderBallot Ballot
	leaseUntil   time.Time
}

// NewNode creates a replica and registers it with the network.
func NewNode(id int, net *Network, lease time.Duration) *Node {
	n := &Node{
		id:        id,
		net:       net,
		lease:     lease,
		log:       make(map[int]*Entry),
		committed: make(map[int]bool),
	}
	net.Register(n)
	return n
}

// ID returns the node's identifier.
func (n *Node) ID() int { return n.id }

func (n *Node) majority() int { return n.net.Size()/2 + 1 }

// Campaign runs Phase 1 with a fresh ballot. On a majority of promises the
// node becomes leader, acquires its lease, and repairs every slot up to the
// highest slot reported by the majority: holes are filled with no-op entries
// and the highest-ballot known value is re-proposed for the rest.
func (n *Node) Campaign() bool {
	n.mu.Lock()
	n.lastBallotN++
	b := Ballot{N: n.lastBallotN, Node: n.id}
	n.mu.Unlock()

	promises := 0
	maxSlot := 0
	accepted := make(map[int]Entry)
	for _, p := range n.net.IDs() {
		resp, ok := n.net.Prepare(n.id, p, PrepareReq{Ballot: b})
		if !ok || !resp.Promised {
			continue
		}
		promises++
		for _, e := range resp.Accepted {
			if cur, seen := accepted[e.Slot]; !seen || e.Ballot.Compare(cur.Ballot) > 0 {
				accepted[e.Slot] = e
			}
			if e.Slot > maxSlot {
				maxSlot = e.Slot
			}
		}
	}
	if promises < n.majority() {
		return false
	}

	n.mu.Lock()
	n.isLeader = true
	n.leaderBallot = b
	n.leaseUntil = time.Now().Add(n.lease)
	n.mu.Unlock()

	// Hole repair: re-run Phase 2 for every slot in [1, maxSlot].
	for s := 1; s <= maxSlot; s++ {
		e, ok := accepted[s]
		if !ok {
			e = Entry{Slot: s, Value: NoOp}
		}
		e.Slot = s
		e.Ballot = b
		n.runPhase2(e)
	}
	return true
}

// Propose appends a value under the leader lease, skipping Phase 1. It
// returns the assigned slot and whether the entry was committed.
func (n *Node) Propose(value string, refs ...int) (int, bool) {
	n.mu.Lock()
	if !n.isLeader || !time.Now().Before(n.leaseUntil) {
		n.mu.Unlock()
		return 0, false
	}
	slot := 1
	for s := range n.log {
		if s >= slot {
			slot = s + 1
		}
	}
	b := n.leaderBallot
	// Reserve the slot under the lock so concurrent proposers never pick
	// the same slot. If Phase 2 fails the entry stays uncommitted and will
	// be repaired or overwritten like any other unchosen value.
	e := Entry{Slot: slot, Value: value, Refs: refs, Ballot: b}
	n.log[slot] = &e
	n.mu.Unlock()

	if !n.runPhase2(e) {
		return 0, false
	}
	return slot, true
}

// runPhase2 replicates one entry; returns true once a majority accepted.
func (n *Node) runPhase2(e Entry) bool {
	acks := 0
	for _, p := range n.net.IDs() {
		resp, ok := n.net.Accept(n.id, p, AcceptReq{Entry: e})
		if !ok {
			continue
		}
		if resp.Accepted {
			acks++
		} else {
			n.observeBallot(resp.Promised)
		}
	}
	if acks >= n.majority() {
		n.markCommitted(e)
		return true
	}
	return false
}

// markCommitted records a chosen entry locally and broadcasts the committed
// prefix so followers converge (filling holes, overwriting dirty entries).
func (n *Node) markCommitted(e Entry) {
	n.mu.Lock()
	cp := e
	n.log[e.Slot] = &cp
	n.committed[e.Slot] = true
	n.advanceLocked()
	prefix := n.committedPrefixLocked()
	n.mu.Unlock()
	n.net.BroadcastCommit(n.id, CommitReq{Entries: prefix})
}

func (n *Node) advanceLocked() {
	for n.committed[n.commitIndex+1] {
		n.commitIndex++
	}
}

func (n *Node) committedPrefixLocked() []Entry {
	out := make([]Entry, 0, n.commitIndex)
	for s := 1; s <= n.commitIndex; s++ {
		if e, ok := n.log[s]; ok {
			out = append(out, *e)
		}
	}
	return out
}

// observeBallot steps the node down if a higher promised ballot is seen.
func (n *Node) observeBallot(b Ballot) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if b.Compare(n.promised) > 0 {
		n.promised = b
	}
	if n.isLeader && b.Compare(n.leaderBallot) > 0 {
		n.isLeader = false
	}
}

// handlePrepare is the Phase 1b acceptor logic.
func (n *Node) handlePrepare(req PrepareReq) PrepareResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Ballot.Compare(n.promised) <= 0 {
		return PrepareResp{Promised: false, Ballot: n.promised}
	}
	n.promised = req.Ballot
	n.isLeader = false
	out := make([]Entry, 0, len(n.log))
	for _, e := range n.log {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return PrepareResp{Promised: true, Ballot: n.promised, Accepted: out}
}

// handleAccept is the Phase 2b acceptor logic. Committed entries are
// immutable; a conflicting accept for a committed slot is rejected.
func (n *Node) handleAccept(req AcceptReq) AcceptResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	e := req.Entry
	if e.Ballot.Compare(n.promised) < 0 {
		return AcceptResp{Accepted: false, Promised: n.promised}
	}
	if n.committed[e.Slot] {
		if cur := n.log[e.Slot]; cur != nil && cur.Value == e.Value {
			return AcceptResp{Accepted: true, Promised: n.promised}
		}
		return AcceptResp{Accepted: false, Promised: n.promised}
	}
	n.promised = e.Ballot
	cp := e
	n.log[e.Slot] = &cp
	return AcceptResp{Accepted: true, Promised: n.promised}
}

// handleCommit merges the leader's committed prefix. Committed slots are
// never mutated; unchosen (dirty) local entries are safely overwritten.
func (n *Node) handleCommit(req CommitReq) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range req.Entries {
		if n.committed[e.Slot] {
			continue
		}
		cp := e
		n.log[e.Slot] = &cp
		n.committed[e.Slot] = true
	}
	n.advanceLocked()
	if n.isLeader && len(req.Entries) > 0 {
		top := req.Entries[len(req.Entries)-1]
		if top.Ballot.Compare(n.leaderBallot) > 0 {
			n.isLeader = false
		}
	}
}

// IsLeader reports whether the node currently believes it leads.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader
}

// CommitIndex returns the contiguous committed prefix length.
func (n *Node) CommitIndex() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// Value returns the accepted value and committed flag for a slot.
func (n *Node) Value(slot int) (string, bool, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.log[slot]
	if !ok {
		return "", false, false
	}
	return e.Value, n.committed[slot], true
}

// Snapshot is a deep, consistent copy of a node's durable state, used by the
// offline Inspector.
type Snapshot struct {
	ID          int
	Promised    Ballot
	Log         map[int]Entry
	Committed   map[int]bool
	CommitIndex int
	IsLeader    bool
}

// Snapshot captures the node's state for offline inspection.
func (n *Node) Snapshot() Snapshot {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := Snapshot{
		ID:          n.id,
		Promised:    n.promised,
		Log:         make(map[int]Entry, len(n.log)),
		Committed:   make(map[int]bool, len(n.committed)),
		CommitIndex: n.commitIndex,
		IsLeader:    n.isLeader,
	}
	for slot, e := range n.log {
		s.Log[slot] = *e
	}
	for slot, ok := range n.committed {
		s.Committed[slot] = ok
	}
	return s
}
