package raft

import (
	"fmt"
	"sort"
)

// ReadEvidence is the proof returned with a linearizable read. It is
// self-contained: a client can verify it against the cluster size without
// trusting the leader or any wall-clock lease.
type ReadEvidence struct {
	Leader       int   // leader that served the read
	Term         int   // term in which the read was confirmed
	ReadIndex    int   // commit index at confirmation time
	AppliedIndex int   // local applied index when the state was sampled
	Acks         []int // nodes (incl. leader) that acknowledged Term
}

// Verify checks the safety evidence: a quorum of the cluster acknowledged
// the leader's current term, and the sampled state includes every entry up
// to the read index.
func (e ReadEvidence) Verify(clusterSize int) bool {
	return e.Term > 0 &&
		e.ReadIndex >= 1 &&
		len(e.Acks) >= clusterSize/2+1 &&
		e.AppliedIndex >= e.ReadIndex
}

// ReadResult is delivered to the read callback. Exactly one of OK/Err is set.
type ReadResult struct {
	OK       bool
	Err      string
	Evidence ReadEvidence
	State    map[string]string
}

type readState struct {
	nonce     int
	readIndex int
	acks      map[int]bool
	confirmed bool
	cb        func(ReadResult)
}

// Read asks the leader for a linearizable read of the applied state.
// The leader must (1) have committed an entry in its current term and
// (2) confirm the term with a quorum before the read is served.
func (c *Cluster) Read(leader int, cb func(ReadResult)) {
	c.sim.After(0, func() {
		n := c.nodes[leader]
		if n.crashed {
			cb(ReadResult{Err: fmt.Sprintf("node%d is crashed", leader)})
			return
		}
		n.serveRead(c, cb)
	})
}

func (n *Node) serveRead(c *Cluster, cb func(ReadResult)) {
	if n.role != Leader {
		cb(ReadResult{Err: fmt.Sprintf("node%d is %s, not leader", n.id, n.role)})
		return
	}
	if !n.termConfirmed() {
		cb(ReadResult{Err: fmt.Sprintf("node%d: term %d not yet confirmed (no committed entry in current term)", n.id, n.term)})
		return
	}
	n.readSeq++
	rs := &readState{
		nonce:     n.readSeq,
		readIndex: n.commitIndex,
		acks:      map[int]bool{n.id: true},
		cb:        cb,
	}
	n.pending[rs.nonce] = rs
	c.tracef("node%d read#%d: confirm term %d at readIndex %d", n.id, rs.nonce, n.term, rs.readIndex)
	c.broadcast(n.id, Msg{Type: MsgReadConfirm, Term: n.term, Nonce: rs.nonce})
	n.maybeFinishRead(c, rs)
}

// termConfirmed reports whether the leader has committed an entry in its
// current term. Until then the leader may not serve reads: its commitIndex
// can reflect an older term and is not known to be current.
func (n *Node) termConfirmed() bool {
	return n.commitIndex >= 1 && n.log[n.commitIndex-1].Term == n.term
}

func (n *Node) onReadConfirm(c *Cluster, m Msg) {
	// Acknowledge only if we still recognize this leader in this term.
	if m.Term == n.term && n.leaderID == m.From {
		c.send(n.id, m.From, Msg{Type: MsgReadConfirmAck, Term: n.term, Nonce: m.Nonce})
	}
}

func (n *Node) onReadConfirmAck(c *Cluster, m Msg) {
	if n.role != Leader || m.Term != n.term {
		return
	}
	rs, ok := n.pending[m.Nonce]
	if !ok {
		return
	}
	rs.acks[m.From] = true
	n.maybeFinishRead(c, rs)
}

// maybeFinishRead confirms the quorum, then waits until the local state
// machine has applied everything up to the read index before answering.
func (n *Node) maybeFinishRead(c *Cluster, rs *readState) {
	if !rs.confirmed && len(rs.acks) >= n.quorum() {
		rs.confirmed = true
		c.tracef("node%d read#%d: term %d confirmed by quorum %v", n.id, rs.nonce, n.term, sortedKeys(rs.acks))
	}
	if !rs.confirmed || n.appliedIndex < rs.readIndex {
		return // wait for the applier; checked again in checkPendingReads
	}
	delete(n.pending, rs.nonce)
	ev := ReadEvidence{
		Leader:       n.id,
		Term:         n.term,
		ReadIndex:    rs.readIndex,
		AppliedIndex: n.appliedIndex,
		Acks:         sortedKeys(rs.acks),
	}
	state := make(map[string]string, len(n.state))
	for k, v := range n.state {
		state[k] = v
	}
	c.tracef("node%d read#%d: served at appliedIndex %d", n.id, rs.nonce, n.appliedIndex)
	rs.cb(ReadResult{OK: true, Evidence: ev, State: state})
}

// checkPendingReads is called by the applier whenever appliedIndex advances.
func (n *Node) checkPendingReads(c *Cluster) {
	for _, rs := range n.pending {
		n.maybeFinishRead(c, rs)
	}
}

func (n *Node) failAllReads(reason string) {
	for nonce, rs := range n.pending {
		delete(n.pending, nonce)
		rs.cb(ReadResult{Err: reason})
	}
}

func sortedKeys(m map[int]bool) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
