package main

import "fmt"

// Machine states.
const (
	Idle = iota
	Loaded  // holds a job, starts processing in the start-work phase of this tick
	Busy    // processing, Remaining ticks of work left
	Blocked // processing finished, downstream full: job holds the machine
)

// Job travels along Route (station indices); Step is its current position.
type Job struct {
	ID    string
	Route []int
	Step  int
}

// NextStation returns the station after the current one, or false if the
// job leaves the system after its current station.
func (j *Job) NextStation() (int, bool) {
	if j.Step+1 < len(j.Route) {
		return j.Route[j.Step+1], true
	}
	return -1, false
}

type Machine struct {
	State     int
	Job       *Job
	Remaining int
}

type Station struct {
	Name     string
	ProcTime int // ticks a job occupies a machine (>= 1)
	Machines []*Machine
}

func NewStation(name string, procTime, capacity int) *Station {
	s := &Station{Name: name, ProcTime: procTime}
	for i := 0; i < capacity; i++ {
		s.Machines = append(s.Machines, &Machine{})
	}
	return s
}

func (s *Station) IdleMachine() *Machine {
	for _, m := range s.Machines {
		if m.State == Idle {
			return m
		}
	}
	return nil
}

func (s *Station) HasBusy() bool {
	for _, m := range s.Machines {
		if m.State == Busy {
			return true
		}
	}
	return false
}

// System is a set of stations with zero intermediate buffers: a finished
// job that cannot enter the downstream station stays on its machine and
// blocks it. Tick phases run in a fixed public order:
//   1. departures  (stations scanned downstream -> upstream, so a freed
//      machine cascades unblocking within the same tick)
//   2. arrivals    (external jobs enter the first station)
//   3. start work  (loaded machines begin processing)
type System struct {
	Stations []*Station
	Entry    []*Job          // FIFO queue in front of the first station
	Arrivals map[int][]*Job  // external arrivals keyed by tick
	Exited   []*Job
	Events   []string        // per-tick log of the current tick
}

// tryMove attempts to push the finished job on machine m of station si
// downstream. Returns false (machine now Blocked) if downstream is full.
func (sys *System) tryMove(si int, m *Machine) bool {
	j := m.Job
	next, ok := j.NextStation()
	if !ok {
		sys.Exited = append(sys.Exited, j)
		sys.Events = append(sys.Events, fmt.Sprintf("%s exits from %s", j.ID, sys.Stations[si].Name))
		m.State, m.Job = Idle, nil
		return true
	}
	if dst := sys.Stations[next].IdleMachine(); dst != nil {
		dst.State, dst.Job = Loaded, j
		j.Step++
		sys.Events = append(sys.Events, fmt.Sprintf("%s: %s -> %s", j.ID, sys.Stations[si].Name, sys.Stations[next].Name))
		m.State, m.Job = Idle, nil
		return true
	}
	if m.State != Blocked {
		sys.Events = append(sys.Events, fmt.Sprintf("%s finished at %s but %s is full: BLOCKED", j.ID, sys.Stations[si].Name, sys.Stations[next].Name))
	}
	m.State = Blocked
	return false
}

// Tick advances the simulation by one time unit.
func (sys *System) Tick(t int) {
	// Phase 1: departures, downstream station first (fixed public order).
	for si := len(sys.Stations) - 1; si >= 0; si-- {
		for _, m := range sys.Stations[si].Machines {
			switch {
			case m.State == Busy && m.Remaining == 0:
				sys.tryMove(si, m)
			case m.State == Blocked:
				sys.tryMove(si, m)
			}
		}
	}
	// Phase 2: arrivals enter the first station.
	sys.Entry = append(sys.Entry, sys.Arrivals[t]...)
	delete(sys.Arrivals, t)
	for len(sys.Entry) > 0 {
		m := sys.Stations[0].IdleMachine()
		if m == nil {
			break
		}
		j := sys.Entry[0]
		sys.Entry = sys.Entry[1:]
		m.State, m.Job = Loaded, j
		sys.Events = append(sys.Events, fmt.Sprintf("%s enters %s", j.ID, sys.Stations[0].Name))
	}
	// Phase 3: start work on loaded machines.
	for _, s := range sys.Stations {
		for _, m := range s.Machines {
			if m.State == Loaded {
				m.State = Busy
				m.Remaining = s.ProcTime
			}
		}
	}
	// Clock: busy machines consume one tick of work.
	for _, s := range sys.Stations {
		for _, m := range s.Machines {
			if m.State == Busy && m.Remaining > 0 {
				m.Remaining--
			}
		}
	}
}

// PendingJobs counts jobs still inside or heading for the system.
func (sys *System) PendingJobs() int {
	n := len(sys.Entry)
	for _, jobs := range sys.Arrivals {
		n += len(jobs)
	}
	for _, s := range sys.Stations {
		for _, m := range s.Machines {
			if m.Job != nil {
				n++
			}
		}
	}
	return n
}

// Status is "DONE", "DEADLOCK" or "RUNNING" evaluated after a tick.
// With no Busy machine anywhere nothing can ever change again, so any
// remaining job means the system is deadlocked.
func (sys *System) Status() string {
	if sys.PendingJobs() == 0 {
		return "DONE"
	}
	for _, s := range sys.Stations {
		if s.HasBusy() {
			return "RUNNING"
		}
	}
	return "DEADLOCK"
}

func (m *Machine) String() string {
	switch m.State {
	case Busy:
		return fmt.Sprintf("%s:%d", m.Job.ID, m.Remaining)
	case Blocked:
		return m.Job.ID + "!"
	case Loaded:
		return m.Job.ID + ">"
	}
	return "."
}

// Row renders the occupancy of every machine at the end of tick t.
func (sys *System) Row(t int) string {
	out := fmt.Sprintf("t=%02d |", t)
	for _, s := range sys.Stations {
		out += " " + s.Name + "["
		for i, m := range s.Machines {
			if i > 0 {
				out += " "
			}
			out += m.String()
		}
		out += "] |"
	}
	if len(sys.Entry) > 0 {
		out += " queue:"
		for _, j := range sys.Entry {
			out += " " + j.ID
		}
	}
	return out
}
