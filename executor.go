package replay

import "fmt"

// State is the executor's derived state, computed purely from events.
type State interface{}

// Reducer is a pure function: it folds an event into the state and
// declares the external effects that event requires. It must be
// deterministic so replay regenerates identical effect descriptions.
type Reducer func(state State, ev Event) (State, []EffectSpec)

// Sender delivers an effect description to the outside world.
// A nil error means the receipt was received; a non-nil error means
// the receipt was lost (the effect may or may not have been applied),
// so the executor retries with the same effect ID.
type Sender interface {
	Send(e Effect) error
}

// Executor separates state computation from external effects. Events
// update state through the reducer; effects are dispatched only if
// not yet confirmed in the journal.
type Executor struct {
	journal    *Journal
	reducer    Reducer
	sender     Sender
	initial    State
	state      State
	maxRetries int
}

func NewExecutor(j *Journal, r Reducer, s Sender, initial State) *Executor {
	return &Executor{
		journal:    j,
		reducer:    r,
		sender:     s,
		initial:    initial,
		state:      initial,
		maxRetries: 5,
	}
}

func (e *Executor) SetMaxRetries(n int) { e.maxRetries = n }

func (e *Executor) State() State { return e.state }

// Handle journals the event (ignoring duplicates), folds it into the
// state, and dispatches any effects not already confirmed.
func (e *Executor) Handle(ev Event) error {
	if !e.journal.AppendEvent(ev) {
		return nil
	}
	state, specs := e.reducer(e.state, ev)
	e.state = state
	for seq, spec := range specs {
		if err := e.dispatch(newEffect(ev.ID, seq, spec)); err != nil {
			return err
		}
	}
	return nil
}

// Recover rebuilds state by replaying the journaled events. Replay
// only regenerates effect descriptions: confirmed effects are skipped,
// and only effects that were never confirmed are dispatched.
func (e *Executor) Recover() error {
	state := e.initial
	for _, ev := range e.journal.Events() {
		var specs []EffectSpec
		state, specs = e.reducer(state, ev)
		for seq, spec := range specs {
			if err := e.dispatch(newEffect(ev.ID, seq, spec)); err != nil {
				return err
			}
		}
	}
	e.state = state
	return nil
}

func (e *Executor) dispatch(eff Effect) error {
	if e.journal.IsConfirmed(eff.ID) {
		return nil
	}
	var err error
	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		if err = e.sender.Send(eff); err == nil {
			e.journal.Confirm(eff.ID)
			return nil
		}
	}
	return fmt.Errorf("effect %s: receipt lost after %d attempts: %v", eff.ID, e.maxRetries+1, err)
}
