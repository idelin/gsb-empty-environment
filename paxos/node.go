package paxos

import (
	"sync"
	"time"
)

// Metric names emitted by the consensus engine.
const (
	MPrepareLatency = "paxos.prepare.latency"
	MAcceptLatency  = "paxos.accept.latency"
	MProposeE2E     = "paxos.propose.e2e"
	MCampaignE2E    = "paxos.campaign.e2e"
	MRepairE2E      = "paxos.repair.e2e"

	CPrepareSent    = "prepare.sent"
	CPrepareOk      = "prepare.ok"
	CPromiseRecv    = "promise.recv"
	CAcceptSent     = "accept.sent"
	CAcceptOk       = "accept.ok"
	CCommitSent     = "commit.sent"
	CCommitRecv     = "commit.recv"
	CProposeOk      = "propose.ok"
	CProposeFail    = "propose.fail"
	CCampaignOk     = "campaign.ok"
	CCampaignFail   = "campaign.fail"
	CRepairSlots    = "repair.slots"
	CRepairNoop     = "repair.noop"
	CRepairAdopt    = "repair.adopt"
	CDirtyOverwrite = "dirtylog.overwrite"
	CStepDown       = "leader.stepdown"
	CFetchSent      = "fetch.sent"
	CFetchRecv      = "fetch.recv"

	RateLease = "lease" // leader-lease fast-path hit rate
)

// DefaultLease is the leader lease duration used when none is configured.
const DefaultLease = 500 * time.Millisecond

// Node is a single Multi-Paxos replica. All exported methods are safe for
// concurrent use; internal phases are serialized per node.
type Node struct {
	id    int
	peers []int
	net   *Network
	m     *Metrics

	leaseDur time.Duration

	mu          sync.Mutex
	promised    Ballot
	maxEpoch    uint64 // highest epoch ever seen (for ballot construction)
	slots       map[uint64]*slot
	maxSlot     uint64
	commitIndex uint64
	applied     []Entry // contiguous committed prefix

	isLeader   bool
	ballot     Ballot // leadership ballot
	leaseUntil time.Time

	propMu sync.Mutex // serializes Propose/Campaign per node
	now    func() time.Time
}

// NewNode creates a replica. peers must include id itself.
func NewNode(id int, peers []int, net *Network, m *Metrics) *Node {
	if m == nil {
		m = NewMetrics()
	}
	n := &Node{
		id:       id,
		peers:    append([]int(nil), peers...),
		net:      net,
		m:        m,
		leaseDur: DefaultLease,
		slots:    make(map[uint64]*slot),
		now:      time.Now,
	}
	net.register(n)
	return n
}

// SetLeaseDuration overrides the leader lease duration.
func (n *Node) SetLeaseDuration(d time.Duration) {
	n.mu.Lock()
	n.leaseDur = d
	n.mu.Unlock()
}

// ID returns the node id.
func (n *Node) ID() int { return n.id }

// Metrics returns the node's metrics hub.
func (n *Node) Metrics() *Metrics { return n.m }

// Status is a read-only view of node consensus state.
type Status struct {
	ID          int
	IsLeader    bool
	Ballot      Ballot
	Promised    Ballot
	CommitIndex uint64
	MaxSlot     uint64
	LeaseValid  bool
}

// Status snapshots the node's consensus state.
func (n *Node) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	return Status{
		ID:          n.id,
		IsLeader:    n.isLeader,
		Ballot:      n.ballot,
		Promised:    n.promised,
		CommitIndex: n.commitIndex,
		MaxSlot:     n.maxSlot,
		LeaseValid:  n.isLeader && n.now().Before(n.leaseUntil),
	}
}

// Applied returns the contiguous committed prefix of the log.
func (n *Node) Applied() []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Entry, len(n.applied))
	copy(out, n.applied)
	return out
}

func (n *Node) majority() int { return len(n.peers)/2 + 1 }

func (n *Node) slotAt(s uint64) *slot {
	sl, ok := n.slots[s]
	if !ok {
		sl = &slot{}
		n.slots[s] = sl
	}
	return sl
}

// ---------------- Phase 1: Prepare / Promise ----------------

// Campaign runs Phase 1 and, on success, repairs uncommitted slots so the
// log has no holes. It establishes a leadership lease.
func (n *Node) Campaign() error {
	n.propMu.Lock()
	defer n.propMu.Unlock()
	return n.campaignLocked()
}

func (n *Node) campaignLocked() error {
	start := n.now()
	n.mu.Lock()
	n.maxEpoch++
	b := Ballot{Epoch: n.maxEpoch, Node: n.id}
	n.mu.Unlock()

	n.m.Inc(CPrepareSent, int64(len(n.peers)))
	promises := make([]Promise, 0, len(n.peers))
	var quorumErr error
	for _, p := range n.peers {
		if p == n.id {
			pr, err := n.handlePrepare(b)
			if err != nil {
				n.m.RecordError(err.(*Error).Code)
				n.m.Inc(CCampaignFail, 1)
				return err
			}
			promises = append(promises, pr)
			continue
		}
		t0 := n.now()
		pr, err := n.net.Prepare(n.id, p, b)
		n.m.Observe(MPrepareLatency, n.now().Sub(t0))
		if err != nil {
			if ce, ok := err.(*Error); ok {
				n.m.RecordError(ce.Code)
			} else {
				n.m.RecordError(ErrPartition)
			}
			quorumErr = err
			continue
		}
		n.m.Inc(CPromiseRecv, 1)
		promises = append(promises, pr)
	}
	if len(promises) < n.majority() {
		n.m.Inc(CCampaignFail, 1)
		n.m.RecordError(ErrNoQuorum)
		if quorumErr == nil {
			quorumErr = errf(ErrNoQuorum, "prepare: %d/%d promises", len(promises), n.majority())
		}
		return quorumErr
	}
	n.m.Inc(CPrepareOk, int64(len(promises)))

	// Adopt committed entries learned from the quorum (committed values are
	// immutable, so merging them is always safe).
	n.mu.Lock()
	for _, pr := range promises {
		for _, e := range pr.Committed {
			n.commitLocked(e)
		}
	}
	n.mu.Unlock()

	n.mu.Lock()
	n.isLeader = true
	n.ballot = b
	n.leaseUntil = n.now().Add(n.leaseDur)
	n.mu.Unlock()
	n.m.Inc(CCampaignOk, 1)
	n.m.Observe(MCampaignE2E, n.now().Sub(start))

	// Repair: cover every slot in (commitIndex, maxSlotSeen] so the log has
	// no holes; re-propose the highest-ballot accepted value per slot, or a
	// noop where the quorum saw nothing.
	repairStart := n.now()
	var maxSeen uint64
	choice := make(map[uint64]Entry)
	best := make(map[uint64]Ballot)
	for _, pr := range promises {
		for s, e := range pr.Accepted {
			if s > maxSeen {
				maxSeen = s
			}
			if cur, ok := best[s]; !ok || cur.Less(e.Ballot) {
				best[s] = e.Ballot
				choice[s] = e
			}
		}
	}
	n.mu.Lock()
	base := n.commitIndex
	n.mu.Unlock()
	for s := base + 1; s <= maxSeen; s++ {
		e, ok := choice[s]
		if !ok {
			e = Entry{Slot: s, Noop: true}
			n.m.Inc(CRepairNoop, 1)
		} else {
			n.m.Inc(CRepairAdopt, 1)
		}
		e.Slot = s
		e.Ballot = b
		n.m.Inc(CRepairSlots, 1)
		if err := n.acceptRound(b, e); err != nil {
			return err
		}
	}
	n.m.Observe(MRepairE2E, n.now().Sub(repairStart))
	return nil
}

// handlePrepare (Phase 1b): promise not to accept lower ballots; report
// local accepted/committed state for repair. Lower ballots are rejected.
func (n *Node) handlePrepare(b Ballot) (Promise, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if b.Epoch > n.maxEpoch {
		n.maxEpoch = b.Epoch
	}
	if b.Less(n.promised) {
		n.m.RecordError(ErrBallotTooLow)
		return Promise{}, errf(ErrBallotTooLow, "prepare %s < promised %s", b, n.promised)
	}
	n.promised = b
	if n.isLeader && n.ballot.Less(b) {
		n.isLeader = false
		n.m.Inc(CStepDown, 1)
	}
	pr := Promise{
		Ballot:      n.promised,
		From:        n.id,
		CommitIndex: n.commitIndex,
		Committed:   append([]Entry(nil), n.applied...),
		Accepted:    make(map[uint64]Entry),
	}
	for s, sl := range n.slots {
		if sl.hasAccepted && !sl.committed {
			pr.Accepted[s] = sl.accepted
		}
	}
	return pr, nil
}

// ---------------- Phase 2: Accept / Accepted ----------------

// acceptRound runs one Phase-2 round for entry e under ballot b and, on
// majority, commits it and broadcasts the commit.
func (n *Node) acceptRound(b Ballot, e Entry) error {
	n.m.Inc(CAcceptSent, int64(len(n.peers)))
	accepted := 0
	for _, p := range n.peers {
		if p == n.id {
			if _, err := n.handleAccept(b, e); err == nil {
				accepted++
			}
			continue
		}
		t0 := n.now()
		promised, err := n.net.Accept(n.id, p, b, e)
		n.m.Observe(MAcceptLatency, n.now().Sub(t0))
		if err != nil {
			if ce, ok := err.(*Error); ok {
				n.m.RecordError(ce.Code)
			} else {
				n.m.RecordError(ErrPartition)
			}
			// Step down if a peer has promised a higher ballot.
			n.mu.Lock()
			if n.ballot.Less(promised) {
				n.isLeader = false
				n.m.Inc(CStepDown, 1)
			}
			n.mu.Unlock()
			continue
		}
		accepted++
	}
	if accepted < n.majority() {
		n.m.RecordError(ErrNoQuorum)
		return errf(ErrNoQuorum, "accept slot %d: %d/%d votes", e.Slot, accepted, n.majority())
	}
	n.m.Inc(CAcceptOk, int64(accepted))

	n.mu.Lock()
	n.commitLocked(e)
	ci := n.commitIndex
	n.mu.Unlock()

	// Best-effort commit broadcast; converges minority dirty logs.
	for _, p := range n.peers {
		if p == n.id {
			continue
		}
		n.m.Inc(CCommitSent, 1)
		if err := n.net.Commit(n.id, p, e, ci); err != nil {
			n.m.RecordError(ErrPartition)
		}
	}
	return nil
}

// handleAccept (Phase 2b): promise check, then record the accepted entry.
// Returns the node's promised ballot for step-down detection.
func (n *Node) handleAccept(b Ballot, e Entry) (Ballot, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if b.Epoch > n.maxEpoch {
		n.maxEpoch = b.Epoch
	}
	if b.Less(n.promised) {
		n.m.RecordError(ErrBallotTooLow)
		return n.promised, errf(ErrBallotTooLow, "accept %s < promised %s", b, n.promised)
	}
	n.promised = b
	if n.isLeader && b.Node != n.id {
		n.isLeader = false
		n.m.Inc(CStepDown, 1)
	}
	sl := n.slotAt(e.Slot)
	if sl.committed {
		if sl.accepted.equalValue(e) {
			return n.promised, nil // idempotent re-accept
		}
		n.m.RecordError(ErrStaleCommit)
		return n.promised, errf(ErrStaleCommit, "slot %d already committed", e.Slot)
	}
	sl.accepted = e
	sl.hasAccepted = true
	if e.Slot > n.maxSlot {
		n.maxSlot = e.Slot
	}
	return n.promised, nil
}

// handleCommit applies a committed entry broadcast from the leader. It
// overwrites any uncommitted (dirty) local value at the slot. If the
// broadcast reveals a gap in the local committed prefix, the follower
// fetches the missing committed entries from the sender.
func (n *Node) handleCommit(from int, e Entry, commitIndex uint64) error {
	n.mu.Lock()
	n.m.Inc(CCommitRecv, 1)
	err := n.learnCommittedLocked(e)
	gap := n.commitIndex < commitIndex
	n.mu.Unlock()
	if err != nil {
		return err
	}
	if gap && from != n.id {
		n.fetchCommitted(from)
	}
	return nil
}

// learnCommittedLocked records a committed entry, overwriting any dirty
// uncommitted value. Caller must hold n.mu.
func (n *Node) learnCommittedLocked(e Entry) error {
	sl := n.slotAt(e.Slot)
	if sl.committed {
		if !sl.accepted.equalValue(e) {
			n.m.RecordError(ErrConflictCommit)
			return errf(ErrConflictCommit, "slot %d committed twice with different values", e.Slot)
		}
		return nil
	}
	if sl.hasAccepted && !sl.accepted.equalValue(e) {
		n.m.Inc(CDirtyOverwrite, 1) // safely overwrite minority dirty log
	}
	n.commitLocked(e)
	return nil
}

// fetchCommitted pulls committed entries in [commitIndex+1, ...] from a peer
// that advertised a higher commit index.
func (n *Node) fetchCommitted(from int) {
	n.mu.Lock()
	fromSlot := n.commitIndex + 1
	n.mu.Unlock()
	n.m.Inc(CFetchSent, 1)
	entries, err := n.net.Fetch(n.id, from, fromSlot)
	if err != nil {
		n.m.RecordError(ErrPartition)
		return
	}
	n.mu.Lock()
	for _, e := range entries {
		n.learnCommittedLocked(e)
	}
	n.mu.Unlock()
}

// handleFetch serves committed entries with Slot >= fromSlot.
func (n *Node) handleFetch(fromSlot uint64) []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.m.Inc(CFetchRecv, 1)
	out := make([]Entry, 0)
	for _, e := range n.applied {
		if e.Slot >= fromSlot {
			out = append(out, e)
		}
	}
	return out
}

// commitLocked marks a slot committed and advances the contiguous commit
// prefix. Caller must hold n.mu.
func (n *Node) commitLocked(e Entry) {
	sl := n.slotAt(e.Slot)
	if sl.committed {
		return
	}
	sl.accepted = e
	sl.hasAccepted = true
	sl.committed = true
	if e.Slot > n.maxSlot {
		n.maxSlot = e.Slot
	}
	for {
		next := n.commitIndex + 1
		ns, ok := n.slots[next]
		if !ok || !ns.committed {
			break
		}
		n.commitIndex = next
		n.applied = append(n.applied, ns.accepted)
	}
}

// ---------------- Client API ----------------

// Propose appends a value to the replicated log and returns its slot.
// Under a valid leader lease it skips Phase 1 (fast path); otherwise it
// campaigns first. Consecutive proposals reuse the lease.
func (n *Node) Propose(value []byte) (uint64, error) {
	n.propMu.Lock()
	defer n.propMu.Unlock()
	start := n.now()

	n.mu.Lock()
	leaseOK := n.isLeader && n.now().Before(n.leaseUntil)
	n.mu.Unlock()

	if !leaseOK {
		n.m.Miss(RateLease)
		if err := n.campaignLocked(); err != nil {
			n.m.Inc(CProposeFail, 1)
			return 0, err
		}
	} else {
		n.m.Hit(RateLease)
	}

	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		n.m.Inc(CProposeFail, 1)
		n.m.RecordError(ErrNotLeader)
		return 0, errf(ErrNotLeader, "node %d lost leadership", n.id)
	}
	slotNum := n.maxSlot + 1
	b := n.ballot
	n.mu.Unlock()

	e := Entry{Slot: slotNum, Value: append([]byte(nil), value...), Ballot: b}
	if err := n.acceptRound(b, e); err != nil {
		n.m.Inc(CProposeFail, 1)
		return 0, err
	}

	// Renew the lease on every successful quorum commit.
	n.mu.Lock()
	n.leaseUntil = n.now().Add(n.leaseDur)
	n.mu.Unlock()

	n.m.Inc(CProposeOk, 1)
	n.m.Observe(MProposeE2E, n.now().Sub(start))
	return slotNum, nil
}
