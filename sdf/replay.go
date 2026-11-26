package sdf

import "errors"

// ReplayError describes a schedule event that violates runtime invariants.
type ReplayError struct {
	EventIndex int
	Event      Event
	Reason     string
}

func (e *ReplayError) Error() string {
	return "sdf replay: event " + itoa(e.EventIndex) + " (" + e.Event.Kind.String() + "): " + e.Reason
}

type replayFlight struct {
	actor int
	end   int64
}

type replayModel struct {
	occ       []int
	resv      []int
	active    []replayFlight
	busyUntil int64
	lastTime  int64
}

// Verify replays the prefix plus the requested number of steady cycles on an
// independent resource model and checks every launch/completion, token count,
// reserved output slot, descriptor limit and monotonic timestamp.
func (s *Schedule) Verify(p *Problem, cycles int) error {
	if cycles < 1 {
		cycles = 1
	}
	idx, err := p.validate()
	if err != nil {
		return err
	}
	E := len(p.Edges)
	m := replayModel{
		occ:       make([]int, E),
		resv:      make([]int, E),
		busyUntil: -1,
		lastTime:  -1,
	}
	for eid, e := range p.Edges {
		m.occ[eid] = e.Delay
	}
	caps := s.caps
	if caps == nil {
		caps = make([]int, E)
		for eid, e := range p.Edges {
			caps[eid] = max2(e.Cap, p.MaxCap)
		}
	}

	ei := 0
	for _, ev := range s.PrefixEvents {
		if err := m.step(p, idx, caps, ev, 0, ei); err != nil {
			return err
		}
		ei++
	}
	for c := 0; c < cycles; c++ {
		shift := s.CycleStart + int64(c)*s.CycleTime
		for _, ev := range s.CycleEvents {
			if err := m.step(p, idx, caps, ev, shift, ei); err != nil {
				return err
			}
			ei++
		}
	}
	return nil
}

func (m *replayModel) step(p *Problem, idx *index, caps []int, ev Event, shift int64, ei int) error {
	t := ev.Time + shift
	if t < m.lastTime {
		return &ReplayError{EventIndex: ei, Event: ev, Reason: "non-monotonic timestamp"}
	}
	m.lastTime = t

	// Publish firings strictly before t; firings ending exactly at t are
	// published by their own Complete event below.
	remain := m.active[:0]
	for _, f := range m.active {
		if f.end < t {
			for _, eid := range idx.out[f.actor] {
				if p.Edges[eid].IsOutput() {
					continue
				}
				m.resv[eid] -= p.Edges[eid].Prod
				m.occ[eid] += p.Edges[eid].Prod
			}
			if m.busyUntil == f.end {
				m.busyUntil = -1
			}
		} else {
			remain = append(remain, f)
		}
	}
	m.active = remain

	switch ev.Kind {
	case EventInputArrive:
		if !p.Edges[ev.Edge].IsInput() {
			return &ReplayError{EventIndex: ei, Event: ev, Reason: "input event on non-input edge"}
		}
		if m.occ[ev.Edge] >= caps[ev.Edge] {
			return &ReplayError{EventIndex: ei, Event: ev, Reason: "input overrun on bounded edge"}
		}
		m.occ[ev.Edge]++
	case EventLaunch:
		a := ev.Actor
		if m.busyUntil > t {
			return &ReplayError{EventIndex: ei, Event: ev, Reason: "compute unit already running a firing"}
		}
		if len(m.active) >= p.Res.Descriptors {
			return &ReplayError{EventIndex: ei, Event: ev, Reason: "DMA descriptor pool exhausted"}
		}
		for _, eid := range idx.in[a] {
			if m.occ[eid] < p.Edges[eid].Cons {
				return &ReplayError{EventIndex: ei, Event: ev, Reason: "launch without enough input tokens"}
			}
		}
		post := append([]int(nil), m.occ...)
		for _, eid := range idx.in[a] {
			post[eid] -= p.Edges[eid].Cons
		}
		for _, eid := range idx.out[a] {
			e := p.Edges[eid]
			if e.IsOutput() {
				continue
			}
			if post[eid]+m.resv[eid]+e.Prod > caps[eid] {
				return &ReplayError{EventIndex: ei, Event: ev, Reason: "reservation exceeds edge capacity"}
			}
		}
		for _, eid := range idx.in[a] {
			m.occ[eid] -= p.Edges[eid].Cons
		}
		for _, eid := range idx.out[a] {
			if !p.Edges[eid].IsOutput() {
				m.resv[eid] += p.Edges[eid].Prod
			}
		}
		m.active = append(m.active, replayFlight{a, t + p.Cost[a]})
		m.busyUntil = t + p.Cost[a]
	case EventComplete:
		pos := -1
		for i, f := range m.active {
			if f.actor == ev.Actor && f.end == t {
				pos = i
				break
			}
		}
		if pos < 0 {
			return &ReplayError{EventIndex: ei, Event: ev, Reason: "complete without matching in-flight firing"}
		}
		f := m.active[pos]
		for _, eid := range idx.out[f.actor] {
			if p.Edges[eid].IsOutput() {
				continue
			}
			m.resv[eid] -= p.Edges[eid].Prod
			m.occ[eid] += p.Edges[eid].Prod
		}
		if m.busyUntil == f.end {
			m.busyUntil = -1
		}
		m.active = append(m.active[:pos], m.active[pos+1:]...)
	case EventOutputCommit:
		if !p.Edges[ev.Edge].IsOutput() {
			return &ReplayError{EventIndex: ei, Event: ev, Reason: "output commit on non-output edge"}
		}
	default:
		return &ReplayError{EventIndex: ei, Event: ev, Reason: "unknown event kind"}
	}
	return nil
}

// VerifyCertificate independently recomputes structural lower bounds and
// checks each one-step reduction witness recorded in cert.
func VerifyCertificate(p *Problem, caps []int, cert *Certificate) error {
	idx, err := p.validate()
	if err != nil {
		return err
	}
	q, _, ok := repVector(p, idx)
	if !ok {
		return errors.New("sdf: certificate on inconsistent graph")
	}
	if len(caps) != len(p.Edges) || len(cert.EdgeLowerBound) != len(p.Edges) {
		return errors.New("sdf: certificate length mismatch")
	}
	for eid := range p.Edges {
		b, _, feasible := edgeLowerBound(p, eid)
		if !feasible {
			return errors.New("sdf: edge " + itoa(eid) + " has no feasible bound")
		}
		if b != cert.EdgeLowerBound[eid] {
			return errors.New("sdf: lower bound mismatch for edge " + itoa(eid))
		}
		if caps[eid] < b {
			return errors.New("sdf: chosen capacity below lower bound for edge " + itoa(eid))
		}
		if caps[eid] > b && !cert.Reduction[eid] {
			return errors.New("sdf: missing reduction witness for edge " + itoa(eid))
		}
		if caps[eid] > b {
			reduced := append([]int(nil), caps...)
			reduced[eid]--
			if evaluate(p, idx, reduced, q).status == StatusFeasible {
				return errors.New("sdf: claimed irreducible edge " + itoa(eid) + " is reducible")
			}
		}
	}
	return nil
}
