// Package paxos implements a Multi-Paxos majority consensus log with
// gap-free commit, Prepare/Promise + Accept/Accepted two-phase protocol,
// leader leases that allow skipping Phase 1, and hole repair.
package paxos

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// MsgType identifies the protocol message kind.
type MsgType int

const (
	MsgPrepare MsgType = iota
	MsgPromise
	MsgAccept
	MsgAccepted
	MsgCommit
)

func (t MsgType) String() string {
	switch t {
	case MsgPrepare:
		return "Prepare"
	case MsgPromise:
		return "Promise"
	case MsgAccept:
		return "Accept"
	case MsgAccepted:
		return "Accepted"
	case MsgCommit:
		return "Commit"
	}
	return "?"
}

// Entry is a single replicated log record. Term is the ballot under which
// the entry was first accepted. Noop entries fill holes so the committed
// prefix is always gap-free.
type Entry struct {
	Term  int
	Noop  bool
	Value string
}

func (e Entry) String() string {
	if e.Noop {
		return fmt.Sprintf("{T%d noop}", e.Term)
	}
	return fmt.Sprintf("{T%d %q}", e.Term, e.Value)
}

// Message is the unit of RPC between nodes.
type Message struct {
	Type MsgType
	From int
	To   int
	Term int

	// Prepare / Promise
	Index   int      // Prepare: first slot the proposer wants to learn
	Entries []Entry  // Promise: log suffix from Index onward
	Reject  bool     // Promise/Accepted: negative reply
	MaxTerm int      // negative replies: highest term the rejector has seen
	LogLen  int      // negative Accepted: rejector's log length (for catch-up)

	// Accept / Accepted
	Entry Entry

	// Commit
	CommitIndex int // number of committed entries (exclusive bound)
}

var ErrNotLeader = errors.New("paxos: not the leader (or lease expired)")

// Node is a single Multi-Paxos replica.
type Node struct {
	mu   sync.Mutex
	id   int
	size int
	net  *Network

	term         int // highest ballot this node has participated in
	promisedTerm int // highest ballot promised (Prepare) or accepted
	log          []Entry
	committed    int // committed prefix length; log[:committed] is gap-free
	knownCommit  int // latest commit index learned from the leader

	isLeader   bool
	leaseUntil time.Time
	leaseDur   time.Duration

	// leader-only state for an in-flight Prepare round
	prepareTerm int
	promises    map[int]Message

	// leader-only accept tracking: slot -> set of acceptor ids
	acceptors map[int]map[int]bool

	inbox chan Message
	done  chan struct{}
	wg    sync.WaitGroup

	commitCond *sync.Cond
}

func newNode(id, size int, net *Network, leaseDur time.Duration) *Node {
	n := &Node{
		id:        id,
		size:      size,
		net:       net,
		leaseDur:  leaseDur,
		promises:  make(map[int]Message),
		acceptors: make(map[int]map[int]bool),
		inbox:     make(chan Message, 4096),
		done:      make(chan struct{}),
	}
	n.commitCond = sync.NewCond(&n.mu)
	n.wg.Add(1)
	go n.loop()
	return n
}

func (n *Node) majority() int { return n.size/2 + 1 }

// ID returns the node identifier.
func (n *Node) ID() int { return n.id }

// Stop shuts the node down; used for goroutine-leak assertions.
func (n *Node) Stop() {
	close(n.done)
	n.wg.Wait()
}

func (n *Node) loop() {
	defer n.wg.Done()
	for {
		select {
		case m := <-n.inbox:
			n.handle(m)
		case <-n.done:
			return
		}
	}
}

func (n *Node) enqueue(m Message) {
	select {
	case n.inbox <- m:
	default:
		// Inbox full: treat as dropped packet.
	}
}

// nextTerm derives a fresh, cluster-unique ballot greater than any seen.
func (n *Node) nextTerm() int {
	base := n.term
	if n.promisedTerm > base {
		base = n.promisedTerm
	}
	return (base/64+1)*64 + n.id
}

// Campaign starts Phase 1: Prepare with a fresh ballot. On a majority of
// promises the node becomes leader, repairs holes in the learned log,
// replicates the repaired suffix via Phase 2 and takes out a lease.
func (n *Node) Campaign() {
	n.mu.Lock()
	n.term = n.nextTerm()
	n.promisedTerm = n.term
	n.isLeader = false
	n.prepareTerm = n.term
	n.promises = make(map[int]Message)
	term := n.term
	n.mu.Unlock()

	for peer := 0; peer < n.size; peer++ {
		if peer == n.id {
			continue
		}
		n.net.Send(Message{Type: MsgPrepare, From: n.id, To: peer, Term: term, Index: 0})
	}
	// Self-promise.
	n.handle(Message{Type: MsgPromise, From: n.id, To: n.id, Term: term, Index: 0, Entries: n.LogSuffix(0)})
}

// LogSuffix returns a copy of log entries from index onward.
func (n *Node) LogSuffix(index int) []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	if index >= len(n.log) {
		return nil
	}
	out := make([]Entry, len(n.log)-index)
	copy(out, n.log[index:])
	return out
}

// Propose appends a value and runs Phase 2 directly, which is only legal
// while the leader lease is valid (Phase 1 is skipped under the lease).
func (n *Node) Propose(value string) (int, error) {
	n.mu.Lock()
	if !n.isLeader || !time.Now().Before(n.leaseUntil) {
		n.mu.Unlock()
		return -1, ErrNotLeader
	}
	index := len(n.log)
	entry := Entry{Term: n.term, Value: value}
	n.log = append(n.log, entry)
	term := n.term
	n.mu.Unlock()

	n.trackAccept(index, n.id, entry)
	for peer := 0; peer < n.size; peer++ {
		if peer == n.id {
			continue
		}
		n.net.Send(Message{Type: MsgAccept, From: n.id, To: peer, Term: term, Index: index, Entry: entry})
	}
	return index, nil
}

// IsLeader reports whether the node holds a valid leader lease.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader && time.Now().Before(n.leaseUntil)
}

// Committed returns the committed prefix length.
func (n *Node) Committed() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.committed
}

// Log returns a copy of the full local log.
func (n *Node) Log() []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Entry, len(n.log))
	copy(out, n.log)
	return out
}

// WaitCommitted blocks until at least count entries are committed or the
// timeout elapses; it reports whether the condition was met.
func (n *Node) WaitCommitted(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	n.mu.Lock()
	defer n.mu.Unlock()
	for n.committed < count {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		// sync.Cond has no timed wait; poll with short sleeps.
		n.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		n.mu.Lock()
		_ = remaining
	}
	return true
}

func (n *Node) handle(m Message) {
	switch m.Type {
	case MsgPrepare:
		n.handlePrepare(m)
	case MsgPromise:
		n.handlePromise(m)
	case MsgAccept:
		n.handleAccept(m)
	case MsgAccepted:
		n.handleAccepted(m)
	case MsgCommit:
		n.handleCommit(m)
	}
}

func (n *Node) handlePrepare(m Message) {
	n.mu.Lock()
	if m.Term > n.promisedTerm {
		n.promisedTerm = m.Term
		n.term = m.Term
		n.isLeader = false
		idx := m.Index
		if idx > len(n.log) {
			idx = len(n.log)
		}
		suffix := make([]Entry, len(n.log)-idx)
		copy(suffix, n.log[idx:])
		committed := n.committed
		n.mu.Unlock()
		n.net.Send(Message{Type: MsgPromise, From: n.id, To: m.From, Term: m.Term,
			Index: idx, Entries: suffix, CommitIndex: committed})
		return
	}
	maxTerm := n.promisedTerm
	n.mu.Unlock()
	n.net.Send(Message{Type: MsgPromise, From: n.id, To: m.From, Term: m.Term, Reject: true, MaxTerm: maxTerm})
}

func (n *Node) handlePromise(m Message) {
	n.mu.Lock()
	if m.Term != n.prepareTerm || n.isLeader {
		n.mu.Unlock()
		return
	}
	if m.Reject {
		if m.MaxTerm > n.term {
			n.term = m.MaxTerm
		}
		n.mu.Unlock()
		return
	}
	n.promises[m.From] = m
	got := len(n.promises)
	n.mu.Unlock()

	if got < n.majority() {
		return
	}
	n.becomeLeader()
}

// becomeLeader merges the logs learned from the promising majority: for
// every slot it keeps the entry accepted under the highest ballot, fills
// holes with no-ops (gap repair), then replicates the repaired suffix with
// Phase 2 so minority dirty entries are safely overwritten.
func (n *Node) becomeLeader() {
	n.mu.Lock()
	if n.isLeader {
		n.mu.Unlock()
		return
	}
	n.isLeader = true
	n.leaseUntil = time.Now().Add(n.leaseDur)
	term := n.term

	// Merge: longest learned log wins slot-by-slot by highest entry term.
	merged := make([]Entry, len(n.log))
	copy(merged, n.log)
	for _, p := range n.promises {
		for i, e := range p.Entries {
			slot := p.Index + i
			for slot >= len(merged) {
				merged = append(merged, Entry{Term: term, Noop: true}) // hole repair
			}
			if e.Term > merged[slot].Term ||
				(e.Term == merged[slot].Term && merged[slot].Noop && !e.Noop) {
				merged[slot] = e
			}
		}
	}
	n.log = merged
	from := n.committed
	n.acceptors = make(map[int]map[int]bool)
	// Anchor the repaired prefix with a no-op of the leader's own term so
	// the whole gap-free prefix becomes committable by the majority rule.
	if from < len(n.log) || len(n.log) == 0 {
		n.log = append(n.log, Entry{Term: term, Noop: true})
	}
	log := make([]Entry, len(n.log))
	copy(log, n.log)
	n.mu.Unlock()

	// Phase 2 for every repaired slot beyond the committed prefix.
	for i := from; i < len(log); i++ {
		n.trackAccept(i, n.id, log[i])
		for peer := 0; peer < n.size; peer++ {
			if peer == n.id {
				continue
			}
			n.net.Send(Message{Type: MsgAccept, From: n.id, To: peer, Term: term, Index: i, Entry: log[i]})
		}
	}
}

func (n *Node) handleAccept(m Message) {
	n.mu.Lock()
	if m.Term < n.promisedTerm {
		maxTerm := n.promisedTerm
		n.mu.Unlock()
		n.net.Send(Message{Type: MsgAccepted, From: n.id, To: m.From, Term: m.Term,
			Index: m.Index, Reject: true, MaxTerm: maxTerm, LogLen: len(n.log)})
		return
	}
	if m.Index > len(n.log) {
		// Out-of-range: the follower is missing a prefix. Reject and report
		// the log length so the leader can backfill the hole.
		logLen := len(n.log)
		n.promisedTerm = m.Term
		n.term = m.Term
		n.mu.Unlock()
		n.net.Send(Message{Type: MsgAccepted, From: n.id, To: m.From, Term: m.Term,
			Index: m.Index, Reject: true, LogLen: logLen})
		return
	}
	n.promisedTerm = m.Term
	n.term = m.Term
	if m.From != n.id {
		n.isLeader = false
	}
	if len(n.log) == m.Index {
		n.log = append(n.log, m.Entry)
	} else if m.Index >= n.committed && n.log[m.Index].Term <= m.Term {
		if n.log[m.Index] != m.Entry {
			n.log[m.Index] = m.Entry
			n.log = n.log[:m.Index+1] // conflicting suffix is discarded
		}
	}
	n.applyCommitLocked()
	n.mu.Unlock()
	n.net.Send(Message{Type: MsgAccepted, From: n.id, To: m.From, Term: m.Term, Index: m.Index, Entry: m.Entry})
}

func (n *Node) handleAccepted(m Message) {
	if m.Reject {
		n.mu.Lock()
		if m.MaxTerm > n.term {
			n.term = m.MaxTerm
			n.isLeader = false
			n.mu.Unlock()
			return
		}
		// Follower is missing a prefix: backfill from its reported length.
		if n.isLeader && m.LogLen < len(n.log) {
			term := n.term
			suffix := make([]Entry, len(n.log)-m.LogLen)
			copy(suffix, n.log[m.LogLen:])
			start := m.LogLen
			n.mu.Unlock()
			for i, e := range suffix {
				n.net.Send(Message{Type: MsgAccept, From: n.id, To: m.From, Term: term, Index: start + i, Entry: e})
			}
			return
		}
		n.mu.Unlock()
		return
	}
	n.mu.Lock()
	entry := Entry{}
	if m.Index < len(n.log) {
		entry = n.log[m.Index]
	}
	n.mu.Unlock()
	n.trackAccept(m.Index, m.From, entry)
}

// trackAccept records an acceptance and commits the gap-free prefix once a
// slot reaches a majority. Only entries of the leader's own term trigger
// direct commit; older entries commit transitively with them.
func (n *Node) trackAccept(index, from int, entry Entry) {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return
	}
	set, ok := n.acceptors[index]
	if !ok {
		set = make(map[int]bool)
		n.acceptors[index] = set
	}
	set[from] = true
	ready := len(set) >= n.majority() && entry.Term == n.term && index >= n.committed
	if ready {
		n.committed = index + 1 // log is contiguous, so the whole prefix commits
	}
	committed := n.committed
	term := n.term
	n.mu.Unlock()

	if ready {
		n.commitCond.Broadcast()
		for peer := 0; peer < n.size; peer++ {
			if peer == n.id {
				continue
			}
			n.net.Send(Message{Type: MsgCommit, From: n.id, To: peer, Term: term, CommitIndex: committed})
		}
	}
}

func (n *Node) handleCommit(m Message) {
	n.mu.Lock()
	if m.Term >= n.promisedTerm && m.CommitIndex > n.knownCommit {
		n.knownCommit = m.CommitIndex
	}
	n.applyCommitLocked()
	n.mu.Unlock()
	n.commitCond.Broadcast()
}

// applyCommitLocked advances the committed prefix as far as the known
// commit index and the local log allow. Callers must hold n.mu.
func (n *Node) applyCommitLocked() {
	if n.knownCommit > n.committed {
		limit := n.knownCommit
		if limit > len(n.log) {
			limit = len(n.log)
		}
		n.committed = limit
	}
}

// Sync re-runs Phase 2 for the uncommitted suffix toward every peer. It is
// used after a partition heals to overwrite dirty minority logs.
func (n *Node) Sync() {
	n.mu.Lock()
	if !n.isLeader {
		n.mu.Unlock()
		return
	}
	term := n.term
	log := make([]Entry, len(n.log))
	copy(log, n.log)
	committed := n.committed
	n.mu.Unlock()

	for peer := 0; peer < n.size; peer++ {
		if peer == n.id {
			continue
		}
		// Send the full log: peers may hold dirty uncommitted entries below
		// our committed index, which these higher-term Accepts overwrite.
		for i := 0; i < len(log); i++ {
			n.net.Send(Message{Type: MsgAccept, From: n.id, To: peer, Term: term, Index: i, Entry: log[i]})
		}
		n.net.Send(Message{Type: MsgCommit, From: n.id, To: peer, Term: term, CommitIndex: committed})
	}
}
