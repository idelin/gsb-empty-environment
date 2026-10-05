package sched

import (
	"fmt"
	"reflect"
	"sort"
)

// Position describes a job position and the qualifications it requires.
type Position struct {
	ID            string   `json:"id"`
	RequiredQuals []string `json:"requiredQuals"`
}

// Shift is a schedulable unit of work over [Start, End) in minutes.
// Cross-midnight shifts simply have End > 1440.
type Shift struct {
	ID       string  `json:"id"`
	Employee string  `json:"employee"`
	Position string  `json:"position"`
	Start    Minutes `json:"start"`
	End      Minutes `json:"end"`
}

// When returns the shift's working interval.
func (s Shift) When() Interval { return Interval{Start: s.Start, End: s.End} }

// QualGap lists the minimal disjoint uncovered intervals for one
// required qualification.
type QualGap struct {
	Qual      string     `json:"qual"`
	Intervals []Interval `json:"intervals"`
}

// Report is the schedulability verdict for one shift at one query sequence.
type Report struct {
	ShiftID     string    `json:"shift"`
	Schedulable bool      `json:"schedulable"`
	Gaps        []QualGap `json:"gaps,omitempty"`
}

// Engine holds the domain data, the event log and the shift interval index.
type Engine struct {
	store     Store
	positions map[string]Position
	shifts    map[string]Shift
	index     map[string]*shiftIndex // employee -> shifts sorted by start
}

// NewEngine validates positions and shifts and builds the interval index.
func NewEngine(positions []Position, shifts []Shift) (*Engine, error) {
	e := &Engine{
		positions: make(map[string]Position, len(positions)),
		shifts:    make(map[string]Shift, len(shifts)),
		index:     make(map[string]*shiftIndex),
	}
	for _, p := range positions {
		if p.ID == "" {
			return nil, fmt.Errorf("position with empty id")
		}
		if _, dup := e.positions[p.ID]; dup {
			return nil, fmt.Errorf("duplicate position %q", p.ID)
		}
		e.positions[p.ID] = p
	}
	for _, sh := range shifts {
		if _, ok := e.positions[sh.Position]; !ok {
			return nil, fmt.Errorf("shift %q: unknown position %q", sh.ID, sh.Position)
		}
		if _, dup := e.shifts[sh.ID]; dup {
			return nil, fmt.Errorf("duplicate shift %q", sh.ID)
		}
		if sh.When().Empty() {
			return nil, fmt.Errorf("shift %q: empty interval [%d, %d)", sh.ID, sh.Start, sh.End)
		}
		e.shifts[sh.ID] = sh
		ix := e.index[sh.Employee]
		if ix == nil {
			ix = &shiftIndex{}
			e.index[sh.Employee] = ix
		}
		ix.add(sh)
	}
	return e, nil
}

// shiftIndex is a per-employee interval index over shifts, kept sorted by
// start minute so candidates for an event can be found with binary search.
type shiftIndex struct {
	shifts []Shift
}

func (ix *shiftIndex) add(s Shift) {
	i := sort.Search(len(ix.shifts), func(i int) bool {
		if ix.shifts[i].Start != s.Start {
			return ix.shifts[i].Start > s.Start
		}
		return ix.shifts[i].ID > s.ID
	})
	ix.shifts = append(ix.shifts, Shift{})
	copy(ix.shifts[i+1:], ix.shifts[i:])
	ix.shifts[i] = s
}

// intersecting returns all shifts whose interval intersects iv.
func (ix *shiftIndex) intersecting(iv Interval) []Shift {
	n := sort.Search(len(ix.shifts), func(i int) bool { return ix.shifts[i].Start >= iv.End })
	var out []Shift
	for _, s := range ix.shifts[:n] {
		if s.End > iv.Start {
			out = append(out, s)
		}
	}
	return out
}

func requiresQual(p Position, qual string) bool {
	for _, q := range p.RequiredQuals {
		if q == qual {
			return true
		}
	}
	return false
}

// Check evaluates one shift as of the given record sequence: every
// required qualification must be covered for the whole shift interval.
func (e *Engine) Check(shiftID string, asOfSeq int) (Report, error) {
	sh, ok := e.shifts[shiftID]
	if !ok {
		return Report{}, fmt.Errorf("unknown shift %q", shiftID)
	}
	rep := Report{ShiftID: shiftID, Schedulable: true}
	for _, qual := range e.positions[sh.Position].RequiredQuals {
		gaps := Subtract(sh.When(), e.store.coverage(sh.Employee, qual, asOfSeq))
		if len(gaps) > 0 {
			rep.Schedulable = false
			rep.Gaps = append(rep.Gaps, QualGap{Qual: qual, Intervals: gaps})
		}
	}
	return rep, nil
}

// ShiftIDs returns all shift IDs in sorted order.
func (e *Engine) ShiftIDs() []string {
	ids := make([]string, 0, len(e.shifts))
	for id := range e.shifts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// CheckAll evaluates every shift as of the given record sequence.
func (e *Engine) CheckAll(asOfSeq int) []Report {
	ids := e.ShiftIDs()
	reps := make([]Report, 0, len(ids))
	for _, id := range ids {
		rep, err := e.Check(id, asOfSeq)
		if err != nil {
			panic(err) // ids come from the engine itself
		}
		reps = append(reps, rep)
	}
	return reps
}

// candidates returns the shifts that an event can possibly affect: same
// employee, interval intersecting the event's business validity, and a
// position requiring the event's qualification.
func (e *Engine) candidates(ev Event) []Shift {
	ix := e.index[ev.Employee]
	if ix == nil {
		return nil
	}
	var out []Shift
	for _, sh := range ix.intersecting(ev.Valid()) {
		if requiresQual(e.positions[sh.Position], ev.Qual) {
			out = append(out, sh)
		}
	}
	return out
}

// AddEvent records an event and returns the IDs of the historical shifts
// whose report changes once the event is known. Only candidate shifts
// found through the interval index are re-evaluated.
func (e *Engine) AddEvent(ev Event) (affected []string, err error) {
	if err := validateEvent(ev); err != nil {
		return nil, err
	}
	cands := e.candidates(ev)
	before := make(map[string]Report, len(cands))
	for _, sh := range cands {
		rep, err := e.Check(sh.ID, ev.Seq)
		if err != nil {
			return nil, err
		}
		before[sh.ID] = rep
	}
	if err := e.store.Add(ev); err != nil {
		return nil, err
	}
	for _, sh := range cands {
		after, err := e.Check(sh.ID, ev.Seq+1)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(before[sh.ID], after) {
			affected = append(affected, sh.ID)
		}
	}
	sort.Strings(affected)
	return affected, nil
}

// AffectedByFullScan is the reference implementation that does not use the
// interval index: it re-evaluates every shift before and after the event.
// The engine is left unmodified.
func (e *Engine) AffectedByFullScan(ev Event) ([]string, error) {
	if err := validateEvent(ev); err != nil {
		return nil, err
	}
	if n := len(e.store.events); n > 0 && e.store.events[n-1].Seq >= ev.Seq {
		return nil, fmt.Errorf("event seq %d: sequence numbers must be strictly increasing (last is %d)", ev.Seq, e.store.events[n-1].Seq)
	}
	before := e.CheckAll(ev.Seq)
	saved := e.store.events
	e.store.events = append(append([]Event(nil), saved...), ev)
	after := e.CheckAll(ev.Seq + 1)
	e.store.events = saved
	var affected []string
	for i := range before {
		if !reflect.DeepEqual(before[i], after[i]) {
			affected = append(affected, before[i].ShiftID)
		}
	}
	return affected, nil
}

// ShiftGaps groups one shift's uncovered intervals under a qualification.
type ShiftGaps struct {
	ShiftID   string     `json:"shift"`
	Intervals []Interval `json:"intervals"`
}

// QualImpact groups, per missing qualification, the shifts involved and
// their minimal disjoint gap intervals.
type QualImpact struct {
	Qual   string      `json:"qual"`
	Shifts []ShiftGaps `json:"shifts"`
}

// AggregateByQual turns per-shift reports into a per-qualification view.
func AggregateByQual(reps []Report) []QualImpact {
	order := []string{}
	byQual := map[string][]ShiftGaps{}
	for _, rep := range reps {
		for _, g := range rep.Gaps {
			if _, ok := byQual[g.Qual]; !ok {
				order = append(order, g.Qual)
			}
			byQual[g.Qual] = append(byQual[g.Qual], ShiftGaps{ShiftID: rep.ShiftID, Intervals: g.Intervals})
		}
	}
	sort.Strings(order)
	out := make([]QualImpact, 0, len(order))
	for _, q := range order {
		out = append(out, QualImpact{Qual: q, Shifts: byQual[q]})
	}
	return out
}
