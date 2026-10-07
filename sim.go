package main

import (
	"fmt"
	"strings"
)

// SlotState is the state of one machine slot in a station.
type SlotState int

const (
	Empty SlotState = iota
	Waiting
	Processing
	Blocked
)

func (s SlotState) String() string {
	switch s {
	case Empty:
		return "."
	case Waiting:
		return "W"
	case Processing:
		return "P"
	case Blocked:
		return "B"
	}
	return "?"
}

// Slot is one machine position. A finished part that cannot leave
// stays Blocked and keeps occupying the slot.
type Slot struct {
	State     SlotState
	Remaining int // ticks of processing left (only when Processing)
}

// Station is a capacity-limited group of identical parallel machines.
// Next is the downstream station index, or -1 for the sink (infinite capacity).
// Link is the deterministic transfer time (in ticks) to Next.
type Station struct {
	Name  string
	Cap   int
	Proc  int
	Next  int
	Link  int
	Slots []Slot
}

// Transit is a part currently moving between stations. While in transit
// it already reserves a slot at its destination station.
type Transit struct {
	Dest   int
	Arrive int // tick at which the part enters Dest
}

// Sim is the whole system. Tick order is fixed and public:
//  1. departures (blocked parts leave if downstream has a free slot,
//     repeated in station order until no more departures -> cascade)
//  2. arrivals (in-transit parts whose transfer ends this tick enter)
//  3. start work (waiting parts begin processing)
type Sim struct {
	Stations   []*Station
	Source     bool // infinite part supply feeding SourceDest
	SourceDest int
	SourceLink int
	Transit    []Transit
	Tick       int
	Deadlock   bool
}

func NewSim(stations []*Station, source bool, sourceDest, sourceLink int) *Sim {
	for _, st := range stations {
		st.Slots = make([]Slot, st.Cap)
		if st.Proc < 1 {
			panic("Proc must be >= 1")
		}
	}
	return &Sim{Stations: stations, Source: source, SourceDest: sourceDest, SourceLink: sourceLink}
}

// freeSlots returns capacity at station idx not occupied and not
// already reserved by in-transit parts bound for it.
func (s *Sim) freeSlots(idx int) int {
	st := s.Stations[idx]
	occ := 0
	for _, sl := range st.Slots {
		if sl.State != Empty {
			occ++
		}
	}
	res := 0
	for _, tr := range s.Transit {
		if tr.Dest == idx {
			res++
		}
	}
	return st.Cap - occ - res
}

// Step advances one tick and returns the events that happened, in order.
func (s *Sim) Step() []string {
	var events []string

	// Parts whose processing completed at this tick boundary become Blocked.
	for _, st := range s.Stations {
		for i := range st.Slots {
			if st.Slots[i].State == Processing && st.Slots[i].Remaining == 0 {
				st.Slots[i].State = Blocked
				events = append(events, fmt.Sprintf("%s slot%d finished -> blocked", st.Name, i))
			}
		}
	}

	// Phase 1: departures, fixed station order, repeat until stable (cascade).
	for changed := true; changed; {
		changed = false
		if s.Source && s.freeSlots(s.SourceDest) > 0 {
			s.Transit = append(s.Transit, Transit{Dest: s.SourceDest, Arrive: s.Tick + s.SourceLink})
			events = append(events, fmt.Sprintf("source -> %s (arrive t=%d)", s.Stations[s.SourceDest].Name, s.Tick+s.SourceLink))
			changed = true
		}
		for _, st := range s.Stations {
			for i := range st.Slots {
				if st.Slots[i].State != Blocked {
					continue
				}
				if st.Next < 0 {
					st.Slots[i] = Slot{}
					events = append(events, fmt.Sprintf("%s slot%d -> sink", st.Name, i))
					changed = true
				} else if s.freeSlots(st.Next) > 0 {
					st.Slots[i] = Slot{}
					arr := s.Tick + st.Link
					s.Transit = append(s.Transit, Transit{Dest: st.Next, Arrive: arr})
					events = append(events, fmt.Sprintf("%s slot%d -> %s (arrive t=%d)", st.Name, i, s.Stations[st.Next].Name, arr))
					changed = true
				}
			}
		}
	}

	// Phase 2: arrivals.
	rest := s.Transit[:0]
	for _, tr := range s.Transit {
		if tr.Arrive == s.Tick {
			st := s.Stations[tr.Dest]
			placed := false
			for i := range st.Slots {
				if st.Slots[i].State == Empty {
					st.Slots[i] = Slot{State: Waiting}
					events = append(events, fmt.Sprintf("%s slot%d arrived", st.Name, i))
					placed = true
					break
				}
			}
			if !placed {
				panic("reservation violated: no free slot on arrival")
			}
		} else {
			rest = append(rest, tr)
		}
	}
	s.Transit = rest

	// Phase 3: start work.
	for _, st := range s.Stations {
		for i := range st.Slots {
			if st.Slots[i].State == Waiting {
				st.Slots[i] = Slot{State: Processing, Remaining: st.Proc}
				events = append(events, fmt.Sprintf("%s slot%d start (done t=%d)", st.Name, i, s.Tick+st.Proc))
			}
		}
	}

	// End of tick: processing advances.
	for _, st := range s.Stations {
		for i := range st.Slots {
			if st.Slots[i].State == Processing {
				st.Slots[i].Remaining--
			}
		}
	}

	s.Deadlock = s.checkDeadlock()
	if s.Deadlock {
		events = append(events, "DEADLOCK: no departures, transit, or work possible")
	}
	s.Tick++
	return events
}

// checkDeadlock reports whether no future progress is possible while
// at least one part is stuck blocked.
func (s *Sim) checkDeadlock() bool {
	processing, waiting, blocked := 0, 0, 0
	for _, st := range s.Stations {
		for _, sl := range st.Slots {
			switch sl.State {
			case Processing:
				processing++
			case Waiting:
				waiting++
			case Blocked:
				blocked++
			}
		}
	}
	if processing+waiting > 0 || len(s.Transit) > 0 {
		return false
	}
	if s.Source && s.freeSlots(s.SourceDest) > 0 {
		return false
	}
	return blocked > 0
}

// Snapshot renders the occupancy of every station, e.g. "S1[B] S2[P3] transit{->S2@4}".
func (s *Sim) Snapshot() string {
	var b strings.Builder
	for _, st := range s.Stations {
		fmt.Fprintf(&b, "%s[", st.Name)
		for _, sl := range st.Slots {
			if sl.State == Processing {
				fmt.Fprintf(&b, "P%d", sl.Remaining)
			} else {
				b.WriteString(sl.State.String())
			}
		}
		b.WriteString("] ")
	}
	if len(s.Transit) > 0 {
		b.WriteString("transit{")
		for i, tr := range s.Transit {
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "->%s@%d", s.Stations[tr.Dest].Name, tr.Arrive)
		}
		b.WriteString("}")
	}
	return strings.TrimSpace(b.String())
}
