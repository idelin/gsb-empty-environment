package paxos

import (
	"sync"
	"time"
)

// Config tunes a Node.
type Config struct {
	Lease       time.Duration // leader lease; while valid the leader skips Phase 1
	IncrEvery   uint64        // applied ops between incremental snapshots (0 = off)
	ChkEvery    uint64        // applied ops between full checkpoints (0 = off)
	SnapshotDir string        // snapshot archive dir ("" = snapshots disabled)
	Retain      int           // checkpoints to keep (<=0 keeps all)
}

// Node is a single Multi-Paxos replica: acceptor + proposer + learner.
// It spawns no goroutines; all progress is driven by Propose/Campaign and
// incoming messages, which also makes leak-checking trivial.
type Node struct {
	mu    sync.Mutex
	id    int
	peers []int // the other node ids
	hub   *Hub
	cfg   Config

	promised Ballot              // highest ballot promised (acceptor)
	seen     Ballot              // highest ballot this node has observed
	log      map[uint64]LogEntry // sparse accepted log (holes allowed)
	decided  map[uint64]bool     // slots chosen by a majority
	maxSlot  uint64
	commit   uint64 // commit index: every slot <= commit is decided
	compact  uint64 // slots <= compact were archived into a checkpoint

	isLeader   bool
	leaseUntil time.Time
	nextSlot   uint64 // proposer's next free slot

	prepares int // Phase-1 rounds run (instrumentation for tests)

	sm   *KV
	snap *Snapshotter
}

func NewNode(id int, peers []int, hub *Hub, cfg Config) (*Node, error) {
	n := &Node{
		id:      id,
		peers:   peers,
		hub:     hub,
		cfg:     cfg,
		log:     map[uint64]LogEntry{},
		decided: map[uint64]bool{},
		sm:      NewKV(),
	}
	if cfg.SnapshotDir != "" {
		snap, err := NewSnapshotter(cfg.SnapshotDir, cfg.Retain)
		if err != nil {
			return nil, err
		}
		n.snap = snap
	}
	return n, nil
}

func (n *Node) ID() int { return n.id }

func (n *Node) majority() int { return len(n.peers)/2 + 1 }

// ---- acceptor ----

// HandlePrepare processes Phase 1a.
func (n *Node) HandlePrepare(req PrepareReq) PrepareResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !req.Ballot.Greater(n.promised) {
		return PrepareResp{Ok: false, Promised: n.promised}
	}
	n.promised = req.Ballot
	if req.Ballot.Greater(n.seen) {
		n.seen = req.Ballot
	}
	n.isLeader = false // a higher proposer appeared: step down
	entries := make(map[uint64]LogEntry)
	for s, e := range n.log {
		if s >= req.From {
			entries[s] = e
		}
	}
	return PrepareResp{Ok: true, Promised: n.promised, Entries: entries, MaxSlot: n.maxSlot}
}

// HandleAccept processes Phase 2a. Accepting at a higher ballot overwrites
// any older uncommitted ("dirty") value in the same slot; decided slots are
// never overwritten.
func (n *Node) HandleAccept(req AcceptReq) AcceptResp {
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Ballot.Less(n.promised) {
		return AcceptResp{Ok: false, Promised: n.promised}
	}
	n.promised = req.Ballot
	if req.Ballot.Greater(n.seen) {
		n.seen = req.Ballot
	}
	e := req.Entry
	e.Ballot = req.Ballot
	if !n.decided[e.Slot] {
		if cur, ok := n.log[e.Slot]; !ok || !req.Ballot.Less(cur.Ballot) {
			n.log[e.Slot] = e
		}
	}
	if e.Slot > n.maxSlot {
		n.maxSlot = e.Slot
	}
	return AcceptResp{Ok: true, Promised: n.promised}
}

// HandleCommit processes a commit notice from the leader.
func (n *Node) HandleCommit(c CommitNotice) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.log[c.Slot]; !ok {
		return // entry missing (partition); a later SyncTo will repair it
	}
	n.decideLocked(c.Slot)
}

// HandleSnapshot installs a snapshot from the leader when this follower's
// log can no longer supply the missing slots (they were compacted).
func (n *Node) HandleSnapshot(m SnapshotMsg) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if m.Index <= n.commit {
		return // stale
	}
	n.sm.Restore(m.Index, m.State)
	n.commit = m.Index
	n.compactLocked(m.Index)
}

// ---- proposer ----

// Campaign runs Phase 1 to become leader: it gathers promises from a
// majority, repairs every slot above the commit index (re-proposing the
// highest-ballot value seen, or a noop to fill a hole), then takes the
// lease. Safe to call at any time; retries with a bumped ballot if another
// proposer raced it.
func (n *Node) Campaign() error {
	for attempt := 0; attempt < 5; attempt++ {
		won, err := n.campaignOnce()
		if err != nil {
			return err
		}
		if won {
			return nil
		}
	}
	return ErrNoMajority
}

func (n *Node) campaignOnce() (bool, error) {
	n.mu.Lock()
	n.seen = Ballot{N: n.seen.N + 1, Node: n.id}
	ballot := n.seen
	from := n.commit + 1
	n.mu.Unlock()

	// Promise to self first.
	if !n.HandlePrepare(PrepareReq{Ballot: ballot, From: from}).Ok {
		return false, nil
	}
	n.mu.Lock()
	n.prepares++
	n.mu.Unlock()

	majority := n.majority()
	oks := 1
	chosen := map[uint64]LogEntry{}
	n.mu.Lock()
	maxSeen := n.maxSlot
	for s, e := range n.log {
		if s >= from {
			chosen[s] = e
		}
	}
	n.mu.Unlock()

	for _, p := range n.peers {
		r, err := n.hub.Send(n.id, p, PrepareReq{Ballot: ballot, From: from})
		if err != nil {
			continue
		}
		resp := r.(PrepareResp)
		if !resp.Ok {
			n.mu.Lock()
			if resp.Promised.N >= n.seen.N {
				n.seen = Ballot{N: resp.Promised.N, Node: n.id}
			}
			n.mu.Unlock()
			continue
		}
		oks++
		if resp.MaxSlot > maxSeen {
			maxSeen = resp.MaxSlot
		}
		for s, e := range resp.Entries {
			if cur, ok := chosen[s]; !ok || e.Ballot.Greater(cur.Ballot) {
				chosen[s] = e
			}
		}
	}
	if oks < majority {
		return false, nil
	}

	// Phase 2: repair every slot in [from, maxSeen]. Slots no majority
	// member knows about are holes and get a noop.
	for s := from; s <= maxSeen; s++ {
		e, ok := chosen[s]
		if !ok {
			e = LogEntry{Slot: s, Op: Op{Kind: OpNoop}}
		}
		e.Slot = s
		if !n.broadcastAccept(ballot, e) {
			return false, nil
		}
	}

	n.mu.Lock()
	n.isLeader = true
	n.leaseUntil = time.Now().Add(n.cfg.Lease)
	if n.nextSlot < maxSeen+1 {
		n.nextSlot = maxSeen + 1
	}
	n.mu.Unlock()
	return true, nil
}

// Propose replicates one op. With a valid leader lease this is a single
// Phase-2 round (Phase 1 is skipped); otherwise ErrNotLeader is returned
// and the caller should Campaign first.
func (n *Node) Propose(op Op) (uint64, error) {
	n.mu.Lock()
	if !n.isLeader || !time.Now().Before(n.leaseUntil) {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	slot := n.nextSlot
	n.nextSlot++
	ballot := n.seen
	n.mu.Unlock()

	e := LogEntry{Slot: slot, Ballot: ballot, Op: op}
	if !n.broadcastAccept(ballot, e) {
		n.mu.Lock()
		n.isLeader = false
		n.mu.Unlock()
		return 0, ErrNoMajority
	}
	n.mu.Lock()
	n.leaseUntil = time.Now().Add(n.cfg.Lease) // activity extends the lease
	n.mu.Unlock()
	return slot, nil
}

// broadcastAccept runs Phase 2 for one entry and, on a majority, decides
// the slot locally and notifies followers.
func (n *Node) broadcastAccept(ballot Ballot, e LogEntry) bool {
	majority := n.majority()
	oks := 0
	if n.HandleAccept(AcceptReq{Ballot: ballot, Entry: e}).Ok {
		oks++
	}
	for _, p := range n.peers {
		r, err := n.hub.Send(n.id, p, AcceptReq{Ballot: ballot, Entry: e})
		if err != nil {
			continue
		}
		if r.(AcceptResp).Ok {
			oks++
		}
	}
	if oks < majority {
		return false
	}
	n.mu.Lock()
	n.decideLocked(e.Slot)
	n.mu.Unlock()
	for _, p := range n.peers {
		_, _ = n.hub.Send(n.id, p, CommitNotice{Slot: e.Slot})
	}
	return true
}

// SyncTo pushes every accepted entry and commit decision to a peer that
// fell behind (e.g. a partitioned minority node). Because the leader's
// ballot dominates, this safely overwrites the peer's dirty log slots.
func (n *Node) SyncTo(peer int) {
	n.mu.Lock()
	ballot := n.seen
	entries := make([]LogEntry, 0)
	decided := make([]uint64, 0)
	for s := n.compact + 1; s <= n.maxSlot; s++ {
		if e, ok := n.log[s]; ok {
			entries = append(entries, e)
		}
		if n.decided[s] {
			decided = append(decided, s)
		}
	}
	var snapMsg *SnapshotMsg
	if n.compact > 0 {
		idx, state := n.sm.Snapshot()
		snapMsg = &SnapshotMsg{Index: idx, State: state}
	}
	n.mu.Unlock()
	if snapMsg != nil {
		_, _ = n.hub.Send(n.id, peer, *snapMsg)
	}
	for _, e := range entries {
		e.Ballot = ballot
		_, _ = n.hub.Send(n.id, peer, AcceptReq{Ballot: ballot, Entry: e})
	}
	for _, s := range decided {
		_, _ = n.hub.Send(n.id, peer, CommitNotice{Slot: s})
	}
}

// ---- commit / apply / snapshot ----

// decideLocked marks a slot chosen and applies every newly-committable
// slot in order, so the state machine never observes holes.
func (n *Node) decideLocked(slot uint64) {
	n.decided[slot] = true
	for n.decided[n.commit+1] {
		n.commit++
		e, ok := n.log[n.commit]
		if !ok {
			break // compacted or not yet arrived; SyncTo will deliver it
		}
		n.applyLocked(e)
	}
}

func (n *Node) applyLocked(e LogEntry) {
	n.sm.Apply(e.Op)
	if n.snap == nil {
		return
	}
	applied := n.sm.Applied()
	if n.cfg.ChkEvery > 0 && applied%n.cfg.ChkEvery == 0 {
		idx, state := n.sm.Snapshot()
		if err := n.snap.WriteCheckpoint(idx, state); err == nil {
			n.sm.ResetJournal()
			n.compactLocked(idx)
		}
	} else if n.cfg.IncrEvery > 0 && applied%n.cfg.IncrEvery == 0 {
		from, to, ops := n.sm.Delta()
		if to > from {
			if err := n.snap.WriteIncremental(from, to, ops); err == nil {
				n.sm.ResetJournal() // next delta chains from this one
			}
		}
	}
}

// compactLocked drops log slots already archived in a checkpoint.
func (n *Node) compactLocked(upto uint64) {
	for s := range n.log {
		if s <= upto {
			delete(n.log, s)
		}
	}
	for s := range n.decided {
		if s <= upto {
			delete(n.decided, s)
		}
	}
	if upto > n.compact {
		n.compact = upto
	}
}

// Recover loads the newest snapshot (checkpoint + incrementals) into the
// state machine and positions the commit index. A no-op on a fresh archive.
func (n *Node) Recover() error {
	if n.snap == nil {
		return nil
	}
	snap, err := n.snap.Latest()
	if err == ErrNoSnapshot {
		return nil
	}
	if err != nil {
		return err
	}
	n.sm.Restore(snap.Index, snap.State)
	n.mu.Lock()
	n.commit = snap.Index
	n.compact = snap.Index
	n.mu.Unlock()
	return nil
}

// ---- test / demo introspection ----

func (n *Node) KV() *KV { return n.sm }

func (n *Node) CommitIndex() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.commit
}

func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.isLeader && time.Now().Before(n.leaseUntil)
}

// Prepares reports how many Phase-1 rounds this node has run; used to
// assert the lease fast path really skips Phase 1.
func (n *Node) Prepares() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.prepares
}

// Entry returns the accepted entry at a slot, for assertions.
func (n *Node) Entry(slot uint64) (LogEntry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.log[slot]
	return e, ok
}

// LogLen returns the number of retained (un-compacted) log slots.
func (n *Node) LogLen() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.log)
}
