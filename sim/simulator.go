package sim

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
)

// LossRecord is an explicit ledger entry for material that left the system.
type LossRecord struct {
	EventID string `json:"event_id"`
	Entity  string `json:"entity"`
	Amount  int64  `json:"amount"`
}

// Simulator holds the material state produced by an event log.
type Simulator struct {
	Entities map[string]*Entity
	Log      []Event
	Losses   []LossRecord
	applied  map[string]bool
}

func New() *Simulator {
	return &Simulator{
		Entities: make(map[string]*Entity),
		applied:  make(map[string]bool),
	}
}

// Apply executes one event. Every event either conserves material exactly or
// records the shortfall in the loss ledger; anything else is rejected and
// leaves the state untouched. Applying an event ID that was already applied
// is a no-op (idempotent) and reports duplicate=true.
func (s *Simulator) Apply(ev Event) (duplicate bool, err error) {
	if ev.ID == "" {
		return false, errors.New("event ID must not be empty")
	}
	if s.applied[ev.ID] {
		return true, nil
	}
	next, loss, err := s.transition(ev)
	if err != nil {
		return false, err
	}
	s.Entities = next
	if loss != nil {
		s.Losses = append(s.Losses, *loss)
	}
	s.applied[ev.ID] = true
	s.Log = append(s.Log, ev)
	return false, nil
}

// transition computes the post-event entity map without mutating the
// simulator, so a rejected event cannot corrupt state.
func (s *Simulator) transition(ev Event) (map[string]*Entity, *LossRecord, error) {
	next := make(map[string]*Entity, len(s.Entities))
	for id, e := range s.Entities {
		next[id] = e
	}
	outID := func(i int) string { return ev.ID + "/" + strconv.Itoa(i) }

	switch ev.Type {
	case EventReceive:
		if ev.Quantity <= 0 {
			return nil, nil, fmt.Errorf("receive %s: quantity must be positive, got %d", ev.ID, ev.Quantity)
		}
		src := ev.Source
		if src == "" {
			src = ev.ID
		}
		if _, exists := next[outID(0)]; exists {
			return nil, nil, fmt.Errorf("receive %s: output %s already exists", ev.ID, outID(0))
		}
		next[outID(0)] = &Entity{
			ID:       outID(0),
			Quantity: ev.Quantity,
			Sources:  SourceVector{src: big.NewRat(ev.Quantity, 1)},
		}
		return next, nil, nil

	case EventSplit:
		in, ok := next[ev.Entity]
		if !ok {
			return nil, nil, fmt.Errorf("split %s: unknown entity %q", ev.ID, ev.Entity)
		}
		if len(ev.Parts) == 0 {
			return nil, nil, fmt.Errorf("split %s: no parts given", ev.ID)
		}
		var sum int64
		for _, p := range ev.Parts {
			if p <= 0 {
				return nil, nil, fmt.Errorf("split %s: part sizes must be positive, got %d", ev.ID, p)
			}
			sum += p
		}
		if sum != in.Quantity {
			return nil, nil, fmt.Errorf("split %s: parts sum to %d, input holds %d (not conservative)", ev.ID, sum, in.Quantity)
		}
		delete(next, ev.Entity)
		for i, p := range ev.Parts {
			id := outID(i)
			if _, exists := next[id]; exists {
				return nil, nil, fmt.Errorf("split %s: output %s already exists", ev.ID, id)
			}
			next[id] = &Entity{
				ID:       id,
				Quantity: p,
				Sources:  in.Sources.scale(big.NewRat(p, in.Quantity)),
			}
		}
		return next, nil, nil

	case EventMerge:
		if len(ev.Entities) == 0 {
			return nil, nil, fmt.Errorf("merge %s: no inputs given", ev.ID)
		}
		merged := SourceVector{}
		var qty int64
		for _, id := range ev.Entities {
			in, ok := next[id]
			if !ok {
				return nil, nil, fmt.Errorf("merge %s: unknown entity %q", ev.ID, id)
			}
			merged.add(in.Sources)
			qty += in.Quantity
		}
		id := outID(0)
		if _, exists := next[id]; exists {
			return nil, nil, fmt.Errorf("merge %s: output %s already exists", ev.ID, id)
		}
		for _, inID := range ev.Entities {
			delete(next, inID)
		}
		next[id] = &Entity{ID: id, Quantity: qty, Sources: merged}
		return next, nil, nil

	case EventLoss:
		in, ok := next[ev.Entity]
		if !ok {
			return nil, nil, fmt.Errorf("loss %s: unknown entity %q", ev.ID, ev.Entity)
		}
		if ev.Quantity <= 0 || ev.Quantity > in.Quantity {
			return nil, nil, fmt.Errorf("loss %s: amount %d outside (0, %d]", ev.ID, ev.Quantity, in.Quantity)
		}
		remaining := in.Quantity - ev.Quantity
		// Proportional rational deduction: every source share keeps its
		// relative weight, scaled by remaining/original.
		factor := big.NewRat(remaining, in.Quantity)
		e := &Entity{ID: in.ID, Quantity: remaining, Sources: in.Sources.scale(factor)}
		delete(next, ev.Entity)
		next[e.ID] = e
		return next, &LossRecord{EventID: ev.ID, Entity: ev.Entity, Amount: ev.Quantity}, nil

	default:
		return nil, nil, fmt.Errorf("unknown event type %q", ev.Type)
	}
}

// Replay rebuilds a simulator from scratch by applying the log in order.
// Duplicate event IDs inside the log collapse to a single application.
func Replay(log []Event) (*Simulator, error) {
	s := New()
	for _, ev := range log {
		if _, err := s.Apply(ev); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// VerifyReplay replays the simulator's own log into a fresh instance and
// checks that the resulting state is identical.
func (s *Simulator) VerifyReplay() error {
	replayed, err := Replay(s.Log)
	if err != nil {
		return fmt.Errorf("replay failed: %v", err)
	}
	if !stateEqual(s, replayed) {
		return errors.New("replay diverged from current state")
	}
	return nil
}

func stateEqual(a, b *Simulator) bool {
	if len(a.Entities) != len(b.Entities) || len(a.Losses) != len(b.Losses) {
		return false
	}
	for id, ea := range a.Entities {
		eb, ok := b.Entities[id]
		if !ok || ea.Quantity != eb.Quantity || len(ea.Sources) != len(eb.Sources) {
			return false
		}
		for src, amt := range ea.Sources {
			other, ok := eb.Sources[src]
			if !ok || amt.Cmp(other) != 0 {
				return false
			}
		}
	}
	for i := range a.Losses {
		if a.Losses[i] != b.Losses[i] {
			return false
		}
	}
	return true
}
