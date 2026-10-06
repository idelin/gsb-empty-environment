package sim

import (
	"errors"
	"fmt"
	"sort"
)

// Event is a unit of work applied to an entity. Ordering is fully
// explicit: events are processed in (Time, Seq) order and the pair must
// be unique, so the caller alone decides the execution order.
type Event struct {
	Time     int64  `json:"time"`
	Seq      uint64 `json:"seq"`
	EntityID string `json:"entity_id"`
	Kind     string `json:"kind"`
	Amount   int64  `json:"amount"`
}

// Handler applies one event to the engine. Entity behavior that needs
// randomness must draw from the target entity's own stream.
type Handler func(eng *Engine, ev Event) error

// Engine holds the whole deterministic simulation state: entities,
// the clock, and the pending event queue.
type Engine struct {
	seed     uint64
	now      int64
	entities map[string]*Entity
	queue    []Event
	handler  Handler
}

// NewEngine creates an empty engine. The seed derives every entity's
// random stream from its ID, so entity streams are independent of each
// other and of creation order.
func NewEngine(seed uint64, h Handler) *Engine {
	return &Engine{
		seed:     seed,
		entities: make(map[string]*Entity),
		handler:  h,
	}
}

// Now returns the current simulation clock.
func (eng *Engine) Now() int64 {
	return eng.now
}

// AddEntity registers a new entity. Its random stream is seeded from
// (engine seed, entity ID) only, so adding unrelated entities cannot
// perturb existing entities' sequences.
func (eng *Engine) AddEntity(id string) (*Entity, error) {
	if _, ok := eng.entities[id]; ok {
		return nil, fmt.Errorf("entity %q already exists", id)
	}
	e := &Entity{
		ID:    id,
		Attrs: make(map[string]int64),
		rng:   newRNG(deriveSeed(eng.seed, id)),
	}
	eng.entities[id] = e
	return e, nil
}

// Entity returns the entity with the given ID, or nil.
func (eng *Engine) Entity(id string) *Entity {
	return eng.entities[id]
}

// EntityIDs returns all entity IDs in sorted (deterministic) order.
func (eng *Engine) EntityIDs() []string {
	ids := make([]string, 0, len(eng.entities))
	for id := range eng.entities {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Schedule enqueues an event. The (Time, Seq) pair must be unique and
// not in the past, keeping event order fully explicit.
func (eng *Engine) Schedule(ev Event) error {
	if ev.Time < eng.now {
		return fmt.Errorf("event at time %d is in the past (now %d)", ev.Time, eng.now)
	}
	for _, q := range eng.queue {
		if q.Time == ev.Time && q.Seq == ev.Seq {
			return fmt.Errorf("duplicate event key (time %d, seq %d)", ev.Time, ev.Seq)
		}
	}
	eng.queue = append(eng.queue, ev)
	return nil
}

// Pending returns the number of queued events.
func (eng *Engine) Pending() int {
	return len(eng.queue)
}

// Step applies the next event in (Time, Seq) order. It returns false
// when the queue is empty.
func (eng *Engine) Step() (bool, error) {
	if len(eng.queue) == 0 {
		return false, nil
	}
	best := 0
	for i := 1; i < len(eng.queue); i++ {
		if lessEvent(eng.queue[i], eng.queue[best]) {
			best = i
		}
	}
	ev := eng.queue[best]
	eng.queue = append(eng.queue[:best], eng.queue[best+1:]...)
	eng.now = ev.Time
	if eng.handler == nil {
		return true, errors.New("no handler registered")
	}
	if err := eng.handler(eng, ev); err != nil {
		return true, err
	}
	return true, nil
}

// Run drains the event queue.
func (eng *Engine) Run() error {
	for {
		ok, err := eng.Step()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
}

func lessEvent(a, b Event) bool {
	if a.Time != b.Time {
		return a.Time < b.Time
	}
	return a.Seq < b.Seq
}

// deriveSeed mixes the engine seed with an entity ID (FNV-1a) so each
// entity gets an independent, creation-order-independent stream.
func deriveSeed(seed uint64, id string) uint64 {
	h := uint64(14695981039346656037) ^ seed
	for i := 0; i < len(id); i++ {
		h ^= uint64(id[i])
		h *= 1099511628211
	}
	return h
}
