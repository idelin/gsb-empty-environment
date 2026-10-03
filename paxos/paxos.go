// Package paxos implements a Multi-Paxos majority consensus log with
// gap-free commit, leader leases (phase-1 bypass on the happy path),
// and hole repair / dirty-log overwrite after partitions.
//
// The protocol has the classic two phases:
//
//	Phase 1 (Prepare/Promise): a candidate gathers a majority of promises
//		and learns any previously accepted values so it can repair holes
//		and re-propose chosen values under its new ballot.
//	Phase 2 (Accept/Accepted): the leader replicates log entries to a
//		majority; an entry is committed once a majority has accepted it.
//
// While a leader holds a valid lease it skips Phase 1 entirely and
// commits consecutive log entries directly with Phase 2.
package paxos

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNotLeader is returned by Propose when the node is not a leader with a
// valid lease.
var ErrNotLeader = errors.New("paxos: not leader or lease expired")

// ErrNoMajority is returned when a majority cannot be reached (e.g. the
// caller is partitioned away from the cluster).
var ErrNoMajority = errors.New("paxos: cannot reach a majority")

// Entry is a single log slot. Ballot is the ballot under which the entry
// was accepted. An empty Value denotes a no-op used to fill holes.
type Entry struct {
	Ballot int
	Value  string
}

// IsNoop reports whether the entry is a hole-filling no-op.
func (e Entry) IsNoop() bool { return e.Value == "" }

func (e Entry) String() string {
	if e.IsNoop() {
		return fmt.Sprintf("noop(b%d)", e.Ballot)
	}
	return fmt.Sprintf("%s(b%d)", e.Value, e.Ballot)
}

// PrepareReq is a Phase-1a message.
type PrepareReq struct {
	Ballot int
	From   int
}

// PrepareResp is a Phase-1b promise. The promise carries the acceptor's
// full log so the candidate can repair holes and re-propose chosen values.
type PrepareResp struct {
	OK          bool
	Ballot      int
	CommitIndex int
	Log         []Entry
}

// AcceptReq is a Phase-2a message.
type AcceptReq struct {
	Ballot int
	From   int
	Index  int
	Entry  Entry
}

// AcceptResp is a Phase-2b message.
type AcceptResp struct {
	OK     bool
	Ballot int
}

// CommitReq notifies an acceptor of the leader's commit index. It carries
// the committed log prefix so lagging or partitioned followers can repair
// conflicting (dirty) uncommitted slots inside the committed range.
type CommitReq struct {
	Ballot      int
	From        int
	CommitIndex int
	Entries     []Entry
}

// Transport routes messages between nodes. *Network implements it; tests
// may substitute their own.
type Transport interface {
	Prepare(from, to int, req PrepareReq) (PrepareResp, bool)
	Accept(from, to int, req AcceptReq) (AcceptResp, bool)
	Commit(from, to int, req CommitReq) bool
}

// Node is a single Multi-Paxos replica.
type Node struct {
	opMu         sync.Mutex // serializes Campaign/Propose
	mu           sync.Mutex
	id           int
	size         int
	net          Transport
	ballot       int // highest ballot promised/seen
	log          []Entry
	commitIndex  int // [0, commitIndex) is committed
	leader       bool
	leaderBallot int
	leaseUntil   time.Time
	lease        time.Duration
	now          func() time.Time
	// matched tracks, for the leader only, which peers accepted each slot.
	matched map[int]map[int]bool
}

// NewNode creates a node. lease is the leader lease duration; pass a large
// value in tests to keep the lease valid deterministically.
func NewNode(id, size int, lease time.Duration) *Node {
	return &Node{
		id:    id,
		size:  size,
		lease: lease,
		now:   time.Now,
	}
}

// SetClock overrides the time source (used by tests).
func (n *Node) SetClock(now func() time.Time) {
	n.mu.Lock()
	n.now = now
	n.mu.Unlock()
}

func (n *Node) attach(net Transport) { n.net = net }

// ID returns the node id.
func (n *Node) ID() int { return n.id }

// Ballot returns the highest ballot the node has promised.
func (n *Node) Ballot() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ballot
}

// IsLeader reports whether the node currently believes it is leader.
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.leader
}

// CommitIndex returns the commit index: slots [0, CommitIndex) are committed.
func (n *Node) CommitIndex() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commitIndex
}

// Log returns a copy of the full (committed + uncommitted) log.
func (n *Node) Log() []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Entry(nil), n.log...)
}

// Committed returns a copy of the committed prefix of the log.
func (n *Node) Committed() []Entry {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Entry(nil), n.log[:n.commitIndex]...)
}

// stepDownLocked clears leadership if a higher ballot was observed.
func (n *Node) stepDownLocked(ballot int) {
	if n.leader && ballot > n.leaderBallot {
		n.leader = false
	}
}

func (n *Node) handlePrepare(req PrepareReq) PrepareResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Ballot < n.ballot {
		return PrepareResp{OK: false, Ballot: n.ballot}
	}
	n.ballot = req.Ballot
	n.stepDownLocked(req.Ballot)
	return PrepareResp{
		OK:          true,
		Ballot:      n.ballot,
		CommitIndex: n.commitIndex,
		Log:         append([]Entry(nil), n.log...),
	}
}

func (n *Node) handleAccept(req AcceptReq) AcceptResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Ballot < n.ballot {
		return AcceptResp{OK: false, Ballot: n.ballot}
	}
	n.ballot = req.Ballot
	n.stepDownLocked(req.Ballot)
	// Extend with no-ops so the log never has physical holes.
	for len(n.log) < req.Index {
		n.log = append(n.log, Entry{})
	}
	switch {
	case req.Index == len(n.log):
		n.log = append(n.log, req.Entry)
	case req.Index < n.commitIndex:
		// Never rewrite committed slots; treat as idempotent retry.
	default:
		// Overwrite an uncommitted (possibly dirty) slot.
		n.log[req.Index] = req.Entry
	}
	return AcceptResp{OK: true, Ballot: n.ballot}
}

func (n *Node) handleCommit(req CommitReq) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Ballot < n.ballot {
		return false
	}
	n.ballot = req.Ballot
	n.stepDownLocked(req.Ballot)
	// Adopt the leader's committed prefix: chosen values are safe to
	// overwrite both missing and dirty uncommitted slots in this range.
	for i := 0; i < len(req.Entries); i++ {
		if i < len(n.log) {
			n.log[i] = req.Entries[i]
		} else {
			n.log = append(n.log, req.Entries[i])
		}
	}
	ci := req.CommitIndex
	if ci > len(req.Entries) {
		ci = len(req.Entries)
	}
	if ci > n.commitIndex {
		n.commitIndex = ci
	}
	return true
}

// Campaign runs Phase 1 (Prepare/Promise) and, on winning a majority,
// repairs the log: every slot above the committed prefix is re-proposed
// under the new ballot with the highest-ballot value seen in the promises
// (holes become no-ops), then committed via Phase 2. Returns true if the
// node became leader.
func (n *Node) Campaign() bool {
	n.opMu.Lock()
	defer n.opMu.Unlock()

	n.mu.Lock()
	// Ballots are unique per node: (round * size) + id.
	b := (n.ballot/n.size+1)*n.size + n.id
	n.mu.Unlock()

	promises := make([]PrepareResp, 0, n.size)
	for to := 0; to < n.size; to++ {
		var resp PrepareResp
		var reachable bool
		req := PrepareReq{Ballot: b, From: n.id}
		if to == n.id {
			resp, reachable = n.handlePrepare(req), true
		} else {
			resp, reachable = n.net.Prepare(n.id, to, req)
		}
		if reachable && resp.OK {
			promises = append(promises, resp)
		}
	}
	if len(promises) <= n.size/2 {
		return false
	}

	n.mu.Lock()
	if b < n.ballot { // a higher ballot appeared while campaigning
		n.mu.Unlock()
		return false
	}
	n.ballot = b
	n.leader = true
	n.leaderBallot = b
	n.leaseUntil = n.now().Add(n.lease)
	n.matched = make(map[int]map[int]bool)
	n.mu.Unlock()

	// Merge: find the committed base and the longest log among promises.
	base, maxLen := 0, 0
	for _, p := range promises {
		if p.CommitIndex > base {
			base = p.CommitIndex
		}
		if len(p.Log) > maxLen {
			maxLen = len(p.Log)
		}
	}

	// Repair every slot in [base, maxLen): re-propose the chosen value
	// (highest ballot wins) or a no-op for holes, under the new ballot.
	for idx := base; idx < maxLen; idx++ {
		best := Entry{Ballot: -1}
		for _, p := range promises {
			if idx < len(p.Log) && p.Log[idx].Ballot > best.Ballot {
				best = p.Log[idx]
			}
		}
		entry := Entry{Ballot: b, Value: best.Value} // empty Value => no-op hole fill
		if !n.acceptToMajority(idx, entry) {
			n.mu.Lock()
			n.leader = false
			n.mu.Unlock()
			return false
		}
	}

	n.mu.Lock()
	n.advanceCommitLocked()
	ci := n.commitIndex
	entries := append([]Entry(nil), n.log[:ci]...)
	n.mu.Unlock()
	n.broadcastCommit(b, ci, entries)
	return true
}

// Propose appends a value to the log. With a valid leader lease this skips
// Phase 1 and goes straight to Phase 2. The value is committed once a
// majority accepts it.
func (n *Node) Propose(value string) (int, error) {
	n.opMu.Lock()
	defer n.opMu.Unlock()

	n.mu.Lock()
	if !n.leader || !n.now().Before(n.leaseUntil) {
		n.mu.Unlock()
		return -1, ErrNotLeader
	}
	idx := len(n.log)
	b := n.leaderBallot
	n.mu.Unlock()

	if !n.acceptToMajority(idx, Entry{Ballot: b, Value: value}) {
		return -1, ErrNoMajority
	}

	n.mu.Lock()
	n.leaseUntil = n.now().Add(n.lease) // renew lease on majority contact
	n.advanceCommitLocked()
	ci := n.commitIndex
	entries := append([]Entry(nil), n.log[:ci]...)
	n.mu.Unlock()
	n.broadcastCommit(b, ci, entries)
	return idx, nil
}

// acceptToMajority runs Phase 2 for one slot and records acceptors.
func (n *Node) acceptToMajority(idx int, e Entry) bool {
	n.mu.Lock()
	b := n.leaderBallot
	n.mu.Unlock()

	accepted := 0
	higherBallot := -1
	for to := 0; to < n.size; to++ {
		var resp AcceptResp
		var reachable bool
		req := AcceptReq{Ballot: b, From: n.id, Index: idx, Entry: e}
		if to == n.id {
			resp, reachable = n.handleAccept(req), true
		} else {
			resp, reachable = n.net.Accept(n.id, to, req)
		}
		if !reachable {
			continue
		}
		if resp.OK {
			accepted++
			n.mu.Lock()
			if n.matched != nil {
				set := n.matched[idx]
				if set == nil {
					set = make(map[int]bool)
					n.matched[idx] = set
				}
				set[to] = true
			}
			n.mu.Unlock()
		} else if resp.Ballot > higherBallot {
			higherBallot = resp.Ballot
		}
	}
	if higherBallot > 0 {
		n.mu.Lock()
		if higherBallot > n.ballot {
			n.ballot = higherBallot
		}
		n.stepDownLocked(higherBallot)
		n.mu.Unlock()
	}
	return accepted > n.size/2
}

// advanceCommitLocked moves the commit index over slots accepted by a
// majority. Must be called with n.mu held; leader only.
func (n *Node) advanceCommitLocked() {
	for n.commitIndex < len(n.log) {
		set := n.matched[n.commitIndex]
		if len(set) <= n.size/2 {
			return
		}
		n.commitIndex++
	}
}

func (n *Node) broadcastCommit(b, ci int, entries []Entry) {
	for to := 0; to < n.size; to++ {
		if to == n.id {
			continue
		}
		n.net.Commit(n.id, to, CommitReq{Ballot: b, From: n.id, CommitIndex: ci, Entries: entries})
	}
}
