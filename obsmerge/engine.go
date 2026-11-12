package obsmerge

import (
	"fmt"
	"slices"
	"sort"
	"sync"
)

// fieldState holds every retained piece of evidence for one key/field.
type fieldState[T any] struct {
	evidence []Evidence[T]
}

type keyState[T any] struct {
	fields map[string]*fieldState[T]
}

// Engine incrementally merges observations and supports retraction and replay.
// It is safe for concurrent use.
type Engine[T any] struct {
	mu        sync.RWMutex
	priority  PriorityFunc
	nextSeq   int64
	obs       map[string]Observation[T] // observation ID -> observation
	byKey     map[string]*keyState[T]   // business key -> state
	obsKey    map[string]string         // observation ID -> business key
	knownKeys map[string]struct{}
}

// NewEngine creates an Engine. priority may be nil, in which case every
// source has priority 0 and all disagreements surface as conflicts.
func NewEngine[T any](priority PriorityFunc) *Engine[T] {
	if priority == nil {
		priority = StaticPriority(0, nil)
	}
	return &Engine[T]{
		priority:  priority,
		nextSeq:   1,
		obs:       make(map[string]Observation[T]),
		byKey:     make(map[string]*keyState[T]),
		obsKey:    make(map[string]string),
		knownKeys: make(map[string]struct{}),
	}
}

// Apply ingests one observation. A later observation with the same ID is
// rejected; retract it first if a replacement is intended. Fields outside
// CoveredFields are ignored by construction.
func (e *Engine[T]) Apply(obs Observation[T]) error {
	if err := validate(obs); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.obs[obs.ID]; exists {
		return fmt.Errorf("obsmerge: observation %q already applied", obs.ID)
	}

	if obs.Seq == 0 {
		obs.Seq = e.nextSeq
	}
	if obs.Seq >= e.nextSeq {
		e.nextSeq = obs.Seq + 1
	}

	e.obs[obs.ID] = obs
	e.obsKey[obs.ID] = obs.BusinessKey
	e.knownKeys[obs.BusinessKey] = struct{}{}

	ks := e.byKey[obs.BusinessKey]
	if ks == nil {
		ks = &keyState[T]{fields: make(map[string]*fieldState[T])}
		e.byKey[obs.BusinessKey] = ks
	}

	for _, field := range obs.CoveredFields {
		fs := ks.fields[field]
		if fs == nil {
			fs = &fieldState[T]{}
			ks.fields[field] = fs
		}

		// The same source only ever keeps its newest evidence for this field.
		fs.evidence = slices.DeleteFunc(fs.evidence, func(existing Evidence[T]) bool {
			return existing.SourceID == obs.SourceID
		})

		n := obs.Fields[field] // zero Nullable means explicit empty when covered
		fs.evidence = append(fs.evidence, Evidence[T]{
			SourceID:      obs.SourceID,
			SourceVersion: obs.SourceVersion,
			PageType:      obs.PageType,
			ObservationID: obs.ID,
			Seq:           obs.Seq,
			Priority:      e.priority(obs.SourceID, field),
			Null:          n.Null,
			Value:         n.Value,
		})
	}
	return nil
}

// Retract removes one previously applied observation and recomputes only the
// affected business key from the remaining observations. It returns
// ErrNotFound if no observation with that ID exists.
func (e *Engine[T]) Retract(observationID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	bk, ok := e.obsKey[observationID]
	if !ok {
		return fmt.Errorf("%w: observation %q", ErrNotFound, observationID)
	}
	delete(e.obs, observationID)
	delete(e.obsKey, observationID)

	stillReferenced := false
	for _, obs := range e.obs {
		if obs.BusinessKey == bk {
			stillReferenced = true
			break
		}
	}
	if !stillReferenced {
		delete(e.byKey, bk)
		delete(e.knownKeys, bk)
		return nil
	}
	e.rebuildKeyLocked(bk)
	return nil
}

// rebuildKeyLocked reconstructs the state of one key by re-folding every
// retained observation that belongs to it, in deterministic Seq order.
func (e *Engine[T]) rebuildKeyLocked(businessKey string) {
	var retained []Observation[T]
	for _, obs := range e.obs {
		if obs.BusinessKey == businessKey {
			retained = append(retained, obs)
		}
	}

	ks := &keyState[T]{fields: make(map[string]*fieldState[T])}
	if len(retained) == 0 {
		e.byKey[businessKey] = ks
		return
	}

	sort.Slice(retained, func(i, j int) bool {
		if retained[i].Seq != retained[j].Seq {
			return retained[i].Seq < retained[j].Seq
		}
		return retained[i].ID < retained[j].ID
	})

	for _, obs := range retained {
		for _, field := range obs.CoveredFields {
			fs := ks.fields[field]
			if fs == nil {
				fs = &fieldState[T]{}
				ks.fields[field] = fs
			}
			fs.evidence = slices.DeleteFunc(fs.evidence, func(existing Evidence[T]) bool {
				return existing.SourceID == obs.SourceID
			})
			n := obs.Fields[field]
			fs.evidence = append(fs.evidence, Evidence[T]{
				SourceID:      obs.SourceID,
				SourceVersion: obs.SourceVersion,
				PageType:      obs.PageType,
				ObservationID: obs.ID,
				Seq:           obs.Seq,
				Priority:      e.priority(obs.SourceID, field),
				Null:          n.Null,
				Value:         n.Value,
			})
		}
	}
	e.byKey[businessKey] = ks
}

// View returns the resolved record for one business key, including only fields
// that at least one retained observation has covered. Use Field to also read
// never-observed fields as StatusUnknown.
func (e *Engine[T]) View(businessKey string) (RecordView[T], error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	ks, ok := e.byKey[businessKey]
	if !ok {
		return RecordView[T]{}, fmt.Errorf("%w: business key %q", ErrNotFound, businessKey)
	}
	return e.resolveLocked(businessKey, ks), nil
}

// Field returns one resolved field. A field never covered by any retained
// observation resolves to StatusUnknown rather than an error.
func (e *Engine[T]) Field(businessKey, field string) (FieldView[T], error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	ks, ok := e.byKey[businessKey]
	if !ok {
		return FieldView[T]{Field: field, Status: StatusUnknown}, nil
	}
	fs := ks.fields[field]
	if fs == nil {
		return FieldView[T]{Field: field, Status: StatusUnknown}, nil
	}
	return resolveField[T](field, fs), nil
}

// Snapshot resolves every business key currently known to the engine.
func (e *Engine[T]) Snapshot() []RecordView[T] {
	e.mu.RLock()
	defer e.mu.RUnlock()

	keys := make([]string, 0, len(e.knownKeys))
	for k := range e.knownKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]RecordView[T], 0, len(keys))
	for _, k := range keys {
		out = append(out, e.resolveLocked(k, e.byKey[k]))
	}
	return out
}

func (e *Engine[T]) resolveLocked(businessKey string, ks *keyState[T]) RecordView[T] {
	rec := RecordView[T]{
		BusinessKey: businessKey,
		Fields:      make(map[string]FieldView[T]),
	}
	fieldNames := make([]string, 0, len(ks.fields))
	for name := range ks.fields {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)
	for _, name := range fieldNames {
		rec.Fields[name] = resolveField[T](name, ks.fields[name])
	}
	return rec
}

func resolveField[T any](field string, fs *fieldState[T]) FieldView[T] {
	fv := FieldView[T]{Field: field, Status: StatusUnknown}
	if len(fs.evidence) == 0 {
		return fv
	}

	sources := append([]Evidence[T](nil), fs.evidence...)
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].Priority != sources[j].Priority {
			return sources[i].Priority > sources[j].Priority
		}
		if sources[i].Seq != sources[j].Seq {
			return sources[i].Seq < sources[j].Seq
		}
		return sources[i].SourceID < sources[j].SourceID
	})
	fv.Sources = sources

	top := sources[0].Priority
	winners := make([]Evidence[T], 0)
	for _, ev := range sources {
		if ev.Priority != top {
			break
		}
		winners = append(winners, ev)
	}
	fv.Winners = winners

	switch {
	case len(winners) == 1 && winners[0].Null:
		fv.Status = StatusDeleted
	case len(winners) == 1:
		fv.Status = StatusResolved
		fv.Value = winners[0].Value
	case len(winners) > 1 && allNull(winners):
		// Every top-priority source agrees the value is empty.
		fv.Status = StatusDeleted
	default:
		// Equal-priority disagreement: retain every side, never let arrival
		// order pick a winner.
		fv.Status = StatusConflict
		fv.Candidates = winners
	}
	return fv
}

func allNull[T any](evs []Evidence[T]) bool {
	for _, ev := range evs {
		if !ev.Null {
			return false
		}
	}
	return true
}

// Keys returns all known business keys in sorted order.
func (e *Engine[T]) Keys() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	keys := make([]string, 0, len(e.knownKeys))
	for k := range e.knownKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Observations returns the IDs of all retained observations in sorted order.
func (e *Engine[T]) Observations() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	ids := make([]string, 0, len(e.obs))
	for id := range e.obs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Observation returns a copy of one retained observation.
func (e *Engine[T]) Observation(id string) (Observation[T], error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	obs, ok := e.obs[id]
	if !ok {
		return Observation[T]{}, fmt.Errorf("%w: observation %q", ErrNotFound, id)
	}
	return obs, nil
}
