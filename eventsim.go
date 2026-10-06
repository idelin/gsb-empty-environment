package flowshop

// This file contains an independent discrete-event simulation of the
// blocking flow shop. It shares no logic with the recursive model in
// flowshop.go so the two can cross-validate each other.

type evKind int

const (
	evProcDone  evKind = iota // priority 0 at equal timestamps
	evDownEnd                 // priority 1
	evDownStart               // priority 2
)

type simEvent struct {
	time    int
	kind    evKind
	machine int // 1 or 2
	gen     int // generation tag to invalidate superseded procDone events
	end     int // for evDownStart: end of that downtime interval
}

type simMachine struct {
	down      bool
	downEndAt int  // end of the downtime interval the machine is currently in
	occupied  bool // a job is on the machine (working, paused or blocked)
	working   bool // processing is advancing right now
	blocked   bool // M1 only: processing done, waiting for machine 2
	job       Job
	remaining int // processing left while paused
	doneAt    int // scheduled completion time while working
	gen       int
}

type simulator struct {
	seq       []Job
	next      int // index of next job to load onto M1
	m1, m2    simMachine
	events    []simEvent
	now       int
	schedule  Schedule
	blockFrom int // start of the current blocking period on M1, -1 if none
}

// simulateEvents runs a discrete-event simulation of seq from time 0 with
// the given (normalized) downtime tables and returns the full schedule.
func simulateEvents(seq []Job, down1, down2 []Interval) Schedule {
	sim := &simulator{seq: seq, blockFrom: -1}
	sim.schedule.Timeline = make([]JobTimeline, 0, len(seq))
	for _, d := range down1 {
		sim.addDowntime(1, d)
	}
	for _, d := range down2 {
		sim.addDowntime(2, d)
	}
	sim.loadM1()
	for len(sim.events) > 0 {
		ev := sim.pop()
		sim.now = ev.time
		switch ev.kind {
		case evProcDone:
			sim.onProcDone(ev)
		case evDownStart:
			sim.onDownStart(ev)
		case evDownEnd:
			sim.onDownEnd(ev)
		}
	}
	if n := len(sim.schedule.Timeline); n > 0 {
		sim.schedule.Makespan = sim.schedule.Timeline[n-1].DepartM2
	}
	return sim.schedule
}

func (sim *simulator) addDowntime(machine int, d Interval) {
	if d.Start <= 0 {
		m := sim.machine(machine)
		m.down = true
		m.downEndAt = d.End
	} else {
		sim.push(simEvent{time: d.Start, kind: evDownStart, machine: machine, end: d.End})
	}
	sim.push(simEvent{time: d.End, kind: evDownEnd, machine: machine})
}

func (sim *simulator) machine(which int) *simMachine {
	if which == 1 {
		return &sim.m1
	}
	return &sim.m2
}

func (sim *simulator) push(ev simEvent) {
	sim.events = append(sim.events, ev)
}

func (sim *simulator) pop() simEvent {
	best := 0
	for i := 1; i < len(sim.events); i++ {
		a, b := sim.events[i], sim.events[best]
		if a.time < b.time || (a.time == b.time && a.kind < b.kind) {
			best = i
		}
	}
	ev := sim.events[best]
	sim.events = append(sim.events[:best], sim.events[best+1:]...)
	return ev
}

// loadM1 moves the next queued job onto machine 1 if it is empty.
func (sim *simulator) loadM1() {
	if sim.next >= len(sim.seq) || sim.m1.occupied {
		return
	}
	j := sim.seq[sim.next]
	sim.next++
	sim.m1.occupied = true
	sim.m1.job = j
	sim.schedule.Timeline = append(sim.schedule.Timeline, JobTimeline{JobID: j.ID, EnterM1: sim.now})
	sim.startWork(&sim.m1, 1, j.P1)
}

// startWork begins (or resumes) processing on a machine, honouring a
// machine that is currently down.
func (sim *simulator) startWork(m *simMachine, which, dur int) {
	if m.down && dur > 0 {
		m.working = false
		m.remaining = dur
		if m.downEndAt > sim.now {
			sim.schedule.Downtime = append(sim.schedule.Downtime, DowntimeEvidence{
				Machine: which, JobID: m.job.ID, Start: sim.now, End: m.downEndAt,
			})
		}
		return
	}
	m.working = true
	m.doneAt = sim.now + dur
	m.gen++
	sim.push(simEvent{time: m.doneAt, kind: evProcDone, machine: which, gen: m.gen})
}

func (sim *simulator) onProcDone(ev simEvent) {
	m := sim.machine(ev.machine)
	if ev.gen != m.gen || !m.working || m.doneAt != ev.time {
		return // superseded event
	}
	m.working = false
	if ev.machine == 1 {
		t := &sim.schedule.Timeline[len(sim.schedule.Timeline)-1]
		t.ProcEndM1 = sim.now
		if sim.m2.occupied {
			// Zero buffer: the finished job blocks machine 1.
			m.blocked = true
			sim.blockFrom = sim.now
		} else {
			sim.transferToM2()
			sim.loadM1()
		}
		return
	}
	// Machine 2 finished a job.
	t := sim.timelineOf(m.job.ID)
	t.DepartM2 = sim.now
	m.occupied = false
	if sim.m1.blocked {
		sim.transferToM2()
		sim.loadM1()
	}
}

// transferToM2 moves the job currently on machine 1 onto machine 2.
func (sim *simulator) transferToM2() {
	j := sim.m1.job
	t := &sim.schedule.Timeline[len(sim.schedule.Timeline)-1]
	if sim.m1.blocked && sim.blockFrom >= 0 && sim.now > sim.blockFrom {
		sim.schedule.Blocking = append(sim.schedule.Blocking, BlockedPeriod{
			JobID: j.ID, Start: sim.blockFrom, End: sim.now,
		})
	}
	sim.blockFrom = -1
	sim.m1.occupied = false
	sim.m1.blocked = false
	t.DepartM1 = sim.now
	t.EnterM2 = sim.now
	sim.m2.occupied = true
	sim.m2.job = j
	sim.startWork(&sim.m2, 2, j.P2)
}

func (sim *simulator) onDownStart(ev simEvent) {
	m := sim.machine(ev.machine)
	m.down = true
	m.downEndAt = ev.end
	if m.working {
		// Processing pauses but the job keeps occupying the machine.
		m.working = false
		m.remaining = m.doneAt - sim.now
		m.gen++ // invalidate the pending procDone event
		sim.schedule.Downtime = append(sim.schedule.Downtime, DowntimeEvidence{
			Machine: ev.machine, JobID: m.job.ID, Start: sim.now, End: ev.end,
		})
	}
}

func (sim *simulator) onDownEnd(ev simEvent) {
	m := sim.machine(ev.machine)
	m.down = false
	if m.occupied && !m.working && !m.blocked {
		// Resume the paused job.
		sim.startWork(m, ev.machine, m.remaining)
	}
}

func (sim *simulator) timelineOf(jobID int) *JobTimeline {
	for i := range sim.schedule.Timeline {
		if sim.schedule.Timeline[i].JobID == jobID {
			return &sim.schedule.Timeline[i]
		}
	}
	return nil
}
