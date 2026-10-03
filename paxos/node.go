// Package paxos implements Multi-Paxos with a hole-free commit log.
//
// Protocol summary:
//   - Phase 1 (Prepare/Promise): a candidate leader picks a fresh ballot,
//     collects promises from a majority, and learns every accepted value
//     above the commit horizon. Holes are repaired and conflicting
//     (minority "dirty") values are safely superseded by the value chosen
//     at the highest accepted ballot, or by a no-op.
//   - Phase 2 (Accept/Accepted): values are accepted slot by slot; a
//     majority accept commits the slot and the commit is broadcast.
//   - Leader lease: while a leader keeps renewing its lease (via Accepts
//     or heartbeats) it skips Phase 1 entirely and appends consecutive
//     log entries with Phase 2 only. Followers holding a valid lease
//     reject Prepare/Accept from any other ballot owner.
package paxos

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrUnreachable   = errors.New("paxos: peer unreachable")
	ErrNotLeader     = errors.New("paxos: not leader")
	ErrLeaseExpired  = errors.New("paxos: leader lease expired")
	ErrNoMajority    = errors.New("paxos: failed to reach a majority")
	ErrHigherPromise = errors.New("paxos: a higher promise exists")
)

// Slot is one log position.
type Slot struct {
	AcceptedBallot Ballot
	Value          string
	HasValue       bool
	Committed      bool
}

// PrepareRequest is a Phase-1a message.
type PrepareRequest struct {
	From   int
	Ballot Ballot
	// Since is the requester's commitIndex+1; the acceptor replies with
	// every slot it knows at or after this index.
	Since uint64
}

// Promise is a Phase-1b reply.
type Promise struct {
	OK          bool
	Promised    Ballot // acceptor's current promise (for backoff)
	Entries     map[uint64]Slot
	CommitIndex uint64
}

// AcceptRequest is a Phase-2a message.
type AcceptRequest struct {
	From   int
	Ballot Ballot
	Slot   uint64
	Value  string
}

// Accepted is a Phase-2b reply.
type Accepted struct {
	OK       bool
	Promised Ballot
}

// CommitRequest informs a follower that a slot is chosen.
type CommitRequest struct {
	From   int
	Ballot Ballot
	Slot   uint64
	Value  string
}

// HeartbeatRequest renews the leader lease and carries the commit horizon.
type HeartbeatRequest struct {
	From        int
	Ballot      Ballot
	CommitIndex uint64
}

// HeartbeatReply also reports follower's uncommitted ("dirty") slots above
// its commit horizon so the leader can repair them after a partition heals.
type HeartbeatReply struct {
	OK          bool
	CommitIndex uint64
	Dirty       []uint64
}

// Node is a single Multi-Paxos replica.
type Node struct {
	mu  sync.Mutex
	id  int
	net *Network

	leaseDuration time.Duration

	promised    Ballot
	slots       map[uint64]*Slot
	commitIndex uint64
	maxSlot     uint64

	// Follower-side lease view.
	leaseLeader int
	leaseExpiry time.Time

	// Leader-side state.
	isLeader            bool
	leaderBallot        Ballot
	lastMajorityContact time.Time

	applied []string // committed values in slot order
}

func NewNode(net *Network, id int, leaseDuration time.Duration) *Node {
	n := &Node{
		id:            id,
		net:           net,
		leaseDuration: leaseDuration,
		slots:         make(map[uint64]*Slot),
		leaseLeader:   -1,
	}
	net.register(n)
	return n
}

func (n *Node) ID() int { return n.id }

func (n *Node) majority() int { return n.net.size()/2 + 1 }

// ---- Acceptor RPC handlers -------------------------------------------------

func (n *Node) HandlePrepare(req PrepareRequest) Promise {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.leaseLockedLocked(req.Ballot) || req.Ballot.Compare(n.promised) <= 0 {
		return Promise{OK: false, Promised: n.promised}
	}
	n.promised = req.Ballot
	n.leaseLeader = req.Ballot.Node
	n.leaseExpiry = time.Now().Add(n.leaseDuration)
	entries := make(map[uint64]Slot)
	for idx, s := range n.slots {
		if idx >= req.Since && s.HasValue {
			entries[idx] = *s
		}
	}
	return Promise{OK: true, Promised: n.promised, Entries: entries, CommitIndex: n.commitIndex}
}

func (n *Node) HandleAccept(req AcceptRequest) Accepted {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.leaseLockedLocked(req.Ballot) || req.Ballot.Less(n.promised) {
		return Accepted{OK: false, Promised: n.promised}
	}
	n.promised = req.Ballot
	n.grantLeaseLocked(req.Ballot.Node)
	s := n.slotLocked(req.Slot)
	s.AcceptedBallot = req.Ballot
	s.Value = req.Value
	s.HasValue = true
	if req.Slot > n.maxSlot {
		n.maxSlot = req.Slot
	}
	return Accepted{OK: true, Promised: n.promised}
}

func (n *Node) HandleCommit(req CommitRequest) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.slotLocked(req.Slot)
	if !s.Committed {
		s.AcceptedBallot = req.Ballot
		s.Value = req.Value
		s.HasValue = true
		s.Committed = true
	}
	if req.Slot > n.maxSlot {
		n.maxSlot = req.Slot
	}
	n.advanceLocked()
}

func (n *Node) HandleHeartbeat(req HeartbeatRequest) HeartbeatReply {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Ballot.Less(n.promised) {
		return HeartbeatReply{}
	}
	n.grantLeaseLocked(req.Ballot.Node)
	var dirty []uint64
	for idx, s := range n.slots {
		if idx > n.commitIndex && s.HasValue && !s.Committed {
			dirty = append(dirty, idx)
		}
	}
	return HeartbeatReply{OK: true, CommitIndex: n.commitIndex, Dirty: dirty}
}

// leaseLockedLocked reports whether a valid follower lease blocks a
// message from a different ballot owner.
func (n *Node) leaseLockedLocked(b Ballot) bool {
	return n.leaseLeader >= 0 && n.leaseLeader != b.Node && time.Now().Before(n.leaseExpiry)
}

func (n *Node) grantLeaseLocked(leader int) {
	n.leaseLeader = leader
	n.leaseExpiry = time.Now().Add(n.leaseDuration)
}

func (n *Node) slotLocked(idx uint64) *Slot {
	s, ok := n.slots[idx]
	if !ok {
		s = &Slot{}
		n.slots[idx] = s
	}
	return s
}

// advanceLocked commits the contiguous prefix and records applied values.
func (n *Node) advanceLocked() {
	for {
		next := n.commitIndex + 1
		s, ok := n.slots[next]
		if !ok || !s.Committed {
			return
		}
		n.commitIndex = next
		n.applied = append(n.applied, s.Value)
	}
}

// ---- Leader logic ----------------------------------------------------------

// BecomeLeader runs Phase 1, repairs every hole above the commit horizon,
// and commits the repaired entries through Phase 2.
func (n *Node) BecomeLeader() error {
	n.mu.Lock()
	ballot := Ballot{Num: n.promised.Num + 1, Node: n.id}
	since := n.commitIndex + 1
	n.mu.Unlock()

	// Phase 1: gather promises from a majority (self included).
	self := n.HandlePrepare(PrepareRequest{From: n.id, Ballot: ballot, Since: since})
	if !self.OK {
		return ErrHigherPromise
	}
	promises := []Promise{self}
	for _, pid := range n.net.peerIDs(n.id) {
		if !n.net.reachable(n.id, pid) {
			continue
		}
		p := n.net.get(pid).HandlePrepare(PrepareRequest{From: n.id, Ballot: ballot, Since: since})
		if !p.OK {
			n.mu.Lock()
			if p.Promised.Greater(n.promised) {
				n.promised = p.Promised
			}
			n.mu.Unlock()
			return ErrHigherPromise
		}
		promises = append(promises, p)
	}
	if len(promises) < n.majority() {
		return ErrNoMajority
	}

	// Merge: adopt the highest commit horizon, learn committed values we
	// missed, and pick a safe value for every open slot.
	n.mu.Lock()
	maxCommit := n.commitIndex
	n.mu.Unlock()
	merged := make(map[uint64]Slot)
	for _, p := range promises {
		if p.CommitIndex > maxCommit {
			maxCommit = p.CommitIndex
		}
		for idx, s := range p.Entries {
			cur, ok := merged[idx]
			if !ok || s.AcceptedBallot.Greater(cur.AcceptedBallot) || (s.Committed && !cur.Committed) {
				merged[idx] = s
			}
		}
	}

	// Learn already-committed values up to maxCommit.
	n.mu.Lock()
	for idx := n.commitIndex + 1; idx <= maxCommit; idx++ {
		if s, ok := merged[idx]; ok && s.HasValue {
			local := n.slotLocked(idx)
			if !local.Committed {
				*local = s
				local.Committed = true
			}
			if idx > n.maxSlot {
				n.maxSlot = idx
			}
		}
	}
	n.advanceLocked()

	// Repair every slot in (commitIndex, maxSlotSeen]: highest-ballot value
	// wins; empty holes get a no-op. This is what safely overwrites dirty
	// minority logs: only a value present in the majority can survive.
	maxSeen := n.maxSlot
	for idx := range merged {
		if idx > maxSeen {
			maxSeen = idx
		}
	}
	type repair struct {
		slot  uint64
		value string
	}
	var repairs []repair
	for idx := n.commitIndex + 1; idx <= maxSeen; idx++ {
		v := NoopValue
		if s, ok := merged[idx]; ok && s.HasValue {
			v = s.Value
		}
		repairs = append(repairs, repair{slot: idx, value: v})
	}
	n.isLeader = false
	n.mu.Unlock()

	// Phase 2 for each repaired slot, then commit.
	for _, r := range repairs {
		if err := n.acceptToMajority(ballot, r.slot, r.value); err != nil {
			return err
		}
		n.commitAndBroadcast(ballot, r.slot, r.value)
	}

	n.mu.Lock()
	n.isLeader = true
	n.leaderBallot = ballot
	n.lastMajorityContact = time.Now()
	n.mu.Unlock()
	return nil
}

// acceptToMajority runs Phase 2 for one slot; returns error without majority.
func (n *Node) acceptToMajority(ballot Ballot, slot uint64, value string) error {
	req := AcceptRequest{From: n.id, Ballot: ballot, Slot: slot, Value: value}
	self := n.HandleAccept(req)
	if !self.OK {
		return ErrHigherPromise
	}
	votes := 1
	for _, pid := range n.net.peerIDs(n.id) {
		if !n.net.reachable(n.id, pid) {
			continue
		}
		if a := n.net.get(pid).HandleAccept(req); a.OK {
			votes++
		} else {
			n.mu.Lock()
			if a.Promised.Greater(n.promised) {
				n.promised = a.Promised
			}
			n.mu.Unlock()
		}
	}
	if votes < n.majority() {
		return ErrNoMajority
	}
	return nil
}

// commitAndBroadcast marks a slot chosen locally and tells every reachable peer.
func (n *Node) commitAndBroadcast(ballot Ballot, slot uint64, value string) {
	n.mu.Lock()
	s := n.slotLocked(slot)
	if !s.Committed {
		s.Committed = true
		s.AcceptedBallot = ballot
		s.Value = value
		s.HasValue = true
	}
	n.advanceLocked()
	n.lastMajorityContact = time.Now()
	n.mu.Unlock()
	req := CommitRequest{From: n.id, Ballot: ballot, Slot: slot, Value: value}
	for _, pid := range n.net.peerIDs(n.id) {
		if n.net.reachable(n.id, pid) {
			n.net.get(pid).HandleCommit(req)
		}
	}
}

// Propose appends a client value. Under a valid lease it runs Phase 2 only.
func (n *Node) Propose(value string) (uint64, error) {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	if time.Now().After(n.lastMajorityContact.Add(n.leaseDuration)) {
		n.mu.Unlock()
		return 0, ErrLeaseExpired
	}
	ballot := n.leaderBallot
	// Allocate the next slot and self-accept atomically so concurrent
	// proposals never share a (ballot, slot) pair with different values.
	slot := n.maxSlot + 1
	s := n.slotLocked(slot)
	s.AcceptedBallot = ballot
	s.Value = value
	s.HasValue = true
	n.maxSlot = slot
	n.mu.Unlock()

	if err := n.acceptOthersToMajority(ballot, slot, value); err != nil {
		return 0, err
	}
	n.commitAndBroadcast(ballot, slot, value)
	return slot, nil
}

// acceptOthersToMajority is acceptToMajority when the leader already
// self-accepted (used by Propose).
func (n *Node) acceptOthersToMajority(ballot Ballot, slot uint64, value string) error {
	req := AcceptRequest{From: n.id, Ballot: ballot, Slot: slot, Value: value}
	votes := 1 // self
	for _, pid := range n.net.peerIDs(n.id) {
		if !n.net.reachable(n.id, pid) {
			continue
		}
		if a := n.net.get(pid).HandleAccept(req); a.OK {
			votes++
		} else {
			n.mu.Lock()
			if a.Promised.Greater(n.promised) {
				n.promised = a.Promised
			}
			n.mu.Unlock()
		}
	}
	if votes < n.majority() {
		return ErrNoMajority
	}
	return nil
}

// Heartbeat renews the lease on every reachable follower and repairs any
// dirty (uncommitted, conflicting) slots followers report — this is what
// converges a healed minority partition onto the chosen log. Returns false
// if a majority could not be refreshed (the leader must re-elect).
func (n *Node) Heartbeat() bool {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return false
	}
	ballot := n.leaderBallot
	ci := n.commitIndex
	n.mu.Unlock()
	ok := 1 // self
	dirty := make(map[uint64]bool)
	minFollowerCI := ci
	for _, pid := range n.net.peerIDs(n.id) {
		if !n.net.reachable(n.id, pid) {
			continue
		}
		rep := n.net.get(pid).HandleHeartbeat(HeartbeatRequest{From: n.id, Ballot: ballot, CommitIndex: ci})
		if rep.OK {
			ok++
			if rep.CommitIndex < minFollowerCI {
				minFollowerCI = rep.CommitIndex
			}
			for _, s := range rep.Dirty {
				dirty[s] = true
			}
		}
	}
	if ok < n.majority() {
		return false
	}
	n.mu.Lock()
	n.lastMajorityContact = time.Now()
	n.mu.Unlock()
	// Replay commits to lagging followers so no committed slot is missing.
	n.mu.Lock()
	myCI := n.commitIndex
	var replay []uint64
	for slot := minFollowerCI + 1; slot <= myCI; slot++ {
		replay = append(replay, slot)
	}
	n.mu.Unlock()
	for _, slot := range replay {
		n.mu.Lock()
		val := ""
		if s, ok := n.slots[slot]; ok {
			val = s.Value
		}
		n.mu.Unlock()
		n.commitAndBroadcast(ballot, slot, val)
	}
	// Repair reported dirty slots above the commit horizon: anything the
	// leader already chose is re-committed; anything unknown was never
	// chosen and becomes a no-op.
	dirtySorted := make([]uint64, 0, len(dirty))
	for slot := range dirty {
		dirtySorted = append(dirtySorted, slot)
	}
	sort.Slice(dirtySorted, func(i, j int) bool { return dirtySorted[i] < dirtySorted[j] })
	for _, slot := range dirtySorted {
		n.mu.Lock()
		if slot <= n.commitIndex {
			n.mu.Unlock()
			continue // already covered by the replay above
		}
		val := NoopValue
		if s, ok := n.slots[slot]; ok && s.HasValue {
			val = s.Value
		}
		n.mu.Unlock()
		if err := n.acceptToMajority(ballot, slot, val); err != nil {
			return false
		}
		n.commitAndBroadcast(ballot, slot, val)
	}
	return true
}

// ---- Inspection (used by tests and the demo) -------------------------------

func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// Applied returns the committed values in slot order.
func (n *Node) Applied() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, len(n.applied))
	copy(out, n.applied)
	return out
}

// SlotValue returns the stored value at idx ("" if none).
func (n *Node) SlotValue(idx uint64) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, ok := n.slots[idx]
	if !ok || !s.HasValue {
		return "", false
	}
	return s.Value, true
}

func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader
}

func (n *Node) String() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return fmt.Sprintf("node%d commit=%d promised=%+v leader=%v", n.id, n.commitIndex, n.promised, n.isLeader)
}
