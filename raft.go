// Package raft implements a single-process, discrete-time replicated log
// with view changes: monotonically increasing election terms, pre-vote,
// log consistency checks and safe truncation of conflicting uncommitted
// entries. It does not use any network stack; message delivery, failures
// and heartbeats are driven by an external test clock (see sim.go).
package raft

import "fmt"

// LogEntry is a single replicated log record. Term is the election term
// in which the entry was created by a leader.
type LogEntry struct {
	Term int
	Data string
}

// State is the role of a node.
type State int

const (
	Follower State = iota
	Candidate
	Leader
)

func (s State) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	}
	return "?"
}

// MsgType identifies the kind of a protocol message.
type MsgType int

const (
	MsgPreVote MsgType = iota
	MsgPreVoteResp
	MsgVote
	MsgVoteResp
	MsgAppend
	MsgAppendResp
)

func (t MsgType) String() string {
	switch t {
	case MsgPreVote:
		return "PreVote"
	case MsgPreVoteResp:
		return "PreVoteResp"
	case MsgVote:
		return "Vote"
	case MsgVoteResp:
		return "VoteResp"
	case MsgAppend:
		return "Append"
	case MsgAppendResp:
		return "AppendResp"
	}
	return "?"
}

// None is the absence of a node id / vote.
const None = -1

// Message is the single envelope used by the protocol. Which fields are
// meaningful depends on Type.
type Message struct {
	Type MsgType
	From int
	To   int
	Term int

	// PreVote / Vote: candidate's log tip.
	LastLogIndex int
	LastLogTerm  int

	// Append: consistency-check cursor, payload and commit pointer.
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int

	// Responses.
	Grant      bool // PreVoteResp / VoteResp
	Reject     bool // AppendResp
	MatchIndex int  // AppendResp: highest index known to match
}

// Node is a single replica. All time advances via Tick; all messages
// arrive via Step. Outgoing messages are buffered in an outbox that the
// owner (e.g. Sim) drains.
type Node struct {
	ID    int
	peers []int

	State State
	Term  int

	vote     int
	preVotes map[int]bool // non-nil while a pre-vote is in flight
	votes    map[int]bool

	// Log is 1-based; Log[0] is a sentinel so that PrevLogIndex=0 works.
	Log         []LogEntry
	CommitIndex int
	LeaderID    int

	electionElapsed  int
	electionTimeout  int
	heartbeatElapsed int
	heartbeatTimeout int

	// Leader-only replication cursors.
	match map[int]int
	next  map[int]int

	outbox []Message
}

// NewNode creates a follower. ids must contain every node id including id.
func NewNode(id int, ids []int, electionTimeout, heartbeatTimeout int) *Node {
	return &Node{
		ID:               id,
		peers:            append([]int(nil), ids...),
		State:            Follower,
		vote:             None,
		LeaderID:         None,
		Log:              []LogEntry{{Term: 0}}, // sentinel
		electionTimeout:  electionTimeout,
		heartbeatTimeout: heartbeatTimeout,
		match:            make(map[int]int),
		next:             make(map[int]int),
	}
}

// SetTimeouts overrides the tick-driven timeouts (used by tests/demos to
// make elections deterministic).
func (n *Node) SetTimeouts(election, heartbeat int) {
	n.electionTimeout = election
	n.heartbeatTimeout = heartbeat
}

func (n *Node) quorum() int { return len(n.peers)/2 + 1 }

// LastIndex is the index of the newest log entry.
func (n *Node) LastIndex() int { return len(n.Log) - 1 }

// LastTerm is the term of the newest log entry.
func (n *Node) LastTerm() int { return n.Log[n.LastIndex()].Term }

// Committed returns the committed prefix of the log (1-based entries).
func (n *Node) Committed() []LogEntry { return n.Log[1 : n.CommitIndex+1] }

// upToDate reports whether a log with the given tip is at least as fresh
// as this node's log ( Raft's "log freshness" comparison).
func (n *Node) upToDate(index, term int) bool {
	return term > n.LastTerm() || (term == n.LastTerm() && index >= n.LastIndex())
}

// Tick advances the local clock by one discrete step. Heartbeats and
// election timeouts are entirely driven by these ticks.
func (n *Node) Tick() {
	if n.State == Leader {
		n.heartbeatElapsed++
		if n.heartbeatElapsed >= n.heartbeatTimeout {
			n.heartbeatElapsed = 0
			n.broadcastAppend()
		}
		return
	}
	n.electionElapsed++
	if n.electionElapsed >= n.electionTimeout {
		n.electionElapsed = 0
		n.startPreVote()
	}
}

// Step delivers one message to the node.
func (n *Node) Step(m Message) {
	switch m.Type {
	case MsgPreVote:
		n.handlePreVote(m)
	case MsgPreVoteResp:
		n.handlePreVoteResp(m)
	case MsgVote:
		n.handleVote(m)
	case MsgVoteResp:
		n.handleVoteResp(m)
	case MsgAppend:
		n.handleAppend(m)
	case MsgAppendResp:
		n.handleAppendResp(m)
	}
}

// Propose appends a client entry. Only a leader accepts proposals; the
// entry becomes committed once a majority of the current term stores it.
func (n *Node) Propose(data string) bool {
	if n.State != Leader {
		return false
	}
	n.Log = append(n.Log, LogEntry{Term: n.Term, Data: data})
	n.match[n.ID] = n.LastIndex()
	n.broadcastAppend()
	return true
}

// Drain returns and clears the node's outgoing messages.
func (n *Node) Drain() []Message {
	out := n.outbox
	n.outbox = nil
	return out
}

func (n *Node) send(m Message) {
	m.From = n.ID
	n.outbox = append(n.outbox, m)
}

// --- elections ---------------------------------------------------------

func (n *Node) startPreVote() {
	// Pre-vote never changes n.Term: a partitioned node that cannot win
	// a quorum therefore cannot inflate its term and disrupt the cluster
	// when it rejoins.
	n.preVotes = map[int]bool{n.ID: true}
	for _, p := range n.peers {
		if p != n.ID {
			n.send(Message{
				Type:         MsgPreVote,
				To:           p,
				Term:         n.Term + 1,
				LastLogIndex: n.LastIndex(),
				LastLogTerm:  n.LastTerm(),
			})
		}
	}
}

func (n *Node) handlePreVote(m Message) {
	grant := false
	// Grant only if the would-be term is fresh, the candidate's log is up
	// to date, and we have not heard from a live leader very recently
	// (a live leader heartbeats every heartbeatTimeout ticks).
	if m.Term >= n.Term && n.State != Leader &&
		n.electionElapsed > n.heartbeatTimeout &&
		n.upToDate(m.LastLogIndex, m.LastLogTerm) {
		grant = true
	}
	n.send(Message{Type: MsgPreVoteResp, To: m.From, Term: n.Term, Grant: grant})
}

func (n *Node) handlePreVoteResp(m Message) {
	if m.Term > n.Term {
		n.becomeFollower(m.Term)
		return
	}
	if n.preVotes == nil || !m.Grant {
		return
	}
	n.preVotes[m.From] = true
	if len(n.preVotes) >= n.quorum() {
		n.preVotes = nil
		n.startElection()
	}
}

func (n *Node) startElection() {
	n.State = Candidate
	n.Term++ // terms only ever increase
	n.vote = n.ID
	n.votes = map[int]bool{n.ID: true}
	n.electionElapsed = 0
	for _, p := range n.peers {
		if p != n.ID {
			n.send(Message{
				Type:         MsgVote,
				To:           p,
				Term:         n.Term,
				LastLogIndex: n.LastIndex(),
				LastLogTerm:  n.LastTerm(),
			})
		}
	}
}

func (n *Node) handleVote(m Message) {
	if m.Term > n.Term {
		n.becomeFollower(m.Term)
	}
	grant := false
	if m.Term == n.Term && (n.vote == None || n.vote == m.From) &&
		n.upToDate(m.LastLogIndex, m.LastLogTerm) {
		grant = true
		n.vote = m.From
		n.electionElapsed = 0
	}
	n.send(Message{Type: MsgVoteResp, To: m.From, Term: n.Term, Grant: grant})
}

func (n *Node) handleVoteResp(m Message) {
	if m.Term > n.Term {
		n.becomeFollower(m.Term)
		return
	}
	if n.State != Candidate || m.Term != n.Term || !m.Grant {
		return
	}
	n.votes[m.From] = true
	if len(n.votes) >= n.quorum() {
		n.becomeLeader()
	}
}

func (n *Node) becomeFollower(term int) {
	n.State = Follower
	n.Term = term
	n.vote = None
	n.LeaderID = None
	n.preVotes = nil
	n.votes = nil
	n.electionElapsed = 0
}

func (n *Node) becomeLeader() {
	n.State = Leader
	n.LeaderID = n.ID
	n.heartbeatElapsed = 0
	for _, p := range n.peers {
		n.next[p] = n.LastIndex() + 1
		n.match[p] = 0
	}
	n.match[n.ID] = n.LastIndex()
	n.broadcastAppend()
}

// --- replication -------------------------------------------------------

func (n *Node) broadcastAppend() {
	for _, p := range n.peers {
		if p != n.ID {
			n.sendAppend(p)
		}
	}
}

// AppendMessage builds the Append (heartbeat) message the leader would
// currently send to `to`, honouring its per-follower next cursor. It is
// exported so tests can inject heartbeats directly.
func (n *Node) AppendMessage(to int) Message {
	nx := n.next[to]
	if nx < 1 {
		nx = 1
	}
	prev := nx - 1
	entries := append([]LogEntry(nil), n.Log[nx:]...)
	return Message{
		Type:         MsgAppend,
		From:         n.ID,
		To:           to,
		Term:         n.Term,
		PrevLogIndex: prev,
		PrevLogTerm:  n.Log[prev].Term,
		Entries:      entries,
		LeaderCommit: n.CommitIndex,
	}
}

func (n *Node) sendAppend(to int) { n.send(n.AppendMessage(to)) }

func (n *Node) handleAppend(m Message) {
	if m.Term < n.Term {
		// Stale leader: reject and tell it the current term.
		n.send(Message{Type: MsgAppendResp, To: m.From, Term: n.Term, Reject: true})
		return
	}
	if m.Term > n.Term {
		n.Term = m.Term
		n.vote = None
	}
	n.State = Follower
	n.LeaderID = m.From
	n.preVotes = nil
	n.votes = nil
	n.electionElapsed = 0

	// Log consistency check: the entry before the payload must exist and
	// carry the expected term.
	if m.PrevLogIndex > n.LastIndex() || n.Log[m.PrevLogIndex].Term != m.PrevLogTerm {
		n.send(Message{Type: MsgAppendResp, To: m.From, Term: n.Term, Reject: true})
		return
	}
	n.appendFrom(m.PrevLogIndex, m.Entries)
	if m.LeaderCommit > n.CommitIndex {
		n.CommitIndex = min(m.LeaderCommit, n.LastIndex())
	}
	n.send(Message{
		Type:       MsgAppendResp,
		To:         m.From,
		Term:       n.Term,
		MatchIndex: m.PrevLogIndex + len(m.Entries),
	})
}

func (n *Node) handleAppendResp(m Message) {
	if m.Term > n.Term {
		n.becomeFollower(m.Term)
		return
	}
	if n.State != Leader || m.Term != n.Term {
		return
	}
	if m.Reject {
		// Consistency check failed: back the cursor off and retry with an
		// earlier prefix.
		if n.next[m.From] > 1 {
			n.next[m.From]--
		}
		n.sendAppend(m.From)
		return
	}
	if m.MatchIndex > n.match[m.From] {
		n.match[m.From] = m.MatchIndex
		n.next[m.From] = m.MatchIndex + 1
	}
	if n.maybeCommit() {
		n.broadcastAppend() // propagate the new commit index
	}
}

// maybeCommit advances the commit index to the newest entry stored on a
// majority. Only entries from the leader's own term are committed by
// counting; older entries commit indirectly with them (Raft safety rule).
func (n *Node) maybeCommit() bool {
	for idx := n.LastIndex(); idx > n.CommitIndex; idx-- {
		if n.Log[idx].Term != n.Term {
			continue
		}
		count := 0
		for _, p := range n.peers {
			if n.match[p] >= idx {
				count++
			}
		}
		if count >= n.quorum() {
			n.CommitIndex = idx
			return true
		}
	}
	return false
}

// appendFrom appends entries after prevIndex, truncating any conflicting
// uncommitted suffix. Truncating a committed entry is a safety violation
// and panics: with correct elections it can never happen, and the guard
// turns a correctness bug into a loud failure instead of silent data loss.
func (n *Node) appendFrom(prevIndex int, entries []LogEntry) {
	for i, e := range entries {
		idx := prevIndex + 1 + i
		if idx < len(n.Log) {
			if n.Log[idx].Term == e.Term {
				continue
			}
			if idx <= n.CommitIndex {
				panic(fmt.Sprintf("node %d: safety violation: truncating committed entry at index %d", n.ID, idx))
			}
			n.Log = n.Log[:idx]
		}
		n.Log = append(n.Log, entries[i:]...)
		return
	}
}
