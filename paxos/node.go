package paxos

import (
	"bytes"
	"sort"
	"sync"
	"time"
)

// DefaultLease is the default leader lease duration.
const DefaultLease = 10 * time.Second

// Node is a single Multi-Paxos replica. All exported methods are safe for
// concurrent use. The node spawns no background goroutines: leases are
// checked lazily, so a cluster can be torn down leak-free by construction.
type Node struct {
	id    int
	net   *Network
	lease time.Duration
	// apply, if non-nil, is invoked (under the node lock, in slot order)
	// for every entry as it becomes applied. It must not call back into
	// the node.
	apply func(slot uint64, value []byte)

	mu            sync.Mutex
	promised      Ballot
	log           map[uint64]Entry // accepted entries (committed or not)
	committed     map[uint64]bool
	applied       uint64 // contiguous applied prefix; no holes by construction
	maxSlot       uint64
	appliedValues [][]byte // applied values in slot order (appliedValues[i] = slot i+1)

	// leader state
	isLeader   bool
	leaderTerm uint64
	leaseUntil time.Time

	// acceptMu serializes leader-side Phase 2 (proposals and repair) so
	// slots are allocated exactly once.
	acceptMu sync.Mutex
}

// NewNode creates a node with the given id on net. lease <= 0 selects
// DefaultLease. apply may be nil.
func NewNode(id int, net *Network, lease time.Duration, apply func(slot uint64, value []byte)) *Node {
	if lease <= 0 {
		lease = DefaultLease
	}
	n := &Node{
		id:        id,
		net:       net,
		lease:     lease,
		apply:     apply,
		log:       make(map[uint64]Entry),
		committed: make(map[uint64]bool),
	}
	net.register(n)
	return n
}

// ID returns the node id.
func (n *Node) ID() int { return n.id }

// Applied returns the contiguous applied prefix length (commit index).
func (n *Node) Applied() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.applied
}

// IsLeader reports whether the node currently holds a valid leader lease.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader && time.Now().Before(n.leaseUntil)
}

// Term returns the current promised term.
func (n *Node) Term() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.promised.Term
}

// AppliedValues returns a copy of the applied values in slot order.
func (n *Node) AppliedValues() [][]byte {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([][]byte, len(n.appliedValues))
	for i, v := range n.appliedValues {
		out[i] = bytes.Clone(v)
	}
	return out
}

// Entry returns the log entry at slot, if any.
func (n *Node) Entry(slot uint64) (Entry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.log[slot]
	return e, ok
}

// Campaign runs Phase 1 (Prepare/Promise) for a new term and, if a majority
// promises, becomes leader, adopts the highest-ballot accepted value for
// every open slot, and repairs all holes up to maxSlot via Phase 2.
func (n *Node) Campaign() error {
	n.acceptMu.Lock()
	defer n.acceptMu.Unlock()

	n.mu.Lock()
	term := n.promised.Term + 1
	fromSlot := n.applied + 1
	n.mu.Unlock()

	req := PrepareRequest{Ballot: Ballot{Term: term, Node: n.id}, FromSlot: fromSlot}
	promises := []Promise{n.handlePrepare(req)} // self
	for _, pid := range n.net.peers(n.id) {
		p, err := n.net.sendPrepare(n.id, pid, req)
		if err == nil && p.Ok {
			promises = append(promises, p)
		}
	}
	if len(promises) < n.net.Majority() {
		return ErrNoQuorum
	}

	// Adopt, per slot, the accepted entry with the highest ballot.
	learned := make(map[uint64]Entry)
	for _, p := range promises {
		for _, e := range p.Accepted {
			if cur, ok := learned[e.Slot]; !ok || e.Ballot.Compare(cur.Ballot) > 0 {
				learned[e.Slot] = e
			}
		}
	}

	n.mu.Lock()
	for slot, e := range learned {
		if n.committed[slot] {
			continue
		}
		if cur, ok := n.log[slot]; !ok || e.Ballot.Compare(cur.Ballot) > 0 {
			n.log[slot] = e
		}
		if slot > n.maxSlot {
			n.maxSlot = slot
		}
	}
	n.isLeader = true
	n.leaderTerm = term
	n.leaseUntil = time.Now().Add(n.lease)
	n.mu.Unlock()

	n.repairLocked()
	return nil
}

// Propose appends value to the log and commits it. Under a valid leader
// lease this is Phase 2 only (no Prepare messages). Returns the slot.
func (n *Node) Propose(value []byte) (uint64, error) {
	n.acceptMu.Lock()
	defer n.acceptMu.Unlock()

	n.mu.Lock()
	if !n.isLeader || time.Now().After(n.leaseUntil) {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	slot := n.maxSlot + 1
	n.mu.Unlock()

	if err := n.acceptSlot(slot, value); err != nil {
		return 0, err
	}
	return slot, nil
}

// repairLocked fills every uncommitted slot in (applied, maxSlot] with its
// learned value (or a no-op) so the committed log has no holes.
// Callers must hold acceptMu.
func (n *Node) repairLocked() {
	n.mu.Lock()
	from, to := n.applied+1, n.maxSlot
	n.mu.Unlock()
	for slot := from; slot <= to; slot++ {
		n.mu.Lock()
		if n.committed[slot] {
			n.mu.Unlock()
			continue
		}
		e, ok := n.log[slot]
		n.mu.Unlock()
		value := Noop
		if ok {
			value = e.Value
		}
		if err := n.acceptSlot(slot, value); err != nil {
			return // lost leadership or quorum; a future campaign retries
		}
	}
}

// acceptSlot runs Phase 2 for one slot and commits it once a majority
// accepts. Callers must hold acceptMu.
func (n *Node) acceptSlot(slot uint64, value []byte) error {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return ErrNotLeader
	}
	ballot := Ballot{Term: n.leaderTerm, Node: n.id}
	n.mu.Unlock()

	entry := Entry{Slot: slot, Ballot: ballot, Value: value}
	if !n.handleAccept(AcceptRequest{Entry: entry}).Ok { // self
		return ErrNotLeader
	}
	acks := 1
	for _, pid := range n.net.peers(n.id) {
		resp, err := n.net.sendAccept(n.id, pid, AcceptRequest{Entry: entry})
		if err != nil {
			continue
		}
		if resp.Ok {
			acks++
		} else {
			n.stepDownIfBehind(resp.Promised)
		}
	}
	if acks < n.net.Majority() {
		return ErrNoQuorum
	}
	n.commit(entry)
	return nil
}

// commit marks the entry chosen and broadcasts a commit notice.
func (n *Node) commit(entry Entry) {
	n.mu.Lock()
	if n.committed[entry.Slot] {
		n.mu.Unlock()
		return
	}
	n.committed[entry.Slot] = true
	n.log[entry.Slot] = entry
	n.advanceLocked()
	n.mu.Unlock()

	for _, pid := range n.net.peers(n.id) {
		_ = n.net.sendCommit(n.id, pid, CommitNotice{Entry: entry})
	}
}

// advanceLocked applies the contiguous committed prefix. Caller holds mu.
func (n *Node) advanceLocked() {
	for n.committed[n.applied+1] {
		n.applied++
		e := n.log[n.applied]
		n.appliedValues = append(n.appliedValues, bytes.Clone(e.Value))
		if n.apply != nil {
			n.apply(n.applied, e.Value)
		}
	}
}

// stepDownIfBehind relinquishes leadership if a peer promises a higher term.
func (n *Node) stepDownIfBehind(promised Ballot) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if promised.Term > n.leaderTerm {
		n.isLeader = false
	}
	if promised.Compare(n.promised) > 0 {
		n.promised = promised
	}
}

// SyncFrom pulls all committed entries at slots >= applied+1 from the leader
// and commits them locally, overwriting any uncommitted (dirty) entries.
// Used to catch up a lagging follower, e.g. after a partition heals.
func (n *Node) SyncFrom(leaderID int) error {
	n.mu.Lock()
	from := n.applied + 1
	n.mu.Unlock()
	entries, err := n.net.sendSync(n.id, leaderID, from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		n.handleCommit(CommitNotice{Entry: e})
	}
	return nil
}

// --- RPC handlers (invoked via Network) ---

func (n *Node) handlePrepare(req PrepareRequest) Promise {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Ballot.Compare(n.promised) < 0 {
		return Promise{Ok: false, Promised: n.promised}
	}
	n.promised = req.Ballot
	n.isLeader = false
	var accepted []Entry
	for slot, e := range n.log {
		if slot >= req.FromSlot {
			accepted = append(accepted, e)
		}
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].Slot < accepted[j].Slot })
	return Promise{Ok: true, Promised: n.promised, Accepted: accepted}
}

func (n *Node) handleAccept(req AcceptRequest) Accepted {
	n.mu.Lock()
	defer n.mu.Unlock()
	e := req.Entry
	if e.Ballot.Compare(n.promised) < 0 {
		return Accepted{Ok: false, Promised: n.promised}
	}
	n.promised = e.Ballot
	if n.committed[e.Slot] {
		// A committed slot is never overwritten; identical re-accepts are
		// acknowledged so retries still reach a quorum.
		return Accepted{Ok: true, Promised: n.promised}
	}
	if cur, ok := n.log[e.Slot]; !ok || e.Ballot.Compare(cur.Ballot) >= 0 {
		n.log[e.Slot] = e
	}
	if e.Slot > n.maxSlot {
		n.maxSlot = e.Slot
	}
	return Accepted{Ok: true, Promised: n.promised}
}

func (n *Node) handleCommit(c CommitNotice) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e := c.Entry
	if n.committed[e.Slot] {
		return
	}
	// Committed entries replace any uncommitted (dirty) entry at the slot.
	n.committed[e.Slot] = true
	n.log[e.Slot] = e
	if e.Slot > n.maxSlot {
		n.maxSlot = e.Slot
	}
	n.advanceLocked()
}

func (n *Node) handleSync(fromSlot uint64) []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []Entry
	for slot := fromSlot; slot <= n.maxSlot; slot++ {
		if n.committed[slot] {
			out = append(out, n.log[slot])
		}
	}
	return out
}
