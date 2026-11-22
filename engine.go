package impact

import (
	"errors"
	"fmt"
	"sort"
)

// Stats reports work performed by the most recent Analyze call.
type Stats struct {
	EntriesRecomputed int
	EntriesReused     int
	StatesEvaluated   int
	Iterations        int
}

type entryCache struct {
	result Result
	deps   *dependencies
}

// Engine owns a mutable analysis snapshot. Updates invalidate only entries
// whose recorded dependencies overlap the changed summary, object, or symbol
// binding. Unaffected entry results (including results computed inside recursive
// cycles) are reused.
type Engine struct {
	snapshot *Snapshot
	revoked  *revocations
	cache    map[SummaryKey]entryCache
	stats    Stats
}

// NewEngine clones a snapshot and starts an engine. Optional objects are
// placed in the initial revocation set.
func NewEngine(snapshot *Snapshot, objects ...ObjectID) (*Engine, error) {
	if snapshot == nil {
		return nil, errors.New("impact: nil snapshot")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	engine := &Engine{
		snapshot: snapshot.Clone(),
		revoked:  newRevocations(nil),
		cache:    make(map[SummaryKey]entryCache),
	}
	engine.RevokeObjects(objects...)
	return engine, nil
}

// NewEngineWithRevocations starts an engine with an initial revocation set.
func NewEngineWithRevocations(snapshot *Snapshot, objects ...ObjectID) (*Engine, error) {
	return NewEngine(snapshot, objects...)
}

// MustNewEngine is NewEngine with panic-on-error semantics.
func MustNewEngine(snapshot *Snapshot, objects ...ObjectID) *Engine {
	e, err := NewEngine(snapshot, objects...)
	if err != nil {
		panic(err)
	}
	return e
}

// Snapshot returns a defensive copy of the engine's current snapshot.
func (e *Engine) Snapshot() *Snapshot { return e.snapshot.Clone() }

// Stats returns the work counters from the last Analyze call.
func (e *Engine) Stats() Stats { return e.stats }

// RevokedObjects returns the current revocation set in sorted order.
func (e *Engine) RevokedObjects() []ObjectID {
	keys := make([]string, 0, len(e.revoked.objects))
	for key := range e.revoked.objects {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ObjectID, 0, len(keys))
	for _, key := range keys {
		out = append(out, e.revoked.objects[key])
	}
	return out
}

// RevokeObject adds one object to the revocation set and invalidates entries
// that reached that object.
func (e *Engine) RevokeObject(object ObjectID) bool {
	return e.RevokeObjects(object)
}

// RevokeObjects adds objects to the revocation set.
func (e *Engine) RevokeObjects(objects ...ObjectID) bool {
	changed := false
	for _, object := range objects {
		if object.IsZero() {
			continue
		}
		key := object.Key()
		if _, ok := e.revoked.objects[key]; ok {
			continue
		}
		e.revoked.objects[key] = object
		e.invalidateObject(object)
		changed = true
	}
	return changed
}

// RevokeProvenance revokes the object identified by a provenance.
func (e *Engine) RevokeProvenance(provenance Provenance) bool {
	return e.RevokeObject(provenance.ObjectID())
}

// RevokeSummary revokes the object belonging to a summary key.
func (e *Engine) RevokeSummary(key SummaryKey) bool {
	provenance, ok := parseProvenanceKey(key)
	if !ok {
		return false
	}
	return e.RevokeObject(provenance.ObjectID())
}

// RestoreObject removes one object from the revocation set.
func (e *Engine) RestoreObject(object ObjectID) bool {
	if object.IsZero() {
		return false
	}
	key := object.Key()
	if _, ok := e.revoked.objects[key]; !ok {
		return false
	}
	delete(e.revoked.objects, key)
	e.invalidateObject(object)
	return true
}

// ReplaceSummary adds or replaces a signed summary and invalidates every entry
// whose dependency closure contained its key.
func (e *Engine) ReplaceSummary(signed SignedSummary) (bool, error) {
	key := signed.Summary.Key()
	if err := e.snapshot.AddSummary(signed); err != nil {
		return false, err
	}
	e.invalidateSummaries(key)
	return true, nil
}

// AddSummary is an alias for ReplaceSummary.
func (e *Engine) AddSummary(signed SignedSummary) (bool, error) {
	return e.ReplaceSummary(signed)
}

// ReplaceSummaryAt replaces key with signed, allowing the provenance key to
// change. Both the old and new keys invalidate dependent entries.
func (e *Engine) ReplaceSummaryAt(key SummaryKey, signed SignedSummary) (bool, error) {
	if key == signed.Summary.Key() {
		return e.ReplaceSummary(signed)
	}
	next := e.snapshot.Clone()
	next.RemoveSummary(key)
	if err := next.AddSummary(signed); err != nil {
		return false, err
	}
	e.snapshot = next
	e.invalidateSummaries(key)
	e.invalidateSummaries(signed.Summary.Key())
	return true, nil
}

// RemoveSummary removes a summary. Calls that selected the key become missing
// evidence; they never fall back to a same-named object.
func (e *Engine) RemoveSummary(key SummaryKey) bool {
	if !e.snapshot.HasSummary(key) {
		return false
	}
	e.snapshot.RemoveSummary(key)
	e.invalidateSummaries(key)
	return true
}

// SetBindings replaces the binding list for symbol.
func (e *Engine) SetBindings(symbol string, bindings []Binding) (bool, error) {
	if err := e.snapshot.SetBindings(symbol, bindings); err != nil {
		return false, err
	}
	e.invalidateSymbols(symbol)
	return true, nil
}

// SetBinding replaces symbol's bindings with one binding.
func (e *Engine) SetBinding(symbol string, binding Binding) (bool, error) {
	return e.SetBindings(symbol, []Binding{binding})
}

// ClearBindings removes every binding for symbol.
func (e *Engine) ClearBindings(symbol string) bool {
	e.snapshot.ClearBindings(symbol)
	e.invalidateSymbols(symbol)
	return true
}

// SetEntries replaces the entry list. Changing the list only invalidates
// results for entries that are no longer present; existing cached entries stay
// valid because the program and option order did not change.
func (e *Engine) SetEntries(entries ...SummaryKey) error {
	if err := e.snapshot.SetEntries(entries...); err != nil {
		return err
	}
	keep := make(map[SummaryKey]bool, len(entries))
	for _, entry := range entries {
		keep[entry] = true
	}
	for key := range e.cache {
		if !keep[key] {
			delete(e.cache, key)
		}
	}
	return nil
}

// SetKeyRing changes certificate verification evidence. All entries are
// invalidated because every summary's evidence status may change.
func (e *Engine) SetKeyRing(ring *KeyRing) {
	e.snapshot.SetKeyRing(ring)
	e.cache = make(map[SummaryKey]entryCache)
}

// SetValid replaces the legal-configuration constraint and invalidates every
// cached entry, because every result is a subset of Valid.
func (e *Engine) SetValid(valid BDD) error {
	if err := e.snapshot.SetValid(valid); err != nil {
		return err
	}
	e.cache = make(map[SummaryKey]entryCache)
	return nil
}

// Analyze computes (or reuses) every entry result in snapshot order.
func (e *Engine) Analyze() (*Analysis, error) {
	if e.snapshot == nil {
		return nil, errors.New("impact: uninitialized engine")
	}
	if err := e.snapshot.Validate(); err != nil {
		return nil, err
	}
	e.stats = Stats{}
	entries := e.snapshot.Entries()
	results := make([]Result, 0, len(entries))
	for _, entry := range entries {
		if cached, ok := e.cache[entry]; ok {
			results = append(results, cached.result)
			e.stats.EntriesReused++
			continue
		}
		result, deps, states, iterations, err := analyzeEntry(e.snapshot, e.revoked, entry)
		if err != nil {
			return nil, fmt.Errorf("impact: entry %q: %w", entry, err)
		}
		e.cache[entry] = entryCache{result: result, deps: deps}
		results = append(results, result)
		e.stats.EntriesRecomputed++
		e.stats.StatesEvaluated += states
		e.stats.Iterations += iterations
	}
	return newAnalysis(e.snapshot.manager, e.snapshot.valid, results), nil
}

// AnalyzeEntry returns one entry's result, using the same cache as Analyze.
func (e *Engine) AnalyzeEntry(entry SummaryKey) (Result, error) {
	if err := e.snapshot.Validate(); err != nil {
		return Result{}, err
	}
	if cached, ok := e.cache[entry]; ok {
		return cached.result, nil
	}
	result, deps, _, _, err := analyzeEntry(e.snapshot, e.revoked, entry)
	if err != nil {
		return Result{}, err
	}
	e.cache[entry] = entryCache{result: result, deps: deps}
	return result, nil
}

func (e *Engine) invalidateSummaries(key SummaryKey) {
	for entry, cached := range e.cache {
		if _, ok := cached.deps.summaries[key]; ok {
			delete(e.cache, entry)
		}
	}
}

func (e *Engine) invalidateObject(object ObjectID) {
	for entry, cached := range e.cache {
		for _, dependency := range cached.deps.objects {
			if objectMatches(object, dependency) {
				delete(e.cache, entry)
				break
			}
		}
	}
}

func (e *Engine) invalidateSymbols(symbol string) {
	for entry, cached := range e.cache {
		if _, ok := cached.deps.symbols[symbol]; ok {
			delete(e.cache, entry)
		}
	}
}
