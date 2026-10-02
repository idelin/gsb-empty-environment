// Package logrepl implements a single-process, discrete-time replicated log
// with Raft-style view changes: monotonically increasing terms, pre-vote,
// log consistency checks and safe truncation of conflicting uncommitted
// entries. There is no network stack: message delivery, link failures and
// heartbeats are all driven by an external (test) clock via Cluster.Tick.
package logrepl

// None is the null node id / unset marker.
const None = -1

// Entry is a single replicated log record.
type Entry struct {
	Term int
	Cmd  string
}

// Role is the node's role in the current view (term).
type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	}
	return "?"
}

// MsgType enumerates the RPC kinds exchanged between nodes.
type MsgType int

const (
	MsgPreVote MsgType = iota
	MsgPreVoteResp
	MsgRequestVote
	MsgRequestVoteResp
	MsgAppend
	MsgAppendResp
)

// Message is the single wire format for all RPCs. Fields are reused per type:
//
//	MsgPreVote/MsgRequestVote: Term, LastLogIndex, LastLogTerm
//	MsgAppend:                 Term, PrevLogIndex, PrevLogTerm, Entries, LeaderCommit
//	responses:                 Term, Reject, MatchIndex, Hint
type Message struct {
	Type MsgType
	From int
	To   int
	Term int

	LastLogIndex int
	LastLogTerm  int

	PrevLogIndex int
	PrevLogTerm  int
	Entries      []Entry
	LeaderCommit int

	Reject     bool
	MatchIndex int
	Hint       int
}

// Node is one replica. Its log is 1-based; log[0] is a sentinel with Term 0
// so that PrevLogIndex/PrevLogTerm checks work uniformly at index 0.
type Node struct {
	id        int
	role      Role
	term      int
	votedFor  int
	leaderID  int
	log       []Entry
	committed int

	electionElapsed  int
	electionTimeout  int
	heartbeatElapsed int

	nextIndex  map[int]int
	matchIndex map[int]int

	preVoteTerm int
	preVotes    map[int]bool
	votes       map[int]bool

	preVoteRounds int
}

// --- read-only accessors used by tests and the demo ---

func (n *Node) ID() int        { return n.id }
func (n *Node) Role() Role     { return n.role }
func (n *Node) Term() int      { return n.term }
func (n *Node) Committed() int { return n.committed }
func (n *Node) LastIndex() int { return len(n.log) - 1 }

// Entries returns a copy of the log without the sentinel.
func (n *Node) Entries() []Entry {
	cp := make([]Entry, len(n.log)-1)
	copy(cp, n.log[1:])
	return cp
}

// PreVoteRounds exposes how many times the node started a pre-vote, so tests
// can prove a partitioned node keeps trying without inflating its term.
func (n *Node) PreVoteRounds() int { return n.preVoteRounds }

func (n *Node) lastTerm() int { return n.log[n.lastIndex()].Term }
func (n *Node) lastIndex() int {
	return len(n.log) - 1
}

// isUpToDate implements the Raft log-freshness comparison: the candidate's
// (lastTerm, lastIndex) must be at least as new as ours.
func (n *Node) isUpToDate(lastTerm, lastIndex int) bool {
	myTerm := n.lastTerm()
	if lastTerm != myTerm {
		return lastTerm > myTerm
	}
	return lastIndex >= n.lastIndex()
}

// becomeFollower moves the node into a (possibly newer) view. Terms only ever
// increase; votedFor is reset exactly once per new term.
func (n *Node) becomeFollower(term int) {
	if term > n.term {
		n.term = term
		n.votedFor = None
	}
	n.role = Follower
	n.electionElapsed = 0
}

// tick advances the node's local clock by one discrete step. Leaders emit
// heartbeats; followers and candidates run the election timer, which starts a
// pre-vote (never a real election directly).
func (n *Node) tick(c *Cluster) {
	if n.role == Leader {
		n.heartbeatElapsed++
		if n.heartbeatElapsed >= c.HeartbeatInterval {
			n.heartbeatElapsed = 0
			for _, id := range c.order {
				if id != n.id {
					n.sendAppend(c, id)
				}
			}
		}
		return
	}
	n.electionElapsed++
	if n.electionElapsed >= n.electionTimeout {
		n.electionElapsed = 0
		n.startPreVote(c)
	}
}

// startPreVote asks peers whether a real election for term+1 would succeed.
// Crucially the local term is NOT incremented, so a partitioned node cannot
// inflate its term and later disrupt a healthy cluster.
func (n *Node) startPreVote(c *Cluster) {
	n.preVoteRounds++
	n.preVoteTerm = n.term + 1
	n.preVotes = map[int]bool{n.id: true}
	for _, id := range c.order {
		if id == n.id {
			continue
		}
		c.send(Message{Type: MsgPreVote, From: n.id, To: id, Term: n.preVoteTerm,
			LastLogIndex: n.lastIndex(), LastLogTerm: n.lastTerm()})
	}
	if len(n.preVotes) >= c.quorum() {
		n.startElection(c)
	}
}

// startElection begins a real election: this is the only place (besides
// adopting a higher term from a peer) where the term increases.
func (n *Node) startElection(c *Cluster) {
	n.role = Candidate
	n.term++
	n.votedFor = n.id
	n.leaderID = None
	n.electionElapsed = 0
	n.preVotes = nil
	n.preVoteTerm = 0
	n.votes = map[int]bool{n.id: true}
	for _, id := range c.order {
		if id == n.id {
			continue
		}
		c.send(Message{Type: MsgRequestVote, From: n.id, To: id, Term: n.term,
			LastLogIndex: n.lastIndex(), LastLogTerm: n.lastTerm()})
	}
	if len(n.votes) >= c.quorum() {
		n.becomeLeader(c)
	}
}

func (n *Node) becomeLeader(c *Cluster) {
	n.role = Leader
	n.leaderID = n.id
	n.heartbeatElapsed = 0
	n.nextIndex = make(map[int]int)
	n.matchIndex = make(map[int]int)
	for _, id := range c.order {
		n.nextIndex[id] = n.lastIndex() + 1
		n.matchIndex[id] = 0
	}
	n.matchIndex[n.id] = n.lastIndex()
	for _, id := range c.order {
		if id != n.id {
			n.sendAppend(c, id)
		}
	}
}

// sendAppend ships everything from nextIndex[to] onward (possibly empty,
// which makes it a heartbeat) together with the consistency checkpoint.
func (n *Node) sendAppend(c *Cluster, to int) {
	next := n.nextIndex[to]
	prev := next - 1
	entries := make([]Entry, 0, len(n.log)-next)
	entries = append(entries, n.log[next:]...)
	c.send(Message{Type: MsgAppend, From: n.id, To: to, Term: n.term,
		PrevLogIndex: prev, PrevLogTerm: n.log[prev].Term,
		Entries: entries, LeaderCommit: n.committed})
}

func (n *Node) step(c *Cluster, m Message) {
	switch m.Type {
	case MsgPreVote:
		n.handlePreVote(c, m)
	case MsgPreVoteResp:
		n.handlePreVoteResp(c, m)
	case MsgRequestVote:
		n.handleRequestVote(c, m)
	case MsgRequestVoteResp:
		n.handleRequestVoteResp(c, m)
	case MsgAppend:
		n.handleAppend(c, m)
	case MsgAppendResp:
		n.handleAppendResp(c, m)
	}
}

func (n *Node) handlePreVote(c *Cluster, m Message) {
	grant := false
	if m.Term > n.term && n.role != Leader && n.isUpToDate(m.LastLogTerm, m.LastLogIndex) {
		// Leader lease: only grant if we have not heard from a leader for
		// at least half an election timeout, so a healthy leader is never
		// disrupted by a rejoining node's pre-vote.
		if n.leaderID == None || n.electionElapsed*2 >= n.electionTimeout {
			grant = true
		}
	}
	respTerm := n.term
	if m.Term > respTerm {
		respTerm = m.Term
	}
	c.send(Message{Type: MsgPreVoteResp, From: n.id, To: m.From, Term: respTerm, Reject: !grant})
}

func (n *Node) handlePreVoteResp(c *Cluster, m Message) {
	if m.Term > n.preVoteTerm {
		if m.Term > n.term {
			n.becomeFollower(m.Term)
		}
		return
	}
	if n.preVotes == nil || m.Term != n.preVoteTerm || m.Reject {
		return
	}
	n.preVotes[m.From] = true
	if len(n.preVotes) >= c.quorum() {
		n.startElection(c)
	}
}

func (n *Node) handleRequestVote(c *Cluster, m Message) {
	if m.Term < n.term {
		c.send(Message{Type: MsgRequestVoteResp, From: n.id, To: m.From, Term: n.term, Reject: true})
		return
	}
	if m.Term > n.term {
		n.becomeFollower(m.Term)
	}
	grant := false
	if (n.votedFor == None || n.votedFor == m.From) && n.isUpToDate(m.LastLogTerm, m.LastLogIndex) {
		grant = true
		n.votedFor = m.From
		n.leaderID = None
		n.electionElapsed = 0
	}
	c.send(Message{Type: MsgRequestVoteResp, From: n.id, To: m.From, Term: n.term, Reject: !grant})
}

func (n *Node) handleRequestVoteResp(c *Cluster, m Message) {
	if m.Term > n.term {
		n.becomeFollower(m.Term)
		return
	}
	if n.role != Candidate || m.Term != n.term || m.Reject {
		return
	}
	n.votes[m.From] = true
	if len(n.votes) >= c.quorum() {
		n.becomeLeader(c)
	}
}

func (n *Node) handleAppend(c *Cluster, m Message) {
	if m.Term < n.term {
		// Stale view: reject and report our (higher) term so the sender
		// steps down.
		c.send(Message{Type: MsgAppendResp, From: n.id, To: m.From, Term: n.term,
			Reject: true, Hint: n.lastIndex()})
		return
	}
	n.becomeFollower(m.Term)
	n.leaderID = m.From
	n.electionElapsed = 0

	// Log consistency check: our log must contain PrevLogIndex with
	// PrevLogTerm, otherwise the leader backs off and retries.
	if m.PrevLogIndex > n.lastIndex() {
		c.send(Message{Type: MsgAppendResp, From: n.id, To: m.From, Term: n.term,
			Reject: true, Hint: n.lastIndex()})
		return
	}
	if n.log[m.PrevLogIndex].Term != m.PrevLogTerm {
		c.send(Message{Type: MsgAppendResp, From: n.id, To: m.From, Term: n.term,
			Reject: true, Hint: m.PrevLogIndex - 1})
		return
	}

	// Merge: keep committed entries untouched; truncate only conflicting
	// uncommitted suffixes, then append the leader's entries.
	for i, e := range m.Entries {
		idx := m.PrevLogIndex + 1 + i
		if idx <= n.committed {
			continue // safety: never overwrite or truncate committed entries
		}
		if idx <= n.lastIndex() {
			if n.log[idx].Term == e.Term {
				continue
			}
			n.log = n.log[:idx] // safe truncation: idx > committed
		}
		n.log = append(n.log, e)
	}
	if m.LeaderCommit > n.committed {
		n.committed = min(m.LeaderCommit, n.lastIndex())
	}
	c.send(Message{Type: MsgAppendResp, From: n.id, To: m.From, Term: n.term,
		MatchIndex: m.PrevLogIndex + len(m.Entries)})
}

func (n *Node) handleAppendResp(c *Cluster, m Message) {
	if m.Term > n.term {
		n.becomeFollower(m.Term)
		return
	}
	if n.role != Leader || m.Term != n.term {
		return
	}
	if m.Reject {
		next := n.nextIndex[m.From] - 1
		if m.Hint+1 < next {
			next = m.Hint + 1
		}
		if next < 1 {
			next = 1
		}
		n.nextIndex[m.From] = next
		n.sendAppend(c, m.From)
		return
	}
	if m.MatchIndex > n.matchIndex[m.From] {
		n.matchIndex[m.From] = m.MatchIndex
		n.nextIndex[m.From] = m.MatchIndex + 1
	}
	n.maybeCommit(c)
}

// maybeCommit advances the commit index to the newest entry of the leader's
// own term that is replicated on a quorum. Restricting to current-term
// entries is what makes committing safe across view changes.
func (n *Node) maybeCommit(c *Cluster) {
	for idx := n.lastIndex(); idx > n.committed; idx-- {
		if n.log[idx].Term != n.term {
			continue
		}
		count := 0
		for _, id := range c.order {
			if n.matchIndex[id] >= idx {
				count++
			}
		}
		if count >= c.quorum() {
			n.committed = idx
			break
		}
	}
}
