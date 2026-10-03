package paxos

import (
	"bytes"
	"errors"
	"sort"
	"sync"
	"time"

	"multipaxos/store"
)

// ErrNotLeader is returned by Propose on followers and on leaders whose
// lease has expired.
var ErrNotLeader = errors.New("paxos: not the leader (or lease expired)")

// Persister durably records accepted entries. FilePersister and
// MemPersister are provided; nodes replay it on startup.
type Persister interface {
	SaveAccepted(e store.Entry) error
	Load() ([]store.Entry, error)
	Close() error
}

// Node is a single Multi-Paxos replica. All exported methods are safe for
// concurrent use.
type Node struct {
	id            int
	peers         []int
	quorum        int
	net           *Network
	persister     Persister
	leaseDuration time.Duration

	mu            sync.Mutex
	promised      Ballot
	accepted      map[uint64]store.Entry
	commitIndex   uint64
	applied       []store.Entry          // gap-free committed prefix, slot 1..commitIndex
	pendingCommit map[uint64]store.Entry // follower: certified-committed entries awaiting contiguous apply

	isLeader   bool
	ballot     Ballot // current round this node leads / campaigns with
	leaseUntil time.Time
	nextSlot   uint64

	promises map[int]*Message        // candidate: collected promises
	acks     map[uint64]map[int]bool // leader: acceptor acks per slot
}

// NewNode builds a node, replaying any persisted accepted entries.
func NewNode(id int, peers []int, net *Network, p Persister, lease time.Duration) (*Node, error) {
	n := &Node{
		id:            id,
		peers:         append([]int(nil), peers...),
		quorum:        len(peers)/2 + 1,
		net:           net,
		persister:     p,
		leaseDuration: lease,
		accepted:      make(map[uint64]store.Entry),
		pendingCommit: make(map[uint64]store.Entry),
		promises:      make(map[int]*Message),
		acks:          make(map[uint64]map[int]bool),
		nextSlot:      1,
	}
	if p != nil {
		entries, err := p.Load()
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			n.accepted[e.Slot] = e
		}
	}
	net.Attach(n)
	return n, nil
}

// ID returns the node identifier.
func (n *Node) ID() int { return n.id }

// IsLeader reports whether the node currently holds a valid leader lease.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader && time.Now().Before(n.leaseUntil)
}

// CommitIndex returns the highest committed slot.
func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// Applied returns a copy of the gap-free committed prefix (slots 1..CommitIndex).
func (n *Node) Applied() []store.Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]store.Entry(nil), n.applied...)
}

// Entry returns the accepted entry at slot, if any.
func (n *Node) Entry(slot uint64) (store.Entry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.accepted[slot]
	return e, ok
}

// Campaign starts Phase 1: a new ballot and Prepare for all slots >= commitIndex+1.
func (n *Node) Campaign() {
	n.mu.Lock()
	n.ballot = Ballot{Term: n.promised.Term + 1, Node: n.id}
	n.isLeader = false
	n.promises = make(map[int]*Message)
	out := n.broadcastLocked(Message{
		Type:    MsgPrepare,
		Ballot:  n.ballot,
		MinSlot: n.commitIndex + 1,
	}, true)
	n.mu.Unlock()
	n.sendAll(out)
}

// Propose appends a client command. A leader with a valid lease skips
// Phase 1 entirely and goes straight to Accept for the next slot.
func (n *Node) Propose(cmd []byte) (uint64, error) {
	n.mu.Lock()
	if !n.isLeader || time.Now().After(n.leaseUntil) {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	slot := n.nextSlot
	n.nextSlot++
	e := store.Entry{
		Slot:    slot,
		Term:    n.ballot.Term,
		NodeID:  uint32(n.ballot.Node),
		Command: append([]byte(nil), cmd...),
	}
	out := n.leaderAcceptLocked(e)
	n.mu.Unlock()
	n.sendAll(out)
	return slot, nil
}

// SyncFollower pushes the leader's committed entries (and any pending
// accepts) to a peer, safely overwriting the peer's uncommitted dirty
// entries because the leader's ballot dominates.
func (n *Node) SyncFollower(peer int) error {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return ErrNotLeader
	}
	var out []Message
	var maxSlot uint64
	for s := range n.accepted {
		if s > maxSlot {
			maxSlot = s
		}
	}
	for s := uint64(1); s <= maxSlot; s++ {
		e, ok := n.accepted[s]
		if !ok {
			continue
		}
		m := Message{From: n.id, To: peer, Ballot: n.ballot, Entry: e}
		if s <= n.commitIndex {
			m.Type = MsgCommit
			m.CommitIndex = n.commitIndex
		} else {
			m.Type = MsgAccept
			m.Slot = s
		}
		out = append(out, m)
	}
	n.mu.Unlock()
	n.sendAll(out)
	return nil
}

// Receive processes one inbound protocol message.
func (n *Node) Receive(m Message) {
	n.mu.Lock()
	out := n.handleLocked(m)
	n.mu.Unlock()
	n.sendAll(out)
}

// Close releases the persister.
func (n *Node) Close() error {
	if n.persister != nil {
		return n.persister.Close()
	}
	return nil
}

// ---- internal handlers (n.mu held) ----

func (n *Node) handleLocked(m Message) []Message {
	switch m.Type {
	case MsgPrepare:
		return n.onPrepareLocked(m)
	case MsgPromise:
		return n.onPromiseLocked(m)
	case MsgAccept:
		return n.onAcceptLocked(m)
	case MsgAccepted:
		return n.onAcceptedLocked(m)
	case MsgCommit:
		return n.onCommitLocked(m)
	}
	return nil
}

func (n *Node) onPrepareLocked(m Message) []Message {
	if m.Ballot.Compare(n.promised) < 0 {
		return []Message{{
			Type: MsgPromise, From: n.id, To: m.From,
			Ballot: n.promised, OK: false,
		}}
	}
	n.promised = m.Ballot
	if n.isLeader && m.Ballot.Compare(n.ballot) > 0 {
		n.stepDownLocked()
	}
	entries := make([]store.Entry, 0)
	for s, e := range n.accepted {
		if s >= m.MinSlot {
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Slot < entries[j].Slot })
	return []Message{{
		Type: MsgPromise, From: n.id, To: m.From,
		Ballot: m.Ballot, Entries: entries, OK: true,
	}}
}

func (n *Node) onPromiseLocked(m Message) []Message {
	if n.isLeader || m.Ballot != n.ballot {
		return nil // stale or already resolved round
	}
	if !m.OK {
		if m.Ballot.Compare(n.promised) > 0 {
			n.promised = m.Ballot
		}
		return nil
	}
	n.promises[m.From] = &m
	if len(n.promises) < n.quorum {
		return nil
	}
	return n.becomeLeaderLocked()
}

func (n *Node) onAcceptLocked(m Message) []Message {
	if m.Ballot.Compare(n.promised) < 0 {
		return []Message{{
			Type: MsgAccepted, From: n.id, To: m.From,
			Ballot: n.promised, Slot: m.Slot, OK: false,
		}}
	}
	n.promised = m.Ballot
	if n.isLeader && m.Ballot.Compare(n.ballot) > 0 {
		n.stepDownLocked()
	}
	e := m.Entry
	e.Slot = m.Slot
	n.accepted[m.Slot] = e
	n.persistLocked(e)
	return []Message{{
		Type: MsgAccepted, From: n.id, To: m.From,
		Ballot: m.Ballot, Slot: m.Slot, OK: true,
	}}
}

func (n *Node) onAcceptedLocked(m Message) []Message {
	if !n.isLeader || m.Ballot != n.ballot {
		return nil
	}
	if !m.OK {
		if m.Ballot.Compare(n.ballot) > 0 {
			n.stepDownLocked()
		}
		return nil
	}
	acks := n.acks[m.Slot]
	if acks == nil {
		acks = make(map[int]bool)
		n.acks[m.Slot] = acks
	}
	acks[m.From] = true
	return n.tryAdvanceLocked()
}

func (n *Node) onCommitLocked(m Message) []Message {
	// A committed value dominates any uncommitted dirty entry at its slot:
	// repair the local accepted log and buffer the certified value. Followers
	// only ever apply values the leader certified as committed, never their
	// own (possibly dirty) accepted copies.
	if m.Entry.Slot > n.commitIndex && !m.Entry.IsZero() {
		n.pendingCommit[m.Entry.Slot] = m.Entry
		if cur, ok := n.accepted[m.Entry.Slot]; !ok || !entriesEqual(cur, m.Entry) {
			n.accepted[m.Entry.Slot] = m.Entry
			n.persistLocked(m.Entry)
		}
	}
	if m.CommitIndex > n.commitIndex {
		for n.commitIndex < m.CommitIndex {
			e, ok := n.pendingCommit[n.commitIndex+1]
			if !ok {
				break // certified entry not yet received; wait for it
			}
			n.commitIndex++
			n.applied = append(n.applied, e)
			delete(n.pendingCommit, n.commitIndex)
		}
	}
	return nil
}

// becomeLeaderLocked finishes Phase 1: it picks, for every uncommitted slot
// seen in the quorum's promises, the value accepted at the highest ballot,
// fills holes with no-ops, and re-proposes everything under the new ballot.
func (n *Node) becomeLeaderLocked() []Message {
	n.isLeader = true
	n.leaseUntil = time.Now().Add(n.leaseDuration)
	n.promised = n.ballot

	chosen := make(map[uint64]store.Entry)
	maxSlot := n.commitIndex
	for _, p := range n.promises {
		for _, e := range p.Entries {
			if e.Slot <= n.commitIndex {
				continue
			}
			cur, ok := chosen[e.Slot]
			if !ok || entryBallot(e).Compare(entryBallot(cur)) > 0 {
				chosen[e.Slot] = e
			}
			if e.Slot > maxSlot {
				maxSlot = e.Slot
			}
		}
	}
	var out []Message
	for s := n.commitIndex + 1; s <= maxSlot; s++ {
		e, ok := chosen[s]
		if !ok {
			e = store.Entry{Slot: s, Flags: FlagNoop} // hole repair
		}
		e.Term = n.ballot.Term
		e.NodeID = uint32(n.id)
		out = append(out, n.leaderAcceptLocked(e)...)
	}
	n.nextSlot = maxSlot + 1
	return out
}

// leaderAcceptLocked records the leader's own accept and broadcasts Accept.
func (n *Node) leaderAcceptLocked(e store.Entry) []Message {
	n.accepted[e.Slot] = e
	n.persistLocked(e)
	// Reset acks: votes from an earlier ballot must never count toward
	// this round's quorum.
	n.acks[e.Slot] = map[int]bool{n.id: true}
	out := n.broadcastLocked(Message{
		Type: MsgAccept, Ballot: n.ballot, Slot: e.Slot, Entry: e,
	}, false)
	return append(out, n.tryAdvanceLocked()...)
}

// tryAdvanceLocked commits the longest gap-free prefix of quorum-acked
// slots and notifies followers.
func (n *Node) tryAdvanceLocked() []Message {
	var out []Message
	for {
		next := n.commitIndex + 1
		if len(n.acks[next]) < n.quorum {
			break
		}
		e, ok := n.accepted[next]
		if !ok {
			break
		}
		n.commitIndex = next
		n.applied = append(n.applied, e)
		out = append(out, n.broadcastLocked(Message{
			Type: MsgCommit, Ballot: n.ballot, Entry: e, CommitIndex: n.commitIndex,
		}, false)...)
	}
	return out
}

func (n *Node) stepDownLocked() {
	n.isLeader = false
	n.leaseUntil = time.Time{}
}

func (n *Node) persistLocked(e store.Entry) {
	if n.persister != nil {
		_ = n.persister.SaveAccepted(e)
	}
}

func (n *Node) broadcastLocked(m Message, includeSelf bool) []Message {
	m.From = n.id
	out := make([]Message, 0, len(n.peers))
	for _, p := range n.peers {
		if p == n.id && !includeSelf {
			continue
		}
		m.To = p
		out = append(out, m)
	}
	return out
}

func (n *Node) sendAll(msgs []Message) {
	for _, m := range msgs {
		n.net.Send(m)
	}
}

func entryBallot(e store.Entry) Ballot {
	return Ballot{Term: e.Term, Node: int(e.NodeID)}
}

func entriesEqual(a, b store.Entry) bool {
	return a.Slot == b.Slot && a.Term == b.Term && a.NodeID == b.NodeID &&
		a.Flags == b.Flags && bytes.Equal(a.Command, b.Command)
}
