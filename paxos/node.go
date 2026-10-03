package paxos

import (
	"sort"
	"sync"
	"time"
)

// Node is a single Multi-Paxos replica: acceptor, proposer and learner.
// It spawns no goroutines; all work happens on the caller's stack.
type Node struct {
	mu        sync.Mutex
	proposeMu sync.Mutex // serializes Propose so slots are allocated uniquely

	id      string
	net     *Network
	members []string // all cluster member ids, including self

	maxBallot   ProposalID // highest ballot promised/accepted
	log         map[uint64]*Slot
	commitIndex uint64
	maxSlot     uint64

	leaderID    string
	ballot      ProposalID // ballot of my current leadership term
	leaseExpiry time.Time
	leaseSource func() time.Duration // optional adaptive lease source
	leaseFixed  time.Duration
	now         func() time.Time

	// instrumentation (read via accessors)
	preparesHandled int
	acceptsHandled  int
}

func NewNode(id string, members []string, net *Network) *Node {
	n := &Node{
		id:         id,
		net:        net,
		members:    append([]string(nil), members...),
		log:        make(map[uint64]*Slot),
		leaseFixed: 300 * time.Millisecond,
		now:        time.Now,
	}
	net.Register(n)
	return n
}

func (n *Node) ID() string            { return n.id }
func (n *Node) quorum() int           { return len(n.members)/2 + 1 }
func (n *Node) majorityOf(c int) bool { return c >= n.quorum() }

// SetLeaseSource installs an adaptive lease-duration provider (e.g. backed
// by the adaptive controller). Falls back to the fixed lease when nil.
func (n *Node) SetLeaseSource(fn func() time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.leaseSource = fn
}

// SetLeaseDuration sets the fixed lease duration (used when no source is set).
func (n *Node) SetLeaseDuration(d time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.leaseFixed = d
}

func (n *Node) leaseDurationLocked() time.Duration {
	if n.leaseSource != nil {
		if d := n.leaseSource(); d > 0 {
			return d
		}
	}
	return n.leaseFixed
}

// Handle dispatches an inbound message. Called by the Network.
func (n *Node) Handle(req Request) Response {
	switch r := req.(type) {
	case *PrepareRequest:
		return n.handlePrepare(r)
	case *AcceptRequest:
		return n.handleAccept(r)
	case *CommitRequest:
		n.handleCommit(r)
		return nil
	}
	return nil
}

// Campaign runs Phase 1 (Prepare/Promise) against a majority and, on
// success, repairs the uncommitted tail: every slot up to the highest seen
// slot is re-proposed with the highest-ballot value found in the promises
// (or a no-op for holes), then committed. Returns true if this node became
// leader.
func (n *Node) Campaign() bool {
	n.mu.Lock()
	ballot := ProposalID{Number: n.maxBallot.Number + 1, NodeID: n.id}
	n.mu.Unlock()

	req := &PrepareRequest{Ballot: ballot, From: n.id}
	promises := make([]Promise, 0, len(n.members))
	if p := n.handlePrepare(req); p.OK {
		promises = append(promises, p)
	}
	for _, peer := range n.members {
		if peer == n.id {
			continue
		}
		resp, err := n.net.Send(n.id, peer, req)
		if err != nil {
			continue
		}
		if p, ok := resp.(Promise); ok && p.OK {
			promises = append(promises, p)
		}
	}
	if !n.majorityOf(len(promises)) {
		return false
	}

	n.mu.Lock()
	if ballot.Compare(n.maxBallot) < 0 { // superseded while campaigning
		n.mu.Unlock()
		return false
	}
	n.ballot = ballot
	n.leaderID = n.id
	n.leaseExpiry = n.now().Add(n.leaseDurationLocked())

	maxCommit := n.commitIndex
	maxSeen := n.maxSlot
	for _, p := range promises {
		if p.CommitIndex > maxCommit {
			maxCommit = p.CommitIndex
		}
		for idx := range p.Entries {
			if idx > maxSeen {
				maxSeen = idx
			}
		}
	}

	// Repair plan: for every slot up to maxSeen pick the highest-ballot
	// value known to the majority; holes get a no-op. Slots already
	// committed locally are left untouched.
	plan := make(map[uint64][]byte)
	for slot := uint64(1); slot <= maxSeen; slot++ {
		if slot <= n.commitIndex {
			continue
		}
		best := ProposalID{}
		var val []byte
		found := false
		if s, ok := n.log[slot]; ok {
			best, val, found = s.Ballot, s.Value, true
		}
		for _, p := range promises {
			if e, ok := p.Entries[slot]; ok && e.Ballot.Compare(best) > 0 {
				best, val, found = e.Ballot, e.Value, true
			}
		}
		if !found {
			val = NoOpValue
		}
		plan[slot] = append([]byte(nil), val...)
	}
	n.mu.Unlock()

	slots := make([]int, 0, len(plan))
	for s := range plan {
		slots = append(slots, int(s))
	}
	sort.Ints(slots)
	for _, s := range slots {
		if !n.accept(uint64(s), plan[uint64(s)]) {
			return false
		}
	}
	_ = maxCommit // committed prefix is adopted via accept+commit of the plan
	return true
}

// Propose appends value to the log and commits it on a majority. With a
// valid leader lease it goes straight to Phase 2 (single round trip);
// otherwise it campaigns first.
func (n *Node) Propose(value []byte) (uint64, bool) {
	n.proposeMu.Lock()
	defer n.proposeMu.Unlock()

	n.mu.Lock()
	leader := n.leaderID == n.id &&
		n.ballot.Compare(n.maxBallot) == 0 &&
		n.now().Before(n.leaseExpiry)
	n.mu.Unlock()
	if !leader {
		if !n.Campaign() {
			return 0, false
		}
	}

	n.mu.Lock()
	if n.leaderID != n.id {
		n.mu.Unlock()
		return 0, false
	}
	slot := n.maxSlot + 1
	if slot <= n.commitIndex {
		slot = n.commitIndex + 1
	}
	n.mu.Unlock()

	if !n.accept(slot, value) {
		return 0, false
	}
	return slot, true
}

// accept runs Phase 2 (Accept/Accepted) for one slot and commits it once a
// majority has accepted.
func (n *Node) accept(slot uint64, value []byte) bool {
	n.mu.Lock()
	if n.leaderID != n.id {
		n.mu.Unlock()
		return false
	}
	ballot := n.ballot
	n.mu.Unlock()

	req := &AcceptRequest{Slot: slot, Ballot: ballot, Value: value, From: n.id}
	accepted := 0
	if n.handleAccept(req).OK {
		accepted++
	}
	for _, peer := range n.members {
		if peer == n.id {
			continue
		}
		resp, err := n.net.Send(n.id, peer, req)
		if err != nil {
			continue
		}
		if a, ok := resp.(Accepted); ok && a.OK {
			accepted++
		}
	}
	if !n.majorityOf(accepted) {
		return false
	}

	n.mu.Lock()
	if s, ok := n.log[slot]; ok && s.Ballot == ballot {
		s.Committed = true
		n.advanceCommitLocked()
	}
	commitIndex := n.commitIndex
	n.mu.Unlock()

	commit := &CommitRequest{Slot: slot, CommitIndex: commitIndex, From: n.id}
	n.handleCommit(commit)
	for _, peer := range n.members {
		if peer == n.id {
			continue
		}
		n.net.Send(n.id, peer, commit) // best effort
	}
	return true
}

// SyncFollowers pushes the committed prefix to every reachable follower,
// healing minority dirty logs and lagging replicas after a partition.
func (n *Node) SyncFollowers() {
	n.mu.Lock()
	if n.leaderID != n.id {
		n.mu.Unlock()
		return
	}
	ballot := n.ballot
	commitIndex := n.commitIndex
	entries := make(map[uint64][]byte, commitIndex)
	for i := uint64(1); i <= commitIndex; i++ {
		if s, ok := n.log[i]; ok {
			entries[i] = append([]byte(nil), s.Value...)
		}
	}
	n.mu.Unlock()

	for _, peer := range n.members {
		if peer == n.id {
			continue
		}
		for i := uint64(1); i <= commitIndex; i++ {
			if v, ok := entries[i]; ok {
				n.net.Send(n.id, peer, &AcceptRequest{Slot: i, Ballot: ballot, Value: v, From: n.id})
			}
		}
		n.net.Send(n.id, peer, &CommitRequest{Slot: commitIndex, CommitIndex: commitIndex, From: n.id})
	}
}

func (n *Node) handlePrepare(req *PrepareRequest) Promise {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.preparesHandled++
	if req.Ballot.Compare(n.maxBallot) <= 0 {
		return Promise{From: n.id, Ballot: n.maxBallot, OK: false}
	}
	n.maxBallot = req.Ballot
	n.stepDownLocked(req.Ballot)
	entries := make(map[uint64]Slot, len(n.log))
	for i, s := range n.log {
		entries[i] = *s
	}
	return Promise{From: n.id, Ballot: req.Ballot, OK: true, Entries: entries, CommitIndex: n.commitIndex}
}

func (n *Node) handleAccept(req *AcceptRequest) Accepted {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.acceptsHandled++
	if req.Ballot.Compare(n.maxBallot) < 0 {
		return Accepted{From: n.id, Slot: req.Slot, Ballot: n.maxBallot, OK: false}
	}
	n.maxBallot = req.Ballot
	n.stepDownLocked(req.Ballot)
	s, ok := n.log[req.Slot]
	if !ok {
		s = &Slot{Index: req.Slot}
		n.log[req.Slot] = s
	}
	s.Ballot = req.Ballot
	s.Value = append([]byte(nil), req.Value...)
	if req.Slot > n.maxSlot {
		n.maxSlot = req.Slot
	}
	return Accepted{From: n.id, Slot: req.Slot, Ballot: req.Ballot, OK: true}
}

func (n *Node) handleCommit(req *CommitRequest) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if s, ok := n.log[req.Slot]; ok {
		s.Committed = true
	}
	n.advanceCommitLocked()
}

// stepDownLocked clears leadership if a higher ballot shows up.
func (n *Node) stepDownLocked(ballot ProposalID) {
	if n.leaderID == n.id && ballot.Compare(n.ballot) > 0 {
		n.leaderID = ""
		n.leaseExpiry = time.Time{}
	}
}

// advanceCommitLocked moves commitIndex over the contiguous committed prefix.
func (n *Node) advanceCommitLocked() {
	for {
		next := n.commitIndex + 1
		if s, ok := n.log[next]; ok && s.Committed {
			n.commitIndex = next
			continue
		}
		return
	}
}

// --- read-only accessors (used by tests, demo and the adaptive wiring) ---

func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.leaderID == n.id && n.now().Before(n.leaseExpiry)
}

func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// CommittedEntries returns the values of the committed prefix 1..commitIndex.
func (n *Node) CommittedEntries() [][]byte {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([][]byte, 0, n.commitIndex)
	for i := uint64(1); i <= n.commitIndex; i++ {
		if s, ok := n.log[i]; ok {
			out = append(out, append([]byte(nil), s.Value...))
		}
	}
	return out
}

// SlotValue returns the (possibly uncommitted) value stored at slot.
func (n *Node) SlotValue(slot uint64) ([]byte, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if s, ok := n.log[slot]; ok {
		return append([]byte(nil), s.Value...), true
	}
	return nil, false
}

func (n *Node) PreparesHandled() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.preparesHandled
}

func (n *Node) AcceptsHandled() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.acceptsHandled
}
