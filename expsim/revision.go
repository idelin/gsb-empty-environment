package expsim

import "fmt"

// Revision is a late correction to a previously recorded observation.
// Revisions never rewrite the original fact: each one creates a new
// fact version.
type Revision struct {
	ObsID string
	Value int64
	Note  string
}

// FactStore keeps every fact version: the base observations plus all
// late revisions. Versions are monotonically increasing integers;
// version 0 is the unrevised base.
type FactStore struct {
	base      map[string]Observation
	order     []string
	revisions []Revision // revisions[i] creates version i+1
}

// NewFactStore seeds the store with the original observations.
func NewFactStore(base []Observation) *FactStore {
	fs := &FactStore{base: make(map[string]Observation)}
	for _, o := range base {
		if _, ok := fs.base[o.ID]; !ok {
			fs.order = append(fs.order, o.ID)
		}
		fs.base[o.ID] = o
	}
	return fs
}

// Revise records a late correction and returns the new fact version.
func (fs *FactStore) Revise(r Revision) (int, error) {
	if _, ok := fs.base[r.ObsID]; !ok {
		return 0, fmt.Errorf("revision targets unknown observation %q", r.ObsID)
	}
	fs.revisions = append(fs.revisions, r)
	return len(fs.revisions), nil
}

// Version returns the current fact version.
func (fs *FactStore) Version() int { return len(fs.revisions) }

// FactsAt materialises the effective observations at a fact version.
func (fs *FactStore) FactsAt(version int) ([]Observation, error) {
	if version < 0 || version > len(fs.revisions) {
		return nil, fmt.Errorf("unknown fact version %d", version)
	}
	vals := make(map[string]int64)
	for id, o := range fs.base {
		vals[id] = o.Value
	}
	for i := 0; i < version; i++ {
		vals[fs.revisions[i].ObsID] = fs.revisions[i].Value
	}
	out := make([]Observation, 0, len(fs.order))
	for _, id := range fs.order {
		o := fs.base[id]
		o.Value = vals[id]
		out = append(out, o)
	}
	return out, nil
}

// Conclusion is the recomputed outcome of the experiment under one
// fact version.
type Conclusion struct {
	FactVersion int
	StopStep    int // 0 while running
	Sum         int64
	Decision    Decision
	Evidence    *StopEvidence
}

// Recompute derives the conclusion from scratch for the given facts,
// finding the first boundary touch by prefix enumeration.
func Recompute(facts []Observation, b Boundaries, factVersion int) Conclusion {
	unique, _, _ := Dedupe(facts)
	SortObs(unique)
	c := Conclusion{FactVersion: factVersion}
	var sum int64
	for i, o := range unique {
		sum += o.Value
		step := i + 1
		if dec, hit := b.touches(step, sum); hit {
			c.StopStep = step
			c.Sum = sum
			c.Decision = dec
			c.Evidence = &StopEvidence{
				Step:     step,
				Sum:      sum,
				Upper:    b.Upper[step-1],
				Lower:    b.Lower[step-1],
				Decision: dec,
				ObsID:    o.ID,
				ObsTime:  o.Time,
				ObsSeq:   o.Seq,
				ObsValue: o.Value,
			}
			return c
		}
	}
	c.Sum = sum
	return c
}

// Shift describes how a recomputed stop point moved relative to the
// previously recorded conclusion.
type Shift int

const (
	ShiftUnchanged Shift = iota
	ShiftEarlier         // stop point moved earlier
	ShiftLater           // stop point moved later (including losing the stop)
	ShiftAppeared        // no stop before, stop now
	ShiftVanished        // stop before, none now
)

func (s Shift) String() string {
	switch s {
	case ShiftEarlier:
		return "earlier"
	case ShiftLater:
		return "later"
	case ShiftAppeared:
		return "appeared"
	case ShiftVanished:
		return "vanished"
	default:
		return "unchanged"
	}
}

// CompareConclusions classifies the movement of the stop point.
func CompareConclusions(old, new Conclusion) Shift {
	switch {
	case old.StopStep == 0 && new.StopStep == 0:
		return ShiftUnchanged
	case old.StopStep == 0:
		return ShiftAppeared
	case new.StopStep == 0:
		return ShiftVanished
	case new.StopStep < old.StopStep:
		return ShiftEarlier
	case new.StopStep > old.StopStep:
		return ShiftLater
	default:
		return ShiftUnchanged
	}
}

// Action is an executed, irreversible real-world action. The ledger
// is append-only: actions are never edited or deleted, only
// compensated.
type Action struct {
	ID          string
	Step        int    // experiment step at which the action executed
	Kind        string // e.g. "ship", "rollback"
	FactVersion int    // fact version the action decision relied on
	Note        string
}

// Ledger is the append-only record of executed actions.
type Ledger struct {
	actions []Action
	seq     int
}

// Append executes an action and records it immutably.
func (l *Ledger) Append(a Action) Action {
	l.seq++
	a.ID = fmt.Sprintf("act-%d", l.seq)
	l.actions = append(l.actions, a)
	return a
}

// Actions returns a copy of the immutable ledger.
func (l *Ledger) Actions() []Action {
	out := make([]Action, len(l.actions))
	copy(out, l.actions)
	return out
}

// Compensation is a suggestion produced when a recomputed conclusion
// conflicts with an already executed action. It never modifies the
// ledger; it only advises a compensating counter-action.
type Compensation struct {
	ActionID   string
	ActionStep int
	Kind       string
	Reason     string
	Suggestion string
}

// Compensate compares the conclusion an action relied on with the
// recomputed conclusion and emits compensation suggestions for every
// executed action that the new facts would have prevented, and for
// the overall experiment direction if the stop moved.
func Compensate(ledger *Ledger, oldConclusion, newConclusion Conclusion) []Compensation {
	var out []Compensation
	shift := CompareConclusions(oldConclusion, newConclusion)
	for _, a := range ledger.Actions() {
		prevented := false
		switch shift {
		case ShiftEarlier:
			// Stop moved earlier: anything executed after the new
			// stop step should not have happened.
			prevented = a.Step > newConclusion.StopStep
		case ShiftVanished:
			// No stop at all under the new facts: every action taken
			// because of the old stop is void.
			prevented = a.Step >= oldConclusion.StopStep
		}
		if prevented {
			out = append(out, Compensation{
				ActionID:   a.ID,
				ActionStep: a.Step,
				Kind:       a.Kind,
				Reason: fmt.Sprintf("stop moved from step %d to %s under fact version %d",
					oldConclusion.StopStep, stopLabel(newConclusion), newConclusion.FactVersion),
				Suggestion: fmt.Sprintf("execute counter-action for %q (executed at step %d); ledger entry %s stays untouched",
					a.Kind, a.Step, a.ID),
			})
		}
	}
	if shift == ShiftLater || shift == ShiftAppeared {
		out = append(out, Compensation{
			Reason: fmt.Sprintf("stop moved %s under fact version %d: actions up to step %d remain valid, experiment should have continued",
				shift, newConclusion.FactVersion, newConclusion.StopStep),
			Suggestion: "resume accumulation from the recorded checkpoint; do not re-execute past actions",
		})
	}
	return out
}

func stopLabel(c Conclusion) string {
	if c.StopStep == 0 {
		return "no stop"
	}
	return fmt.Sprintf("step %d", c.StopStep)
}

// Add registers a base fact if its ID is not yet known. Repeated
// deliveries of the same ID keep the first record.
func (fs *FactStore) Add(o Observation) {
	if _, ok := fs.base[o.ID]; ok {
		return
	}
	fs.base[o.ID] = o
	fs.order = append(fs.order, o.ID)
}
