package batcher

// ItemCheckpoint captures one request's identity and confirmed state.
type ItemCheckpoint struct {
	Request   Request
	Confirmed bool
	Version   uint64
	Value     string
	Conflict  bool
}

// Checkpoint is a restorable snapshot: request identities, confirmed
// versions, and the missing set still awaiting dispatch.
type Checkpoint struct {
	Items   []ItemCheckpoint // in original input order
	Missing []string         // IDs of items still pending
}

// Save snapshots the engine. Confirmed versions are preserved so that
// stale or replayed responses after a restore cannot roll them back.
func (e *Engine) Save() Checkpoint {
	cp := Checkpoint{Items: make([]ItemCheckpoint, 0, len(e.order))}
	for _, id := range e.order {
		it := e.items[id]
		cp.Items = append(cp.Items, ItemCheckpoint{
			Request:   it.req,
			Confirmed: it.confirmed,
			Version:   it.version,
			Value:     it.value,
			Conflict:  it.conflict,
		})
		if !it.confirmed && !it.conflict {
			cp.Missing = append(cp.Missing, id)
		}
	}
	return cp
}

// Restore rebuilds an Engine from a checkpoint. Only the missing set
// will be dispatched again, with original request IDs intact; confirmed
// items keep their versions and are never re-sent.
func Restore(t Transport, batchSize, maxAttempts int, cp Checkpoint) (*Engine, error) {
	if batchSize <= 0 {
		batchSize = len(cp.Items)
		if batchSize == 0 {
			batchSize = 1
		}
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	e := &Engine{
		transport:   t,
		batchSize:   batchSize,
		maxAttempts: maxAttempts,
		items:       make(map[string]*itemState, len(cp.Items)),
	}
	missing := make(map[string]bool, len(cp.Missing))
	for _, id := range cp.Missing {
		missing[id] = true
	}
	for _, ic := range cp.Items {
		if _, dup := e.items[ic.Request.ID]; dup {
			return nil, errCheckpointInconsistent
		}
		it := &itemState{
			req:       ic.Request,
			confirmed: ic.Confirmed,
			version:   ic.Version,
			value:     ic.Value,
			conflict:  ic.Conflict,
		}
		if missing[ic.Request.ID] && (it.confirmed || it.conflict) {
			return nil, errCheckpointInconsistent
		}
		e.order = append(e.order, ic.Request.ID)
		e.items[ic.Request.ID] = it
	}
	for _, id := range cp.Missing {
		if _, ok := e.items[id]; !ok {
			return nil, errCheckpointInconsistent
		}
	}
	return e, nil
}
