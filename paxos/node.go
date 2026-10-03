// Package paxos implements Multi-Paxos majority consensus with a
// hole-free commit engine.
//
// Protocol summary:
//   - Phase 1 (Prepare/Promise): a candidate collects promises from a
//     majority; each promise carries the acceptor's full accepted log so
//     the new leader can repair uncommitted slots and safely overwrite
//     dirty minority entries.
//   - Phase 2 (Accept/Accepted): the leader re-proposes the highest-ballot
//     value for every outstanding slot (filling holes with no-ops), then
//     serves client proposals.
//   - Leader lease: while the lease is valid the leader bypasses Phase 1
//     and commits consecutive log entries with a single Phase 2 round.
//
// Concurrency design: there is no coarse global mutex. Ballots, commit
// index, and the apply watermark are atomics; the log is a CAS-based
// growable array of immutable slots; inbound proposals ride a lock-free
// MPMC queue drained by a single dispatcher goroutine per node, which
// serializes slot assignment and guarantees hole-free commits.
package paxos

import (
	"bytes"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"multipaxos/queue"
)

const proposalQueueCap = 1024

type proposal struct {
	value []byte
	res   chan error
}

// Node is a single Multi-Paxos replica (proposer + acceptor + learner).
type Node struct {
	id   int
	size int
	t    *Transport

	log *logStore

	promised atomic.Uint64 // highest ballot promised to any proposer
	term     atomic.Uint64 // highest term observed

	ballot  atomic.Uint64 // my leadership ballot (0 when not leader)
	leader  atomic.Bool
	nextIdx atomic.Uint64 // last slot assigned by me as leader

	leaseUntil atomic.Int64 // unix nano
	leaseDur   time.Duration

	commitIndex atomic.Uint64
	applied     atomic.Uint64
	applyLock   atomic.Bool // CAS spinlock guarding the apply critical section

	queue  *queue.MPMC[proposal]
	notify chan struct{}
	done   chan struct{}
	closed atomic.Bool
	once   sync.Once
	wg     sync.WaitGroup

	applyFn func(idx uint64, e Entry)
}

// NewNode creates and starts a node. applyFn is invoked in log order for
// every committed entry (including no-ops) exactly once per node.
func NewNode(id, size int, t *Transport, applyFn func(idx uint64, e Entry), leaseDur time.Duration) *Node {
	n := &Node{
		id:       id,
		size:     size,
		t:        t,
		log:      newLogStore(),
		leaseDur: leaseDur,
		queue:    queue.New[proposal](proposalQueueCap),
		notify:   make(chan struct{}, 1),
		done:     make(chan struct{}),
		applyFn:  applyFn,
	}
	t.attach(id, n)
	n.wg.Add(1)
	go n.dispatch()
	return n
}

// Close stops the node's dispatcher and waits for it to exit. Pending
// proposals are failed with ErrClosed.
func (n *Node) Close() {
	n.once.Do(func() {
		n.closed.Store(true)
		close(n.done)
		n.wg.Wait()
	})
}

// ID returns the node id.
func (n *Node) ID() int { return n.id }

// IsLeader reports whether this node currently believes it is the leader.
func (n *Node) IsLeader() bool { return n.leader.Load() }

// CommitIndex returns the highest committed log index.
func (n *Node) CommitIndex() uint64 { return n.commitIndex.Load() }

// Applied returns the highest applied log index.
func (n *Node) Applied() uint64 { return n.applied.Load() }

// LogEntry returns the accepted entry at a 1-based index.
func (n *Node) LogEntry(idx uint64) (Entry, bool) {
	st := n.log.get(idx)
	if st == nil {
		return Entry{}, false
	}
	return st.entry, true
}

// Propose submits a value for consensus and blocks until it is committed
// or an error occurs (ErrNotLeader, ErrNoQuorum, ErrBackpressure,
// ErrClosed).
func (n *Node) Propose(v []byte) error {
	if n.closed.Load() {
		return ErrClosed
	}
	p := proposal{value: v, res: make(chan error, 1)}
	if err := n.queue.Enqueue(p); err != nil {
		return ErrBackpressure
	}
	select {
	case n.notify <- struct{}{}:
	default:
	}
	select {
	case err := <-p.res:
		return err
	case <-n.done:
		return ErrClosed
	}
}

// dispatch is the single per-node goroutine that serializes proposals,
// giving each one a consecutive log slot. This is what makes the commit
// engine hole-free without any global lock.
func (n *Node) dispatch() {
	defer n.wg.Done()
	for {
		select {
		case <-n.done:
			for {
				p, err := n.queue.Dequeue()
				if err != nil {
					return
				}
				p.res <- ErrClosed
			}
		case <-n.notify:
		}
		for {
			p, err := n.queue.Dequeue()
			if err != nil {
				break
			}
			n.handleProposal(p)
		}
	}
}

func (n *Node) handleProposal(p proposal) {
	if !n.ensureLeader() {
		p.res <- ErrNotLeader
		return
	}
	idx := n.nextIdx.Add(1)
	if n.acceptRound(idx, Entry{Value: p.value}) {
		p.res <- nil
	} else {
		p.res <- ErrNoQuorum
	}
}

// ensureLeader takes the lease fast path when possible, otherwise runs a
// full Phase 1 campaign.
func (n *Node) ensureLeader() bool {
	if n.leader.Load() && n.leaseValid() {
		return true
	}
	return n.Campaign()
}

func (n *Node) leaseValid() bool {
	return time.Now().UnixNano() < n.leaseUntil.Load()
}

func makeBallot(term uint64, id int) uint64 {
	return term<<16 | uint64(id+1)
}

// Campaign executes Phase 1 (Prepare/Promise) against a majority and, on
// success, repairs all outstanding slots before granting the lease.
func (n *Node) Campaign() bool {
	term := n.term.Add(1)
	ballot := makeBallot(term, n.id)
	msg := PrepareMsg{From: n.id, Ballot: ballot}

	promises := make([]PromiseMsg, 0, n.size)
	self := n.handlePrepare(msg)
	if self.OK {
		promises = append(promises, self)
	}
	votes := len(promises)
	for peer := 0; peer < n.size; peer++ {
		if peer == n.id {
			continue
		}
		pm, ok := n.t.Prepare(n.id, peer, msg)
		if !ok {
			continue // link down (partition)
		}
		n.observeBallot(pm.Ballot)
		if pm.OK {
			promises = append(promises, pm)
			votes++
		}
	}
	if votes <= n.size/2 {
		return false
	}
	n.ballot.Store(ballot)
	n.leader.Store(true)
	if !n.repair(promises) {
		n.stepDown()
		return false
	}
	n.leaseUntil.Store(time.Now().Add(n.leaseDur).UnixNano())
	return true
}

// repair implements the Phase 1 value-selection rule: for every slot up
// to the highest accepted index seen in the promises, re-propose the
// highest-ballot accepted value, or a no-op if the slot is empty
// everywhere (hole filling). This both completes uncommitted slots and
// guarantees the committed prefix is contiguous.
func (n *Node) repair(promises []PromiseMsg) bool {
	var maxIdx uint64
	for _, pm := range promises {
		if pm.MaxIndex > maxIdx {
			maxIdx = pm.MaxIndex
		}
	}
	for idx := uint64(1); idx <= maxIdx; idx++ {
		var best *AcceptedSlot
		for _, pm := range promises {
			if s, ok := pm.Accepted[idx]; ok && (best == nil || s.Ballot > best.Ballot) {
				cp := s
				best = &cp
			}
		}
		var e Entry
		if best != nil {
			e = best.Entry
		} else {
			e = Entry{Noop: true}
		}
		if !n.acceptRound(idx, e) {
			return false
		}
	}
	n.nextIdx.Store(maxIdx)
	return true
}

// acceptRound runs Phase 2 (Accept/Accepted) for one slot and, on
// majority, advances the commit index and broadcasts it.
func (n *Node) acceptRound(idx uint64, e Entry) bool {
	ballot := n.ballot.Load()
	e.Ballot = ballot
	msg := AcceptMsg{From: n.id, Ballot: ballot, Index: idx, Entry: e}

	votes := 0
	if n.handleAccept(msg).OK {
		votes++
	}
	for peer := 0; peer < n.size; peer++ {
		if peer == n.id {
			continue
		}
		am, ok := n.t.Accept(n.id, peer, msg)
		if !ok {
			continue
		}
		n.observeBallot(am.Ballot)
		if am.OK {
			votes++
		}
	}
	if votes <= n.size/2 {
		n.stepDown()
		return false
	}
	for {
		c := n.commitIndex.Load()
		if idx <= c || n.commitIndex.CompareAndSwap(c, idx) {
			break
		}
	}
	n.applyUpTo(idx)
	n.broadcastCommit()
	return true
}

func (n *Node) broadcastCommit() {
	ci := n.commitIndex.Load()
	entries := make([]IndexedEntry, 0, ci)
	for i := uint64(1); i <= ci; i++ {
		if st := n.log.get(i); st != nil {
			entries = append(entries, IndexedEntry{Index: i, Entry: st.entry})
		}
	}
	msg := CommitMsg{From: n.id, Ballot: n.ballot.Load(), CommitIndex: ci, Entries: entries}
	for peer := 0; peer < n.size; peer++ {
		if peer != n.id {
			n.t.Commit(n.id, peer, msg)
		}
	}
}

func (n *Node) stepDown() {
	n.leader.Store(false)
	n.ballot.Store(0)
}

func (n *Node) observeBallot(b uint64) {
	term := b >> 16
	for {
		t := n.term.Load()
		if term <= t || n.term.CompareAndSwap(t, term) {
			return
		}
	}
}

// handlePrepare is the acceptor's Phase 1b handler.
func (n *Node) handlePrepare(m PrepareMsg) PromiseMsg {
	n.observeBallot(m.Ballot)
	for {
		p := n.promised.Load()
		if m.Ballot <= p {
			return PromiseMsg{From: n.id, OK: false, Ballot: p}
		}
		if n.promised.CompareAndSwap(p, m.Ballot) {
			break
		}
	}
	if m.From != n.id && n.ballot.Load() < m.Ballot {
		n.stepDown()
	}
	return PromiseMsg{
		From:     n.id,
		OK:       true,
		Ballot:   m.Ballot,
		MaxIndex: n.log.maxIndex(),
		Accepted: n.log.snapshot(),
	}
}

// handleAccept is the acceptor's Phase 2b handler.
func (n *Node) handleAccept(m AcceptMsg) AcceptedMsg {
	n.observeBallot(m.Ballot)
	if m.Ballot < n.promised.Load() {
		return AcceptedMsg{From: n.id, OK: false, Ballot: n.promised.Load()}
	}
	// An accept at ballot b is also an implicit promise to b.
	for {
		p := n.promised.Load()
		if m.Ballot <= p || n.promised.CompareAndSwap(p, m.Ballot) {
			break
		}
	}
	if m.From != n.id && n.ballot.Load() < m.Ballot {
		n.stepDown()
	}
	ok := n.log.accept(m.Index, &slotState{ballot: m.Ballot, entry: m.Entry})
	return AcceptedMsg{From: n.id, OK: ok, Ballot: n.promised.Load()}
}

// handleCommit installs the leader's committed prefix (overwriting dirty
// minority slots whose accept ballot is dominated) and applies it.
func (n *Node) handleCommit(m CommitMsg) {
	n.observeBallot(m.Ballot)
	if m.Ballot < n.promised.Load() {
		return // stale leader
	}
	if m.From != n.id && n.ballot.Load() < m.Ballot {
		n.stepDown()
	}
	for _, ie := range m.Entries {
		cur := n.log.get(ie.Index)
		same := cur != nil && cur.entry.Ballot == ie.Entry.Ballot &&
			cur.entry.Noop == ie.Entry.Noop && bytes.Equal(cur.entry.Value, ie.Entry.Value)
		if same {
			continue
		}
		// Overwrite only when the committed entry's ballot dominates the
		// local (possibly dirty) slot. Committed entries carry the current
		// leader's ballot, which dominates any minority dirty write.
		if cur == nil || ie.Entry.Ballot >= cur.ballot {
			n.log.accept(ie.Index, &slotState{ballot: ie.Entry.Ballot, entry: ie.Entry})
		}
	}
	for {
		c := n.commitIndex.Load()
		if m.CommitIndex <= c || n.commitIndex.CompareAndSwap(c, m.CommitIndex) {
			break
		}
	}
	n.applyUpTo(m.CommitIndex)
}

// applyUpTo applies committed entries in strict log order. The CAS
// spinlock is the only mutual exclusion in the system and covers a tiny,
// bounded critical section; the watermark itself is atomic.
func (n *Node) applyUpTo(idx uint64) {
	for !n.applyLock.CompareAndSwap(false, true) {
		runtime.Gosched()
	}
	for {
		next := n.applied.Load() + 1
		if next > idx {
			break
		}
		st := n.log.get(next)
		if st == nil {
			break // hole: catch-up will re-drive application
		}
		n.applied.Store(next)
		if n.applyFn != nil {
			n.applyFn(next, st.entry)
		}
	}
	n.applyLock.Store(false)
}
