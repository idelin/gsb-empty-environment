package capsum

// returnTarget records one parked caller/task-consumer context on a call or
// consume edge, waiting for the invoked frame to reach its matched return.
type returnTarget struct {
	fromLabel int
	state     frameState
	mask      uint16
}

func stateEqual(a, b frameState) bool {
	return a.s.equal(b.s) && a.f.equal(b.f)
}

func (a *analysis) parkReturn(ei int, rt returnTarget) {
	for _, existing := range a.parked[ei] {
		if existing.fromLabel == rt.fromLabel &&
			existing.mask == rt.mask &&
			stateEqual(existing.state, rt.state) {
			return
		}
	}
	a.parked[ei] = append(a.parked[ei], rt)
}

// releaseInvocations sends every context parked on an edge to its matched
// return point using the exact caller frame captured at the edge. The callee
// label is irrelevant here because the caller frame is restored verbatim.
func (a *analysis) releaseInvocations(ei int) {
	p := a.x.prog
	edge := &p.Edges[ei]
	var retEI int
	switch edge.Kind {
	case EdgeCall, EdgePriv:
		var ok bool
		retEI, ok = a.x.pairReturn[ei]
		if !ok {
			return
		}
	case EdgeConsume:
		var ok bool
		retEI, ok = a.x.taskReturn[ei]
		if !ok {
			return
		}
	default:
		return
	}
	ret := &p.Edges[retEI]
	for _, rt := range a.parked[ei] {
		a.put(stateKey{node: ret.To, mask: rt.mask, label: rt.fromLabel}, rt.state)
	}
}

// releaseExitsFor re-runs every invocation edge entering node when node's
// abstract states grow, so newly discovered callee/task exits feed returns.
func (a *analysis) releaseExitsFor(node string) {
	for _, ei := range a.invokingEdges(node) {
		a.releaseInvocations(ei)
	}
}

func (a *analysis) invokingEdges(node string) []int {
	var out []int
	for ei := range a.x.prog.Edges {
		e := &a.x.prog.Edges[ei]
		if e.To != node {
			continue
		}
		if e.Kind == EdgeCall || e.Kind == EdgePriv || e.Kind == EdgeConsume {
			out = append(out, ei)
		}
	}
	return out
}
