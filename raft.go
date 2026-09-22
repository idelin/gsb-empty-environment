package raft

import "fmt"

// Role is the Raft server role.
type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Candidate:
		return "candidate"
	case Leader:
		return "leader"
	default:
		return "follower"
	}
}

// Entry is a single log record. Cmd "set k v" mutates the state machine;
// the empty Cmd is the leader no-op used to confirm a new term.
type Entry struct {
	Term int
	Cmd  string
}

// Msg is the single wire type for every RPC in the cluster.
type Msg struct {
	Type MsgType
	Term int
	From int
	To   int

	// RequestVote
	LastLogIndex int
	LastLogTerm  int
	Granted      bool

	// AppendEntries
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []Entry
	LeaderCommit int
	Success      bool
	MatchIndex   int

	// ReadIndex confirmation round
	Nonce int
}

// MsgType identifies the RPC kind.
type MsgType int

const (
	MsgRequestVote MsgType = iota
	MsgRequestVoteResp
	MsgAppend
	MsgAppendResp
	MsgReadConfirm
	MsgReadConfirmAck
)

// Node is a single Raft server. All state transitions happen inside
// simulator events, so no locks are needed.
type Node struct {
	id   int
	size int

	role     Role
	term     int
	votedFor int
	leaderID int

	log          []Entry // log[i] is log position i+1
	commitIndex  int
	appliedIndex int
	state        map[string]string
	crashed      bool

	electionGen int
	votes       map[int]bool

	nextIndex  []int
	matchIndex []int
	hbGen      int

	applyScheduled bool

	readSeq int
	pending map[int]*readState
}

// Cluster wires nodes together through the simulator. Message delivery,
// partitions and delays are fully controlled here.
type Cluster struct {
	sim   *Sim
	nodes []*Node

	blocked [][]bool // blocked[i][j]: drop messages from i to j

	NetDelay       int64
	ApplyDelay     int64
	HeartbeatEvery int64
	ElectionBase   int64
	ElectionSpread int64

	// Trace, when non-nil, receives human-readable protocol events.
	Trace func(at int64, format string, args ...any)
}

// NewCluster builds n nodes with deterministic, staggered election timeouts.
func NewCluster(sim *Sim, n int) *Cluster {
	c := &Cluster{
		sim:            sim,
		NetDelay:       5,
		ApplyDelay:     2,
		HeartbeatEvery: 20,
		ElectionBase:   100,
		ElectionSpread: 50,
	}
	c.blocked = make([][]bool, n)
	for i := range c.blocked {
		c.blocked[i] = make([]bool, n)
	}
	for i := 0; i < n; i++ {
		node := &Node{
			id:       i,
			size:     n,
			role:     Follower,
			votedFor: -1,
			leaderID: -1,
			state:    map[string]string{},
			pending:  map[int]*readState{},
		}
		c.nodes = append(c.nodes, node)
		node.resetElection(c)
	}
	return c
}

func (c *Cluster) tracef(format string, args ...any) {
	if c.Trace != nil {
		c.Trace(c.sim.Now(), format, args...)
	}
}

// Node exposes a node for inspection by tests and demos.
func (c *Cluster) Node(id int) *Node { return c.nodes[id] }

// LeaderID returns the id of the first live leader, or -1.
func (c *Cluster) LeaderID() int {
	for _, n := range c.nodes {
		if !n.crashed && n.role == Leader {
			return n.id
		}
	}
	return -1
}

// Partition blocks all traffic between different groups.
func (c *Cluster) Partition(groups ...[]int) {
	inGroup := make([]int, len(c.nodes))
	for gi, g := range groups {
		for _, id := range g {
			inGroup[id] = gi + 1
		}
	}
	for i := range c.blocked {
		for j := range c.blocked[i] {
			c.blocked[i][j] = inGroup[i] != inGroup[j]
		}
	}
}

// Heal removes all partitions.
func (c *Cluster) Heal() {
	for i := range c.blocked {
		for j := range c.blocked[i] {
			c.blocked[i][j] = false
		}
	}
}

// Crash permanently stops a node.
func (c *Cluster) Crash(id int) { c.nodes[id].crashed = true }

func (c *Cluster) send(from, to int, m Msg) {
	m.From, m.To = from, to
	c.sim.After(c.NetDelay, func() {
		if c.blocked[from][to] {
			return
		}
		n := c.nodes[to]
		if n.crashed {
			return
		}
		n.handle(c, m)
	})
}

func (c *Cluster) broadcast(from int, m Msg) {
	for j := range c.nodes {
		if j != from {
			c.send(from, j, m)
		}
	}
}

// Propose asks the leader to replicate cmd ("set k v").
func (c *Cluster) Propose(leader int, cmd string) {
	c.sim.After(0, func() {
		n := c.nodes[leader]
		if n.crashed || n.role != Leader {
			return
		}
		n.appendLocal(c, cmd)
	})
}

// --- inspection helpers (read-only, used by tests and the demo) ---

// Term returns the node's current term.
func (n *Node) Term() int { return n.term }

// Role returns the node's role.
func (n *Node) Role() Role { return n.role }

// CommitIndex returns the node's commit index.
func (n *Node) CommitIndex() int { return n.commitIndex }

// AppliedIndex returns the node's applied index.
func (n *Node) AppliedIndex() int { return n.appliedIndex }

// --- Raft protocol ---

func (n *Node) quorum() int { return n.size/2 + 1 }

func (n *Node) lastIndex() int { return len(n.log) }

func (n *Node) lastTerm() int {
	if len(n.log) == 0 {
		return 0
	}
	return n.log[len(n.log)-1].Term
}

func (n *Node) handle(c *Cluster, m Msg) {
	if m.Term > n.term {
		n.stepDown(c, m.Term)
	}
	switch m.Type {
	case MsgRequestVote:
		n.onRequestVote(c, m)
	case MsgRequestVoteResp:
		n.onVoteResp(c, m)
	case MsgAppend:
		n.onAppend(c, m)
	case MsgAppendResp:
		n.onAppendResp(c, m)
	case MsgReadConfirm:
		n.onReadConfirm(c, m)
	case MsgReadConfirmAck:
		n.onReadConfirmAck(c, m)
	}
}

func (n *Node) stepDown(c *Cluster, term int) {
	c.tracef("node%d steps down: term %d -> %d", n.id, n.term, term)
	n.term = term
	n.role = Follower
	n.votedFor = -1
	n.leaderID = -1
	n.hbGen++
	n.failAllReads(fmt.Sprintf("stepped down in term %d", term))
	n.resetElection(c)
}

func (n *Node) resetElection(c *Cluster) {
	n.electionGen++
	gen := n.electionGen
	timeout := c.ElectionBase + int64(n.id)*c.ElectionSpread
	c.sim.After(timeout, func() {
		if n.crashed || n.electionGen != gen || n.role == Leader {
			return
		}
		n.startElection(c)
	})
}

func (n *Node) startElection(c *Cluster) {
	n.term++
	n.role = Candidate
	n.votedFor = n.id
	n.leaderID = -1
	n.votes = map[int]bool{n.id: true}
	n.resetElection(c)
	c.tracef("node%d starts election for term %d", n.id, n.term)
	c.broadcast(n.id, Msg{
		Type:         MsgRequestVote,
		Term:         n.term,
		LastLogIndex: n.lastIndex(),
		LastLogTerm:  n.lastTerm(),
	})
	n.maybeWin(c)
}

func (n *Node) maybeWin(c *Cluster) {
	if n.role == Candidate && len(n.votes) >= n.quorum() {
		n.becomeLeader(c)
	}
}

func (n *Node) onRequestVote(c *Cluster, m Msg) {
	granted := false
	if m.Term >= n.term {
		upToDate := m.LastLogTerm > n.lastTerm() ||
			(m.LastLogTerm == n.lastTerm() && m.LastLogIndex >= n.lastIndex())
		if (n.votedFor == -1 || n.votedFor == m.From) && upToDate {
			granted = true
			n.votedFor = m.From
			n.resetElection(c)
		}
	}
	c.send(n.id, m.From, Msg{Type: MsgRequestVoteResp, Term: n.term, Granted: granted})
}

func (n *Node) onVoteResp(c *Cluster, m Msg) {
	if n.role != Candidate || m.Term != n.term || !m.Granted {
		return
	}
	n.votes[m.From] = true
	n.maybeWin(c)
}

func (n *Node) becomeLeader(c *Cluster) {
	n.role = Leader
	n.leaderID = n.id
	n.nextIndex = make([]int, n.size)
	n.matchIndex = make([]int, n.size)
	for j := range n.nextIndex {
		n.nextIndex[j] = n.lastIndex() + 1
	}
	n.matchIndex[n.id] = n.lastIndex()
	c.tracef("node%d becomes leader for term %d", n.id, n.term)
	// A new leader first commits a no-op in its own term; only then is the
	// term confirmed and reads are allowed (no stale-state reads).
	n.appendLocal(c, "")
	n.startHeartbeats(c)
}

func (n *Node) appendLocal(c *Cluster, cmd string) {
	n.log = append(n.log, Entry{Term: n.term, Cmd: cmd})
	n.matchIndex[n.id] = n.lastIndex()
	n.nextIndex[n.id] = n.lastIndex() + 1
	for j := range c.nodes {
		if j != n.id {
			n.replicateTo(c, j)
		}
	}
	n.advanceCommit(c)
}

func (n *Node) replicateTo(c *Cluster, j int) {
	prev := n.nextIndex[j] - 1
	prevTerm := 0
	if prev >= 1 {
		prevTerm = n.log[prev-1].Term
	}
	entries := append([]Entry(nil), n.log[prev:]...)
	c.send(n.id, j, Msg{
		Type:         MsgAppend,
		Term:         n.term,
		PrevLogIndex: prev,
		PrevLogTerm:  prevTerm,
		Entries:      entries,
		LeaderCommit: n.commitIndex,
	})
}

func (n *Node) onAppend(c *Cluster, m Msg) {
	ok := false
	if m.Term >= n.term {
		n.role = Follower
		n.leaderID = m.From
		n.resetElection(c)
		prevOK := m.PrevLogIndex <= n.lastIndex() &&
			(m.PrevLogIndex == 0 || n.log[m.PrevLogIndex-1].Term == m.PrevLogTerm)
		if prevOK {
			ok = true
			idx := m.PrevLogIndex
			for _, e := range m.Entries {
				idx++
				if idx <= n.lastIndex() && n.log[idx-1].Term != e.Term {
					n.log = n.log[:idx-1] // drop conflicting suffix
				}
				if idx > n.lastIndex() {
					n.log = append(n.log, e)
				}
			}
			if m.LeaderCommit > n.commitIndex {
				n.commitIndex = min(m.LeaderCommit, n.lastIndex())
				n.maybeApply(c)
			}
		}
	}
	c.send(n.id, m.From, Msg{Type: MsgAppendResp, Term: n.term, Success: ok, MatchIndex: n.lastIndex()})
}

func (n *Node) onAppendResp(c *Cluster, m Msg) {
	if n.role != Leader || m.Term != n.term {
		return
	}
	if m.Success {
		if m.MatchIndex > n.matchIndex[m.From] {
			n.matchIndex[m.From] = m.MatchIndex
			n.nextIndex[m.From] = m.MatchIndex + 1
		}
	} else {
		if n.nextIndex[m.From] > 1 {
			n.nextIndex[m.From]--
		}
		n.replicateTo(c, m.From)
		return
	}
	n.advanceCommit(c)
}

func (n *Node) advanceCommit(c *Cluster) {
	if n.role != Leader {
		return
	}
	for idx := n.lastIndex(); idx > n.commitIndex; idx-- {
		if n.log[idx-1].Term != n.term {
			continue // only count-replicate entries from the current term
		}
		count := 0
		for j := range c.nodes {
			if n.matchIndex[j] >= idx {
				count++
			}
		}
		if count >= n.quorum() {
			n.commitIndex = idx
			c.tracef("node%d commits index %d (term %d)", n.id, idx, n.term)
			n.maybeApply(c)
			for j := range c.nodes {
				if j != n.id {
					n.replicateTo(c, j)
				}
			}
			break
		}
	}
}

func (n *Node) startHeartbeats(c *Cluster) {
	n.hbGen++
	gen := n.hbGen
	var beat func()
	beat = func() {
		if n.crashed || n.role != Leader || n.hbGen != gen {
			return
		}
		for j := range c.nodes {
			if j != n.id {
				n.replicateTo(c, j)
			}
		}
		c.sim.After(c.HeartbeatEvery, beat)
	}
	c.sim.After(c.HeartbeatEvery, beat)
}

// maybeApply applies committed entries to the state machine after
// ApplyDelay, modelling asynchronous apply on real storage.
func (n *Node) maybeApply(c *Cluster) {
	if n.applyScheduled || n.appliedIndex >= n.commitIndex {
		return
	}
	n.applyScheduled = true
	c.sim.After(c.ApplyDelay, func() {
		n.applyScheduled = false
		if n.crashed {
			return
		}
		for n.appliedIndex < n.commitIndex {
			n.appliedIndex++
			e := n.log[n.appliedIndex-1]
			var k, v string
			if fmt.Sscanf(e.Cmd, "set %s %s", &k, &v); k != "" {
				n.state[k] = v
			}
		}
		c.tracef("node%d applied up to %d", n.id, n.appliedIndex)
		n.checkPendingReads(c)
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
