package netsec

import "fmt"

// MaxEvents is the maximum number of events a System can hold.
const MaxEvents = 50

// Probe identifies one of the two sensors whose clock offsets are unknown.
type Probe uint8

const (
	ProbeA Probe = 0
	ProbeB Probe = 1
)

func (p Probe) String() string {
	if p == ProbeA {
		return "A"
	}
	return "B"
}

// Interval is a closed integer interval [Lo, Hi] in milliseconds.
type Interval struct {
	Lo int64
	Hi int64
}

func (iv Interval) valid() bool { return iv.Lo <= iv.Hi }

// Event is a single observation. Its global time is Local + offset[Probe],
// where the probe's offset is only known to lie inside its Interval.
type Event struct {
	Probe Probe
	Local int64 // local clock reading, integer milliseconds
}

// ConstraintKind selects the meaning of a Constraint.
type ConstraintKind uint8

const (
	// Precedence means event I happens no later than event J:
	// global[J] - global[I] >= 0.
	Precedence ConstraintKind = iota
	// MaxGap means event J happens at most Gap milliseconds after event I:
	// global[J] - global[I] <= Gap.
	MaxGap
)

// Constraint relates the global times of two events.
type Constraint struct {
	Kind ConstraintKind
	I, J int   // event indices
	Gap  int64 // only used by MaxGap, inclusive bound in milliseconds
}

// System holds the events, the offset intervals of both probes and the
// constraints between event global times.
type System struct {
	offset [2]Interval
	events []Event
	cons   []Constraint
}

// NewSystem creates an empty system with the given offset intervals for the
// two probes.
func NewSystem(offsetA, offsetB Interval) (*System, error) {
	if !offsetA.valid() || !offsetB.valid() {
		return nil, fmt.Errorf("netsec: empty offset interval")
	}
	return &System{offset: [2]Interval{offsetA, offsetB}}, nil
}

// OffsetInterval returns the current offset interval of a probe.
func (s *System) OffsetInterval(p Probe) Interval { return s.offset[p] }

// Events returns the registered events.
func (s *System) Events() []Event { return s.events }

// Constraints returns the registered constraints.
func (s *System) Constraints() []Constraint { return s.cons }

// AddEvent registers an event and returns its index.
func (s *System) AddEvent(ev Event) (int, error) {
	if len(s.events) >= MaxEvents {
		return -1, fmt.Errorf("netsec: at most %d events", MaxEvents)
	}
	if ev.Probe != ProbeA && ev.Probe != ProbeB {
		return -1, fmt.Errorf("netsec: unknown probe %d", ev.Probe)
	}
	s.events = append(s.events, ev)
	return len(s.events) - 1, nil
}

// AddConstraint adds a precedence or max-gap constraint between two events.
func (s *System) AddConstraint(c Constraint) error {
	n := len(s.events)
	if c.I < 0 || c.I >= n || c.J < 0 || c.J >= n {
		return fmt.Errorf("netsec: constraint references unknown event")
	}
	if c.Kind == MaxGap && c.Gap < 0 {
		return fmt.Errorf("netsec: negative max gap")
	}
	s.cons = append(s.cons, c)
	return nil
}

// build assembles the difference-constraint graph. Variables are, in order:
// the global time of each event, offset[A], offset[B], and a zero anchor.
func (s *System) build(extra []edge) (int, []edge) {
	n := len(s.events)
	offA, offB, zero := n, n+1, n+2
	offVar := [2]int{offA, offB}

	var edges []edge
	for i, ev := range s.events {
		o := offVar[ev.Probe]
		edges = append(edges,
			edge{o, i, ev.Local, fmt.Sprintf("event %d (probe %s): g[%d] - offset[%s] <= %d", i, ev.Probe, i, ev.Probe, ev.Local)},
			edge{i, o, -ev.Local, fmt.Sprintf("event %d (probe %s): offset[%s] - g[%d] <= %d", i, ev.Probe, ev.Probe, i, -ev.Local)},
		)
	}
	for p := 0; p < 2; p++ {
		iv := s.offset[p]
		edges = append(edges,
			edge{zero, offVar[p], iv.Hi, fmt.Sprintf("calibration: offset[%s] <= %d", Probe(p), iv.Hi)},
			edge{offVar[p], zero, -iv.Lo, fmt.Sprintf("calibration: %d <= offset[%s]", iv.Lo, Probe(p))},
		)
	}
	for _, c := range s.cons {
		switch c.Kind {
		case Precedence:
			edges = append(edges, edge{c.J, c.I, 0,
				fmt.Sprintf("precedence: g[%d] <= g[%d]", c.I, c.J)})
		case MaxGap:
			edges = append(edges, edge{c.I, c.J, c.Gap,
				fmt.Sprintf("max gap: g[%d] - g[%d] <= %d", c.J, c.I, c.Gap)})
		}
	}
	edges = append(edges, extra...)
	return n + 3, edges
}

// Solution is a feasible assignment: one concrete offset per probe and the
// resulting global time of every event.
type Solution struct {
	Offsets [2]int64
	Global  []int64
}

func solutionFrom(s *System, dist []int64) *Solution {
	n := len(s.events)
	z := dist[n+2] // shift so the anchor variable is zero
	sol := &Solution{Global: make([]int64, n)}
	for i := range sol.Global {
		sol.Global[i] = dist[i] - z
	}
	sol.Offsets[0] = dist[n] - z
	sol.Offsets[1] = dist[n+1] - z
	return sol
}

// Solve checks the feasibility of the system. On success it returns a
// Solution witnessing concrete feasible offsets; on failure it returns the
// negative cycle that proves infeasibility.
func (s *System) Solve() (*Solution, *CycleEvidence) {
	numVars, edges := s.build(nil)
	dist, cycle := bellmanFord(numVars, edges)
	if cycle != nil {
		return nil, evidenceFrom(cycle)
	}
	return solutionFrom(s, dist), nil
}

// UpdateCalibration narrows (or replaces) the offset interval of a probe,
// e.g. after a new clock calibration. If the update contradicts the existing
// constraints the update is rejected, the previous interval is kept, and the
// negative cycle proving the contradiction is returned.
func (s *System) UpdateCalibration(p Probe, iv Interval) (*CycleEvidence, error) {
	if p != ProbeA && p != ProbeB {
		return nil, fmt.Errorf("netsec: unknown probe %d", p)
	}
	if !iv.valid() {
		return nil, fmt.Errorf("netsec: empty offset interval")
	}
	old := s.offset[p]
	s.offset[p] = iv
	if _, cyc := s.Solve(); cyc != nil {
		s.offset[p] = old
		return cyc, nil
	}
	return nil, nil
}
