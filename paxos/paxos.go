// Package paxos implements a Multi-Paxos majority-consensus replicated log
// with a gap-free commit engine.
//
// Protocol overview:
//   - Phase 1 (Prepare/Promise): a would-be leader picks a proposal term
//     higher than anything it has seen and collects promises plus already
//     accepted entries for every slot at or beyond its commit index.
//   - Phase 2 (Accept/Accepted): the leader re-proposes the highest-term
//     value found per slot (or a no-op to fill holes) under its new term.
//   - Leader lease: once elected, the leader holds a lease and commits
//     consecutive log entries with Phase 2 only, skipping Phase 1.
//   - Gap repair: holes (slots accepted only by a minority) are filled with
//     no-op entries during Phase 1, and committed entries are pushed to
//     lagging followers via anti-entropy Sync messages, which also safely
//     overwrite dirty minority log entries.
//
// The commit engine only advances the commit index over a contiguous
// prefix of the log, so the applied stream never contains holes.
package paxos

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	// ErrNotLeader is returned by Propose when the node is not a leader
	// holding a valid lease.
	ErrNotLeader = errors.New("paxos: not leader or lease expired")
	// ErrLostMajority is returned when the leader fails to reach a
	// majority of acceptors and steps down.
	ErrLostMajority = errors.New("paxos: lost majority, stepped down")
)

// ProposalID identifies a ballot. Higher rounds win; node IDs break ties.
type ProposalID struct {
	Round uint64
	Node  int
}

// Less reports whether a is strictly dominated by b.
func (a ProposalID) Less(b ProposalID) bool {
	return a.Round < b.Round || (a.Round == b.Round && a.Node < b.Node)
}

// Entry is a single replicated log record.
type Entry struct {
	Slot  uint64
	Term  ProposalID
	Value string
	Noop  bool // true for hole-filling no-op entries
}

// PrepareRequest is a Phase 1a message.
type PrepareRequest struct {
	Term     ProposalID
	FromSlot uint64 // first slot the proposer cares about
}

// PrepareResponse is a Phase 1b message.
type PrepareResponse struct {
	Promised    bool
	PromisedID  ProposalID // current promise, set when Promised is false
	Entries     []Entry    // accepted entries with Slot >= FromSlot
	CommitIndex uint64
}

// AcceptRequest is a Phase 2a message.
type AcceptRequest struct {
	Entry        Entry
	LeaderCommit uint64
}

// AcceptResponse is a Phase 2b message.
type AcceptResponse struct {
	Accepted    bool
	PromisedID  ProposalID
	CommitIndex uint64
}

// SyncRequest is an anti-entropy message carrying committed entries only.
// Committed entries are final, so followers may blindly overwrite any
// conflicting dirty (uncommitted) minority entries with them.
type SyncRequest struct {
	Term         ProposalID
	Entries      []Entry
	LeaderCommit uint64
}

// SyncResponse reports the follower commit index after applying the sync.
type SyncResponse struct {
	OK          bool
	CommitIndex uint64
}

// Node is a single Paxos replica.
type Node struct {
	mu  sync.Mutex
	id  int
	net *Network

	promised    ProposalID
	log         map[uint64]Entry
	maxSlot     uint64
	commitIndex uint64
	applied     []Entry

	// Leader state.
	isLeader      bool
	term          ProposalID
	leaseUntil    time.Time
	leaseDuration time.Duration
	nextSlot      uint64

	// commitMu serializes Phase 2 rounds so the leader's commit index
	// only advances over contiguous slots even under concurrent Propose.
	commitMu sync.Mutex

	// Instrumentation (leader side).
	phase1Rounds int
	phase2Rounds int
}

// NewNode creates a node and registers it with the network.
func NewNode(id int, net *Network) *Node {
	n := &Node{
		id:            id,
		net:           net,
		log:           make(map[uint64]Entry),
		leaseDuration: 10 * time.Second,
	}
	net.Register(n)
	return n
}

// SetLeaseDuration configures the leader lease length.
func (n *Node) SetLeaseDuration(d time.Duration) { n.leaseDuration = d }

// ID returns the node identifier.
func (n *Node) ID() int { return n.id }

// IsLeader reports whether the node currently holds leadership.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader && time.Now().Before(n.leaseUntil)
}

// CommitIndex returns the highest contiguous committed slot.
func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// Applied returns a copy of the contiguous committed prefix of the log.
func (n *Node) Applied() []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Entry, len(n.applied))
	copy(out, n.applied)
	return out
}

// LogEntry returns the entry stored at slot, if any.
func (n *Node) LogEntry(slot uint64) (Entry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.log[slot]
	return e, ok
}

// Stats returns the number of leader-side Phase 1 and Phase 2 rounds.
func (n *Node) Stats() (phase1, phase2 int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.phase1Rounds, n.phase2Rounds
}

// BecomeLeader runs Phase 1 (Prepare/Promise) against a majority, adopts
// the highest-term value per uncommitted slot, fills holes with no-ops,
// commits them via Phase 2, and takes out a leader lease.
func (n *Node) BecomeLeader() bool {
	n.mu.Lock()
	round := n.promised.Round
	if n.term.Round > round {
		round = n.term.Round
	}
	n.term = ProposalID{Round: round + 1, Node: n.id}
	term := n.term
	fromSlot := n.commitIndex + 1
	n.mu.Unlock()

	// ---- Phase 1: Prepare / Promise ----
	collected := make(map[uint64]Entry)
	maxSeen := fromSlot - 1
	merge := func(entries []Entry) {
		for _, e := range entries {
			if cur, ok := collected[e.Slot]; !ok || cur.Term.Less(e.Term) {
				collected[e.Slot] = e
			}
			if e.Slot > maxSeen {
				maxSeen = e.Slot
			}
		}
	}

	self := n.HandlePrepare(PrepareRequest{Term: term, FromSlot: fromSlot})
	n.mu.Lock()
	n.phase1Rounds++
	n.mu.Unlock()
	if !self.Promised {
		return false
	}
	promises := 1
	merge(self.Entries)

	for _, peer := range n.net.Peers(n.id) {
		resp, ok := n.net.Prepare(n.id, peer, PrepareRequest{Term: term, FromSlot: fromSlot})
		if !ok || !resp.Promised {
			continue
		}
		promises++
		merge(resp.Entries)
	}
	if promises < n.net.Majority() {
		return false
	}

	// ---- Decide values: adopt highest-term value per slot, no-op holes ----
	var fill []Entry
	for slot := fromSlot; slot <= maxSeen; slot++ {
		if e, ok := collected[slot]; ok {
			fill = append(fill, Entry{Slot: slot, Term: term, Value: e.Value, Noop: e.Noop})
		} else {
			fill = append(fill, Entry{Slot: slot, Term: term, Noop: true})
		}
	}

	// ---- Phase 2: commit the repaired prefix ----
	for _, e := range fill {
		if !n.broadcastAccept(e) {
			n.mu.Lock()
			n.isLeader = false
			n.mu.Unlock()
			return false
		}
	}

	n.mu.Lock()
	if n.promised != term {
		// A higher promise intervened; we are not the leader.
		n.mu.Unlock()
		return false
	}
	n.isLeader = true
	n.leaseUntil = time.Now().Add(n.leaseDuration)
	n.nextSlot = maxSeen + 1
	n.mu.Unlock()

	// Push the committed prefix to every reachable follower: repairs
	// holes and overwrites dirty minority entries.
	n.SyncFollowers()
	return true
}

// Propose appends a value to the log. With a valid lease this runs Phase 2
// only (one round trip to a majority), skipping Phase 1 entirely.
func (n *Node) Propose(value string) (uint64, error) {
	n.mu.Lock()
	if !n.isLeader || !time.Now().Before(n.leaseUntil) {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	slot := n.nextSlot
	n.nextSlot++
	term := n.term
	n.mu.Unlock()

	// Best-effort anti-entropy before extending the log.
	n.SyncFollowers()

	if !n.broadcastAccept(Entry{Slot: slot, Term: term, Value: value}) {
		n.mu.Lock()
		n.isLeader = false
		n.mu.Unlock()
		return 0, ErrLostMajority
	}
	// Propagate the new commit index to followers.
	n.SyncFollowers()
	return slot, nil
}

// broadcastAccept runs one Phase 2 round for a single entry. The leader
// commits the entry locally only after a majority (including self) has
// accepted it; followers learn the new commit index via subsequent
// Accept/Sync messages. Serialized via commitMu so commits advance over
// contiguous slots only.
func (n *Node) broadcastAccept(e Entry) bool {
	n.commitMu.Lock()
	defer n.commitMu.Unlock()

	n.mu.Lock()
	n.phase2Rounds++
	commit := n.commitIndex
	n.mu.Unlock()

	req := AcceptRequest{Entry: e, LeaderCommit: commit}
	acks := 0
	if n.HandleAccept(req).Accepted {
		acks++
	}
	for _, peer := range n.net.Peers(n.id) {
		resp, ok := n.net.Accept(n.id, peer, req)
		if ok && resp.Accepted {
			acks++
		}
	}
	if acks < n.net.Majority() {
		return false
	}
	// Majority reached: the entry is committed. Advance the local commit
	// index over the contiguous prefix.
	n.mu.Lock()
	n.advanceCommitLocked(e.Slot)
	n.mu.Unlock()
	return true
}

// SyncFollowers pushes the committed prefix to all reachable peers.
func (n *Node) SyncFollowers() {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return
	}
	term := n.term
	commit := n.commitIndex
	entries := make([]Entry, 0, commit)
	for s := uint64(1); s <= commit; s++ {
		if e, ok := n.log[s]; ok {
			entries = append(entries, e)
		}
	}
	n.mu.Unlock()

	req := SyncRequest{Term: term, Entries: entries, LeaderCommit: commit}
	for _, peer := range n.net.Peers(n.id) {
		n.net.Sync(n.id, peer, req)
	}
}

// HandlePrepare processes a Phase 1a request (acceptor role).
func (n *Node) HandlePrepare(req PrepareRequest) PrepareResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	if req.Term.Less(n.promised) {
		return PrepareResponse{Promised: false, PromisedID: n.promised}
	}
	n.promised = req.Term
	if req.Term.Round > n.term.Round {
		n.isLeader = false // a higher ballot exists: step down
	}
	entries := make([]Entry, 0)
	for slot, e := range n.log {
		if slot >= req.FromSlot {
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Slot < entries[j].Slot })
	return PrepareResponse{Promised: true, Entries: entries, CommitIndex: n.commitIndex}
}

// HandleAccept processes a Phase 2a request (acceptor role).
func (n *Node) HandleAccept(req AcceptRequest) AcceptResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	if req.Entry.Term.Less(n.promised) {
		return AcceptResponse{Accepted: false, PromisedID: n.promised}
	}
	n.promised = req.Entry.Term
	if req.Entry.Term.Round > n.term.Round {
		n.isLeader = false
	}
	if cur, ok := n.log[req.Entry.Slot]; !ok || !req.Entry.Term.Less(cur.Term) {
		n.log[req.Entry.Slot] = req.Entry
	}
	if req.Entry.Slot > n.maxSlot {
		n.maxSlot = req.Entry.Slot
	}
	n.advanceCommitLocked(req.LeaderCommit)
	return AcceptResponse{Accepted: true, CommitIndex: n.commitIndex}
}

// HandleSync applies committed entries pushed by the leader. Committed
// entries are final, so conflicting dirty entries are safely overwritten.
func (n *Node) HandleSync(req SyncRequest) SyncResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	if req.Term.Less(n.promised) {
		return SyncResponse{OK: false, CommitIndex: n.commitIndex}
	}
	if req.Term.Round > n.term.Round {
		n.isLeader = false
	}
	for _, e := range req.Entries {
		if cur, ok := n.log[e.Slot]; !ok || cur.Term != e.Term || cur.Value != e.Value || cur.Noop != e.Noop {
			n.log[e.Slot] = e
		}
		if e.Slot > n.maxSlot {
			n.maxSlot = e.Slot
		}
	}
	n.advanceCommitLocked(req.LeaderCommit)
	return SyncResponse{OK: true, CommitIndex: n.commitIndex}
}

// advanceCommitLocked moves the commit index over the contiguous prefix
// only; it stops at the first hole, guaranteeing a gap-free applied log.
// Callers must hold n.mu.
func (n *Node) advanceCommitLocked(leaderCommit uint64) {
	for n.commitIndex < leaderCommit {
		next := n.commitIndex + 1
		e, ok := n.log[next]
		if !ok {
			return // hole: never skip a slot
		}
		n.commitIndex = next
		n.applied = append(n.applied, e)
	}
}
