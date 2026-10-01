package main

import (
	"fmt"
	"sort"
	"sync"
)

// ConflictError reports that two distinct input series map to the same
// output label set while the engine is not in aggregation mode.
type ConflictError struct {
	Output LabelSet
	Pair   [2]string // minimal conflicting input pair (sorted series keys)
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict: inputs %s and %s both map to output %s (aggregation not enabled)",
		PrettySeriesKey(e.Pair[0]), PrettySeriesKey(e.Pair[1]), e.Output)
}

// Output is a materialized output series. Contributions keeps each input
// series' share so that retracting a rule restores the original series.
type Output struct {
	Labels        LabelSet
	Contributions map[string]float64 // input series key -> value
}

// Value is the aggregated value of the output.
func (o *Output) Value() float64 {
	var sum float64
	for _, v := range o.Contributions {
		sum += v
	}
	return sum
}

// Engine applies an ordered rule list to input series and materializes
// outputs. Merging of distinct inputs is only allowed when the engine was
// built with explicit aggregation mode.
type Engine struct {
	mu        sync.Mutex
	rules     []Rule
	version   uint64
	aggregate bool
	reserved  map[string]bool

	inputs     map[string]Series // input key -> original series
	outputs    map[string]*Output
	membership map[string]string // input key -> output key

	// rewrite caches are isolated per rule version: caches[version]
	caches map[uint64]map[string]LabelSet

	tele *Telemetry
}

func NewEngine(aggregate bool, reserved []string, tele *Telemetry) *Engine {
	res := make(map[string]bool, len(reserved))
	for _, r := range reserved {
		res[r] = true
	}
	if tele == nil {
		tele = NewTelemetry()
	}
	return &Engine{
		aggregate:  aggregate,
		reserved:   res,
		inputs:     make(map[string]Series),
		outputs:    make(map[string]*Output),
		membership: make(map[string]string),
		caches:     map[uint64]map[string]LabelSet{0: {}},
		tele:       tele,
	}
}

// Version is the current rule-set version.
func (e *Engine) Version() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.version
}

// Rules returns the active ordered rule list.
func (e *Engine) Rules() []Rule {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Rule, len(e.rules))
	copy(out, e.rules)
	return out
}

// Outputs snapshots the materialized outputs (keyed by canonical label key).
func (e *Engine) Outputs() map[string]Output {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]Output, len(e.outputs))
	for k, o := range e.outputs {
		cp := Output{Labels: o.Labels.Clone(), Contributions: make(map[string]float64, len(o.Contributions))}
		for ik, v := range o.Contributions {
			cp.Contributions[ik] = v
		}
		out[k] = cp
	}
	return out
}

// CacheSize reports the number of cached rewrites for a rule version.
func (e *Engine) CacheSize(version uint64) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.caches[version])
}

func (e *Engine) rewrite(ls LabelSet) LabelSet {
	bucket := e.caches[e.version]
	inKey := ls.Key()
	if cached, ok := bucket[inKey]; ok {
		e.tele.Emit(e.version, EventCacheHit, map[string]string{"input": inKey})
		return cached.Clone()
	}
	out := ls
	for _, r := range e.rules {
		out = r.Apply(out)
	}
	bucket[inKey] = out.Clone()
	e.tele.Emit(e.version, EventCacheStore, map[string]string{"input": inKey, "output": out.Key()})
	return out
}

// AddSeries ingests one input series. Without aggregation mode, a collision
// with an already-materialized output from a different input is rejected:
// the existing output is never overwritten.
func (e *Engine) AddSeries(s Series) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	inKey := s.Key()
	outLabels := e.rewrite(s.Labels)
	outKey := outLabels.Key()
	if existing, ok := e.outputs[outKey]; ok {
		if _, mine := existing.Contributions[inKey]; !mine && !e.aggregate {
			pair := [2]string{firstKey(existing.Contributions), inKey}
			if pair[1] < pair[0] {
				pair[0], pair[1] = pair[1], pair[0]
			}
			e.tele.Emit(e.version, EventConflictRejected, map[string]string{
				"output": outKey, "a": pair[0], "b": pair[1],
			})
			return &ConflictError{Output: existing.Labels.Clone(), Pair: pair}
		}
		if _, mine := existing.Contributions[inKey]; !mine {
			e.tele.Emit(e.version, EventSeriesMerged, map[string]string{
				"output": outKey, "input": inKey,
			})
		}
		existing.Contributions[inKey] = s.Value
		e.inputs[inKey] = s
		e.membership[inKey] = outKey
		return nil
	}
	e.inputs[inKey] = s
	e.outputs[outKey] = &Output{
		Labels:        outLabels,
		Contributions: map[string]float64{inKey: s.Value},
	}
	e.membership[inKey] = outKey
	e.tele.Emit(e.version, EventSeriesAdded, map[string]string{"input": inKey, "output": outKey})
	return nil
}

// RemoveSeries retracts one input series and its contribution.
func (e *Engine) RemoveSeries(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.inputs, key)
	e.recomputeLocked()
}

// UpdateRules atomically validates and publishes a new ordered rule batch.
// If the batch would merge distinct inputs without aggregation, nothing is
// published and a ConflictError with the minimal conflicting pair returns.
func (e *Engine) UpdateRules(rules []Rule) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ValidateRules(rules, e.reserved); err != nil {
		return err
	}
	if !e.aggregate {
		if cerr := e.checkBatchLocked(rules); cerr != nil {
			e.tele.Emit(e.version, EventConflictRejected, map[string]string{
				"output": cerr.Output.Key(), "a": cerr.Pair[0], "b": cerr.Pair[1],
			})
			return cerr
		}
	}
	e.publishLocked(rules)
	e.tele.Emit(e.version, EventRulesPublished, map[string]string{
		"rules": fmt.Sprint(len(rules)),
	})
	return nil
}

// RetractRule dynamically removes one rule by ID and republishes the rest.
// Series previously merged by that rule are split back with their original
// contributions restored.
func (e *Engine) RetractRule(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var kept []Rule
	found := false
	for _, r := range e.rules {
		if r.ID() == id {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return fmt.Errorf("no rule with id %q", id)
	}
	if err := ValidateRules(kept, e.reserved); err != nil {
		return err
	}
	if !e.aggregate {
		if cerr := e.checkBatchLocked(kept); cerr != nil {
			return cerr
		}
	}
	e.publishLocked(kept)
	e.tele.Emit(e.version, EventRuleRetracted, map[string]string{"rule": id})
	return nil
}

// publishLocked swaps the rule set, bumps the version, opens a fresh cache
// bucket for the new version and rebuilds outputs.
func (e *Engine) publishLocked(rules []Rule) {
	e.rules = append([]Rule(nil), rules...)
	e.version++
	if _, ok := e.caches[e.version]; !ok {
		e.caches[e.version] = map[string]LabelSet{}
	}
	e.recomputeLocked()
}

// checkBatchLocked simulates a rule batch against current inputs and returns
// the minimal conflicting input pair, if any.
func (e *Engine) checkBatchLocked(rules []Rule) *ConflictError {
	groups := map[string][]string{}
	labels := map[string]LabelSet{}
	keys := make([]string, 0, len(e.inputs))
	for k := range e.inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out := e.inputs[k].Labels
		for _, r := range rules {
			out = r.Apply(out)
		}
		ok := out.Key()
		groups[ok] = append(groups[ok], k)
		labels[ok] = out
	}
	var colliding []string
	for k, g := range groups {
		if len(g) > 1 {
			colliding = append(colliding, k)
		}
	}
	if len(colliding) == 0 {
		return nil
	}
	sort.Strings(colliding)
	g := groups[colliding[0]]
	return &ConflictError{Output: labels[colliding[0]], Pair: [2]string{g[0], g[1]}}
}

// recomputeLocked rebuilds all outputs and membership from stored inputs.
// Inputs that shared an output before and no longer do are reported as
// split, with each original contribution restored.
func (e *Engine) recomputeLocked() {
	oldOutputs := e.outputs
	e.outputs = map[string]*Output{}
	e.membership = map[string]string{}

	keys := make([]string, 0, len(e.inputs))
	for k := range e.inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := e.inputs[k]
		outLabels := s.Labels
		for _, r := range e.rules {
			outLabels = r.Apply(outLabels)
		}
		ok := outLabels.Key()
		out, exists := e.outputs[ok]
		if !exists {
			out = &Output{Labels: outLabels, Contributions: map[string]float64{}}
			e.outputs[ok] = out
		}
		out.Contributions[k] = s.Value
		e.membership[k] = ok
	}

	for oldOut, old := range oldOutputs {
		if len(old.Contributions) < 2 {
			continue
		}
		now := map[string]bool{}
		for ik := range old.Contributions {
			if nk, ok := e.membership[ik]; ok {
				now[nk] = true
			}
		}
		if len(now) > 1 {
			e.tele.Emit(e.version, EventSeriesSplit, map[string]string{
				"output": oldOut, "into": fmt.Sprint(len(now)),
			})
		}
	}
}

func firstKey(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys[0]
}
