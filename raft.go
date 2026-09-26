// Package raftsim implements Raft log replication (AppendEntries with
// prevLogIndex/prevLogTerm consistency checks, conflict truncation of
// uncommitted suffixes, committed-entry protection, and conflict hints for
// fast follower catch-up) on top of a single-process event simulator that
// controls communication, failures, and time.
package raftsim

import (
	"fmt"
	"math/rand"
)

// Role is the Raft server role.
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

// Entry is a single log record.
type Entry struct {
	Term int
	Cmd  string
}

// MsgType enumerates the RPC kinds carried by the simulator.
type MsgType int

const (
	MsgRequestVote MsgType = iota
	MsgRequestVoteResp
	MsgAppend
	MsgAppendResp
)

// Message is the single envelope used for every RPC and its response.
type Message struct {
	From, To int
	Type     MsgType
	Term     int

	// RequestVote payload.
	LastIndex int
	LastTerm  int
	Granted   bool

	// AppendEntries payload: every append carries the index and term of the
	// entry immediately preceding the new ones (前项索引和任期).
	PrevIndex    int
	PrevTerm     int
	Entries      []Entry
	LeaderCommit int

	// AppendEntries response. On rejection the follower returns a catch-up
	// hint (追赶提示) so the leader can back up nextIndex in one hop.
	Success       bool
	MatchIndex    int
	ConflictTerm  int // term of the conflicting entry; -1 when the log is too short
	ConflictIndex int // first index the leader should probe next
}

const (
	heartbeatInterval = 3
	electionBase      = 10
	electionSpread    = 10
)

// Node is one Raft server. Term, VotedFor, Log and Applied survive crashes;
// everything else is volatile and reset on restart.
type Node struct {
	ID int
	N  int // cluster size

	// Persistent state.
	Term     int
	VotedFor int
	Log      []Entry // 1-based via termAt(); Log[i-1] is entry i
	Applied  []Entry // state machine: committed prefix, grows monotonically

	// Volatile state.
	role         Role
	commitIndex  int
	votes        map[int]bool
	electionDue  int
	heartbeatDue int

	// Volatile leader state.
	next  []int
	match []int

	now    int
	rng    *rand.Rand
	logger func(format string, args ...any)
}

// NewNode creates a follower. rng must be non-nil and is used only for
// election-timeout jitter, keeping simulations deterministic per seed.
func NewNode(id, n int, rng *rand.Rand) *Node {
	nd := &Node{ID: id, N: n, VotedFor: -1, role: Follower, rng: rng}
	nd.resetElectionTimer()
	return nd
}

func (n *Node) logf(format string, args ...any) {
	if n.logger != nil {
		n.logger(format, args...)
	}
}

// Role exposes the current role for tests and the demo.
func (n *Node) Role() Role { return n.role }

// CommitIndex exposes the volatile commit index.
func (n *Node) CommitIndex() int { return n.commitIndex }

func (n *Node) lastIndex() int { return len(n.Log) }

func (n *Node) lastTerm() int { return n.termAt(n.lastIndex()) }

// termAt returns the term of the 1-based log index, 0 for the sentinel
// position before the first entry, and -1 when the index is beyond the log.
func (n *Node) termAt(index int) int {
	switch {
	case index == 0:
		return 0
	case index > len(n.Log):
		return -1
	default:
		return n.Log[index-1].Term
	}
}

func (n *Node) resetElectionTimer() {
	n.electionDue = n.now + electionBase + n.rng.Intn(electionSpread)
}

func (n *Node) becomeFollower(term int) {
	if term > n.Term {
		n.Term = term
		n.VotedFor = -1
	}
	n.role = Follower
	n.resetElectionTimer()
}

func (n *Node) becomeLeader() []Message {
	n.role = Leader
	n.next = make([]int, n.N)
	n.match = make([]int, n.N)
	for i := range n.next {
		n.next[i] = n.lastIndex() + 1
	}
	// Anchor the new term with a no-op so entries from older terms become
	// committable by the replication-counting rule.
	n.Log = append(n.Log, Entry{Term: n.Term, Cmd: ""})
	n.match[n.ID] = n.lastIndex()
	n.heartbeatDue = n.now + heartbeatInterval
	n.logf("node %d becomes LEADER for term %d (lastIndex=%d)", n.ID, n.Term, n.lastIndex())
	return n.broadcastAppend()
}

func (n *Node) startElection() []Message {
	n.role = Candidate
	n.Term++
	n.VotedFor = n.ID
	n.votes = map[int]bool{n.ID: true}
	n.resetElectionTimer()
	n.logf("node %d starts election for term %d", n.ID, n.Term)
	var msgs []Message
	for j := 0; j < n.N; j++ {
		if j == n.ID {
			continue
		}
		msgs = append(msgs, Message{
			From: n.ID, To: j, Type: MsgRequestVote,
			Term: n.Term, LastIndex: n.lastIndex(), LastTerm: n.lastTerm(),
		})
	}
	return msgs
}

// Tick advances the node's logical clock by one unit; the simulator is the
// only caller, so all timeouts are under simulator control.
func (n *Node) Tick() []Message {
	n.now++
	if n.role == Leader {
		if n.now >= n.heartbeatDue {
			n.heartbeatDue = n.now + heartbeatInterval
			return n.broadcastAppend()
		}
		return nil
	}
	if n.now >= n.electionDue {
		return n.startElection()
	}
	return nil
}

// Step processes one inbound message and returns outbound messages.
func (n *Node) Step(m Message) []Message {
	if m.Term > n.Term {
		n.becomeFollower(m.Term)
	}
	switch m.Type {
	case MsgRequestVote:
		return []Message{n.handleRequestVote(m)}
	case MsgRequestVoteResp:
		if n.role == Candidate && m.Term == n.Term && m.Granted {
			n.votes[m.From] = true
			if len(n.votes) > n.N/2 {
				return n.becomeLeader()
			}
		}
	case MsgAppend:
		if m.Term >= n.Term && n.role != Follower {
			n.becomeFollower(m.Term)
		}
		return []Message{n.handleAppend(m)}
	case MsgAppendResp:
		if n.role == Leader && m.Term == n.Term {
			return n.handleAppendResp(m)
		}
	}
	return nil
}

// Propose appends a client command on the leader and kicks off replication.
// It returns nil when this node is not the leader.
func (n *Node) Propose(cmd string) []Message {
	if n.role != Leader {
		return nil
	}
	n.Log = append(n.Log, Entry{Term: n.Term, Cmd: cmd})
	n.match[n.ID] = n.lastIndex()
	n.logf("node %d accepts proposal %q at index %d (term %d)", n.ID, cmd, n.lastIndex(), n.Term)
	return n.broadcastAppend()
}

// Crash simulates a fail-stop: volatile state is lost, persistent state
// (Term, VotedFor, Log, Applied) survives for the later restart.
func (n *Node) Crash() {
	n.role = Follower
	n.commitIndex = 0
	n.votes = nil
	n.next, n.match = nil, nil
	n.resetElectionTimer()
}

func (n *Node) handleRequestVote(m Message) Message {
	resp := Message{From: n.ID, To: m.From, Type: MsgRequestVoteResp, Term: n.Term}
	if m.Term < n.Term {
		return resp
	}
	upToDate := m.LastTerm > n.lastTerm() ||
		(m.LastTerm == n.lastTerm() && m.LastIndex >= n.lastIndex())
	if (n.VotedFor == -1 || n.VotedFor == m.From) && upToDate {
		n.VotedFor = m.From
		n.resetElectionTimer()
		resp.Granted = true
		n.logf("node %d grants vote to %d for term %d", n.ID, m.From, m.Term)
	}
	return resp
}

// handleAppend is the AppendEntries receiver. It enforces the
// prevLogIndex/prevLogTerm consistency check, deletes only uncommitted
// conflicting suffixes, treats duplicate appends as no-ops (idempotent),
// and refuses — by panic, which the tests treat as a safety proof
// obligation — to touch any committed entry.
func (n *Node) handleAppend(m Message) Message {
	resp := Message{From: n.ID, To: m.From, Type: MsgAppendResp, Term: n.Term}
	if m.Term < n.Term {
		resp.ConflictTerm = -1
		resp.ConflictIndex = n.lastIndex() + 1
		return resp
	}
	n.resetElectionTimer()

	// Consistency check: the entry before the new ones must exist and match.
	if m.PrevIndex > n.lastIndex() {
		resp.ConflictTerm = -1
		resp.ConflictIndex = n.lastIndex() + 1
		n.logf("node %d rejects AppendEntries from %d: log too short (prevIndex=%d, lastIndex=%d), hint index=%d",
			n.ID, m.From, m.PrevIndex, n.lastIndex(), resp.ConflictIndex)
		return resp
	}
	if pt := n.termAt(m.PrevIndex); pt != m.PrevTerm {
		resp.ConflictTerm = pt
		resp.ConflictIndex = m.PrevIndex
		for resp.ConflictIndex > 1 && n.termAt(resp.ConflictIndex-1) == pt {
			resp.ConflictIndex--
		}
		n.logf("node %d rejects AppendEntries from %d: prevTerm mismatch at %d (have term %d, want %d), hint term=%d index=%d",
			n.ID, m.From, m.PrevIndex, pt, m.PrevTerm, resp.ConflictTerm, resp.ConflictIndex)
		return resp
	}

	// Merge: identical existing entries are skipped (idempotent re-append);
	// the first mismatch truncates the uncommitted suffix.
	idx := m.PrevIndex
	for _, e := range m.Entries {
		idx++
		switch existing := n.termAt(idx); {
		case existing == -1:
			n.Log = append(n.Log, e)
		case existing != e.Term:
			if idx <= n.commitIndex {
				panic(fmt.Sprintf("SAFETY: node %d asked to overwrite committed entry at index %d", n.ID, idx))
			}
			n.logf("node %d truncates uncommitted suffix from index %d (conflict term %d -> %d)",
				n.ID, idx, existing, e.Term)
			n.Log = append(n.Log[:idx-1], e)
		}
	}
	if m.LeaderCommit > n.commitIndex {
		n.setCommitIndex(min(m.LeaderCommit, n.lastIndex()))
	}
	resp.Success = true
	resp.MatchIndex = m.PrevIndex + len(m.Entries)
	return resp
}

func (n *Node) handleAppendResp(m Message) []Message {
	if m.Success {
		if m.MatchIndex > n.match[m.From] {
			n.match[m.From] = m.MatchIndex
			n.next[m.From] = m.MatchIndex + 1
		}
		if n.advanceCommit() {
			return n.broadcastAppend()
		}
		return nil
	}
	// Rejection: use the follower's catch-up hint to rewind nextIndex
	// directly instead of probing one entry at a time.
	if m.ConflictTerm > 0 {
		if last := n.lastIndexOfTerm(m.ConflictTerm); last > 0 {
			n.next[m.From] = last + 1
		} else {
			n.next[m.From] = m.ConflictIndex
		}
	} else {
		n.next[m.From] = m.ConflictIndex
	}
	if n.next[m.From] < 1 {
		n.next[m.From] = 1
	}
	n.logf("node %d backs up nextIndex[%d] to %d using hint (term=%d, index=%d)",
		n.ID, m.From, n.next[m.From], m.ConflictTerm, m.ConflictIndex)
	return []Message{n.makeAppend(m.From)}
}

// advanceCommit applies the Raft rule: only entries from the leader's own
// term are committed by counting replicas; older entries ride along.
func (n *Node) advanceCommit() bool {
	advanced := false
	for idx := n.commitIndex + 1; idx <= n.lastIndex(); idx++ {
		if n.Log[idx-1].Term != n.Term {
			continue
		}
		count := 0
		for j := 0; j < n.N; j++ {
			if n.match[j] >= idx {
				count++
			}
		}
		if count > n.N/2 {
			n.setCommitIndex(idx)
			advanced = true
		}
	}
	return advanced
}

func (n *Node) setCommitIndex(idx int) {
	if idx <= n.commitIndex {
		return
	}
	n.commitIndex = idx
	for len(n.Applied) < n.commitIndex {
		e := n.Log[len(n.Applied)]
		n.Applied = append(n.Applied, e)
		n.logf("node %d commits index %d (term %d, cmd %q)", n.ID, len(n.Applied), e.Term, e.Cmd)
	}
}

func (n *Node) lastIndexOfTerm(term int) int {
	for i := n.lastIndex(); i >= 1; i-- {
		if n.Log[i-1].Term == term {
			return i
		}
	}
	return 0
}

func (n *Node) makeAppend(to int) Message {
	prev := n.next[to] - 1
	entries := append([]Entry(nil), n.Log[prev:]...) // copy: messages must not alias the log
	return Message{
		From: n.ID, To: to, Type: MsgAppend, Term: n.Term,
		PrevIndex: prev, PrevTerm: n.termAt(prev),
		Entries: entries, LeaderCommit: n.commitIndex,
	}
}

func (n *Node) broadcastAppend() []Message {
	var msgs []Message
	for j := 0; j < n.N; j++ {
		if j != n.ID {
			msgs = append(msgs, n.makeAppend(j))
		}
	}
	return msgs
}
