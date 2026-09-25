// Package raft implements the log-replication core of Raft: AppendEntries
// consistency checks, conflict handling, commit advancement and leader
// catch-up hints. It is driven by the single-process event simulator in
// sim.go, which controls all communication, failures and time.
package raft

import "fmt"

const (
	heartbeatInterval    = 50  // logical milliseconds between leader heartbeats
	electionTimeoutMin   = 150 // logical milliseconds
	electionTimeoutRange = 150 // timeout is min + rng(range)
)

type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Follower:
		return "follower"
	case Candidate:
		return "candidate"
	case Leader:
		return "leader"
	}
	return "?"
}

// Entry is one replicated log record. Logs are 1-indexed: index 0 is a
// sentinel with term 0 so that PrevLogIndex/PrevLogTerm checks also work at
// the head of the log.
type Entry struct {
	Term    int
	Command string
}

// AppendEntriesRequest is sent by the leader to replicate log entries. It
// always carries the index and term of the entry immediately preceding the
// new ones (PrevLogIndex/PrevLogTerm) so the follower can verify that its
// log prefix matches the leader's before accepting anything.
type AppendEntriesRequest struct {
	Term         int
	LeaderID     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []Entry
	LeaderCommit int
}

// AppendEntriesReply reports whether the entries were accepted. On rejection
// it carries a catch-up hint so the leader can skip whole conflicting
// regions instead of backing off one index at a time.
type AppendEntriesReply struct {
	Term    int
	Success bool
	// Catch-up hint, valid when Success is false. ConflictTerm is the term
	// of the conflicting entry (0 when the follower's log is simply too
	// short); ConflictIndex is the first index the leader should retry from.
	ConflictTerm  int
	ConflictIndex int
	// MatchIndex is the last index known to match the leader, on success.
	MatchIndex int
	// CommittedConflict reports a refused attempt to overwrite a committed
	// entry. It must never happen in a correct execution; it is surfaced
	// explicitly so tests can assert committed entries are immutable.
	CommittedConflict bool
}

type VoteRequest struct {
	Term         int
	CandidateID  int
	LastLogIndex int
	LastLogTerm  int
}

type VoteReply struct {
	Term    int
	Granted bool
}

// Node is a single Raft server. Persistent fields (currentTerm, votedFor,
// log, commitIndex) are mirrored to stable storage (Sim.disks) on every
// change, so a crash/restart cycle preserves them.
type Node struct {
	id  int
	sim *Sim

	// Persistent state.
	currentTerm int
	votedFor    int
	log         []Entry
	commitIndex int

	// Volatile state.
	role         Role
	lastApplied  int
	dead         bool
	timerGen     int
	heartbeatGen int

	// Volatile leader state.
	nextIndex  []int
	matchIndex []int
}

func (n *Node) lastIndex() int { return len(n.log) - 1 }
func (n *Node) lastTerm() int  { return n.log[n.lastIndex()].Term }

func (n *Node) persist() {
	n.sim.disks[n.id] = Disk{
		Term:        n.currentTerm,
		VotedFor:    n.votedFor,
		Log:         append([]Entry(nil), n.log...),
		CommitIndex: n.commitIndex,
	}
}

func (n *Node) stepDown(term int) {
	n.role = Follower
	n.currentTerm = term
	n.votedFor = -1
	n.persist()
}

// HandleAppendEntries implements the Raft log-consistency rules:
//
//  1. reject stale terms;
//  2. reject when the PrevLogIndex/PrevLogTerm prefix check fails, returning
//     a catch-up hint — a prefix mismatch never results in appending at the
//     tail;
//  3. delete the conflicting *uncommitted* suffix before appending new
//     entries, while refusing to touch committed entries;
//  4. treat already-present entries as idempotent duplicates, so repeated
//     deliveries of the same request are harmless.
func (n *Node) HandleAppendEntries(req *AppendEntriesRequest) *AppendEntriesReply {
	reply := &AppendEntriesReply{Term: n.currentTerm}
	if req.Term < n.currentTerm {
		return reply
	}
	if req.Term > n.currentTerm {
		n.stepDown(req.Term)
	} else if n.role == Candidate {
		n.role = Follower
	}
	n.resetElectionTimer()

	// Prefix check: the follower's log must contain PrevLogIndex with
	// PrevLogTerm, otherwise nothing may be appended.
	if req.PrevLogIndex > n.lastIndex() {
		// Log too short: tell the leader exactly where to resume from.
		reply.ConflictIndex = n.lastIndex() + 1
		return reply
	}
	if n.log[req.PrevLogIndex].Term != req.PrevLogTerm {
		// Term mismatch at PrevLogIndex: report the first index of the
		// conflicting term so the leader can skip the whole run.
		conflictTerm := n.log[req.PrevLogIndex].Term
		conflictIndex := req.PrevLogIndex
		for conflictIndex > 1 && n.log[conflictIndex-1].Term == conflictTerm {
			conflictIndex--
		}
		reply.ConflictTerm = conflictTerm
		reply.ConflictIndex = conflictIndex
		return reply
	}

	// Merge entries: skip identical duplicates, truncate conflicting
	// uncommitted suffixes, refuse to overwrite committed entries.
	for i, e := range req.Entries {
		idx := req.PrevLogIndex + 1 + i
		if idx <= n.lastIndex() {
			if n.log[idx].Term == e.Term {
				continue // duplicate delivery: entry already present
			}
			if idx <= n.commitIndex {
				n.sim.logf("node %d: REFUSED to overwrite committed entry at index %d", n.id, idx)
				reply.CommittedConflict = true
				return reply
			}
			n.sim.logf("node %d: truncating uncommitted suffix from index %d (term %d -> %d)",
				n.id, idx, n.log[idx].Term, e.Term)
			n.log = n.log[:idx]
		}
		n.log = append(n.log, e)
	}

	if req.LeaderCommit > n.commitIndex {
		n.commitIndex = min(req.LeaderCommit, n.lastIndex())
	}
	n.persist()
	n.apply()

	reply.Success = true
	reply.MatchIndex = req.PrevLogIndex + len(req.Entries)
	return reply
}

// HandleRequestVote grants a vote only if the candidate's log is at least as
// up-to-date as the voter's (the election restriction that guarantees leader
// completeness).
func (n *Node) HandleRequestVote(req *VoteRequest) *VoteReply {
	reply := &VoteReply{Term: n.currentTerm}
	if req.Term < n.currentTerm {
		return reply
	}
	if req.Term > n.currentTerm {
		n.stepDown(req.Term)
	}
	upToDate := req.LastLogTerm > n.lastTerm() ||
		(req.LastLogTerm == n.lastTerm() && req.LastLogIndex >= n.lastIndex())
	if upToDate && (n.votedFor == -1 || n.votedFor == req.CandidateID) {
		n.votedFor = req.CandidateID
		n.persist()
		n.resetElectionTimer()
		reply.Granted = true
	}
	reply.Term = n.currentTerm
	return reply
}

func (n *Node) resetElectionTimer() {
	n.timerGen++
	gen := n.timerGen
	due := n.sim.now + electionTimeoutMin + n.sim.rng.Intn(electionTimeoutRange)
	n.sim.schedule(due, func() {
		if n.dead || n.timerGen != gen || n.role == Leader {
			return
		}
		n.startElection()
	})
}

func (n *Node) startElection() {
	n.role = Candidate
	n.currentTerm++
	n.votedFor = n.id
	n.persist()
	n.resetElectionTimer()
	term := n.currentTerm
	votes := 1
	n.sim.logf("node %d: starting election for term %d", n.id, term)
	if votes*2 > len(n.sim.nodes) {
		n.becomeLeader()
		return
	}
	for peer := 0; peer < len(n.sim.nodes); peer++ {
		if peer == n.id {
			continue
		}
		peer := peer
		req := &VoteRequest{
			Term:         term,
			CandidateID:  n.id,
			LastLogIndex: n.lastIndex(),
			LastLogTerm:  n.lastTerm(),
		}
		n.sim.send(n.id, peer, func() {
			reply := n.sim.nodes[peer].HandleRequestVote(req)
			n.sim.send(peer, n.id, func() {
				if n.dead || n.role != Candidate || n.currentTerm != term {
					return
				}
				if reply.Term > n.currentTerm {
					n.stepDown(reply.Term)
					return
				}
				if reply.Granted {
					votes++
					if votes*2 > len(n.sim.nodes) {
						n.becomeLeader()
					}
				}
			})
		})
	}
}

func (n *Node) becomeLeader() {
	if n.role != Candidate {
		return
	}
	n.role = Leader
	n.nextIndex = make([]int, len(n.sim.nodes))
	n.matchIndex = make([]int, len(n.sim.nodes))
	for i := range n.nextIndex {
		n.nextIndex[i] = n.lastIndex() + 1
	}
	n.sim.logf("node %d: became leader for term %d (lastIndex=%d)", n.id, n.currentTerm, n.lastIndex())
	n.broadcastAppend()
	n.scheduleHeartbeat()
}

func (n *Node) scheduleHeartbeat() {
	n.heartbeatGen++
	gen := n.heartbeatGen
	n.sim.schedule(n.sim.now+heartbeatInterval, func() {
		if n.dead || n.heartbeatGen != gen || n.role != Leader {
			return
		}
		n.broadcastAppend()
		n.scheduleHeartbeat()
	})
}

func (n *Node) broadcastAppend() {
	for peer := 0; peer < len(n.sim.nodes); peer++ {
		if peer != n.id {
			n.sendAppendTo(peer)
		}
	}
}

func (n *Node) sendAppendTo(peer int) {
	next := n.nextIndex[peer]
	prev := next - 1
	entries := append([]Entry(nil), n.log[next:]...)
	req := &AppendEntriesRequest{
		Term:         n.currentTerm,
		LeaderID:     n.id,
		PrevLogIndex: prev,
		PrevLogTerm:  n.log[prev].Term,
		Entries:      entries,
		LeaderCommit: n.commitIndex,
	}
	term := n.currentTerm
	n.sim.send(n.id, peer, func() {
		reply := n.sim.nodes[peer].HandleAppendEntries(req)
		n.sim.send(peer, n.id, func() {
			n.handleAppendReply(peer, term, req, reply)
		})
	})
}

func (n *Node) handleAppendReply(peer, term int, req *AppendEntriesRequest, reply *AppendEntriesReply) {
	if n.dead || n.role != Leader || n.currentTerm != term {
		return
	}
	if reply.Term > n.currentTerm {
		n.stepDown(reply.Term)
		return
	}
	if reply.Success {
		if m := req.PrevLogIndex + len(req.Entries); m > n.matchIndex[peer] {
			n.matchIndex[peer] = m
			n.nextIndex[peer] = m + 1
		}
		n.advanceCommit()
		return
	}
	if reply.CommittedConflict {
		n.sim.Violations = append(n.sim.Violations,
			fmt.Sprintf("leader %d (term %d) tried to overwrite a committed entry on node %d", n.id, term, peer))
		return
	}
	// Catch-up hint: jump straight to the conflicting region instead of
	// decrementing nextIndex one step at a time.
	if reply.ConflictTerm > 0 {
		if li := n.lastIndexOfTerm(reply.ConflictTerm); li > 0 {
			n.nextIndex[peer] = li + 1
		} else {
			n.nextIndex[peer] = reply.ConflictIndex
		}
	} else {
		n.nextIndex[peer] = reply.ConflictIndex
	}
	if n.nextIndex[peer] < 1 {
		n.nextIndex[peer] = 1
	}
	n.sendAppendTo(peer)
}

func (n *Node) lastIndexOfTerm(term int) int {
	for i := n.lastIndex(); i >= 1; i-- {
		if n.log[i].Term == term {
			return i
		}
	}
	return 0
}

// advanceCommit commits the highest index replicated on a majority, but only
// if it belongs to the leader's current term (Raft §5.4.2); earlier-term
// entries become committed indirectly together with it.
func (n *Node) advanceCommit() {
	for idx := n.lastIndex(); idx > n.commitIndex; idx-- {
		if n.log[idx].Term != n.currentTerm {
			continue
		}
		count := 1
		for i := range n.sim.nodes {
			if i != n.id && n.matchIndex[i] >= idx {
				count++
			}
		}
		if count*2 > len(n.sim.nodes) {
			n.commitIndex = idx
			n.persist()
			n.sim.logf("node %d: committed up to index %d", n.id, idx)
			n.apply()
			n.broadcastAppend() // propagate LeaderCommit promptly
			return
		}
	}
}

// Propose appends a client command to the leader's log and starts
// replicating it. Returns false if this node is not a live leader.
func (n *Node) Propose(command string) bool {
	if n.dead || n.role != Leader {
		return false
	}
	n.log = append(n.log, Entry{Term: n.currentTerm, Command: command})
	n.persist()
	n.sim.logf("node %d: proposed %q at index %d (term %d)", n.id, command, n.lastIndex(), n.currentTerm)
	n.broadcastAppend()
	return true
}

func (n *Node) apply() {
	for n.lastApplied < n.commitIndex {
		n.lastApplied++
		n.sim.onApply(n.id, n.lastApplied, n.log[n.lastApplied])
	}
}
