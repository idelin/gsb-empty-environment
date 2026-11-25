package capsum

func (a *analysis) process(k stateKey, st frameState) {
	p := a.x.prog
	for _, ei := range a.x.out[k.node] {
		e := &p.Edges[ei]
		switch e.Kind {
		case EdgeLocal:
			a.put(stateKey{node: e.To, mask: k.mask, label: k.label}, st)
		case EdgeCall:
			a.invoke(k, st, e, false)
		case EdgePriv:
			a.invoke(k, st, e, true)
		case EdgePost:
			a.post(k, st, e)
		case EdgeConsume:
			a.consume(k, st, e)
		case EdgeReturn, EdgeTaskReturn:
			// Callee/task-side states never cross a return edge; matched
			// returns are emitted by the owning call/consume edge.
		}
	}
}

func (a *analysis) invoke(k stateKey, st frameState, e *Edge, privileged bool) {
	frameDomains := dnfFromGenerators(a.x.domainUnion(e.Domains))
	var calleeS dnf
	if privileged {
		// Privileged truncation removes the synchronous stack below this frame.
		calleeS = frameDomains
	} else {
		// Ordinary frame: intersect the new frame domains with every frame
		// below it on the synchronous stack.
		calleeS = intersectDNF(st.s, frameDomains)
	}
	// The task-carried restriction F is never removed by privilege.
	calleeF := st.f
	target := a.internLabel(a.edgeIndex(e.ID), calleeS, calleeF)

	// Park the exact caller frame under the current label so the matched
	// return restores it; the mask is unchanged across a synchronous call.
	a.parkReturn(a.edgeIndex(e.ID), returnTarget{
		fromLabel: k.label,
		state:     st,
		mask:      k.mask,
	})
	a.put(stateKey{node: e.To, mask: k.mask, label: target},
		frameState{s: calleeS, f: calleeF})
}

func (a *analysis) post(k stateKey, st frameState, e *Edge) {
	eff := effective(st)
	hid := a.x.handleIndex[e.Handle]
	bit := a.handleBit(e.Handle)

	// Freeze the effective domains at the post site. Inside an asynchronous
	// task F already contains the parent freeze, so a nested post's freeze is
	// a subset and the child can never escape the parent restriction.
	old := a.handles[hid]
	merged := unionDNF(old, eff)
	if !old.equal(merged) {
		a.handles[hid] = merged
		for _, consumeEI := range a.consumeEdgesFor(bit) {
			consume := &a.x.prog.Edges[consumeEI]
			for key := range a.statesAtNode(consume.From) {
				if key.mask&bit != 0 && !a.queued[key] {
					a.queued[key] = true
					a.queue = append(a.queue, key)
				}
			}
		}
	}

	a.put(stateKey{node: e.To, mask: k.mask | bit, label: k.label}, st)
}

func (a *analysis) consume(k stateKey, st frameState, e *Edge) {
	bit := a.handleBit(e.Handle)
	if k.mask&bit == 0 {
		return // slot not pending: the one-shot consume cannot fire
	}
	hid := a.x.handleIndex[e.Handle]
	frozen, ok := a.handles[hid]
	if !ok || len(frozen) == 0 {
		return
	}

	taskF := intersectDNF(frozen, effective(st))
	taskS := universalDNF()
	target := a.internLabel(a.edgeIndex(e.ID), taskS, taskF)
	afterMask := k.mask &^ bit // one-shot: the slot leaves the queue

	a.parkReturn(a.edgeIndex(e.ID), returnTarget{
		fromLabel: k.label,
		state:     st,
		mask:      afterMask,
	})
	a.put(stateKey{node: e.To, mask: afterMask, label: target},
		frameState{s: taskS, f: taskF})
}

func (a *analysis) handleBit(name string) uint16 {
	return uint16(1) << uint(a.x.handleIndex[name])
}

func (a *analysis) edgeIndex(id string) int {
	for ei := range a.x.prog.Edges {
		if a.x.prog.Edges[ei].ID == id {
			return ei
		}
	}
	return -1
}

func (a *analysis) consumeEdgesFor(bit uint16) []int {
	var out []int
	for ei := range a.x.prog.Edges {
		e := &a.x.prog.Edges[ei]
		if e.Kind == EdgeConsume && a.handleBit(e.Handle) == bit {
			out = append(out, ei)
		}
	}
	return out
}

func (a *analysis) statesAtNode(node string) map[stateKey]frameState {
	out := map[stateKey]frameState{}
	for k, st := range a.states {
		if k.node == node {
			out[k] = st
		}
	}
	return out
}

// internLabel returns a stable label for an edge whose target starts with the
// given entry DNFs. Equal entry DNFs reuse one label, keeping the abstract
// domain finite even under unbounded concrete recursion depth.
func (a *analysis) internLabel(ei int, entryS, entryF dnf) int {
	for _, id := range a.edgeLabels[ei] {
		l := a.labels[id]
		if l.entryS.equal(entryS) && l.entryF.equal(entryF) {
			return id
		}
	}
	id := len(a.labels)
	a.labels = append(a.labels, label{entryS: entryS, entryF: entryF})
	a.edgeLabels[ei] = append(a.edgeLabels[ei], id)
	return id
}
