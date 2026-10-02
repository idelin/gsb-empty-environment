package paxos

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrNotLeader is returned by Propose on followers or after lease expiry.
	ErrNotLeader = errors.New("paxos: not leader")
	// ErrNoMajority is returned when a quorum cannot be reached.
	ErrNoMajority = errors.New("paxos: cannot reach a majority")
	// ErrStaleBallot is returned when a higher ballot was observed.
	ErrStaleBallot = errors.New("paxos: a higher ballot was promised elsewhere")
)

// DefaultLeaseDuration is the leader lease used when Config leaves it zero.
const DefaultLeaseDuration = 5 * time.Second

// Config configures a Node.
type Config struct {
	ID            int
	LeaseDuration time.Duration
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

// Node is a single Multi-Paxos participant combining proposer, acceptor
// and learner roles.
type Node struct {
	mu  sync.Mutex
	id  int
	net *Network

	// Acceptor state. promised is the highest ballot this node has
	// promised not to vote below; it covers every slot (Multi-Paxos).
	promised Ballot
	log      map[uint64]*LogEntry

	commitIndex uint64
	maxSlot     uint64

	// Leader state.
	isLeader      bool
	ballot        Ballot
	leaseUntil    time.Time
	leaseDuration time.Duration

	now func() time.Time
}

// NewNode creates a node and attaches it to the network.
func NewNode(cfg Config, net *Network) *Node {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	lease := cfg.LeaseDuration
	if lease <= 0 {
		lease = DefaultLeaseDuration
	}
	n := &Node{
		id:            cfg.ID,
		net:           net,
		log:           make(map[uint64]*LogEntry),
		leaseDuration: lease,
		now:           now,
	}
	return n
}

// ID returns the node identifier.
func (n *Node) ID() int { return n.id }

// IsLeader reports whether the node currently believes it is leader.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader
}

// Ballot returns the ballot this node leads with, if any.
func (n *Node) Ballot() Ballot {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ballot
}

// CommitIndex returns the highest contiguous committed slot.
func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// MaxSlot returns the highest slot present in the local log.
func (n *Node) MaxSlot() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.maxSlot
}

// Entry returns a copy of the log entry at slot, or nil.
func (n *Node) Entry(slot uint64) *LogEntry {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.log[slot]
	if !ok {
		return nil
	}
	cp := *e
	cp.Value = cloneBytes(e.Value)
	return &cp
}

// CommittedEntries returns copies of all committed entries in slot order.
func (n *Node) CommittedEntries() []LogEntry {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]LogEntry, 0, n.commitIndex)
	for s := uint64(1); s <= n.commitIndex; s++ {
		if e, ok := n.log[s]; ok && e.State == StateCommitted {
			cp := *e
			cp.Value = cloneBytes(e.Value)
			out = append(out, cp)
		}
	}
	return out
}

// stepDownLocked relinquishes leadership if a higher ballot showed up.
func (n *Node) stepDownLocked(b Ballot) {
	if n.isLeader && b.Greater(n.ballot) {
		n.isLeader = false
	}
}

// advanceCommitLocked raises commitIndex over contiguous committed slots,
// guaranteeing a gap-free committed prefix.
func (n *Node) advanceCommitLocked() {
	for {
		e, ok := n.log[n.commitIndex+1]
		if !ok || e.State != StateCommitted {
			return
		}
		n.commitIndex++
	}
}

// --------------------------------------------------------------------
// Acceptor handlers (invoked through the Network, possibly concurrently).
// --------------------------------------------------------------------

// HandlePrepare processes a Phase 1 request.
func (n *Node) HandlePrepare(req PrepareReq) PromiseResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !req.Ballot.Greater(n.promised) {
		return PromiseResp{OK: false, Promised: n.promised}
	}
	n.promised = req.Ballot
	n.stepDownLocked(req.Ballot)
	resp := PromiseResp{
		OK:          true,
		Promised:    n.promised,
		CommitIndex: n.commitIndex,
		MaxSlot:     n.maxSlot,
	}
	for s := req.From; s <= n.maxSlot; s++ {
		if e, ok := n.log[s]; ok {
			resp.Slots = append(resp.Slots, SlotInfo{
				Slot:      s,
				Ballot:    e.Ballot,
				Value:     cloneBytes(e.Value),
				Committed: e.State == StateCommitted,
			})
		}
	}
	return resp
}

// HandleAccept processes a Phase 2 request.
func (n *Node) HandleAccept(req AcceptReq) AcceptedResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Ballot.Less(n.promised) {
		return AcceptedResp{OK: false, Promised: n.promised}
	}
	n.promised = req.Ballot
	n.stepDownLocked(req.Ballot)
	if e, ok := n.log[req.Slot]; ok && e.State == StateCommitted {
		// Already chosen: accepting again is a harmless no-op.
		return AcceptedResp{OK: true, Promised: n.promised}
	}
	n.log[req.Slot] = &LogEntry{
		Slot:   req.Slot,
		Ballot: req.Ballot,
		Value:  cloneBytes(req.Value),
		State:  StateAccepted,
	}
	if req.Slot > n.maxSlot {
		n.maxSlot = req.Slot
	}
	return AcceptedResp{OK: true, Promised: n.promised}
}

// HandleCommit processes a commit notification from a leader.
func (n *Node) HandleCommit(req CommitReq) {
	n.mu.Lock()
	syncNeeded := false
	if !req.Ballot.Less(n.promised) {
		n.promised = req.Ballot
		n.stepDownLocked(req.Ballot)
		if e, ok := n.log[req.Slot]; ok && e.State == StateAccepted {
			// Only the exact (slot, ballot) proposal we accepted may be
			// marked committed; anything else is fixed by catch-up sync.
			if e.Ballot == req.Ballot {
				e.State = StateCommitted
			}
		}
		n.advanceCommitLocked()
		syncNeeded = n.commitIndex < req.CommitIndex
	}
	leaderID := req.Ballot.ID
	n.mu.Unlock()
	if syncNeeded {
		n.pullSync(leaderID)
	}
}

// HandleSync serves committed entries to a lagging peer.
func (n *Node) HandleSync(req SyncReq) SyncResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	b := n.promised
	if n.isLeader {
		b = n.ballot
	}
	resp := SyncResp{Ballot: b, CommitIndex: n.commitIndex}
	for s := req.From; s <= n.commitIndex; s++ {
		if e, ok := n.log[s]; ok && e.State == StateCommitted {
			cp := *e
			cp.Value = cloneBytes(e.Value)
			resp.Entries = append(resp.Entries, cp)
		}
	}
	return resp
}

// pullSync fetches and installs committed entries from the leader,
// overwriting local uncommitted ("dirty") entries. Committed entries are
// never overwritten; a conflicting committed value would be a safety
// violation and panics loudly.
func (n *Node) pullSync(leaderID int) {
	n.mu.Lock()
	from := n.commitIndex + 1
	n.mu.Unlock()
	resp, ok := n.net.Sync(n.id, leaderID, SyncReq{From: from})
	if !ok {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if resp.Ballot.Greater(n.promised) {
		n.promised = resp.Ballot
	}
	n.stepDownLocked(resp.Ballot)
	for _, e := range resp.Entries {
		if e.Slot <= n.commitIndex {
			continue
		}
		if local, ok := n.log[e.Slot]; ok &&
			local.State == StateCommitted &&
			!bytes.Equal(local.Value, e.Value) {
			panic(fmt.Sprintf("paxos: SAFETY VIOLATION on node %d: committed slot %d changed", n.id, e.Slot))
		}
		n.log[e.Slot] = &LogEntry{
			Slot:   e.Slot,
			Ballot: e.Ballot,
			Value:  cloneBytes(e.Value),
			State:  StateCommitted,
		}
		if e.Slot > n.maxSlot {
			n.maxSlot = e.Slot
		}
	}
	n.advanceCommitLocked()
}

// --------------------------------------------------------------------
// Proposer / leader path.
// --------------------------------------------------------------------

// Campaign runs Phase 1 with a fresh ballot and, on success, becomes
// leader: it adopts every committed value learned from promises,
// re-proposes the highest-ballot accepted value per open slot and plugs
// holes with no-ops, repairing the log up to the highest slot any
// acceptor has seen.
func (n *Node) Campaign() error {
	n.mu.Lock()
	next := n.promised.N
	if n.ballot.N > next {
		next = n.ballot.N
	}
	n.ballot = Ballot{N: next + 1, ID: n.id}
	b := n.ballot
	from := n.commitIndex + 1
	n.mu.Unlock()

	peers := n.net.Peers()
	type result struct {
		reachable bool
		resp      PromiseResp
	}
	results := make(chan result, len(peers))
	for _, p := range peers {
		go func(p int) {
			resp, ok := n.net.Prepare(n.id, p, PrepareReq{Ballot: b, From: from})
			if !ok {
				results <- result{}
				return
			}
			results <- result{reachable: true, resp: resp}
		}(p)
	}
	var promises []PromiseResp
	var maxPromised Ballot
	rejected := false
	for range peers {
		r := <-results
		if !r.reachable {
			continue
		}
		if r.resp.OK {
			promises = append(promises, r.resp)
		} else {
			rejected = true
			if maxPromised.Less(r.resp.Promised) {
				maxPromised = r.resp.Promised
			}
		}
	}
	if len(promises) < n.net.Majority() {
		if rejected {
			n.mu.Lock()
			if maxPromised.Greater(n.promised) {
				n.promised = maxPromised
			}
			n.mu.Unlock()
			return ErrStaleBallot
		}
		return ErrNoMajority
	}

	// Merge promises: per slot keep the accepted value with the highest
	// ballot; committed values are authoritative.
	type choice struct {
		has       bool
		committed bool
		ballot    Ballot
		value     []byte
	}
	chosen := make(map[uint64]choice)
	var maxSlotSeen uint64
	for _, pr := range promises {
		if pr.MaxSlot > maxSlotSeen {
			maxSlotSeen = pr.MaxSlot
		}
		for _, si := range pr.Slots {
			c := chosen[si.Slot]
			switch {
			case si.Committed:
				c.has, c.committed, c.value = true, true, si.Value
			case c.committed:
				// keep authoritative committed value
			case !c.has || c.ballot.Less(si.Ballot):
				c.has, c.ballot, c.value = true, si.Ballot, si.Value
			}
			chosen[si.Slot] = c
		}
	}

	n.mu.Lock()
	if n.promised.Greater(b) {
		n.mu.Unlock()
		return ErrStaleBallot
	}
	n.isLeader = true
	n.leaseUntil = n.now().Add(n.leaseDuration)
	for s, c := range chosen {
		if c.committed && s > n.commitIndex {
			n.log[s] = &LogEntry{Slot: s, Ballot: b, Value: cloneBytes(c.value), State: StateCommitted}
			if s > n.maxSlot {
				n.maxSlot = s
			}
		}
	}
	n.advanceCommitLocked()
	repairFrom := n.commitIndex + 1
	n.mu.Unlock()

	// Gap repair: re-propose every open slot up to the highest slot any
	// acceptor has seen; slots nobody accepted get a no-op.
	for s := repairFrom; s <= maxSlotSeen; s++ {
		c := chosen[s]
		if c.committed {
			continue
		}
		var val []byte
		if c.has {
			val = c.value
		}
		if err := n.proposeAt(s, b, val); err != nil {
			return err
		}
	}
	return nil
}

// Propose appends value to the log and commits it. While the leader lease
// holds this takes the fast path: Phase 1 is skipped and the entry is
// committed with a single Phase 2 round.
func (n *Node) Propose(value []byte) (uint64, error) {
	n.mu.Lock()
	if !n.isLeader || !n.now().Before(n.leaseUntil) {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	slot := n.maxSlot + 1
	b := n.ballot
	n.mu.Unlock()
	if err := n.proposeAt(slot, b, value); err != nil {
		return 0, err
	}
	return slot, nil
}

// proposeAt runs Phase 2 for one slot and, once a majority accepted,
// commits it and notifies the followers.
func (n *Node) proposeAt(slot uint64, b Ballot, value []byte) error {
	peers := n.net.Peers()
	type result struct {
		reachable bool
		ok        bool
		promised  Ballot
	}
	results := make(chan result, len(peers))
	for _, p := range peers {
		go func(p int) {
			resp, ok := n.net.Accept(n.id, p, AcceptReq{Ballot: b, Slot: slot, Value: value})
			if !ok {
				results <- result{}
				return
			}
			results <- result{reachable: true, ok: resp.OK, promised: resp.Promised}
		}(p)
	}
	accepts := 0
	var maxPromised Ballot
	rejected := false
	for range peers {
		r := <-results
		if !r.reachable {
			continue
		}
		if r.ok {
			accepts++
		} else {
			rejected = true
			if maxPromised.Less(r.promised) {
				maxPromised = r.promised
			}
		}
	}
	if accepts >= n.net.Majority() {
		n.mu.Lock()
		if e, ok := n.log[slot]; ok && e.Ballot == b {
			e.State = StateCommitted
		}
		n.advanceCommitLocked()
		// A successful quorum round renews the leader lease.
		n.leaseUntil = n.now().Add(n.leaseDuration)
		ci := n.commitIndex
		n.mu.Unlock()
		n.broadcastCommit(slot, b, ci)
		return nil
	}
	if rejected {
		n.mu.Lock()
		if maxPromised.Greater(n.promised) {
			n.promised = maxPromised
		}
		n.stepDownLocked(maxPromised)
		n.mu.Unlock()
		return ErrStaleBallot
	}
	return ErrNoMajority
}

// broadcastCommit notifies all peers about a freshly committed slot.
func (n *Node) broadcastCommit(slot uint64, b Ballot, commitIndex uint64) {
	for _, p := range n.net.Peers() {
		if p == n.id {
			continue
		}
		n.net.Commit(n.id, p, CommitReq{Ballot: b, Slot: slot, CommitIndex: commitIndex})
	}
}
