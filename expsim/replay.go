package expsim

import "fmt"

// EventKind enumerates the replayable event types.
type EventKind int

const (
	EvObserve EventKind = iota
	EvRevise
	EvAction
	EvCheckpoint
)

// Event is one entry in the system event log.
type Event struct {
	Kind EventKind
	Obs  *Observation
	Rev  *Revision
	Act  *Action
}

// ObserveEvent, ReviseEvent, ActionEvent, CheckpointEvent build events.
func ObserveEvent(o Observation) Event { return Event{Kind: EvObserve, Obs: &o} }
func ReviseEvent(r Revision) Event     { return Event{Kind: EvRevise, Rev: &r} }
func ActionEvent(a Action) Event       { return Event{Kind: EvAction, Act: &a} }
func CheckpointEvent() Event           { return Event{Kind: EvCheckpoint} }

// System ties the three versioned stores together: fact versions
// (FactStore), recomputed conclusions and action versions (Ledger).
// The action ledger is append-only; late revisions only ever produce
// compensation suggestions.
type System struct {
	bounds        Boundaries
	engine        *Engine
	facts         *FactStore
	ledger        *Ledger
	rawLog        []Observation
	checkpoints   []Checkpoint
	conclusions   []Conclusion // one per revision, in revision order
	compensations []Compensation
}

// NewSystem creates an empty system with the given boundaries.
func NewSystem(b Boundaries) *System {
	return &System{
		bounds: b,
		engine: NewEngine(b),
		facts:  NewFactStore(nil),
		ledger: &Ledger{},
	}
}

// Apply folds one event into the system.
func (s *System) Apply(ev Event) error {
	switch ev.Kind {
	case EvObserve:
		s.rawLog = append(s.rawLog, *ev.Obs)
		s.facts.Add(*ev.Obs)
		s.engine.Process(*ev.Obs)
	case EvCheckpoint:
		s.checkpoints = append(s.checkpoints, s.engine.SaveCheckpoint())
	case EvRevise:
		v, err := s.facts.Revise(*ev.Rev)
		if err != nil {
			return err
		}
		facts, err := s.facts.FactsAt(v)
		if err != nil {
			return err
		}
		oldC := s.CurrentConclusion()
		newC := Recompute(facts, s.bounds, v)
		s.conclusions = append(s.conclusions, newC)
		s.compensations = append(s.compensations, Compensate(s.ledger, oldC, newC)...)
	case EvAction:
		a := *ev.Act
		a.FactVersion = s.facts.Version()
		s.ledger.Append(a)
	}
	return nil
}

// CurrentConclusion is the latest known conclusion: the most recent
// recomputation if any revision exists, otherwise the live engine
// state at fact version 0.
func (s *System) CurrentConclusion() Conclusion {
	if len(s.conclusions) > 0 {
		return s.conclusions[len(s.conclusions)-1]
	}
	c := Conclusion{FactVersion: 0, Sum: s.engine.Sum()}
	if ev := s.engine.Evidence(); ev != nil {
		c.StopStep = ev.Step
		c.Decision = s.engine.Decision()
		evCopy := *ev
		c.Evidence = &evCopy
	}
	return c
}

// Shifts lists how the stop point moved across consecutive fact
// versions (base conclusion vs. each recomputation).
func (s *System) Shifts() []Shift {
	var out []Shift
	prev := s.baseConclusion()
	for _, c := range s.conclusions {
		out = append(out, CompareConclusions(prev, c))
		prev = c
	}
	return out
}

func (s *System) baseConclusion() Conclusion {
	if len(s.conclusions) > 0 {
		// Re-derive the version-0 conclusion from stored facts.
		facts, err := s.facts.FactsAt(0)
		if err == nil {
			return Recompute(facts, s.bounds, 0)
		}
	}
	return s.CurrentConclusion()
}

// Engine exposes the live engine (post-stop conclusions stay frozen).
func (s *System) Engine() *Engine { return s.engine }

// Ledger exposes the append-only action ledger.
func (s *System) Ledger() *Ledger { return s.ledger }

// Facts exposes the versioned fact store.
func (s *System) Facts() *FactStore { return s.facts }

// Checkpoints returns the recorded checkpoints.
func (s *System) Checkpoints() []Checkpoint { return s.checkpoints }

// Conclusions returns all recomputed conclusions, one per revision.
func (s *System) Conclusions() []Conclusion { return s.conclusions }

// Compensations returns all compensation suggestions emitted so far.
func (s *System) Compensations() []Compensation { return s.compensations }

// Replay rebuilds a system by folding the event log from scratch.
func Replay(b Boundaries, events []Event) (*System, error) {
	s := NewSystem(b)
	for i, ev := range events {
		if err := s.Apply(ev); err != nil {
			return nil, fmt.Errorf("event %d: %v", i, err)
		}
	}
	return s, nil
}

// VerifyRecovery checks checkpoint recovery by event replay: for
// every recorded checkpoint, it replays the log up to the checkpoint,
// restores a fresh engine from it, feeds the remaining observations,
// and requires the recovered engine to reach the same frozen state
// as the straight-through replay.
func VerifyRecovery(b Boundaries, events []Event) error {
	full, err := Replay(b, events)
	if err != nil {
		return err
	}
	// Re-walk the log, snapshotting at each checkpoint event.
	var prefix []Event
	cpIdx := 0
	for i, ev := range events {
		prefix = append(prefix, ev)
		if ev.Kind != EvCheckpoint {
			continue
		}
		if cpIdx >= len(full.checkpoints) {
			return fmt.Errorf("checkpoint %d missing from replay", cpIdx)
		}
		cp := full.checkpoints[cpIdx]
		cpIdx++
		if err := VerifyCheckpoint(full.rawLog[:obsCount(prefix)], b, cp); err != nil {
			return fmt.Errorf("checkpoint %d at event %d: %v", cpIdx-1, i, err)
		}
		// Restore from the checkpoint and continue with the rest.
		recovered := Restore(b, cp)
		for _, ev2 := range events[i+1:] {
			if ev2.Kind == EvObserve {
				recovered.Process(*ev2.Obs)
			}
		}
		if err := sameEngine(full.engine, recovered); err != nil {
			return fmt.Errorf("recovery from checkpoint %d: %v", cpIdx-1, err)
		}
	}
	return nil
}

func obsCount(events []Event) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == EvObserve {
			n++
		}
	}
	return n
}

func sameEngine(a, b *Engine) error {
	an, as, ai := a.Counts()
	bn, bs, bi := b.Counts()
	if an != bn || as != bs || ai != bi {
		return fmt.Errorf("counts differ: (%d,%d,%d) vs (%d,%d,%d)", an, as, ai, bn, bs, bi)
	}
	if a.Sum() != b.Sum() {
		return fmt.Errorf("sums differ: %d vs %d", a.Sum(), b.Sum())
	}
	if a.Stopped() != b.Stopped() || a.Decision() != b.Decision() {
		return fmt.Errorf("conclusions differ: (%v,%s) vs (%v,%s)",
			a.Stopped(), a.Decision(), b.Stopped(), b.Decision())
	}
	ae, be := a.Evidence(), b.Evidence()
	if (ae == nil) != (be == nil) {
		return fmt.Errorf("evidence presence differs")
	}
	if ae != nil && *ae != *be {
		return fmt.Errorf("evidence differs: %v vs %v", *ae, *be)
	}
	return nil
}
