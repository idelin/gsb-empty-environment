package tree

// Checkpoint captures everything needed to resume merging later: the full
// canonical operation log plus a cached snapshot for fast reads.
//
// Recovery is total-order replay, not incremental patching: Resume combines
// the checkpointed log with the follow-up operations and re-replays the
// union from scratch. This is what guarantees that a late-arriving early
// operation correctly rolls back and re-decides every later operation, and
// that any batching of the same operation set converges to the same parent
// mapping and rejection list.
type Checkpoint struct {
	Log   []Op     // every op merged so far, including rejected duplicates
	State Snapshot // cached replayed state, valid for reads as-is
}

// Checkpoint snapshots the engine. The engine remains usable afterwards.
func (e *Engine) Checkpoint() Checkpoint {
	return Checkpoint{
		Log:   append([]Op(nil), e.log...),
		State: e.Snapshot(),
	}
}

// Resume restores an engine from a checkpoint and merges follow-up
// operations. Resume(cp, more...) is guaranteed to equal merging
// cp.Log and more... into a fresh engine in any order.
func Resume(cp Checkpoint, more ...Op) *Engine {
	e := NewEngine()
	all := make([]Op, 0, len(cp.Log)+len(more))
	all = append(all, cp.Log...)
	all = append(all, more...)
	e.Merge(all...)
	return e
}
