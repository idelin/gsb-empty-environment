package paxos

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNoQuorum is returned when a proposer cannot reach a majority.
var ErrNoQuorum = errors.New("paxos: no quorum")

// DefaultLease is the leader lease duration used when none is specified.
const DefaultLease = 250 * time.Millisecond

// Node is a single Paxos replica: acceptor + learner + proposer.
//
// Concurrency model: all acceptor/learner state is guarded by mu. RPC
// handlers only ever take the receiver's mu, and the proposer never holds mu
// (or proposeMu) while performing network calls, so lock ordering is a DAG
// and no deadlock is possible. The node spawns no background goroutines; all
// broadcast goroutines are joined with a WaitGroup before returning, so the
// implementation is goroutine-leak free.
type Node struct {
	mu        sync.Mutex
	proposeMu sync.Mutex // serializes client proposals on this node

	id    int
	size  int
	net   *Network
	lease time.Duration

	// Acceptor state.
	promised  Ballot
	accepted  map[int]Entry
	committed map[int]bool
	applied   []Entry // contiguous committed prefix; applied[i-1] is slot i

	// Lease granted to the leader whose entries we last accepted: rejects
	// other leaders' Prepares until it expires, which is what allows the
	// lease fast path. Granted on Accept (not Prepare) so that a failed
	// campaign cannot block the cluster for a whole lease period.
	leaseGrantedTo    int
	leaseGrantedUntil time.Time

	// Proposer / leader state.
	tried        Ballot    // highest ballot this node has attempted
	leaderBallot Ballot    // ballot of the current lease, if we are leader
	leaderUntil  time.Time // expiry of our own leadership lease
	nextIndex    int       // next free slot, valid while leader

	// Stats (observability for tests).
	campaigns int // number of full Phase 1 rounds run by this node
}

func newNode(id, size int, net *Network, lease time.Duration) *Node {
	return &Node{
		id:        id,
		size:      size,
		net:       net,
		lease:     lease,
		accepted:  make(map[int]Entry),
		committed: make(map[int]bool),
	}
}

func (nd *Node) majority() int { return nd.size/2 + 1 }

// --- Acceptor / learner RPC handlers ------------------------------------

func (nd *Node) handlePrepare(req PrepareRequest) PrepareResponse {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	now := time.Now()
	if req.Ballot.Less(nd.promised) {
		return PrepareResponse{OK: false, Promised: nd.promised}
	}
	// Honour an active lease granted to a different leader.
	if req.Ballot.ID != nd.leaseGrantedTo && now.Before(nd.leaseGrantedUntil) {
		return PrepareResponse{OK: false, Promised: nd.promised}
	}
	nd.promised = req.Ballot
	nd.leaderUntil = time.Time{} // someone else is taking over
	accepted := make([]Entry, 0, len(nd.accepted))
	for _, e := range nd.accepted {
		accepted = append(accepted, e)
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].Index < accepted[j].Index })
	return PrepareResponse{OK: true, Promised: nd.promised, Accepted: accepted}
}

func (nd *Node) handleAccept(req AcceptRequest) AcceptResponse {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	if req.Ballot.Less(nd.promised) {
		return AcceptResponse{OK: false, Promised: nd.promised}
	}
	nd.promised = req.Ballot
	nd.leaseGrantedTo = req.Ballot.ID
	nd.leaseGrantedUntil = time.Now().Add(nd.lease)
	for _, e := range req.Entries {
		if nd.committed[e.Index] {
			continue // a chosen value is immutable
		}
		if cur, ok := nd.accepted[e.Index]; ok && cur.Ballot.Greater(e.Ballot) {
			continue
		}
		nd.accepted[e.Index] = e
	}
	return AcceptResponse{OK: true, Promised: nd.promised}
}

func (nd *Node) handleCommit(req CommitRequest) CommitResponse {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	e := req.Entry
	if nd.committed[e.Index] {
		return CommitResponse{OK: true}
	}
	// The entry was accepted by a majority, so it is chosen: installing it
	// over an uncommitted local value is safe and repairs dirty slots.
	nd.accepted[e.Index] = e
	nd.committed[e.Index] = true
	nd.advanceLocked()
	return CommitResponse{OK: true}
}

// advanceLocked extends the contiguous committed prefix.
func (nd *Node) advanceLocked() {
	for {
		next := len(nd.applied) + 1
		if !nd.committed[next] {
			return
		}
		nd.applied = append(nd.applied, nd.accepted[next])
	}
}

// --- Proposer ------------------------------------------------------------

// Propose submits value to the replicated log and returns its slot once it
// is committed by a majority. With a valid leader lease it skips Phase 1 and
// goes straight to Accept; otherwise it runs a full campaign.
func (nd *Node) Propose(value string) (int, error) {
	nd.proposeMu.Lock()
	defer nd.proposeMu.Unlock()

	nd.mu.Lock()
	if time.Now().Before(nd.leaderUntil) {
		idx := nd.nextIndex
		nd.nextIndex++
		ballot := nd.leaderBallot
		nd.mu.Unlock()
		entry := Entry{Index: idx, Ballot: ballot, Value: value}
		if nd.acceptPhase(ballot, []Entry{entry}) {
			nd.commitPhase(ballot, []Entry{entry})
			return idx, nil
		}
		// Lease was lost (partition or a higher ballot appeared): fall
		// through to a full campaign with the same value.
	} else {
		nd.mu.Unlock()
	}
	return nd.campaign(value)
}

// campaign runs Phase 1 (Prepare/Promise) over a majority, repairs holes in
// the log, then runs Phase 2 (Accept/Accepted) for the repaired prefix plus
// the new value, and finally commits everything chosen.
func (nd *Node) campaign(value string) (int, error) {
	nd.mu.Lock()
	nd.campaigns++
	for i, e := range nd.applied {
		if !e.Noop && e.Value == value {
			nd.mu.Unlock()
			return i + 1, nil // idempotent retry: already committed
		}
	}
	ballot := Ballot{N: nd.promised.N + 1, ID: nd.id}
	if nd.tried.Greater(ballot) || nd.tried == ballot {
		ballot.N = nd.tried.N + 1
	}
	nd.tried = ballot
	nd.mu.Unlock()

	// Phase 1: promise from self, then from the reachable peers.
	self := nd.handlePrepare(PrepareRequest{Ballot: ballot})
	if !self.OK {
		return 0, ErrNoQuorum
	}
	resps := nd.net.broadcastPrepare(nd.id, PrepareRequest{Ballot: ballot})
	okResps := []PrepareResponse{self}
	maxSeen := ballot
	for _, r := range resps {
		if r.OK {
			okResps = append(okResps, r)
		} else if r.Promised.Greater(maxSeen) {
			maxSeen = r.Promised
		}
	}
	if len(okResps) < nd.majority() {
		nd.bumpTried(maxSeen)
		return 0, ErrNoQuorum
	}

	// Merge Phase 1 results: for every slot anyone has seen, adopt the
	// value accepted with the highest ballot; fill gaps with no-ops.
	chosen := make(map[int]Entry)
	maxSlot := 0
	for _, r := range okResps {
		for _, e := range r.Accepted {
			if e.Index > maxSlot {
				maxSlot = e.Index
			}
			if cur, ok := chosen[e.Index]; !ok || e.Ballot.Greater(cur.Ballot) {
				chosen[e.Index] = e
			}
		}
	}
	// Idempotency: the value may already have been chosen by an earlier
	// attempt whose result was lost (e.g. failed accept after a majority
	// had stored it). Re-commit the repaired prefix and return its slot.
	for _, e := range chosen {
		if !e.Noop && e.Value == value {
			entries := make([]Entry, 0, maxSlot)
			for i := 1; i <= maxSlot; i++ {
				if ce, ok := chosen[i]; ok {
					ce.Ballot = ballot
					entries = append(entries, ce)
				} else {
					entries = append(entries, Entry{Index: i, Ballot: ballot, Noop: true})
				}
			}
			if !nd.acceptPhase(ballot, entries) {
				return 0, ErrNoQuorum
			}
			nd.mu.Lock()
			nd.leaderBallot = ballot
			nd.leaderUntil = time.Now().Add(nd.lease)
			nd.nextIndex = maxSlot + 1
			nd.mu.Unlock()
			nd.commitPhase(ballot, entries)
			return e.Index, nil
		}
	}
	entries := make([]Entry, 0, maxSlot+1)
	for i := 1; i <= maxSlot; i++ {
		if e, ok := chosen[i]; ok {
			e.Ballot = ballot
			entries = append(entries, e)
		} else {
			entries = append(entries, Entry{Index: i, Ballot: ballot, Noop: true})
		}
	}
	newEntry := Entry{Index: maxSlot + 1, Ballot: ballot, Value: value}
	entries = append(entries, newEntry)

	// Phase 2.
	if !nd.acceptPhase(ballot, entries) {
		return 0, ErrNoQuorum
	}

	// We now hold a lease: subsequent proposals skip Phase 1.
	nd.mu.Lock()
	nd.leaderBallot = ballot
	nd.leaderUntil = time.Now().Add(nd.lease)
	nd.nextIndex = maxSlot + 2
	nd.mu.Unlock()

	nd.commitPhase(ballot, entries)
	return newEntry.Index, nil
}

func (nd *Node) bumpTried(b Ballot) {
	nd.mu.Lock()
	if b.Greater(nd.tried) {
		nd.tried = b
	}
	nd.mu.Unlock()
}

// acceptPhase runs Phase 2 for entries under ballot. It never holds nd.mu
// while talking to the network.
func (nd *Node) acceptPhase(ballot Ballot, entries []Entry) bool {
	req := AcceptRequest{Ballot: ballot, Entries: entries}
	if !nd.handleAccept(req).OK {
		return false
	}
	oks := 1
	maxSeen := ballot
	for _, r := range nd.net.broadcastAccept(nd.id, req) {
		if r.OK {
			oks++
		} else if r.Promised.Greater(maxSeen) {
			maxSeen = r.Promised
		}
	}
	if oks < nd.majority() {
		nd.bumpTried(maxSeen)
		return false
	}
	return true
}

// commitPhase marks entries chosen locally and broadcasts Commit best-effort.
func (nd *Node) commitPhase(ballot Ballot, entries []Entry) {
	for _, e := range entries {
		req := CommitRequest{Ballot: ballot, Entry: e}
		nd.handleCommit(req)
		nd.net.broadcastCommit(nd.id, req)
	}
}

// --- Observers (used by tests and the demo) ------------------------------

// Applied returns a copy of the contiguous committed log prefix.
func (nd *Node) Applied() []Entry {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	out := make([]Entry, len(nd.applied))
	copy(out, nd.applied)
	return out
}

// Campaigns reports how many full Phase 1 rounds this node has run.
func (nd *Node) Campaigns() int {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	return nd.campaigns
}

// IsLeader reports whether this node currently holds a valid lease.
func (nd *Node) IsLeader() bool {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	return time.Now().Before(nd.leaderUntil)
}

// LeaderBallot returns the ballot of the node's current lease.
func (nd *Node) LeaderBallot() Ballot {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	return nd.leaderBallot
}
