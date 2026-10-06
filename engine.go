package sim

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrOutOfOrderEvent is returned when an event's Seq does not equal the
	// engine's next expected sequence number.
	ErrOutOfOrderEvent = errors.New("sim: event sequence out of order")
	// ErrDuplicateEntity is returned when adding an entity that exists.
	ErrDuplicateEntity = errors.New("sim: entity already exists")
	// ErrUnknownEntity is returned when mutating a missing entity.
	ErrUnknownEntity = errors.New("sim: unknown entity")
)

// Engine applies an explicitly ordered event log to a set of entities, each
// with an independent deterministic RNG stream.
type Engine struct {
	seed     uint64
	nextSeq  uint64
	entities map[string]*Entity
}

// New creates an engine whose entity streams derive from worldSeed.
func New(worldSeed uint64) *Engine {
	return &Engine{seed: worldSeed, entities: make(map[string]*Entity)}
}

// NextSeq is the sequence number the next applied event must carry.
func (e *Engine) NextSeq() uint64 {
	return e.nextSeq
}

// Apply executes one event. The event must carry Seq == NextSeq(); anything
// else is rejected so the event order stays explicit and unambiguous.
func (e *Engine) Apply(ev Event) error {
	if ev.Seq != e.nextSeq {
		return fmt.Errorf("%v: got seq %d, want %d", ErrOutOfOrderEvent, ev.Seq, e.nextSeq)
	}
	switch ev.Kind {
	case KindAddEntity:
		if _, ok := e.entities[ev.EntityID]; ok {
			return fmt.Errorf("%v: %q", ErrDuplicateEntity, ev.EntityID)
		}
		ent := newEntity(e.seed, ev.EntityID)
		if ev.Key != "" {
			ent.Attrs[ev.Key] = ev.Delta
		}
		e.entities[ev.EntityID] = ent
	case KindMutate:
		ent, ok := e.entities[ev.EntityID]
		if !ok {
			return fmt.Errorf("%v: %q", ErrUnknownEntity, ev.EntityID)
		}
		jitter := int64(ent.rng.Intn(3))
		ent.Attrs[ev.Key] += ev.Delta + jitter
	default:
		return fmt.Errorf("sim: unknown event kind %d", ev.Kind)
	}
	e.nextSeq++
	return nil
}

// RNGCalls returns a copy of each entity's random draw count, for comparing
// replayed branches.
func (e *Engine) RNGCalls() map[string]uint64 {
	out := make(map[string]uint64, len(e.entities))
	for id, ent := range e.entities {
		out[id] = ent.rng.Calls()
	}
	return out
}

// Attr returns an entity attribute, for assertions.
func (e *Engine) Attr(id, key string) (int64, bool) {
	ent, ok := e.entities[id]
	if !ok {
		return 0, false
	}
	v, ok := ent.Attrs[key]
	return v, ok
}

// StateHash is a deterministic FNV-1a hash over the full engine state:
// entities (sorted), their attributes (sorted), RNG states, draw counts, and
// the next expected sequence number. Identical replays hash identically.
func (e *Engine) StateHash() uint64 {
	h := uint64(14695981039346656037)
	mix := func(b byte) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	mixUint := func(v uint64) {
		for i := 0; i < 8; i++ {
			mix(byte(v >> (8 * uint(i))))
		}
	}
	mixStr := func(s string) {
		for i := 0; i < len(s); i++ {
			mix(s[i])
		}
		mix(0)
	}
	mixUint(e.seed)
	mixUint(e.nextSeq)
	for _, id := range e.sortedIDs() {
		ent := e.entities[id]
		mixStr(id)
		keys := make([]string, 0, len(ent.Attrs))
		for k := range ent.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			mixStr(k)
			mixUint(uint64(ent.Attrs[k]))
		}
		mixUint(ent.rng.state)
		mixUint(ent.rng.calls)
	}
	return h
}

func (e *Engine) sortedIDs() []string {
	ids := make([]string, 0, len(e.entities))
	for id := range e.entities {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
