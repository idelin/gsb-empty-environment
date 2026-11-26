package sdf

// completeAt finishes every in-flight firing whose end time has arrived,
// publishing internal tokens, committing external outputs and releasing the
// shared compute unit.
func completeAt(p *Problem, idx *index, st *timedState, record func(Event)) {
	remaining := st.flights[:0]
	for _, f := range st.flights {
		if f.end > st.now {
			remaining = append(remaining, f)
			continue
		}
		for _, pt := range f.prod {
			st.resv[pt.edge] -= pt.n
			st.occ[pt.edge] += pt.n
		}
		for _, op := range f.outProd {
			st.totalOut += int64(op.n)
			record(Event{Time: st.now, Kind: EventOutputCommit, Edge: op.edge, Tokens: op.n})
		}
		record(Event{Time: st.now, Kind: EventComplete, Actor: f.actor})
	}
	st.flights = remaining
	if st.busy && st.busyUntil <= st.now {
		st.busy = false
	}
}

// injectOverrun pushes every external token due at or before now into its
// bounded edge. It returns true when an edge is full (its output space is
// still reserved by an in-flight firing) and the token cannot be accepted.
func injectOverrun(p *Problem, idx *index, st *timedState, caps []int, record func(Event)) bool {
	for ci, eid := range st.inputEids {
		c := &st.cursors[ci]
		for c.nextTime() <= st.now {
			t := c.nextTime()
			if st.occ[eid] >= caps[eid] {
				record(Event{Time: t, Kind: EventInputArrive, Edge: eid, Tokens: 1})
				return true
			}
			st.occ[eid]++
			st.totalIn++
			record(Event{Time: t, Kind: EventInputArrive, Edge: eid, Tokens: 1})
			c.advance()
		}
	}
	return false
}

func launchAll(p *Problem, idx *index, st *timedState, caps []int, record func(Event)) bool {
	for {
		a := pickActor(p, idx, st, caps)
		if a < 0 {
			return true
		}
		if !launch(p, idx, st, a, record) {
			return false
		}
	}
}

// pickActor applies a deterministic priority: the lowest-id ready actor.
func pickActor(p *Problem, idx *index, st *timedState, caps []int) int {
	if st.busy || len(st.flights) >= p.Res.Descriptors {
		return -1
	}
	for a := 0; a < p.Actors; a++ {
		if ready(p, idx, st, caps, a) {
			return a
		}
	}
	return -1
}

func ready(p *Problem, idx *index, st *timedState, caps []int, a int) bool {
	for _, eid := range idx.in[a] {
		if st.occ[eid] < p.Edges[eid].Cons {
			return false
		}
	}
	post := append([]int(nil), st.occ...)
	for _, eid := range idx.in[a] {
		post[eid] -= p.Edges[eid].Cons
	}
	for _, eid := range idx.out[a] {
		e := p.Edges[eid]
		if e.IsOutput() {
			continue
		}
		if post[eid]+st.resv[eid]+e.Prod > caps[eid] {
			return false
		}
	}
	return true
}

func launch(p *Problem, idx *index, st *timedState, a int, record func(Event)) bool {
	f := inflight{actor: a, launched: st.now}
	for _, eid := range idx.in[a] {
		n := p.Edges[eid].Cons
		st.occ[eid] -= n
	}
	for _, eid := range idx.out[a] {
		e := p.Edges[eid]
		if e.IsOutput() {
			f.outProd = append(f.outProd, edgeTok{eid, e.Prod})
			continue
		}
		st.resv[eid] += e.Prod
		f.prod = append(f.prod, edgeTok{eid, e.Prod})
	}
	st.busy = true
	end, ok := add64(st.now, p.Cost[a])
	if !ok {
		st.busy = false
		return false
	}
	st.busyUntil = end
	f.end = st.busyUntil
	st.flights = append(st.flights, f)
	st.fireCount[a]++
	record(Event{Time: st.now, Kind: EventLaunch, Actor: a})
	return true
}

// snapshotKey identifies a state up to absolute-time translation. Monotonic
// counters are excluded; bounded queues, in-flight structure, input cursor
// phase and run-internal position are retained.
func snapshotKey(p *Problem, st *timedState) string {
	b := make([]byte, 0, 256)
	for eid := range st.occ {
		b = appendSmall(b, st.occ[eid])
		b = appendSmall(b, st.resv[eid])
	}
	b = appendSmall(b, len(st.flights))
	for _, f := range st.flights {
		b = appendSmall(b, f.actor)
		b = appendLong(b, f.end-st.now)
	}
	for _, c := range st.cursors {
		b = appendLong(b, c.phase(st.now))
		b = appendLong(b, c.next)
	}
	b = appendLong(b, st.busyUntil-st.now)
	return string(b)
}

func appendLong(b []byte, v int64) []byte {
	var buf [8]byte
	x := v
	for i := 0; i < 8; i++ {
		buf[i] = byte(x & 0xff)
		x >>= 8
	}
	return append(b, buf[:]...)
}

func firstBlocked(p *Problem, idx *index, st *timedState, caps []int) (int, int, int) {
	for a := 0; a < p.Actors; a++ {
		if st.busy || len(st.flights) >= p.Res.Descriptors {
			return a, -1, -1
		}
		ok, se, fe := blockedReason(p, idx, st, caps, a)
		if !ok {
			return a, se, fe
		}
	}
	return -1, -1, -1
}

// quiescentSnapshot returns the normalized state key for an input-free graph
// together with whether at least one actor can fire from this state.
func quiescentSnapshot(p *Problem, idx *index, st *timedState, caps []int) (string, bool) {
	ready := false
	for a := 0; a < p.Actors; a++ {
		if ok, _, _ := blockedReason(p, idx, st, caps, a); ok {
			ready = true
			break
		}
	}
	if !ready {
		return "", false
	}
	return snapshotKey(p, st), true
}

func blockedReason(p *Problem, idx *index, st *timedState, caps []int, a int) (bool, int, int) {
	for _, eid := range idx.in[a] {
		if st.occ[eid] < p.Edges[eid].Cons {
			return false, eid, -1
		}
	}
	post := append([]int(nil), st.occ...)
	for _, eid := range idx.in[a] {
		post[eid] -= p.Edges[eid].Cons
	}
	for _, eid := range idx.out[a] {
		e := p.Edges[eid]
		if e.IsOutput() {
			continue
		}
		if post[eid]+st.resv[eid]+e.Prod > caps[eid] {
			return false, -1, eid
		}
	}
	return true, -1, -1
}

func describeStarve(p *Problem, eid int) string {
	e := p.Edges[eid]
	if e.IsInput() {
		return "deadlock: input edge " + itoa(eid) + " has not delivered enough tokens"
	}
	return "deadlock: edge " + itoa(eid) + " initial delay tokens insufficient"
}

func describeFull(p *Problem, eid int) string {
	if eid < 0 {
		return "overrun: edge buffer full while output space is reserved by an in-flight firing"
	}
	return "overrun: edge " + itoa(eid) + " buffer full while output space is reserved by an in-flight firing"
}

func itoa(v int) string {
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
