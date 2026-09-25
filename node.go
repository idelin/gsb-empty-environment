package snaprepl

import (
	"crypto/sha256"
)

const (
	chunkSize         = 16 // bytes per snapshot chunk
	compactThreshold  = 10 // leader compacts once its log reaches this length
	heartbeatInterval = 10
	snapRetryInterval = 30
)

type Entry struct {
	Index int
	Term  int
	Cmd   string
}

type MsgKind int

const (
	MsgAppend MsgKind = iota
	MsgAppendReply
	MsgSnapChunk
	MsgSnapReply
)

// Msg is the single wire format. Fields are used per Kind:
//
//	MsgAppend:      PrevIndex/PrevTerm/Entries/LeaderCommit
//	MsgAppendReply: Success/Match
//	MsgSnapChunk:   SnapIndex/SnapTerm/Offset/Total/Data/Digest
//	MsgSnapReply:   Success/Installed, SnapIndex, Match (acked offset,
//	                installed snapshot index, or -1 for rollback nack)
type Msg struct {
	Kind MsgKind
	From int
	Term int

	PrevIndex    int
	PrevTerm     int
	Entries      []Entry
	LeaderCommit int

	SnapIndex int
	SnapTerm  int
	Offset    int
	Total     int
	Data      []byte
	Digest    [32]byte

	Success   bool
	Installed bool
	Match     int
}

// Node is one replica. Node 0 is the fixed leader; the simulation
// focuses on snapshot installation on lagging followers, not elections.
type Node struct {
	ID      int
	sim     *Sim
	cluster *Cluster

	Term int

	// Log entries live at log[Index-baseIndex-1]; everything at or
	// below baseIndex is covered by an installed snapshot.
	baseIndex int
	baseTerm  int
	log       []Entry

	commitIndex int
	lastApplied int
	SM          *KV

	staging *staging // incoming snapshot, invisible to readers

	isLeader bool
	next     []int
	match    []int
	snap     *snapshot
	snapSend map[int]*snapSend
}

func (n *Node) lastIndex() int { return n.baseIndex + len(n.log) }

func (n *Node) termAt(idx int) int {
	switch {
	case idx == n.baseIndex:
		return n.baseTerm
	case idx < n.baseIndex || idx > n.lastIndex():
		return -1
	default:
		return n.log[idx-n.baseIndex-1].Term
	}
}

// Read serves a client read from the local state machine. While a
// snapshot is being staged, readers keep seeing the old state; after
// the atomic publish they see only the new state. Never a mix.
func (n *Node) Read(key string) (string, bool) { return n.SM.Get(key) }

func (n *Node) send(to int, m Msg) { n.cluster.Net.Send(n.ID, to, m) }

func (n *Node) Deliver(m Msg) {
	switch m.Kind {
	case MsgAppend:
		n.onAppend(m)
	case MsgAppendReply:
		n.onAppendReply(m)
	case MsgSnapChunk:
		n.onSnapChunk(m)
	case MsgSnapReply:
		n.onSnapReply(m)
	}
}

// ---- leader side ----

// Propose appends a client command to the leader's log.
func (n *Node) Propose(cmd string) {
	if !n.isLeader {
		return
	}
	e := Entry{Index: n.lastIndex() + 1, Term: n.Term, Cmd: cmd}
	n.log = append(n.log, e)
	n.match[n.ID] = e.Index
	n.cluster.trace("t=%d client write -> leader log[%d] %q", n.sim.Now(), e.Index, cmd)
	n.broadcast()
}

func (n *Node) broadcast() {
	for id := range n.cluster.Nodes {
		if id != n.ID {
			n.sendAppend(id)
		}
	}
}

func (n *Node) sendAppend(to int) {
	ni := n.next[to]
	if ni <= n.baseIndex {
		n.sendSnapshot(to) // follower is behind the compacted prefix
		return
	}
	prev := ni - 1
	entries := append([]Entry(nil), n.log[ni-n.baseIndex-1:]...)
	n.cluster.Net.Send(n.ID, to, Msg{
		Kind: MsgAppend, From: n.ID, Term: n.Term,
		PrevIndex: prev, PrevTerm: n.termAt(prev),
		Entries: entries, LeaderCommit: n.commitIndex,
	})
}

func (n *Node) onAppendReply(m Msg) {
	if !n.isLeader {
		return
	}
	if m.Success {
		if m.Match > n.match[m.From] {
			n.match[m.From] = m.Match
		}
		if n.next[m.From] < m.Match+1 {
			n.next[m.From] = m.Match + 1
		}
		n.advanceCommit()
	} else {
		if n.next[m.From] > 1 {
			n.next[m.From]--
		}
		n.sendAppend(m.From)
	}
}

func (n *Node) advanceCommit() {
	if !n.isLeader {
		return
	}
	old := n.commitIndex
	for idx := n.commitIndex + 1; idx <= n.lastIndex(); idx++ {
		cnt := 0
		for id := range n.cluster.Nodes {
			if n.match[id] >= idx {
				cnt++
			}
		}
		if cnt*2 > len(n.cluster.Nodes) && n.termAt(idx) == n.Term {
			n.commitIndex = idx
		}
	}
	if n.commitIndex != old {
		n.cluster.trace("t=%d leader: commitIndex %d -> %d", n.sim.Now(), old, n.commitIndex)
	}
	n.applyCommitted()
	n.maybeCompact()
}

// maybeCompact snapshots the committed state and drops the log prefix.
// Entries beyond commitIndex (not yet committed) are kept.
func (n *Node) maybeCompact() {
	if !n.isLeader || n.commitIndex-n.baseIndex < compactThreshold {
		return
	}
	term := n.termAt(n.commitIndex)
	data := n.SM.Encode()
	n.snap = &snapshot{Index: n.commitIndex, Term: term, Data: data, Digest: sha256.Sum256(data)}
	n.log = append([]Entry(nil), n.log[n.commitIndex-n.baseIndex:]...)
	n.baseIndex = n.commitIndex
	n.baseTerm = term
	n.cluster.trace("t=%d leader: compacted log through index %d (snapshot %d bytes, digest %x)",
		n.sim.Now(), n.baseIndex, len(data), n.snap.Digest[:4])
}

func (n *Node) sendSnapshot(to int) {
	if n.snap == nil {
		return
	}
	if ss, ok := n.snapSend[to]; ok && ss.index == n.snap.Index {
		return // stream in flight; the retry timer covers losses
	}
	ss := &snapSend{index: n.snap.Index}
	n.snapSend[to] = ss
	n.cluster.trace("t=%d leader: follower %d fell behind the log, streaming snapshot index=%d (%d bytes)",
		n.sim.Now(), to, n.snap.Index, len(n.snap.Data))
	n.sendChunks(to, ss)
	n.scheduleSnapRetry(to, ss)
}

func (n *Node) sendChunks(to int, ss *snapSend) {
	data := n.snap.Data
	if len(data) == 0 {
		n.sendChunk(to, 0, nil)
		return
	}
	for off := 0; off < len(data); off += chunkSize {
		end := off + chunkSize
		if end > len(data) {
			end = len(data)
		}
		n.sendChunk(to, off, data[off:end])
	}
}

func (n *Node) sendChunk(to, off int, data []byte) {
	n.cluster.Net.Send(n.ID, to, Msg{
		Kind: MsgSnapChunk, From: n.ID, Term: n.Term,
		SnapIndex: n.snap.Index, SnapTerm: n.snap.Term,
		Offset: off, Total: len(n.snap.Data), Data: data, Digest: n.snap.Digest,
	})
}

func (n *Node) scheduleSnapRetry(to int, ss *snapSend) {
	n.sim.After(snapRetryInterval, func() {
		if n.snap == nil || n.snap.Index != ss.index {
			return
		}
		if cur, ok := n.snapSend[to]; !ok || cur != ss {
			return
		}
		n.sendChunks(to, ss) // retransmit until installed or superseded
		n.scheduleSnapRetry(to, ss)
	})
}

func (n *Node) onSnapReply(m Msg) {
	if !n.isLeader {
		return
	}
	ss, ok := n.snapSend[m.From]
	if m.Installed {
		if ok && ss.index == m.SnapIndex {
			delete(n.snapSend, m.From)
		}
		if m.Match > n.match[m.From] {
			n.match[m.From] = m.Match
		}
		if n.next[m.From] < m.Match+1 {
			n.next[m.From] = m.Match + 1
		}
		n.cluster.trace("t=%d leader: follower %d installed snapshot %d, resuming log replication",
			n.sim.Now(), m.From, m.Match)
		n.advanceCommit()
		n.sendAppend(m.From)
		return
	}
	if !ok || ss.index != m.SnapIndex {
		return // stale reply for a superseded snapshot
	}
	if !m.Success {
		n.sendChunks(m.From, ss) // follower rolled back: resend at once
	}
}

// ---- follower side ----

func (n *Node) onAppend(m Msg) {
	ok := true
	switch {
	case m.PrevIndex > n.lastIndex():
		ok = false // gap: entries missing before PrevIndex
	case m.PrevIndex > n.baseIndex && n.termAt(m.PrevIndex) != m.PrevTerm:
		ok = false
	case m.PrevIndex == n.baseIndex && n.baseTerm != m.PrevTerm:
		ok = false
	}
	if ok {
		for _, e := range m.Entries {
			if e.Index <= n.baseIndex {
				continue // covered by installed snapshot: skip, never re-apply
			}
			li := e.Index - n.baseIndex - 1
			switch {
			case li < len(n.log):
				if n.log[li].Term != e.Term {
					n.log = append(n.log[:li], e) // conflict: truncate
				} // else duplicate delivery: already have it
			case li == len(n.log):
				n.log = append(n.log, e)
			default:
				ok = false // gap inside the batch
			}
			if !ok {
				break
			}
		}
	}
	if ok {
		if m.LeaderCommit > n.commitIndex {
			n.commitIndex = min(m.LeaderCommit, n.lastIndex())
		}
		n.applyCommitted()
	}
	n.send(m.From, Msg{Kind: MsgAppendReply, From: n.ID, Term: n.Term, Success: ok, Match: n.lastIndex()})
}

func (n *Node) onSnapChunk(m Msg) {
	if m.SnapIndex <= n.lastApplied {
		// The local state machine is already at or beyond this snapshot
		// (duplicate, reordered or stale stream). Installing it would
		// roll the state machine back, so just acknowledge it.
		n.send(m.From, Msg{Kind: MsgSnapReply, From: n.ID, Term: n.Term,
			Success: true, Installed: true, SnapIndex: m.SnapIndex, Match: m.SnapIndex})
		return
	}
	if n.staging == nil || n.staging.index != m.SnapIndex || !n.staging.consistent(m) {
		// New snapshot (or inconsistent metadata): discard staged bytes.
		n.staging = newStaging(m.SnapIndex, m.SnapTerm, m.Total, m.Digest)
		n.cluster.trace("t=%d node %d: staging snapshot index=%d (old state keeps serving reads)",
			n.sim.Now(), n.ID, m.SnapIndex)
	}
	n.staging.add(m.Offset, m.Data)
	if !n.staging.complete() {
		return // missing chunks: nothing is published
	}
	if m.SnapIndex <= n.lastApplied {
		// Log entries applied while staging made the snapshot stale.
		n.staging = nil
		n.send(m.From, Msg{Kind: MsgSnapReply, From: n.ID, Term: n.Term,
			Success: true, Installed: true, SnapIndex: m.SnapIndex, Match: m.SnapIndex})
		return
	}
	data := n.staging.assemble()
	if sha256.Sum256(data) != n.staging.digest {
		// Digest mismatch: roll back. Old state is untouched.
		n.staging = nil
		n.cluster.Mon.RecordRollback(n.ID, m.SnapIndex)
		n.cluster.trace("t=%d node %d: snapshot %d digest mismatch, rolled back staged bytes",
			n.sim.Now(), n.ID, m.SnapIndex)
		n.send(m.From, Msg{Kind: MsgSnapReply, From: n.ID, Term: n.Term,
			Success: false, SnapIndex: m.SnapIndex, Match: -1})
		return
	}
	n.publishSnapshot(m.SnapIndex, m.SnapTerm, data)
	n.send(m.From, Msg{Kind: MsgSnapReply, From: n.ID, Term: n.Term,
		Success: true, Installed: true, SnapIndex: m.SnapIndex, Match: m.SnapIndex})
}

// publishSnapshot atomically swaps the state machine. Entries beyond
// the snapshot index are kept; covered entries are dropped so they can
// never be applied a second time.
func (n *Node) publishSnapshot(index, term int, data []byte) {
	kv := NewKV()
	if err := kv.Restore(data); err != nil {
		n.staging = nil
		n.cluster.Mon.RecordRollback(n.ID, index)
		return
	}
	n.SM = kv
	kept := make([]Entry, 0, len(n.log))
	for _, e := range n.log {
		if e.Index > index {
			kept = append(kept, e)
		}
	}
	n.log = kept
	n.baseIndex = index
	n.baseTerm = term
	if n.commitIndex < index {
		n.commitIndex = index
	}
	if n.lastApplied < index {
		n.lastApplied = index
	}
	n.staging = nil
	n.cluster.Mon.RecordPublish(n.ID, index)
	n.cluster.trace("t=%d node %d: snapshot %d published atomically; log entries <= %d skipped from now on",
		n.sim.Now(), n.ID, index, index)
	n.applyCommitted()
}

// applyCommitted applies committed entries in index order, exactly once.
func (n *Node) applyCommitted() {
	for n.lastApplied < n.commitIndex {
		next := n.lastApplied + 1
		if next <= n.baseIndex {
			n.lastApplied = next // included in the installed snapshot
			continue
		}
		e := n.log[next-n.baseIndex-1]
		n.SM.Apply(e.Cmd)
		n.lastApplied = next
		n.cluster.Mon.RecordApply(n.ID, e)
	}
}
