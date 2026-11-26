package sdf

import "sort"

// finalizeTimed builds the periodic Schedule from the two matching snapshots
// and then checks the sliding-window constraint over the infinite horizon.
func finalizeTimed(p *Problem, idx *index, caps []int, q []int64, events []Event, cycleFrom int, st timedState) timedOutcome {
	anchor := events[cycleFrom].Time
	cycleTime := st.now - anchor
	if cycleTime <= 0 {
		return timedOutcome{status: StatusInconclusive, reason: "non-advancing state repeat"}
	}
	prefix := append([]Event(nil), events[:cycleFrom]...)
	cycle := append([]Event(nil), events[cycleFrom:]...)
	rebaseEvents(prefix, cycle, anchor)

	var cycleIn, cycleOut int64
	for _, ev := range cycle {
		if ev.Kind == EventInputArrive {
			cycleIn += int64(ev.Tokens)
		}
		if ev.Kind == EventOutputCommit {
			cycleOut += int64(ev.Tokens)
		}
	}
	sched := &Schedule{
		PrefixEvents: prefix,
		CycleEvents:  cycle,
		CycleTime:    cycleTime,
		CycleInputs:  cycleIn,
		CycleOutputs: cycleOut,
		Repetition:   append([]int64(nil), q...),
		CycleStart:   anchor,
		caps:         append([]int(nil), caps...),
	}

	ok, badAt := checkWindow(p, sched)
	if !ok {
		return timedOutcome{
			status:   StatusWindow,
			reason:   "sliding-window constraint violated at input ordinal " + itoa64(badAt),
			sched:    sched,
			windowOK: false,
			badAt:    badAt,
		}
	}
	return timedOutcome{status: StatusFeasible, sched: sched, windowOK: true}
}

// rebaseEvents makes cycle times relative to the cycle anchor and rebuilds
// cumulative counters independently for prefix and cycle.
func rebaseEvents(prefix, cycle []Event, anchor int64) {
	var in, out int64
	for i := range prefix {
		if prefix[i].Kind == EventInputArrive {
			in += int64(prefix[i].Tokens)
		}
		if prefix[i].Kind == EventOutputCommit {
			out += int64(prefix[i].Tokens)
		}
		prefix[i].InInputs = in
		prefix[i].OutOutputs = out
	}
	in, out = 0, 0
	for i := range cycle {
		cycle[i].Time -= anchor
		if cycle[i].Kind == EventInputArrive {
			in += int64(cycle[i].Tokens)
		}
		if cycle[i].Kind == EventOutputCommit {
			out += int64(cycle[i].Tokens)
		}
		cycle[i].InInputs = in
		cycle[i].OutOutputs = out
	}
}

// outputCounter answers cumulative output counts on the infinite periodic
// schedule in O(log E) time without materializing repeated cycles.
type outputCounter struct {
	prefix     []Event // output-only prefix events, time ascending
	cycleRel   []int64 // relative times of output events in one steady cycle
	cycleCum   []int64 // cumulative outputs within the cycle
	cycleTotal int64
	cycleStart int64
	cycleTime  int64
	prefixTot  int64
}

func newOutputCounter(s *Schedule) *outputCounter {
	c := &outputCounter{cycleStart: s.CycleStart, cycleTime: s.CycleTime}
	for _, ev := range s.PrefixEvents {
		if ev.Kind == EventOutputCommit {
			c.prefix = append(c.prefix, ev)
			c.prefixTot += int64(ev.Tokens)
		}
	}
	var cum int64
	for _, ev := range s.CycleEvents {
		if ev.Kind != EventOutputCommit {
			continue
		}
		c.cycleRel = append(c.cycleRel, ev.Time)
		cum += int64(ev.Tokens)
		c.cycleCum = append(c.cycleCum, cum)
	}
	c.cycleTotal = cum
	return c
}

// at returns outputs committed at or before absolute time t.
func (c *outputCounter) at(t int64) int64 {
	if t < c.cycleStart {
		idx := sort.Search(len(c.prefix), func(i int) bool { return c.prefix[i].Time > t })
		if idx == 0 {
			return 0
		}
		return c.prefix[idx-1].OutOutputs
	}
	rel := t - c.cycleStart
	k := rel / c.cycleTime
	r := rel - k*c.cycleTime
	idx := sort.Search(len(c.cycleRel), func(i int) bool { return c.cycleRel[i] > r })
	var within int64
	if idx > 0 {
		within = c.cycleCum[idx-1]
	}
	return c.prefixTot + k*c.cycleTotal + within
}

// checkWindow verifies every block of N consecutive input tokens has at least
// M outputs committed by the arrival of its last token.
//
// The transient prefix is checked window by window; in steady state the
// residual is periodic in the input phase, so each of the Count input phases
// is evaluated once and all further periods are covered by construction.
func checkWindow(p *Problem, s *Schedule) (bool, int64) {
	if p.WindowM == 0 {
		return true, 0
	}
	eid := firstInputEdge(p)
	if eid < 0 {
		return true, 0
	}
	in := p.inputPattern(eid)
	counter := newOutputCounter(s)

	kthTime := func(k int64) int64 { return inputArrivalAt(in, k-1) }

	// Number of input tokens that lie strictly in the displayed prefix.
	prefixInputs := int64(0)
	for _, ev := range s.PrefixEvents {
		if ev.Kind == EventInputArrive {
			prefixInputs += int64(ev.Tokens)
		}
	}

	violates := func(j int64) bool {
		end := kthTime(j)
		var before int64
		if j > p.WindowN {
			before = counter.at(kthTime(j-p.WindowN) - 1)
		}
		return counter.at(end)-before < p.WindowM
	}

	// 1. Every window ending at or before the seam is checked explicitly. The
	// seam is the first input token handled inside the steady cycle; windows
	// ending there may still draw prefix outputs, so they cannot be inferred
	// from periodicity alone.
	seam := prefixInputs + 1
	firstEnd := p.WindowN
	for j := firstEnd; j <= seam; j++ {
		if violates(j) {
			return false, j
		}
	}

	// 2. Beyond the seam both streams are periodic. The window residual depends
	// only on the input phase, so evaluate every phase in two adjacent input
	// periods: the first period anchors the phase, the second proves the value
	// repeats (and therefore repeats forever via closed-form periodic counts).
	base := seam + 1
	if base < p.WindowN {
		base = p.WindowN
	}
	for period := int64(0); period < 2; period++ {
		for r := int64(0); r < in.Count; r++ {
			j := base + period*in.Count + r
			if violates(j) {
				return false, j
			}
		}
	}
	return true, 0
}

func firstInputEdge(p *Problem) int {
	for eid, e := range p.Edges {
		if e.IsInput() {
			return eid
		}
	}
	return -1
}

func (p *Problem) inputPattern(eid int) Input {
	for _, in := range p.Inputs {
		if in.EdgeID == eid {
			return in
		}
	}
	return Input{}
}

// inputArrivalAt returns the absolute arrival time of the zero-based token
// ordinal using arithmetic on the run descriptor.
func inputArrivalAt(in Input, ordinal int64) int64 {
	period := ordinal / in.Count
	idx := ordinal - period*in.Count
	runNo, pos := runPosition(in, idx)
	return absArrival(in, period, runNo, pos)
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
