package permcompile

// TraceKind 标记轨迹条目类型。
type TraceKind string

const (
	TraceEvent   TraceKind = "event"
	TraceAuth    TraceKind = "authorize"
	TraceCommit  TraceKind = "commit"
	TraceExpired TraceKind = "expired"
)

// LeafSnapshot 是某一时刻一片叶子在某个可能世界中的剩余配额快照。
type LeafSnapshot struct {
	Leaf      int
	Object    ObjectID
	Gen       Generation
	NodeState nodeState
	Bytes     IntervalSet
	Reads     IntervalSet
}

// WorldSnapshot 是一个可能世界的可观察状态。
type WorldSnapshot struct {
	Nodes  []nodeState
	Leaves []LeafSnapshot
}

// TraceEntry 是全轨迹解释器消费的单步记录。
type TraceEntry struct {
	Kind       TraceKind
	Detail     string
	Verdict    Verdict
	Waiting    []WaitEvent
	WorldCount int
	Worlds     []WorldSnapshot
}

func (r *Runtime) snapshotLocked() []WorldSnapshot {
	out := make([]WorldSnapshot, 0, len(r.worlds))
	for _, w := range r.worlds {
		ws := WorldSnapshot{
			Nodes:  append([]nodeState(nil), w.state...),
			Leaves: make([]LeafSnapshot, 0, len(r.cp.leaves)),
		}
		for leaf, g := range r.cp.leaves {
			node := r.cp.leafNode[leaf]
			ws.Leaves = append(ws.Leaves, LeafSnapshot{
				Leaf:      leaf,
				Object:    g.object,
				Gen:       g.gen,
				NodeState: w.state[node],
				Bytes:     append(IntervalSet(nil), w.leaves[leaf].bytes...),
				Reads:     append(IntervalSet(nil), w.leaves[leaf].reads...),
			})
		}
		out = append(out, ws)
	}
	return out
}

func (r *Runtime) recordEvent(ev Event, suffix string) {
	detail := ev.Kind.String() + " gate=" + ev.Gate
	if ev.Option != "" {
		detail += " option=" + ev.Option
	}
	if suffix != "" {
		detail += " (" + suffix + ")"
	}
	r.trace = append(r.trace, TraceEntry{
		Kind:       TraceEvent,
		Detail:     detail,
		WorldCount: len(r.worlds),
		Worlds:     r.snapshotLocked(),
	})
}

func (r *Runtime) recordCommit(req ReadRequest, worldCount int) {
	r.trace = append(r.trace, TraceEntry{
		Kind:       TraceCommit,
		Detail:     formatReq(req),
		WorldCount: worldCount,
		Worlds:     r.snapshotLocked(),
	})
}

// RecordAuth 把一次判定写入轨迹（不影响状态），供解释器核对。
func (r *Runtime) RecordAuth(req ReadRequest, d Decision) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace = append(r.trace, TraceEntry{
		Kind:       TraceAuth,
		Detail:     formatReq(req),
		Verdict:    d.Verdict,
		Waiting:    append([]WaitEvent(nil), d.Waiting...),
		WorldCount: len(r.worlds),
		Worlds:     r.snapshotLocked(),
	})
}

// Trace 返回轨迹副本。
func (r *Runtime) Trace() []TraceEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]TraceEntry, len(r.trace))
	copy(out, r.trace)
	return out
}

// Snapshot 返回当前全部可能世界的配额快照。
func (r *Runtime) Snapshot() []WorldSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

func formatReq(req ReadRequest) string {
	return "object=" + string(req.Object) +
		" gen=" + u64(uint64(req.Gen)) +
		" range=[" + u64(req.Range.Lo) + "," + u64(req.Range.Hi) + ")"
}
