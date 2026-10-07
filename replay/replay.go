// Package replay implements a replay executor that separates state
// computation from external effects.
//
// Events are applied to a pure handler that computes the next state and
// returns effect descriptions. Effects are the only channel through which
// the executor touches the outside world. Replaying the event log
// regenerates the same effect descriptions deterministically, but an
// effect whose receipt is already recorded is never executed again.
//
// Effect IDs are derived from the event ID and a per-event sequence
// number ("<eventID>#<seq>"), so a replay produces identical IDs. If a
// receipt is lost (e.g. the process crashed after delivering an effect
// but before recording the receipt), the effect is re-sent with the same
// ID and the receiver deduplicates it via its idempotency table.
package replay

import (
	"errors"
	"fmt"
	"sync"
)

// Event is an input fact recorded in the event log.
type Event struct {
	ID   string
	Type string
	Data any
}

// Effect is a description of an external side effect. It carries no
// behavior; executing it is the job of a Sink.
type Effect struct {
	ID      string // "<EventID>#<Seq>"
	EventID string
	Seq     int
	Kind    string
	Payload any
}

// EffectID builds the deterministic effect ID from an event ID and a
// per-event sequence number.
func EffectID(eventID string, seq int) string {
	return fmt.Sprintf("%s#%d", eventID, seq)
}

// EffectSpec is what a handler emits; the executor assigns IDs.
type EffectSpec struct {
	Kind    string
	Payload any
}

// Handler is a pure state transition: given the current state and an
// event, it returns the next state and the effects that event implies.
// It must be deterministic so that replay regenerates identical effects.
type Handler func(state any, ev Event) (next any, effects []EffectSpec)

// Store persists the event log and effect receipts.
type Store interface {
	AppendEvent(ev Event) error
	HasEvent(id string) bool
	Events() []Event
	RecordReceipt(effectID string) error
	HasReceipt(effectID string) bool
}

// MemStore is an in-memory Store.
type MemStore struct {
	mu       sync.Mutex
	events   []Event
	eventIDs map[string]bool
	receipts map[string]bool
}

func NewMemStore() *MemStore {
	return &MemStore{eventIDs: map[string]bool{}, receipts: map[string]bool{}}
}

func (s *MemStore) AppendEvent(ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eventIDs[ev.ID] {
		return fmt.Errorf("duplicate event %q", ev.ID)
	}
	s.events = append(s.events, ev)
	s.eventIDs[ev.ID] = true
	return nil
}

func (s *MemStore) HasEvent(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eventIDs[id]
}

func (s *MemStore) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

func (s *MemStore) RecordReceipt(effectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts[effectID] = true
	return nil
}

func (s *MemStore) HasReceipt(effectID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.receipts[effectID]
}

// Sink executes effects against the outside world.
type Sink interface {
	Execute(e Effect) error
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(e Effect) error

func (f SinkFunc) Execute(e Effect) error { return f(e) }

// Executor applies events and replays the log. State computation
// (Handler) is fully separated from external effects (Sink): replay
// recomputes state and effect descriptions without re-executing any
// effect that already has a recorded receipt.
type Executor struct {
	store   Store
	sink    Sink
	handler Handler
	initial func() any

	mu    sync.Mutex
	state any
}

func NewExecutor(store Store, sink Sink, handler Handler, initial func() any) *Executor {
	ex := &Executor{store: store, sink: sink, handler: handler, initial: initial}
	ex.state = initial()
	return ex
}

var ErrDuplicateEvent = errors.New("duplicate event")

// Execute records an event, folds it into the state, and executes any
// effects that do not yet have a receipt. Re-delivering an already
// recorded event is a no-op returning ErrDuplicateEvent.
func (ex *Executor) Execute(ev Event) error {
	ex.mu.Lock()
	defer ex.mu.Unlock()

	if ex.store.HasEvent(ev.ID) {
		return ErrDuplicateEvent
	}
	if err := ex.store.AppendEvent(ev); err != nil {
		return err
	}
	return ex.applyLocked(ev)
}

// applyLocked computes the transition and runs pending effects.
func (ex *Executor) applyLocked(ev Event) error {
	next, specs := ex.handler(ex.state, ev)
	ex.state = next
	for i, spec := range specs {
		eff := Effect{
			ID:      EffectID(ev.ID, i),
			EventID: ev.ID,
			Seq:     i,
			Kind:    spec.Kind,
			Payload: spec.Payload,
		}
		if err := ex.runEffectLocked(eff); err != nil {
			return err
		}
	}
	return nil
}

// runEffectLocked executes an effect unless its receipt is already
// recorded, then records the receipt.
func (ex *Executor) runEffectLocked(eff Effect) error {
	if ex.store.HasReceipt(eff.ID) {
		return nil
	}
	if err := ex.sink.Execute(eff); err != nil {
		return err
	}
	return ex.store.RecordReceipt(eff.ID)
}

// Replay rebuilds the state from the event log and regenerates every
// effect description. Effects with a recorded receipt are skipped;
// effects without one (e.g. their receipt was lost after delivery) are
// re-sent with the same ID, relying on the receiver to deduplicate.
// Replay returns the rebuilt state and all regenerated effect
// descriptions for inspection.
func (ex *Executor) Replay() (state any, effects []Effect, err error) {
	ex.mu.Lock()
	defer ex.mu.Unlock()

	ex.state = ex.initial()
	for _, ev := range ex.store.Events() {
		next, specs := ex.handler(ex.state, ev)
		ex.state = next
		for i, spec := range specs {
			eff := Effect{
				ID:      EffectID(ev.ID, i),
				EventID: ev.ID,
				Seq:     i,
				Kind:    spec.Kind,
				Payload: spec.Payload,
			}
			effects = append(effects, eff)
			if ex.store.HasReceipt(eff.ID) {
				continue // already confirmed: never re-execute
			}
			if err := ex.runEffectLocked(eff); err != nil {
				return nil, effects, err
			}
		}
	}
	return ex.state, effects, nil
}

// State returns the current in-memory state.
func (ex *Executor) State() any {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	return ex.state
}
