package sim

import "fmt"

// Simulator wires the versioned fact store, the live engine, the
// recomputed-conclusion history and the append-only action ledger
// together.
type Simulator struct {
	Lower int
	Upper int

	Store  *FactStore
	Live   *Engine
	Ledger *Ledger

	// Conclusions is the versioned history of conclusions. Entry 0 is the
	// empty experiment; every applied event appends exactly one entry.
	Conclusions []Conclusion
}

func NewSimulator(lower, upper int) *Simulator {
	s := &Simulator{
		Lower:  lower,
		Upper:  upper,
		Store:  NewFactStore(),
		Live:   NewEngine(lower, upper),
		Ledger: NewLedger(),
	}
	s.Conclusions = []Conclusion{s.Live.Conclusion()}
	return s
}

// Apply ingests one event in arrival order and returns any ledger entries
// it produced.
func (s *Simulator) Apply(ev Event) []ActionEntry {
	changed, _ := s.Store.Apply(ev)
	if !changed {
		return nil // duplicate observation or revision of an unknown fact
	}
	if ev.Kind == EventRevise {
		return s.recompute()
	}
	if s.Live.Ingest(Observation{Key: ev.Key, Value: ev.Value}) {
		entry := s.Ledger.Execute(s.Live.Conclusion())
		s.Conclusions = append(s.Conclusions, s.Live.Conclusion())
		return []ActionEntry{entry}
	}
	s.Conclusions = append(s.Conclusions, s.Live.Conclusion())
	return nil
}

// recompute rebuilds the conclusion from the newest fact versions after a
// late revision. The live engine is replaced so the frozen-conclusion
// guarantee follows the recomputed truth. Already executed actions stay
// untouched; divergence is handled by appending compensation suggestions.
func (s *Simulator) recompute() []ActionEntry {
	obs := s.Store.Latest()
	concl := Recompute(s.Lower, s.Upper, obs)

	live := NewEngine(s.Lower, s.Upper)
	for _, ob := range obs {
		live.Ingest(ob)
	}
	s.Live = live
	s.Conclusions = append(s.Conclusions, concl)

	executed := s.Ledger.Executed()
	if len(executed) == 0 {
		if concl.Stopped {
			return []ActionEntry{s.Ledger.Execute(concl)}
		}
		return nil
	}
	last := executed[len(executed)-1]
	if last.Conclusion.Equal(concl) {
		return nil
	}
	entry := s.Ledger.Compensate(last, concl, compensationReason(last.Conclusion, concl))
	return []ActionEntry{entry}
}

func compensationReason(old, new Conclusion) string {
	switch {
	case old.Stopped && !new.Stopped:
		return fmt.Sprintf("revision erased the %s touch at %+v; executed action stands, offset advised",
			old.Boundary, old.Evidence)
	case old.Boundary != new.Boundary:
		return fmt.Sprintf("revision flipped the touched edge from %s to %s; executed action stands, offset advised",
			old.Boundary, new.Boundary)
	case new.Steps < old.Steps:
		return fmt.Sprintf("stop moved earlier from step %d to step %d; executed action stands, offset advised",
			old.Steps, new.Steps)
	default:
		return fmt.Sprintf("stop moved later from step %d to step %d; executed action stands, offset advised",
			old.Steps, new.Steps)
	}
}
