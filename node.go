package main

// Role of a node in its current view.
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

// Node is one replica. view, votedFor, log, baseCfg and commit survive
// crashes (persistent state); everything else is volatile.
type Node struct {
	id int

	// persistent
	view     int
	votedFor int
	log      []Entry
	baseCfg  Config
	cfg      Config // derived: baseCfg overridden by last config entry in log
	commit   int

	// volatile
	role        Role
	crashed     bool
	applied     []string
	lastApplied int

	// candidate/leader state
	votes map[int]bool
	match map[int]int
	next  map[int]int
}

func newNode(id int, base Config) *Node {
	return &Node{
		id:       id,
		votedFor: -1,
		baseCfg:  base,
		cfg:      base,
		role:     Follower,
	}
}

func (n *Node) lastIndex() int { return len(n.log) }

func (n *Node) viewAt(index int) int {
	if index <= 0 || index > len(n.log) {
		return 0
	}
	return n.log[index-1].View
}

func (n *Node) lastView() int { return n.viewAt(n.lastIndex()) }

func (n *Node) recomputeCfg() {
	n.cfg = n.baseCfg
	for _, e := range n.log {
		if e.Cfg != nil {
			n.cfg = *e.Cfg
		}
	}
}

func (n *Node) becomeFollower(view int) {
	n.view = view
	n.votedFor = -1
	n.role = Follower
	n.votes = nil
	n.match = nil
	n.next = nil
}

// --- election ---

func (n *Node) startElection(s *Sim) {
	if n.crashed || len(n.cfg.Old) == 0 {
		return
	}
	n.view++
	n.role = Candidate
	n.votedFor = n.id
	n.votes = map[int]bool{n.id: true}
	s.tracef("node %d: start election for view %d under %s", n.id, n.view, n.cfg)
	for _, p := range n.cfg.Voters() {
		if p != n.id {
			s.send(Message{Type: MsgVoteRequest, View: n.view, From: n.id, To: p,
				LastIndex: n.lastIndex(), LastView: n.lastView()})
		}
	}
	n.maybeBecomeLeader(s)
}

func upToDate(candView, candIndex, myView, myIndex int) bool {
	return candView > myView || (candView == myView && candIndex >= myIndex)
}

func (n *Node) onVoteRequest(s *Sim, m Message) {
	grant := false
	switch {
	case m.View > n.view:
		n.becomeFollower(m.View)
		if upToDate(m.LastView, m.LastIndex, n.lastView(), n.lastIndex()) {
			n.votedFor = m.From
			grant = true
		}
	case m.View == n.view && n.votedFor == m.From:
		grant = true // idempotent re-vote
	default:
		s.tracef("node %d: reject vote request from %d for stale view %d (my view %d)",
			n.id, m.From, m.View, n.view)
	}
	s.send(Message{Type: MsgVoteResponse, View: m.View, From: n.id, To: m.From, Granted: grant})
}

func (n *Node) onVoteResponse(s *Sim, m Message) {
	if m.View != n.view {
		s.tracef("node %d: ignore late vote from %d stamped view %d (my view %d)",
			n.id, m.From, m.View, n.view)
		return
	}
	if n.role != Candidate {
		return
	}
	n.votes[m.From] = m.Granted
	n.maybeBecomeLeader(s)
}

func (n *Node) maybeBecomeLeader(s *Sim) {
	if n.role != Candidate || !n.cfg.Quorum(n.votes) {
		return
	}
	n.role = Leader
	n.match = map[int]int{n.id: n.lastIndex()}
	n.next = map[int]int{}
	s.tracef("node %d: leader of view %d under %s", n.id, n.view, n.cfg)
	// Anchor commitment with a no-op entry in this view; only entries of
	// the leader's own view are committed by counting replicas.
	n.appendEntry(s, Entry{})
	n.ensureSwitchCompletion(s)
}

// --- replication ---

func (n *Node) replicate(s *Sim) {
	for _, p := range n.cfg.Voters() {
		if p != n.id {
			n.replicateTo(s, p)
		}
	}
}

func (n *Node) replicateTo(s *Sim, peer int) {
	nx, ok := n.next[peer]
	if !ok || nx < 1 {
		nx = n.lastIndex() + 1
	}
	if nx > n.lastIndex()+1 {
		nx = n.lastIndex() + 1
	}
	prev := nx - 1
	entries := append([]Entry(nil), n.log[prev:]...)
	s.send(Message{Type: MsgAppend, View: n.view, From: n.id, To: peer,
		PrevIndex: prev, PrevView: n.viewAt(prev), Entries: entries, LeaderCommit: n.commit})
}

func (n *Node) onAppend(s *Sim, m Message) {
	if m.View < n.view {
		s.tracef("node %d: reject Append from %d with stale view %d (my view %d)",
			n.id, m.From, m.View, n.view)
		s.send(Message{Type: MsgAppendAck, View: m.View, From: n.id, To: m.From, Accept: false})
		return
	}
	if m.View > n.view {
		n.becomeFollower(m.View)
	}
	n.role = Follower

	if m.PrevIndex > n.lastIndex() ||
		(m.PrevIndex > 0 && n.viewAt(m.PrevIndex) != m.PrevView) {
		s.send(Message{Type: MsgAppendAck, View: m.View, From: n.id, To: m.From,
			Accept: false, AckIndex: n.lastIndex()})
		return
	}

	// Skip entries already present (same view at same index); find the
	// divergence point.
	i := m.PrevIndex
	pos := 0
	for pos < len(m.Entries) {
		idx := i + 1
		if idx <= n.lastIndex() && n.viewAt(idx) == m.Entries[pos].View {
			i = idx
			pos++
		} else {
			break
		}
	}
	// A legitimate leader can never conflict with entries we already
	// committed (leader completeness); reject instead of truncating them.
	if i < n.commit {
		s.send(Message{Type: MsgAppendAck, View: m.View, From: n.id, To: m.From,
			Accept: false, AckIndex: n.lastIndex()})
		return
	}
	newEntries := m.Entries[pos:]

	// Configuration guard: verify the chain Stable(A)->Joint(A,B)->Stable(B)
	// for entries that are actually new to this log. A direct jump to a
	// different stable set (e.g. a disjoint new majority) is rejected here,
	// BEFORE anything is truncated or appended.
	cur := n.baseCfg
	for _, e := range n.log[:i] {
		if e.Cfg != nil {
			cur = *e.Cfg
		}
	}
	for _, e := range newEntries {
		if e.Cfg != nil {
			if !cur.canTransitionTo(*e.Cfg) {
				s.tracef("node %d: REJECT direct config switch %s -> %s from %d",
					n.id, cur, *e.Cfg, m.From)
				s.send(Message{Type: MsgAppendAck, View: m.View, From: n.id, To: m.From,
					Accept: false, AckIndex: n.lastIndex()})
				return
			}
			cur = *e.Cfg
		}
	}

	// All checks passed: truncate the conflicting suffix and append.
	if i < n.lastIndex() {
		n.log = n.log[:i]
	}
	for _, e := range newEntries {
		n.log = append(n.log, e)
	}
	n.recomputeCfg()

	if m.LeaderCommit > n.commit {
		n.commit = m.LeaderCommit
		if n.commit > n.lastIndex() {
			n.commit = n.lastIndex()
		}
		n.applyCommitted(s)
	}
	s.send(Message{Type: MsgAppendAck, View: m.View, From: n.id, To: m.From,
		Accept: true, AckIndex: n.lastIndex()})
}

func (n *Node) onAck(s *Sim, m Message) {
	if n.role != Leader || m.View != n.view {
		return
	}
	if !m.Accept {
		cand := m.AckIndex + 1
		if cand < 1 {
			cand = 1
		}
		if n.next[m.From] == 0 || cand < n.next[m.From] {
			n.next[m.From] = cand
		}
		n.replicateTo(s, m.From)
		return
	}
	n.match[m.From] = m.AckIndex
	if n.match[m.From] > n.lastIndex() {
		n.match[m.From] = n.lastIndex()
	}
	n.next[m.From] = m.AckIndex + 1
	n.tryCommit(s)
}

// tryCommit advances the commit index. Only entries of the leader's own
// view are committed by counting replicas (older entries ride along);
// the quorum must satisfy the configuration currently in effect, which
// during the joint phase means a majority of BOTH old and new sets.
func (n *Node) tryCommit(s *Sim) {
	advanced := false
	for idx := n.commit + 1; idx <= n.lastIndex(); idx++ {
		if n.log[idx-1].View != n.view {
			continue
		}
		voters := map[int]bool{n.id: true}
		for p, mi := range n.match {
			if mi >= idx {
				voters[p] = true
			}
		}
		if n.cfg.Quorum(voters) {
			n.commit = idx
			s.recordCert(idx, n.view, n.cfg, voters)
			s.tracef("node %d: commit index %d in view %d under %s with votes %v",
				n.id, idx, n.view, n.cfg, sortedSet(voters))
			n.applyCommitted(s)
			advanced = true
		}
	}
	if advanced && n.role == Leader {
		n.replicate(s) // broadcast the new commit index
	}
}

func (n *Node) applyCommitted(s *Sim) {
	for n.lastApplied < n.commit {
		e := n.log[n.lastApplied]
		n.lastApplied++
		if e.Cmd != "" {
			n.applied = append(n.applied, e.Cmd)
			s.tracef("node %d: apply %q at index %d", n.id, e.Cmd, e.Index)
		}
		if e.Cfg != nil {
			s.tracef("node %d: config %s committed at index %d", n.id, *e.Cfg, e.Index)
		}
	}
	if n.role == Leader {
		n.ensureSwitchCompletion(s)
		// A leader steps down only once a stable config that excludes it
		// is *committed* (not merely present in its log).
		cc := n.committedCfg()
		if !cc.Joint && len(cc.Old) > 0 && !contains(cc.Old, n.id) {
			s.tracef("node %d: not in committed config %s, stepping down", n.id, cc)
			n.role = Follower
		}
	}
}

// committedCfg is the configuration implied by committed entries only.
func (n *Node) committedCfg() Config {
	cfg := n.baseCfg
	for i := 0; i < n.commit && i < len(n.log); i++ {
		if n.log[i].Cfg != nil {
			cfg = *n.log[i].Cfg
		}
	}
	return cfg
}

// ensureSwitchCompletion finishes an in-progress reconfiguration: once the
// joint entry is committed (or found in the log by a new leader), the leader
// proposes the stable new config. This is what makes an interrupted switch
// recoverable by the next view's leader.
func (n *Node) ensureSwitchCompletion(s *Sim) {
	if n.role != Leader || !n.cfg.Joint {
		return
	}
	// The joint entry must be committed before the completion entry may
	// be proposed (Raft: Cnew only after Cold,new is committed).
	lastCfgIdx := -1
	for i, e := range n.log {
		if e.Cfg != nil {
			lastCfgIdx = i
		}
	}
	if lastCfgIdx < 0 || lastCfgIdx+1 > n.commit {
		return
	}
	for _, e := range n.log {
		if e.Cfg != nil && !e.Cfg.Joint && equalSets(e.Cfg.Old, n.cfg.New) {
			return // completion entry already in log
		}
	}
	target := Stable(n.cfg.New...)
	s.tracef("node %d: joint committed, proposing completion %s", n.id, target)
	n.appendEntry(s, Entry{Cfg: &target})
}

// appendEntry appends a leader-local entry and starts replicating it.
// A config entry takes effect immediately (Raft rule).
func (n *Node) appendEntry(s *Sim, e Entry) {
	e.Index = n.lastIndex() + 1
	e.View = n.view
	n.log = append(n.log, e)
	if e.Cfg != nil {
		n.cfg = *e.Cfg
	}
	n.match[n.id] = e.Index
	n.replicate(s)
}

func sortedSet(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
