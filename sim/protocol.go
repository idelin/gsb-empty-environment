package sim

import "fmt"

// Entry is one replicated log entry. Index is 1-based and contiguous.
type Entry struct {
	Index int
	Data  string
}

// Snapshot is the durable prefix a node can hold after log compaction.
type Snapshot struct {
	LastIndex int
	Hash      uint64 // state-machine hash over entries 1..LastIndex
}

// Protocol message types exchanged on the simulator's links.
const (
	TypeProbe   = 1 // heartbeat / status probe
	TypeAppend  = 2 // log entries + reclaim watermark
	TypeAck     = 3 // follower status reply
	TypeInstall = 4 // leader -> follower snapshot
	TypeSnapAck = 5 // snapshot installed
	TypeRejoin  = 6 // explicitly readded (formerly evicted) node -> leader
)

// Message is a single protocol message.
type Message struct {
	From int
	To   int
	Kind int

	// TypeProbe / TypeAppend
	Probe bool

	// TypeAppend: predecessor consistency check.
	PrevIndex int

	// TypeAppend: log entries that follow PrevIndex.
	Entries []Entry

	// TypeAppend: leader's durable reclaim point. Followers may compact up to
	// min(Watermark, their own durable last index).
	Watermark int

	// TypeInstall: snapshot payload.
	Snap *Snapshot

	// TypeAck / TypeSnapAck: durable progress of the sender.
	SnapLast int
	Last     int
}

// EvictRecord documents why an offline node no longer gates prefix recycling:
// the node was explicitly removed; to re-enter it MUST supply or receive a
// snapshot at least at RequiredSnap, because logs <= ReclaimPoint may be gone.
type EvictRecord struct {
	Node         int
	AtTime       int64
	ReclaimPoint int
	RequiredSnap int
}

// durable is everything that survives a crash.
type durable struct {
	snap      Snapshot
	log       []Entry // entries after snap.LastIndex, contiguous
	evicted   bool
	evictNote *EvictRecord
}

func (d *durable) lastIndex() int {
	if n := len(d.log); n > 0 {
		return d.log[n-1].Index
	}
	return d.snap.LastIndex
}

func (d *durable) entryAt(index int) (Entry, bool) {
	if index <= d.snap.LastIndex {
		return Entry{}, false
	}
	off := index - d.snap.LastIndex - 1
	if off < 0 || off >= len(d.log) {
		return Entry{}, false
	}
	return d.log[off], true
}

// node is volatile protocol state, rebuilt from durable after restart.
type node struct {
	id           int
	leader       bool
	pending      map[int]Entry // received/created but not yet durable
	pendingAt    int64
	pendingSn    *Snapshot // received snapshot awaiting durability
	heartbeat    bool      // periodic heartbeat timer armed
	compactAfter int       // compact to this durable index after flush
	replyTo      int       // ack this node once persistence settles
}

// peer tracks one follower from the leader's perspective.
type peer struct {
	match    int // highest index known durable on the follower
	snapLast int
	joining  bool // re-entering via snapshot, not yet caught up
}

// System wires the protocol onto a Simulator.
type System struct {
	S *Simulator
	N int

	dur   []*durable
	nodes []*node

	peers   map[int]*peer
	records map[int]EvictRecord

	clientData map[int]string
	clientSeq  int

	// Observation histories used by tests to prove monotonicity.
	ReclaimSeries []int
	MatchSeries   map[int][]int

	Logf func(format string, args ...any)
}

const (
	leaderID        = 0
	heartbeatPeriod = 20
	persistDelay    = 3
)

func (sys *System) logf(format string, args ...any) {
	if sys.Logf != nil {
		sys.Logf(format, args...)
	}
}

// NewSystem builds n nodes; node 0 is the stable leader.
func NewSystem(s *Simulator, n int) *System {
	sys := &System{
		S:           s,
		N:           n,
		dur:         make([]*durable, n),
		nodes:       make([]*node, n),
		peers:       map[int]*peer{},
		records:     map[int]EvictRecord{},
		MatchSeries: map[int][]int{},
		clientData:  map[int]string{},
	}
	for i := 0; i < n; i++ {
		sys.dur[i] = &durable{}
		sys.nodes[i] = &node{id: i}
	}
	sys.nodes[leaderID].leader = true

	s.OnDeliver = sys.onDeliver
	s.OnTimer = sys.onTimer
	s.OnCallback = func(_ int64, name string) { sys.handleClientCallback(name) }

	// Leader starts its periodic heartbeat; followers only reply on traffic.
	sys.armHeartbeat(leaderID)
	return sys
}

// fnv1a64 is a self-contained FNV-1a hash so the state-machine digest uses
// only the standard library.
func fnv1a64(b []byte, h uint64) uint64 {
	const offset = 1469598103934665603
	const prime = 1099511628211
	if h == 0 {
		h = offset
	}
	for _, c := range b {
		h ^= uint64(c)
		h *= prime
	}
	return h
}

func hashData(h uint64, data string) uint64 {
	return fnv1a64([]byte(data), h)
}

// armHeartbeat schedules the next periodic heartbeat for the leader.
func (sys *System) armHeartbeat(id int) {
	nd := sys.nodes[id]
	if !nd.leader || nd.heartbeat {
		return
	}
	nd.heartbeat = true
	sys.S.Schedule(heartbeatPeriod, id, "heartbeat")
}

func (sys *System) onTimer(t int64, id int, name string) {
	nd := sys.nodes[id]
	switch name {
	case "heartbeat":
		nd.heartbeat = false
		if nd.leader {
			sys.broadcast(id)
			sys.armHeartbeat(id)
		}
	case "flush":
		sys.flush(id, t)
	}
}

func (sys *System) onDeliver(t int64, id int, m *Message) {
	if sys.dur[id].evicted && m.Kind != TypeRejoin {
		// An evicted node is outside the configuration until explicitly
		// readded; it silently discards replication traffic.
		return
	}
	switch m.Kind {
	case TypeProbe:
		sys.handleProbe(id, m)
	case TypeAppend:
		sys.handleAppend(id, m, t)
	case TypeAck:
		sys.handleAck(id, m, t)
	case TypeInstall:
		sys.handleInstall(id, m, t)
	case TypeSnapAck:
		sys.handleSnapAck(id, m, t)
	case TypeRejoin:
		sys.handleRejoin(id, m, t)
	}
}

// Evicted reports whether the node was explicitly removed from the config.
func (sys *System) Evicted(id int) bool { return sys.dur[id].evicted }

// EvictRecordFor returns the recorded snapshot re-entry requirement for node.
func (sys *System) EvictRecordFor(id int) (EvictRecord, bool) {
	r, ok := sys.records[id]
	return r, ok
}

// Submit appends a client command on the leader. The returned index is
// provisional: until a flush it lives only in memory and MUST NOT be counted
// as durable/acked. ok is false when the leader is crashed, in which case the
// caller retries later via SubmitWithRetry.
func (sys *System) Submit(data string) (index int, ok bool) {
	d := sys.dur[leaderID]
	nd := sys.nodes[leaderID]
	if !sys.S.Alive(leaderID) || d.evicted {
		return 0, false
	}
	idx := d.lastIndex() + 1
	if e, dup := nd.pending[idx]; dup {
		if e.Data != data {
			idx = sys.nextFreePending(leaderID, idx)
		}
	}
	sys.stage(leaderID, Entry{Index: idx, Data: data})
	return idx, true
}

func (sys *System) nextFreePending(id, idx int) int {
	for {
		if _, exists := sys.nodes[id].pending[idx]; !exists {
			return idx
		}
		idx++
	}
}

// SubmitWithRetry submits a command and schedules a client callback that
// retries the whole command until it is durable on the leader. A leader crash
// discards only memory-only entries, so the retry is exactly what real clients
// must do after losing an un-acknowledged append.
func (sys *System) SubmitWithRetry(data string) {
	sys.clientSeq++
	id := sys.clientSeq
	sys.clientData[id] = data
	sys.retryClient(id)
}

func (sys *System) retryClient(id int) {
	data := sys.clientData[id]
	idx, ok := sys.Submit(data)
	if !ok {
		sys.S.Schedule(heartbeatPeriod, -1, fmt.Sprintf("clientretry:%d", id))
		return
	}
	sys.S.Schedule(persistDelay+2, -1, fmt.Sprintf("clientcheck:%d:%d", id, idx))
}

func (sys *System) handleClientCallback(name string) {
	var id, idx int
	if n, _ := fmt.Sscanf(name, "clientretry:%d", &id); n == 1 {
		if _, ok := sys.clientData[id]; ok {
			sys.retryClient(id)
		}
		return
	}
	if n, _ := fmt.Sscanf(name, "clientcheck:%d:%d", &id, &idx); n != 2 {
		return
	}
	data, ok := sys.clientData[id]
	if !ok {
		return
	}
	if e, exists := sys.dur[leaderID].entryAt(idx); exists && e.Data == data {
		delete(sys.clientData, id) // committed to the client
		return
	}
	sys.retryClient(id)
}

// Crash simulates a process crash: volatile state (un-persisted entries,
// timers, leader peer bookkeeping) is discarded; durable state survives.
func (sys *System) Crash(id int) {
	sys.S.Crash(id)
	nd := sys.nodes[id]
	nd.pending = nil
	nd.pendingAt = 0
	nd.pendingSn = nil
	nd.heartbeat = false
	if nd.leader {
		// Leader identity survives (single stable leader model); peer progress
		// is volatile and must be rebuilt from follower status reports.
		sys.peers = map[int]*peer{}
	}
	sys.logf("t=%3d  node%d CRASH", sys.S.Now(), id)
}

// Restart brings the node back from durable storage.
func (sys *System) Restart(id int) {
	sys.S.Restart(id)
	nd := sys.nodes[id]
	nd.pending = map[int]Entry{}
	nd.pendingSn = nil
	nd.replyTo = -1
	nd.compactAfter = 0
	if nd.leader {
		sys.armHeartbeat(id)
	}
	sys.logf("t=%3d  node%d restart (durable snap=%d log=[%d..%d])",
		sys.S.Now(), id, sys.dur[id].snap.LastIndex,
		sys.dur[id].firstAfter(), sys.dur[id].lastIndex())
	if id != leaderID && !sys.dur[id].evicted {
		sys.reportStatus(id, leaderID)
	}
}

func (d *durable) firstAfter() int {
	if len(d.log) == 0 {
		return d.snap.LastIndex + 1
	}
	return d.log[0].Index
}

// stage puts an entry into volatile memory and arms a persistence flush.
// Nothing staged here is observable as durable/acked until flush completes.
func (sys *System) stage(id int, e Entry) {
	nd := sys.nodes[id]
	if nd.pending == nil {
		nd.pending = map[int]Entry{}
	}
	if existing, dup := nd.pending[e.Index]; dup && existing.Data == e.Data {
		return
	}
	nd.pending[e.Index] = e
	if nd.pendingAt == 0 {
		nd.pendingAt = sys.S.Now()
		sys.S.Schedule(persistDelay, id, "flush")
	}
}

// stageSnapshot marks a received snapshot for durability.
func (sys *System) stageSnapshot(id int, sn Snapshot) {
	nd := sys.nodes[id]
	nd.pendingSn = &sn
	sys.S.Schedule(persistDelay, id, "flush")
}

// flush makes all currently contiguous volatile state durable in one step.
func (sys *System) flush(id int, t int64) {
	nd := sys.nodes[id]
	d := sys.dur[id]

	installedSnap := false
	if nd.pendingSn != nil {
		sn := *nd.pendingSn
		nd.pendingSn = nil
		sys.installDurableSnapshot(id, sn)
		installedSnap = true
	}

	if len(nd.pending) > 0 {
		next := d.lastIndex() + 1
		var fresh []Entry
		for {
			e, ok := nd.pending[next]
			if !ok {
				break
			}
			fresh = append(fresh, e)
			delete(nd.pending, next)
			next++
		}
		if len(fresh) > 0 {
			d.log = append(d.log, fresh...)
		}
	}
	nd.pendingAt = 0

	if nd.leader {
		// Leader persistence is the ack point for its own entries.
		sys.tryAdvance(t)
		sys.broadcast(id)
		return
	}

	if nd.compactAfter > 0 {
		target := nd.compactAfter
		nd.compactAfter = 0
		if target <= d.lastIndex() {
			sys.compactTo(id, target)
		}
	}
	if installedSnap {
		sys.ackSnapshot(id, leaderID)
	} else if nd.replyTo >= 0 {
		// Reply after durability: an ack reports only durable progress.
		to := nd.replyTo
		nd.replyTo = -1
		if to >= 0 {
			sys.reportStatus(id, to)
		}
	}
	_ = t
}

// installDurableSnapshot replaces durable prefix+log with snapshot + later log.
func (sys *System) installDurableSnapshot(id int, sn Snapshot) {
	d := sys.dur[id]
	if sn.LastIndex <= d.snap.LastIndex {
		return
	}
	var later []Entry
	for _, e := range d.log {
		if e.Index > sn.LastIndex {
			later = append(later, e)
		}
	}
	d.snap = sn
	d.log = later
	// Entries covered by the snapshot that were only in memory are gone too.
	for idx := range sys.nodes[id].pending {
		if idx <= sn.LastIndex {
			delete(sys.nodes[id].pending, idx)
		}
	}
}

// broadcast sends one replication round from the leader to every follower.
func (sys *System) broadcast(leader int) {
	ld := sys.dur[leader]
	for f := 0; f < sys.N; f++ {
		if f == leader {
			continue
		}
		fd := sys.dur[f]
		if fd.evicted {
			// Explicitly removed: no traffic, and it no longer gates recycling.
			continue
		}
		p, known := sys.peers[f]
		if !known {
			// Volatile progress after leader (re)start: probe first, never
			// guess the follower's durable point.
			sys.send(Message{From: leader, To: f, Kind: TypeProbe, Probe: true})
			continue
		}

		if p.match < ld.snap.LastIndex {
			// Follower needs a prefix the leader has already compacted: the
			// ONLY safe path is a snapshot; deleted logs are never referenced.
			snap := ld.snap
			sys.send(Message{From: leader, To: f, Kind: TypeInstall, Snap: &snap})
			continue
		}

		msg := Message{
			From:      leader,
			To:        f,
			Kind:      TypeAppend,
			PrevIndex: p.match,
			Watermark: ld.snap.LastIndex,
		}
		start := p.match - ld.snap.LastIndex
		if start < 0 {
			start = 0
		}
		if start < len(ld.log) {
			msg.Entries = append([]Entry(nil), ld.log[start:]...)
		}
		sys.send(msg)
	}
}

func (sys *System) send(m Message) {
	sys.S.Send(m.From, &m)
}

// reportStatus is sent by a follower on restart or when probed.
func (sys *System) reportStatus(id, to int) {
	d := sys.dur[id]
	sys.send(Message{
		From: id, To: to, Kind: TypeAck,
		SnapLast: d.snap.LastIndex, Last: d.lastIndex(),
	})
}

func (sys *System) handleProbe(id int, m *Message) {
	if sys.nodes[id].leader {
		return
	}
	// Respond with true durable position; memory-only entries are excluded.
	sys.reportStatus(id, m.From)
}

func (sys *System) handleAck(leader int, m *Message, t int64) {
	if leader != leaderID || !sys.nodes[leader].leader {
		return
	}
	p, known := sys.peers[m.From]
	if !known {
		p = &peer{}
		sys.peers[m.From] = p
	}
	p.snapLast = m.SnapLast
	joining := p.joining

	// Monotonicity: stale acks can only report progress at or below the last
	// durable point the leader already recorded.
	if m.Last >= p.match {
		p.match = m.Last
	}
	sys.MatchSeries[m.From] = append(sys.MatchSeries[m.From], p.match)

	if joining && p.match >= sys.dur[leader].lastIndex() {
		// Snapshot-based re-entry complete: the node is a full member again.
		p.joining = false
		sys.logf("t=%3d  node%d rejoined at index %d", t, m.From, p.match)
	}
	sys.tryAdvance(t)
}

func (sys *System) handleAppend(id int, m *Message, t int64) {
	nd := sys.nodes[id]
	d := sys.dur[id]
	if nd.leader {
		return
	}
	if m.Probe {
		sys.reportStatus(id, m.From)
		return
	}
	if nd.pendingSn != nil {
		// Snapshot install in flight: it will re-sync once durable.
		return
	}

	// Stale message (reordering under jitter): a snapshot already moved us past
	// this predecessor. Silently discard; the leader's view is corrected by
	// status acks and never by acting on deleted prefixes.
	if m.PrevIndex < d.snap.LastIndex {
		return
	}
	if m.PrevIndex > d.lastIndex() {
		// Gap: ask the leader where we actually are so it sends a snapshot.
		sys.reportStatus(id, m.From)
		return
	}

	changed := false
	for _, e := range m.Entries {
		if e.Index <= d.snap.LastIndex {
			// Stale batch overtaken by a snapshot: ignore covered entries.
			continue
		}
		if existing, ok := d.entryAt(e.Index); ok {
			if existing.Data == e.Data {
				continue
			}
			// Conflicting tail: truncate at conflict, then merge.
			d.log = d.log[:e.Index-d.snap.LastIndex-1]
		}
		if _, inMemory := nd.pending[e.Index]; inMemory {
			nd.pending[e.Index] = e // overwrite conflicting un-persisted tail
		}
		sys.stage(id, e)
		nd.replyTo = m.From
		changed = true
	}

	// Reclaim watermark: compact only what BOTH the leader offered and we hold
	// durably. Never compact past our own last durable index.
	target := m.Watermark
	if target > d.lastIndex() {
		target = d.lastIndex()
	}
	if target > d.snap.LastIndex {
		if changed {
			// Apply+compact atomically once the new prefix is durable.
			nd.compactAfter = target
		} else {
			sys.compactTo(id, target)
		}
	}
	if changed {
		// Ack is sent from flush(), after the entries are durable.
		return
	}
	sys.reportStatus(id, m.From)
	_ = t
}

func (sys *System) handleInstall(id int, m *Message, t int64) {
	if sys.nodes[id].leader || m.Snap == nil {
		return
	}
	d := sys.dur[id]
	if m.Snap.LastIndex <= d.snap.LastIndex {
		// Stale snapshot; confirm current state so the leader re-evaluates.
		sys.ackSnapshot(id, m.From)
		return
	}
	sys.stageSnapshot(id, *m.Snap)
	sys.logf("t=%3d  node%d installing snapshot up to %d", t, id, m.Snap.LastIndex)
}

func (sys *System) handleSnapAck(leader int, m *Message, t int64) {
	if leader != leaderID {
		return
	}
	p, known := sys.peers[m.From]
	if !known {
		p = &peer{joining: true}
		sys.peers[m.From] = p
	}
	if m.SnapLast > p.match {
		p.match = m.SnapLast
	}
	p.snapLast = m.SnapLast
	sys.MatchSeries[m.From] = append(sys.MatchSeries[m.From], p.match)
	if p.joining && p.match >= sys.dur[leader].lastIndex() {
		p.joining = false
		sys.logf("t=%3d  node%d rejoined at index %d", t, m.From, p.match)
	}
	sys.tryAdvance(t)
}

func (sys *System) ackSnapshot(id, to int) {
	d := sys.dur[id]
	sys.send(Message{From: id, To: to, Kind: TypeSnapAck,
		SnapLast: d.snap.LastIndex, Last: d.lastIndex()})
}

// Evict explicitly removes an offline node from the configuration. This is the
// ONLY action that stops it gating prefix recycling. The record keeps the
// snapshot requirement any future re-entry must satisfy.
func (sys *System) Evict(id int, t int64) {
	if id == leaderID {
		panic("cannot evict the stable leader")
	}
	d := sys.dur[id]
	baseline := sys.dur[leaderID].snap.LastIndex
	d.evicted = true
	d.evictNote = &EvictRecord{Node: id, AtTime: t, ReclaimPoint: baseline}
	delete(sys.peers, id)
	// With the node out of the configuration the prefix can now advance; the
	// resulting point is the minimum snapshot any re-entry must provide.
	sys.tryAdvance(t)
	required := sys.dur[leaderID].snap.LastIndex
	rec := EvictRecord{Node: id, AtTime: t, ReclaimPoint: required, RequiredSnap: required}
	d.evictNote = &rec
	sys.records[id] = rec
	sys.logf("t=%3d  node%d EVICTED: re-entry requires snapshot >= %d (log prefix <= %d may be deleted)",
		t, id, rec.RequiredSnap, rec.ReclaimPoint)
}

// Readd wipes a previously evicted node (modeling a fresh/rebuilt replica) and
// lets it ask the leader for a snapshot. It starts with empty durable state,
// so it can only re-enter via snapshot - never via deleted log entries.
func (sys *System) Readd(id int, t int64) {
	d := sys.dur[id]
	if !d.evicted {
		panic(fmt.Sprintf("node%d is not evicted", id))
	}
	d.evicted = false
	d.evictNote = nil
	d.snap = Snapshot{}
	d.log = nil
	sys.nodes[id].pending = map[int]Entry{}
	sys.nodes[id].pendingSn = nil
	sys.nodes[id].compactAfter = 0
	sys.nodes[id].replyTo = -1
	sys.peers[id] = &peer{match: 0, snapLast: 0, joining: true}
	if sys.S.Alive(id) {
		sys.send(Message{From: id, To: leaderID, Kind: TypeRejoin})
	} else {
		// Offline readd: when it comes back later it probes; but the leader can
		// already start pushing the required snapshot once links exist.
	}
	sys.broadcast(leaderID)
	sys.logf("t=%3d  node%d readded (empty): requesting snapshot re-entry", t, id)
}

func (sys *System) handleRejoin(leader int, m *Message, t int64) {
	if leader != leaderID {
		return
	}
	p, known := sys.peers[m.From]
	if !known || !p.joining {
		sys.peers[m.From] = &peer{match: 0, snapLast: 0, joining: true}
	}
	// Fresh node has no logs; the next broadcast must choose InstallSnapshot
	// because match(0) < leader snapshot index.
	sys.broadcast(leader)
	_ = t
}

// tryAdvance computes the recyclable prefix and advances the leader snapshot.
//
//	reclaim = min durable last index across ALL active (non-evicted, non-
//	          re-entering) members, leader included.
//
// The point can only move forward (monotonic). Slow active followers hold it
// back; evicted followers disappear from the set; re-entering followers are
// not yet voting members.
func (sys *System) tryAdvance(t int64) {
	if !sys.S.Alive(leaderID) {
		return
	}
	reclaim := sys.dur[leaderID].lastIndex()
	for f := 0; f < sys.N; f++ {
		if f == leaderID {
			continue
		}
		if sys.dur[f].evicted {
			continue
		}
		p, known := sys.peers[f]
		if known && p.joining {
			continue
		}
		if !known {
			// Unknown active follower: assume nothing until its durable
			// status is reported. This blocks recycling conservatively.
			reclaim = 0
			break
		}
		if p.match < reclaim {
			reclaim = p.match
		}
	}
	if reclaim <= sys.dur[leaderID].snap.LastIndex {
		return
	}
	sys.compactTo(leaderID, reclaim)
	sys.ReclaimSeries = append(sys.ReclaimSeries, sys.dur[leaderID].snap.LastIndex)
	sys.logf("t=%3d  leader reclaim point advanced to %d", t, reclaim)
}

// compactTo applies entries through target and replaces the durable prefix
// with a snapshot. target must be <= the node's durable last index.
func (sys *System) compactTo(id, target int) {
	d := sys.dur[id]
	if target <= d.snap.LastIndex || target > d.lastIndex() {
		return
	}
	h := d.snap.Hash
	cut := target - d.snap.LastIndex
	for _, e := range d.log[:cut] {
		h = hashData(h, e.Data)
	}
	d.snap = Snapshot{LastIndex: target, Hash: h}
	d.log = d.log[cut:]
}
