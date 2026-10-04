package paxos

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotLeader  = errors.New("paxos: not leader")
	ErrNoMajority = errors.New("paxos: majority unreachable")
)

// Node is a single Multi-Paxos replica: acceptor, proposer and learner.
type Node struct {
	mu  sync.Mutex
	id  int
	net *Network
	ctl *Controller

	promised  Ballot // highest ballot promised to any proposer
	maxBallot Ballot // highest ballot ever seen (drives ballot allocation)
	entries   map[uint64]Entry
	commitIdx uint64 // contiguous committed prefix; the log has no holes below it

	isLeader   bool
	ballot     Ballot // leadership ballot
	leaseUntil time.Time

	propMu sync.Mutex // serializes Propose so slots are allocated densely

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	loopOnce sync.Once
}

func NewNode(id int, net *Network, ctl *Controller) *Node {
	if ctl == nil {
		ctl = NewController(DefaultConfig(), nil)
	}
	n := &Node{
		id:      id,
		net:     net,
		ctl:     ctl,
		entries: make(map[uint64]Entry),
		stopCh:  make(chan struct{}),
	}
	net.Add(n)
	return n
}

func (n *Node) ID() int { return n.id }

func (n *Node) majority() int { return n.net.Size()/2 + 1 }

// IsLeader reports whether this node currently holds leadership.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader
}

// CommitIndex is the length of the contiguous committed prefix.
func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIdx
}

// EntryAt returns the log entry at slot, if any.
func (n *Node) EntryAt(slot uint64) (Entry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.entries[slot]
	return e, ok
}

// LeaseValid reports whether the cached leadership lease is still fresh,
// meaning Phase 1 may be skipped for new proposals.
func (n *Node) LeaseValid() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader && time.Now().Before(n.leaseUntil)
}

// ---------------------------------------------------------------------------
// Acceptor RPC handlers
// ---------------------------------------------------------------------------

// HandlePrepare is Phase 1b. A promise carries the acceptor's uncommitted
// tail so the proposer can reconstruct and repair the log.
func (n *Node) HandlePrepare(req PrepareRequest) PrepareResponse {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Ballot.LessThan(n.promised) {
		return PrepareResponse{OK: false, Promised: n.promised}
	}
	n.promised = req.Ballot
	n.noteBallotLocked(req.Ballot)
	if n.isLeader && req.Ballot.GreaterThan(n.ballot) {
		n.isLeader = false // a higher ballot is campaigning: yield
	}
	resp := PrepareResponse{OK: true, Promised: n.promised, CommitIndex: n.commitIdx}
	for slot, e := range n.entries {
		if slot > n.commitIdx && !e.Committed {
			resp.Entries = append(resp.Entries, e)
		}
	}
	sort.Slice(resp.Entries, func(i, j int) bool { return resp.Entries[i].Slot < resp.Entries[j].Slot })
	return resp
}

// HandleAccept is Phase 2b. Committed entries are immutable; uncommitted
// entries may be overwritten by an equal-or-higher ballot.
func (n *Node) HandleAccept(req AcceptRequest) AcceptResponse {
	n.mu.Lock()
	defer n.mu.Unlock()
	e := req.Entry
	if e.Ballot.LessThan(n.promised) {
		return AcceptResponse{OK: false, Promised: n.promised}
	}
	n.promised = e.Ballot
	n.noteBallotLocked(e.Ballot)
	if existing, ok := n.entries[e.Slot]; ok {
		if existing.Committed {
			return AcceptResponse{OK: true, Promised: n.promised}
		}
		if existing.Ballot.GreaterThan(e.Ballot) {
			return AcceptResponse{OK: true, Promised: n.promised}
		}
	}
	n.entries[e.Slot] = Entry{Slot: e.Slot, Ballot: e.Ballot, Value: e.Value}
	return AcceptResponse{OK: true, Promised: n.promised}
}

// HandleCommit learns a committed entry. It overwrites any dirty
// (accepted-but-uncommitted) minority copy, because commitment means a
// majority already agreed on this exact value for this slot.
func (n *Node) HandleCommit(req CommitRequest) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.commitLocked(req.Entry)
}

// HandleHeartbeat maintains the leader lease and triggers lag repair.
func (n *Node) HandleHeartbeat(req HeartbeatRequest) HeartbeatResponse {
	n.mu.Lock()
	if req.Ballot.LessThan(n.promised) {
		idx := n.commitIdx
		n.mu.Unlock()
		return HeartbeatResponse{OK: false, CommitIndex: idx}
	}
	n.promised = req.Ballot
	n.noteBallotLocked(req.Ballot)
	if n.isLeader && req.Ballot.GreaterThan(n.ballot) {
		n.isLeader = false // deposed by a newer leader
	}
	needSync := req.CommitIndex > n.commitIdx
	n.mu.Unlock()
	if needSync {
		n.syncFrom(req.Ballot.ID)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return HeartbeatResponse{OK: true, CommitIndex: n.commitIdx}
}

// HandleFetchCommitted serves committed entries in [from, commitIdx] to a
// lagging follower.
func (n *Node) HandleFetchCommitted(from uint64) []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Entry, 0, n.commitIdx-from+1)
	for slot := from; slot <= n.commitIdx; slot++ {
		if e, ok := n.entries[slot]; ok && e.Committed {
			out = append(out, e)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Proposer: election, reconstruction, normal operation
// ---------------------------------------------------------------------------

// Campaign runs Phase 1 with a fresh ballot. On a majority of promises the
// node becomes leader, repairs the log tail (filling holes with noops and
// re-proposing the highest-ballot value per slot), and arms its lease.
func (n *Node) Campaign() bool {
	n.mu.Lock()
	ballot := Ballot{N: n.maxBallot.N + 1, ID: n.id}
	n.mu.Unlock()

	req := PrepareRequest{Ballot: ballot}
	promises := make([]PrepareResponse, 0, n.majority())
	self := n.HandlePrepare(req)
	if !self.OK {
		return false
	}
	promises = append(promises, self)
	for _, peer := range n.net.peers(n.id) {
		if len(promises) >= n.majority() {
			break
		}
		p, ok := n.net.route(n.id, peer)
		if !ok {
			continue
		}
		resp := p.HandlePrepare(req)
		if resp.OK {
			promises = append(promises, resp)
		}
	}
	if len(promises) < n.majority() {
		return false
	}

	n.mu.Lock()
	n.isLeader = true
	n.ballot = ballot
	n.promised = ballot
	n.leaseUntil = time.Now().Add(n.ctl.LeaseDuration())
	n.mu.Unlock()

	n.reconstruct(promises)
	return true
}

// reconstruct merges the promised tails: for every slot beyond the highest
// known commit index, re-propose the highest-ballot accepted value (or a noop
// to plug a hole) so the committed prefix becomes contiguous and
// majority-durable.
func (n *Node) reconstruct(promises []PrepareResponse) {
	n.mu.Lock()
	ballot := n.ballot
	maxCommit := n.commitIdx
	n.mu.Unlock()

	chosen := make(map[uint64]Entry)
	maxSlot := maxCommit
	consider := func(e Entry) {
		if e.Slot <= maxCommit {
			return
		}
		if e.Slot > maxSlot {
			maxSlot = e.Slot
		}
		if cur, ok := chosen[e.Slot]; !ok || e.Ballot.GreaterThan(cur.Ballot) {
			chosen[e.Slot] = e
		}
	}
	for _, p := range promises {
		if p.CommitIndex > maxCommit {
			maxCommit = p.CommitIndex
		}
	}
	for _, p := range promises {
		for _, e := range p.Entries {
			consider(e)
		}
	}
	n.mu.Lock()
	for slot, e := range n.entries {
		if slot > maxCommit && !e.Committed {
			consider(e)
		}
	}
	n.mu.Unlock()
	if maxSlot < maxCommit {
		maxSlot = maxCommit
	}

	for slot := maxCommit + 1; slot <= maxSlot; slot++ {
		e, ok := chosen[slot]
		if !ok {
			e = Entry{Slot: slot, Value: NoopValue}
		}
		e.Ballot = ballot
		e.Committed = false
		if !n.phase2(e) {
			return // lost majority mid-repair; a later campaign will resume
		}
	}
}

// Propose appends value to the log. With a valid lease it goes straight to
// Phase 2 (the Multi-Paxos fast path); otherwise it re-runs Phase 1 first.
func (n *Node) Propose(value string) (uint64, error) {
	n.propMu.Lock()
	defer n.propMu.Unlock()

	if err := n.ctl.Acquire(); err != nil {
		return 0, err
	}
	start := time.Now()
	success := false
	defer func() { n.ctl.Release(success, time.Since(start)) }()

	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	if !time.Now().Before(n.leaseUntil) {
		n.mu.Unlock()
		if !n.Campaign() {
			return 0, ErrNoMajority
		}
		n.mu.Lock()
		if !n.isLeader {
			n.mu.Unlock()
			return 0, ErrNotLeader
		}
	}
	slot := n.maxSlotLocked() + 1
	ballot := n.ballot
	n.mu.Unlock()

	e := Entry{Slot: slot, Ballot: ballot, Value: value}
	if !n.phase2(e) {
		return 0, ErrNoMajority
	}
	success = true
	return slot, nil
}

// phase2 runs Accept/Accepted against a majority, then commits and
// broadcasts the decision.
func (n *Node) phase2(e Entry) bool {
	self := n.HandleAccept(AcceptRequest{Entry: e})
	if !self.OK {
		return false
	}
	acks := 1
	need := n.majority()
	for _, peer := range n.net.peers(n.id) {
		if acks >= need {
			break
		}
		p, ok := n.net.route(n.id, peer)
		if !ok {
			continue
		}
		if resp := p.HandleAccept(AcceptRequest{Entry: e}); resp.OK {
			acks++
		}
	}
	if acks < need {
		return false
	}
	e.Committed = true
	n.mu.Lock()
	n.commitLocked(e)
	n.mu.Unlock()
	for _, peer := range n.net.peers(n.id) {
		if p, ok := n.net.route(n.id, peer); ok {
			p.HandleCommit(CommitRequest{Entry: e})
		}
	}
	return true
}

// SendHeartbeats refreshes the lease and pushes the commit index to
// followers. The lease is only renewed when a majority acknowledges.
func (n *Node) SendHeartbeats() {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return
	}
	req := HeartbeatRequest{Ballot: n.ballot, CommitIndex: n.commitIdx}
	n.mu.Unlock()

	acks := 1
	for _, peer := range n.net.peers(n.id) {
		p, ok := n.net.route(n.id, peer)
		if !ok {
			continue
		}
		if resp := p.HandleHeartbeat(req); resp.OK {
			acks++
		}
	}
	if acks >= n.majority() {
		n.mu.Lock()
		if n.isLeader {
			n.leaseUntil = time.Now().Add(n.ctl.LeaseDuration())
		}
		n.mu.Unlock()
	}
}

// StartHeartbeatLoop sends heartbeats every interval until Stop.
func (n *Node) StartHeartbeatLoop(interval time.Duration) {
	n.loopOnce.Do(func() {
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-n.stopCh:
					return
				case <-t.C:
					n.SendHeartbeats()
				}
			}
		}()
	})
}

// Stop terminates background loops and waits for them; no goroutine leaks.
func (n *Node) Stop() {
	n.stopOnce.Do(func() { close(n.stopCh) })
	n.wg.Wait()
}

// ---------------------------------------------------------------------------
// internals
// ---------------------------------------------------------------------------

func (n *Node) noteBallotLocked(b Ballot) {
	if b.GreaterThan(n.maxBallot) {
		n.maxBallot = b
	}
}

func (n *Node) maxSlotLocked() uint64 {
	var max uint64
	for slot := range n.entries {
		if slot > max {
			max = slot
		}
	}
	return max
}

// commitLocked records a committed entry and advances the contiguous
// committed prefix. A committed decision always wins over a dirty local copy.
func (n *Node) commitLocked(e Entry) {
	e.Committed = true
	if existing, ok := n.entries[e.Slot]; ok && existing.Committed {
		return
	}
	n.entries[e.Slot] = e
	for {
		next, ok := n.entries[n.commitIdx+1]
		if !ok || !next.Committed {
			return
		}
		n.commitIdx++
	}
}

// syncFrom pulls the committed prefix from the leader, repairing lag and
// overwriting dirty minority entries with the committed truth.
func (n *Node) syncFrom(leaderID int) {
	leader, ok := n.net.route(n.id, leaderID)
	if !ok {
		return
	}
	n.mu.Lock()
	from := n.commitIdx + 1
	n.mu.Unlock()
	entries := leader.HandleFetchCommitted(from)
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range entries {
		n.commitLocked(e)
	}
}
