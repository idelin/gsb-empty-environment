package paxos

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// LeaseDuration is how long a leader lease stays valid after a quorum of
// heartbeat acknowledgements. While the lease holds, the leader commits
// consecutive entries with Phase 2 only (the Multi-Paxos optimization).
const LeaseDuration = 150 * time.Millisecond

var (
	// ErrNotLeader is returned by Propose on non-leader nodes.
	ErrNotLeader = errors.New("paxos: not leader")
	// ErrNoQuorum is returned when a majority cannot be reached.
	ErrNoQuorum = errors.New("paxos: no quorum")
	// ErrNotPrepared is returned when a leader has not finished the
	// catch-up (hole repair) phase yet.
	ErrNotPrepared = errors.New("paxos: leader catch-up not finished")
)

// Node is a single Multi-Paxos replica combining proposer, acceptor and
// learner roles. All mutable state is guarded by mu; the node never
// starts background goroutines, so there is nothing to leak.
type Node struct {
	mu     sync.Mutex
	propMu sync.Mutex // serializes leader-side proposal sequencing
	id     int
	net    *Cluster

	currentTerm int
	promised    Ballot // highest ballot this acceptor has promised
	log         []Slot // 1-based; index 0 is an unused sentinel
	commitIndex int    // highest contiguous committed slot

	isLeader   bool
	prepared   bool // catch-up (hole repair) completed for currentTerm
	leaseUntil time.Time

	// instrumentation counters (also guarded by mu)
	prepares int
	accepts  int
}

func newNode(id int, net *Cluster) *Node {
	return &Node{id: id, net: net, log: make([]Slot, 1)}
}

// ID returns the node id.
func (n *Node) ID() int { return n.id }

// Stats exposes the message counters for tests and diagnostics.
func (n *Node) Stats() (prepares, accepts int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.prepares, n.accepts
}

// Snapshot is a consistent copy of a node's externally visible state.
type Snapshot struct {
	ID          int
	CurrentTerm int
	Promised    Ballot
	Log         []Slot
	CommitIndex int
	IsLeader    bool
	Prepared    bool
}

// Snapshot returns a deep-enough copy of the node state for offline
// inspection; the returned slice is independent of the node.
func (n *Node) Snapshot() Snapshot {
	n.mu.Lock()
	defer n.mu.Unlock()
	cp := make([]Slot, len(n.log))
	copy(cp, n.log)
	return Snapshot{
		ID:          n.id,
		CurrentTerm: n.currentTerm,
		Promised:    n.promised,
		Log:         cp,
		CommitIndex: n.commitIndex,
		IsLeader:    n.isLeader,
		Prepared:    n.prepared,
	}
}

// ensureSlotLocked grows the log so that slot is a valid index.
// Callers must hold n.mu.
func (n *Node) ensureSlotLocked(slot int) {
	for len(n.log) <= slot {
		n.log = append(n.log, Slot{})
	}
}

// advanceCommitLocked moves commitIndex over contiguous committed slots.
// Callers must hold n.mu.
func (n *Node) advanceCommitLocked() {
	for n.commitIndex+1 < len(n.log) && n.log[n.commitIndex+1].Committed {
		n.commitIndex++
	}
}

// stepDownLocked updates the term and drops leadership when a higher
// term is observed. Callers must hold n.mu.
func (n *Node) stepDownLocked(term int) {
	if term > n.currentTerm {
		n.currentTerm = term
		n.isLeader = false
		n.prepared = false
		n.leaseUntil = time.Time{}
	}
}

// --------------------------------------------------------------------
// Acceptor handlers (message entry points)
// --------------------------------------------------------------------

// HandlePrepareAll processes a Phase-1a request covering every slot; it
// is the Multi-Paxos catch-up optimization used by new leaders.
func (n *Node) HandlePrepareAll(b Ballot) Promise {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.prepares++
	if b.Compare(n.promised) < 0 {
		return Promise{OK: false, Term: n.currentTerm}
	}
	n.promised = b
	n.stepDownLocked(b.Term)
	p := Promise{OK: true, Term: n.currentTerm, MaxSlot: len(n.log) - 1, Accepted: map[int]Slot{}}
	for s := 1; s < len(n.log); s++ {
		if n.log[s].HasAccepted {
			p.Accepted[s] = n.log[s]
		}
	}
	return p
}

// HandlePrepare processes a Phase-1a request for a single slot.
func (n *Node) HandlePrepare(slot int, b Ballot) Promise {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.prepares++
	if b.Compare(n.promised) < 0 {
		return Promise{OK: false, Term: n.currentTerm}
	}
	n.promised = b
	n.stepDownLocked(b.Term)
	pr := Promise{OK: true, Term: n.currentTerm, MaxSlot: len(n.log) - 1, Accepted: map[int]Slot{}}
	n.ensureSlotLocked(slot)
	if n.log[slot].Promised.Compare(b) < 0 {
		n.log[slot].Promised = b
	}
	if n.log[slot].HasAccepted {
		pr.Accepted[slot] = n.log[slot]
	}
	return pr
}

// HandleAccept processes a Phase-2a request.
func (n *Node) HandleAccept(slot int, b Ballot, e LogEntry) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.accepts++
	if b.Compare(n.promised) < 0 {
		return false
	}
	n.ensureSlotLocked(slot)
	if b.Compare(n.log[slot].Promised) < 0 {
		return false
	}
	n.stepDownLocked(b.Term)
	n.log[slot].Accepted = b
	n.log[slot].HasAccepted = true
	n.log[slot].Entry = e
	return true
}

// HandleCommit learns that slot is committed with entry e.
func (n *Node) HandleCommit(slot int, e LogEntry, term int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if term < n.currentTerm {
		return
	}
	n.stepDownLocked(term)
	n.ensureSlotLocked(slot)
	if n.log[slot].Committed && !n.log[slot].Entry.Equal(e) {
		// Safety violation somewhere upstream; keep the first committed
		// value and let the inspector flag the divergence.
		return
	}
	n.log[slot].Entry = e
	n.log[slot].HasAccepted = true
	n.log[slot].Accepted = Ballot{Term: e.Term, Node: n.id}
	n.log[slot].Committed = true
	n.advanceCommitLocked()
}

// CommittedEntry is a (slot, entry) pair piggybacked on heartbeats so
// lagging followers converge after partitions heal.
type CommittedEntry struct {
	Slot  int
	Entry LogEntry
}

// HandleHeartbeat processes a leader heartbeat. It acknowledges the
// lease and adopts any committed entries it is missing, which repairs
// holes left by message loss or partitions.
func (n *Node) HandleHeartbeat(term, from int, committed []CommittedEntry) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if term < n.currentTerm {
		return false
	}
	n.stepDownLocked(term)
	for _, ce := range committed {
		n.ensureSlotLocked(ce.Slot)
		s := &n.log[ce.Slot]
		if s.Committed && !s.Entry.Equal(ce.Entry) {
			continue // conflicting committed value: keep local, inspector flags it
		}
		s.Entry = ce.Entry
		s.HasAccepted = true
		s.Accepted = Ballot{Term: ce.Entry.Term, Node: n.id}
		s.Committed = true
	}
	n.advanceCommitLocked()
	return true
}

// --------------------------------------------------------------------
// Proposer / leader logic
// --------------------------------------------------------------------

// BecomeLeader runs a full Phase 1 (Prepare) for every known slot,
// repairs holes by re-proposing learned values or no-ops, and finally
// establishes a lease. It is the new-leader catch-up procedure.
func (n *Node) BecomeLeader() error {
	n.propMu.Lock()
	defer n.propMu.Unlock()

	n.mu.Lock()
	n.currentTerm++
	term := n.currentTerm
	n.isLeader = false
	n.prepared = false
	n.leaseUntil = time.Time{}
	n.mu.Unlock()

	ballot := Ballot{Term: term, Node: n.id}

	// Phase 1a/1b across all slots.
	promises := []Promise{n.HandlePrepareAll(ballot)}
	for _, p := range n.net.reachablePeers(n.id) {
		promises = append(promises, n.net.nodes[p].HandlePrepareAll(ballot))
	}
	ok := 0
	maxSlot := 0
	learned := map[int]Slot{}
	for _, pr := range promises {
		if !pr.OK {
			continue
		}
		ok++
		if pr.MaxSlot > maxSlot {
			maxSlot = pr.MaxSlot
		}
		for s, sl := range pr.Accepted {
			cur, exists := learned[s]
			if !exists || sl.Accepted.Compare(cur.Accepted) > 0 {
				learned[s] = sl
			}
		}
	}
	if ok < n.net.Quorum() {
		return ErrNoQuorum
	}

	// Phase 2: repair every slot up to maxSlot so the log has no holes.
	for s := 1; s <= maxSlot; s++ {
		n.mu.Lock()
		committed := s < len(n.log) && n.log[s].Committed
		n.mu.Unlock()
		if committed {
			continue
		}
		var entry LogEntry
		if sl, ok := learned[s]; ok {
			entry = sl.Entry // safety: re-propose the already-accepted value
		} else {
			entry = LogEntry{Term: term, Value: "noop", Prev: s - 1}
		}
		if err := n.acceptAndCommit(s, ballot, entry); err != nil {
			return err
		}
	}

	n.mu.Lock()
	n.isLeader = true
	n.prepared = true
	n.mu.Unlock()
	n.SendHeartbeats()
	return nil
}

// SendHeartbeats refreshes the leader lease and pushes committed entries
// to all reachable followers.
func (n *Node) SendHeartbeats() {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return
	}
	term := n.currentTerm
	committed := make([]CommittedEntry, 0, n.commitIndex)
	for s := 1; s <= n.commitIndex && s < len(n.log); s++ {
		if n.log[s].Committed {
			committed = append(committed, CommittedEntry{Slot: s, Entry: n.log[s].Entry})
		}
	}
	n.mu.Unlock()

	acks := 1 // self
	for _, p := range n.net.reachablePeers(n.id) {
		if n.net.nodes[p].HandleHeartbeat(term, n.id, committed) {
			acks++
		}
	}
	if acks >= n.net.Quorum() {
		n.mu.Lock()
		n.leaseUntil = time.Now().Add(LeaseDuration)
		n.mu.Unlock()
	}
}

// LeaseValid reports whether the leader lease is currently held.
func (n *Node) LeaseValid() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader && time.Now().Before(n.leaseUntil)
}

// Propose appends value to the log and commits it on a majority. With a
// valid lease it skips Phase 1 entirely; otherwise it runs Prepare for
// the new slot first. If Phase 1 learns a previously accepted value for
// the slot, that value is re-proposed instead (Paxos safety).
func (n *Node) Propose(value string) (int, error) {
	n.propMu.Lock()
	defer n.propMu.Unlock()

	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	if !n.prepared {
		n.mu.Unlock()
		return 0, ErrNotPrepared
	}
	slot := len(n.log)
	term := n.currentTerm
	leaseOK := time.Now().Before(n.leaseUntil)
	n.mu.Unlock()

	ballot := Ballot{Term: term, Node: n.id}
	entry := LogEntry{Term: term, Value: value, Prev: slot - 1}
	if !leaseOK {
		learned, err := n.prepareSlot(slot, ballot)
		if err != nil {
			return 0, err
		}
		if learned != nil {
			entry = *learned
		}
	}
	if err := n.acceptAndCommit(slot, ballot, entry); err != nil {
		return 0, err
	}
	return slot, nil
}

// prepareSlot runs Phase 1 for a single slot and returns the highest
// ballot value learned from the quorum, if any.
func (n *Node) prepareSlot(slot int, b Ballot) (*LogEntry, error) {
	promises := []Promise{n.HandlePrepare(slot, b)}
	for _, p := range n.net.reachablePeers(n.id) {
		promises = append(promises, n.net.nodes[p].HandlePrepare(slot, b))
	}
	ok := 0
	var learned *LogEntry
	var learnedBallot Ballot
	for _, pr := range promises {
		if !pr.OK {
			continue
		}
		ok++
		if sl, ok := pr.Accepted[slot]; ok && sl.HasAccepted {
			if learned == nil || sl.Accepted.Compare(learnedBallot) > 0 {
				e := sl.Entry
				learned = &e
				learnedBallot = sl.Accepted
			}
		}
	}
	if ok < n.net.Quorum() {
		return nil, ErrNoQuorum
	}
	return learned, nil
}

// acceptAndCommit runs Phase 2 for (slot, entry) and, once a majority
// has accepted, commits the slot and broadcasts the decision.
func (n *Node) acceptAndCommit(slot int, b Ballot, e LogEntry) error {
	oks := 0
	if n.HandleAccept(slot, b, e) {
		oks++
	}
	for _, p := range n.net.reachablePeers(n.id) {
		if n.net.nodes[p].HandleAccept(slot, b, e) {
			oks++
		}
	}
	if oks < n.net.Quorum() {
		return ErrNoQuorum
	}
	n.mu.Lock()
	n.ensureSlotLocked(slot)
	n.log[slot].Committed = true
	n.advanceCommitLocked()
	term := n.currentTerm
	n.mu.Unlock()
	for _, p := range n.net.reachablePeers(n.id) {
		n.net.nodes[p].HandleCommit(slot, e, term)
	}
	return nil
}

// String renders the committed prefix of the log for demos and tests.
func (n *Node) String() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := fmt.Sprintf("node%d term=%d leader=%v commitIndex=%d log=[", n.id, n.currentTerm, n.isLeader, n.commitIndex)
	for s := 1; s < len(n.log); s++ {
		mark := " "
		if n.log[s].Committed {
			mark = "*"
		}
		if s > 1 {
			out += ","
		}
		if n.log[s].HasAccepted || n.log[s].Committed {
			out += fmt.Sprintf("%s%d:%q", mark, s, n.log[s].Entry.Value)
		} else {
			out += fmt.Sprintf(" %d:_", s)
		}
	}
	return out + "]"
}
