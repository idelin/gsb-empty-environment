package paxos

import (
	"errors"
	"sync"
	"time"
)

// ErrNotLeader is returned by Propose when the node is not a lease-holding
// leader.
var ErrNotLeader = errors.New("paxos: not leader")

// Node is a single Multi-Paxos participant.
//
// Protocol summary:
//   - Phase 1 (Prepare/Promise): a candidate picks a ballot higher than any
//     it has promised, collects promises from a majority, and learns any
//     previously accepted values.
//   - Repair: the new leader re-proposes the highest-ballot value per open
//     slot and fills holes with no-ops, so the log has no gaps.
//   - Phase 2 (Accept/Accepted): entries are replicated to a majority and
//     committed in slot order.
//   - Leader lease: while the lease holds, the leader skips Phase 1 and
//     commits consecutive entries with Phase 2 only; followers reject
//     Prepare from others until their granted lease expires.
type Node struct {
	id       int
	peers    []int
	majority int
	leaseDur time.Duration
	store    *Store
	tr       Transport

	mu           sync.Mutex
	promised     Ballot
	log          map[uint64]Entry
	commitIndex  uint64
	applied      []Entry
	leaseGranted time.Time // follower side: lease granted to current leader

	isLeader    bool
	ballot      Ballot
	leaseExpiry time.Time // leader side: own lease
	promises    map[int]Msg
	acks        map[uint64]map[int]bool
	nextSlot    uint64

	inbox chan Msg
	done  chan struct{}
	wg    sync.WaitGroup
}

// NewNode creates a node. store may be nil for a purely in-memory node;
// otherwise the node's accepted log is recovered from it.
func NewNode(id int, peers []int, store *Store, leaseDur time.Duration, tr Transport) (*Node, error) {
	n := &Node{
		id:       id,
		peers:    peers,
		majority: len(peers)/2 + 1,
		leaseDur: leaseDur,
		store:    store,
		tr:       tr,
		log:      make(map[uint64]Entry),
		acks:     make(map[uint64]map[int]bool),
		nextSlot: 1,
		inbox:    make(chan Msg, 4096),
		done:     make(chan struct{}),
	}
	if store != nil {
		log, err := store.LoadLog()
		if err != nil {
			return nil, err
		}
		n.log = log
	}
	return n, nil
}

// Start launches the node's message loop.
func (n *Node) Start() {
	n.wg.Add(1)
	go n.run()
}

// Close stops the node and waits for its goroutine to exit.
func (n *Node) Close() {
	close(n.done)
	n.wg.Wait()
}

// ID returns the node id.
func (n *Node) ID() int { return n.id }

// Deliver enqueues an incoming message; safe for concurrent use.
func (n *Node) Deliver(m Msg) {
	select {
	case n.inbox <- m:
	case <-n.done:
	}
}

func (n *Node) run() {
	defer n.wg.Done()
	for {
		select {
		case <-n.done:
			return
		case m := <-n.inbox:
			n.handle(m)
		}
	}
}

func (n *Node) handle(m Msg) {
	switch m.Type {
	case MsgPrepare:
		n.onPrepare(m)
	case MsgPromise:
		n.onPromise(m)
	case MsgAccept:
		n.onAccept(m)
	case MsgAccepted:
		n.onAccepted(m)
	case MsgCommit:
		n.onCommit(m)
	case MsgFetch:
		n.onFetch(m)
	}
}

func (n *Node) send(to int, m Msg) {
	if to == n.id {
		return
	}
	m.From = n.id
	m.To = to
	n.tr.Send(to, m)
}

func (n *Node) broadcast(m Msg) {
	for _, p := range n.peers {
		n.send(p, m)
	}
}

// Campaign starts Phase 1 with a fresh ballot.
func (n *Node) Campaign() {
	n.mu.Lock()
	b := Ballot{N: n.promised.N + 1, ID: uint32(n.id)}
	n.promised = b
	n.ballot = b
	n.isLeader = false
	n.promises = make(map[int]Msg)
	self := Msg{From: n.id, Type: MsgPromise, Ballot: b, OK: true}
	for _, e := range n.log {
		self.Entries = append(self.Entries, e)
	}
	n.promises[n.id] = self
	n.mu.Unlock()
	n.broadcast(Msg{Type: MsgPrepare, Ballot: b})
	n.tryBecomeLeader()
}

// Propose replicates cmd under the leader lease, skipping Phase 1.
func (n *Node) Propose(cmd []byte) (uint64, error) {
	n.mu.Lock()
	if !n.isLeader || time.Now().After(n.leaseExpiry) {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	e := Entry{
		Term:  n.ballot.N,
		Node:  n.ballot.ID,
		Index: n.nextSlot,
		Cmd:   append([]byte(nil), cmd...),
	}
	n.nextSlot++
	n.mu.Unlock()
	n.acceptEntry(e)
	return e.Index, nil
}

// IsLeader reports whether the node currently holds leadership.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader
}

// CommitIndex returns the highest committed slot.
func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// Applied returns a copy of the committed entries in slot order.
func (n *Node) Applied() []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Entry, len(n.applied))
	for i, e := range n.applied {
		out[i] = CloneEntry(e)
	}
	return out
}

// LogEntry returns the accepted entry at slot i.
func (n *Node) LogEntry(i uint64) (Entry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.log[i]
	return e, ok
}

func (n *Node) onPrepare(m Msg) {
	n.mu.Lock()
	var reply Msg
	if m.Ballot.Compare(n.promised) > 0 && time.Now().After(n.leaseGranted) {
		n.promised = m.Ballot
		n.isLeader = false
		reply = Msg{Type: MsgPromise, Ballot: m.Ballot, OK: true}
		for _, e := range n.log {
			reply.Entries = append(reply.Entries, e)
		}
	} else {
		reply = Msg{Type: MsgPromise, Ballot: n.promised, OK: false}
	}
	n.mu.Unlock()
	n.send(m.From, reply)
}

func (n *Node) onPromise(m Msg) {
	n.mu.Lock()
	if m.OK && m.Ballot == n.ballot && !n.isLeader && n.promises != nil {
		n.promises[m.From] = m
	}
	n.mu.Unlock()
	n.tryBecomeLeader()
}

// tryBecomeLeader completes Phase 1 once a majority promised, then repairs
// the log: every open slot is re-proposed with the highest-ballot value seen
// (or a no-op to fill a hole), so no committed value is lost and no gap
// remains.
func (n *Node) tryBecomeLeader() {
	n.mu.Lock()
	if n.isLeader || n.promises == nil || len(n.promises) < n.majority {
		n.mu.Unlock()
		return
	}
	n.isLeader = true
	n.leaseExpiry = time.Now().Add(n.leaseDur)
	chosen := make(map[uint64]Entry)
	var max uint64
	for _, p := range n.promises {
		for _, e := range p.Entries {
			if e.Index > max {
				max = e.Index
			}
			if cur, ok := chosen[e.Index]; !ok || entryBallotLess(cur, e) {
				chosen[e.Index] = e
			}
		}
	}
	var repair []Entry
	for i := n.commitIndex + 1; i <= max; i++ {
		e, ok := chosen[i]
		if !ok {
			e = Entry{Index: i, Cmd: NoopCmd}
		}
		e.Term = n.ballot.N
		e.Node = n.ballot.ID
		repair = append(repair, e)
	}
	n.nextSlot = max + 1
	if n.nextSlot <= n.commitIndex {
		n.nextSlot = n.commitIndex + 1
	}
	n.promises = nil
	n.mu.Unlock()
	for _, e := range repair {
		n.acceptEntry(e)
	}
}

// acceptEntry persists the entry locally and broadcasts Phase 2a.
func (n *Node) acceptEntry(e Entry) {
	n.mu.Lock()
	n.log[e.Index] = e
	if n.store != nil {
		n.store.Append(e)
	}
	if n.acks[e.Index] == nil {
		n.acks[e.Index] = make(map[int]bool)
	}
	n.acks[e.Index][n.id] = true
	n.mu.Unlock()
	n.broadcast(Msg{Type: MsgAccept, Ballot: Ballot{N: e.Term, ID: e.Node}, Entries: []Entry{e}})
	n.tryCommit()
}

func (n *Node) onAccept(m Msg) {
	n.mu.Lock()
	ok := false
	if m.Ballot.Compare(n.promised) >= 0 {
		ok = true
		n.promised = m.Ballot
		n.isLeader = false
		n.leaseGranted = time.Now().Add(n.leaseDur)
		for _, e := range m.Entries {
			if cur, has := n.log[e.Index]; !has || entryBallotLess(cur, e) {
				n.log[e.Index] = CloneEntry(e)
				if n.store != nil {
					n.store.Append(e)
				}
			}
		}
	}
	reply := Msg{Type: MsgAccepted, Ballot: m.Ballot, OK: ok, Entries: m.Entries}
	if !ok {
		reply.Ballot = n.promised
	}
	n.mu.Unlock()
	n.send(m.From, reply)
}

func (n *Node) onAccepted(m Msg) {
	n.mu.Lock()
	if n.isLeader && m.Ballot == n.ballot {
		if !m.OK {
			n.isLeader = false
			n.mu.Unlock()
			return
		}
		for _, e := range m.Entries {
			if n.acks[e.Index] == nil {
				n.acks[e.Index] = make(map[int]bool)
			}
			n.acks[e.Index][m.From] = true
		}
	}
	n.mu.Unlock()
	n.tryCommit()
}

// tryCommit advances the commit index over slots acknowledged by a majority
// and broadcasts the newly committed range.
func (n *Node) tryCommit() {
	n.mu.Lock()
	var committed []Entry
	for {
		next := n.commitIndex + 1
		if len(n.acks[next]) < n.majority {
			break
		}
		e, ok := n.log[next]
		if !ok {
			break
		}
		n.commitIndex = next
		n.applied = append(n.applied, CloneEntry(e))
		delete(n.acks, next)
		committed = append(committed, e)
	}
	if len(committed) == 0 {
		n.mu.Unlock()
		return
	}
	n.leaseExpiry = time.Now().Add(n.leaseDur)
	ci := n.commitIndex
	slots := n.fingerprintsLocked()
	n.mu.Unlock()
	n.broadcast(Msg{Type: MsgCommit, CommitIndex: ci, Entries: committed, Slots: slots})
}

// fingerprintsLocked returns the ballots of all committed slots; the caller
// must hold n.mu. Committed entries never change, so this is a stable
// fingerprint of the committed log.
func (n *Node) fingerprintsLocked() []Ballot {
	slots := make([]Ballot, n.commitIndex)
	for i := uint64(1); i <= n.commitIndex; i++ {
		if e, ok := n.log[i]; ok {
			slots[i-1] = entryBallot(e)
		}
	}
	return slots
}

func (n *Node) onCommit(m Msg) {
	n.mu.Lock()
	if m.CommitIndex <= n.commitIndex {
		n.mu.Unlock()
		return
	}
	for _, e := range m.Entries {
		if e.Index > n.commitIndex && e.Index <= m.CommitIndex {
			// Committed values are authoritative: overwrite any dirty
			// minority copy.
			n.log[e.Index] = CloneEntry(e)
			if n.store != nil {
				n.store.Append(e)
			}
		}
	}
	for n.commitIndex < m.CommitIndex {
		i := n.commitIndex + 1
		e, ok := n.log[i]
		if !ok || !fingerprintMatch(m, i, e) {
			break
		}
		n.commitIndex++
		n.applied = append(n.applied, CloneEntry(e))
	}
	needFetch := n.commitIndex < m.CommitIndex
	ci := n.commitIndex
	n.mu.Unlock()
	if needFetch {
		n.send(m.From, Msg{Type: MsgFetch, CommitIndex: ci})
	}
}

// fingerprintMatch reports whether e is the committed value for slot i. A
// local entry whose ballot differs from the committed fingerprint is dirty
// (written by a deposed leader in a minority partition) and must be
// refetched rather than applied.
func fingerprintMatch(m Msg, i uint64, e Entry) bool {
	if i == 0 || i > uint64(len(m.Slots)) {
		// No fingerprint available: fall back to trusting the local copy.
		return true
	}
	return m.Slots[i-1] == entryBallot(e)
}

func (n *Node) onFetch(m Msg) {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return
	}
	var entries []Entry
	for i := m.CommitIndex + 1; i <= n.commitIndex; i++ {
		if e, ok := n.log[i]; ok {
			entries = append(entries, e)
		}
	}
	ci := n.commitIndex
	slots := n.fingerprintsLocked()
	n.mu.Unlock()
	n.send(m.From, Msg{Type: MsgCommit, CommitIndex: ci, Entries: entries, Slots: slots})
}
