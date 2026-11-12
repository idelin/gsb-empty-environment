package netsec

import "sort"

func newAnalyzer(prog *Program) *analyzer {
	a := &analyzer{
		prog:         prog,
		nodes:        map[string]Node{},
		preds:        map[string][]string{},
		checkTargets: map[string][]Target{},
		readTargets:  map[string][]Target{},
		writeTargets: map[string][]Target{},
		callTargets:  map[string][]Target{},
		labels:       map[string]int{},
		states:       map[string]*flowState{},
	}

	for _, n := range prog.Nodes {
		a.nodes[n.ID] = n
	}
	for cur, succs := range prog.CFG.Successors {
		for _, s := range succs {
			a.preds[s] = append(a.preds[s], cur)
		}
	}
	for id := range a.preds {
		sort.Strings(a.preds[id])
	}

	// One version label per check/write/call node, in stable node order.
	label := 0
	for _, n := range prog.Nodes {
		switch n.Kind {
		case NodeCheck, NodeWrite, NodeCall:
			label++
			a.labels[n.ID] = label
		}
	}

	for _, n := range prog.Nodes {
		switch n.Kind {
		case NodeCheck:
			a.checkTargets[n.ID] = prog.Graph.Resolve(n.Root, n.Field)
		case NodeRead:
			a.readTargets[n.ID] = prog.Graph.Resolve(n.Root, n.Field)
		case NodeWrite:
			a.writeTargets[n.ID] = prog.Graph.Resolve(n.Root, n.Field)
		case NodeCall:
			a.callTargets[n.ID] = a.resolveCall(n)
		}
	}

	rel := map[Target]bool{}
	collect := func(m map[string][]Target) {
		for _, ts := range m {
			for _, t := range ts {
				rel[t] = true
			}
		}
	}
	collect(a.checkTargets)
	collect(a.readTargets)
	for t := range rel {
		a.relevant = append(a.relevant, t)
	}
	sort.Slice(a.relevant, func(i, j int) bool {
		if a.relevant[i].Object != a.relevant[j].Object {
			return a.relevant[i].Object < a.relevant[j].Object
		}
		return a.relevant[i].Field < a.relevant[j].Field
	})
	return a
}

func (a *analyzer) resolveCall(n Node) []Target {
	sum := a.summary(n.Summary)
	if sum == nil {
		return nil
	}
	bindRoot := map[string]string{}
	for _, c := range a.prog.Calls {
		if c.NodeID != n.ID {
			continue
		}
		for _, b := range c.Bindings {
			bindRoot[b.Param] = b.Root
		}
	}
	var out []Target
	for _, w := range sum.MayWrites {
		root, ok := bindRoot[w.Param]
		if !ok {
			continue
		}
		out = append(out, a.prog.Graph.Resolve(root, w.Field)...)
	}
	return dedupTargets(out)
}

func (a *analyzer) inState(id string) *flowState {
	preds := a.preds[id]
	if len(preds) == 0 {
		return initialState(a.relevant)
	}
	parts := make([]*flowState, 0, len(preds))
	for _, p := range preds {
		if s, ok := a.states[p]; ok {
			parts = append(parts, s)
		} else {
			parts = append(parts, initialState(a.relevant))
		}
	}
	return joinStates(parts, a.relevant)
}

func (a *analyzer) solve() {
	queue := []string{a.prog.CFG.Entry}
	queued := map[string]bool{a.prog.CFG.Entry: true}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		queued[id] = false

		in := a.inState(id)
		out := a.transfer(a.nodes[id], in)
		if old, ok := a.states[id]; ok && statesEqual(old, out) {
			continue
		}
		a.states[id] = out
		for _, s := range a.prog.CFG.Successors[id] {
			if !queued[s] {
				queued[s] = true
				queue = append(queue, s)
			}
		}
	}
}

func (a *analyzer) transfer(n Node, in *flowState) *flowState {
	switch n.Kind {
	case NodeCheck:
		out := in.clone()
		for _, t := range a.checkTargets[n.ID] {
			cur := out.versions[t]
			v := version{label: a.labels[n.ID]}
			out.versions[t] = v
			out.mayDef[t] = true
			if !cur.top {
				out.facts[fact{check: n.ID, target: t, bound: v}] = struct{}{}
			}
		}
		return out

	case NodeWrite:
		return a.applyWrites(in, a.writeTargets[n.ID], n.ID)

	case NodeCall:
		return a.applyWrites(in, a.callTargets[n.ID], n.ID)

	default:
		// entry, read, join, exit propagate state unchanged.
		return in.clone()
	}
}

func (a *analyzer) applyWrites(in *flowState, writes []Target, nodeID string) *flowState {
	out := in.clone()
	for _, t := range a.relevant {
		for _, w := range writes {
			if overlaps(w, t) {
				v := version{label: a.labels[nodeID]}
				out.versions[t] = v
				out.mayDef[t] = true
				break
			}
		}
	}
	for f := range out.facts {
		for _, w := range writes {
			if overlaps(w, f.target) {
				delete(out.facts, f)
				break
			}
		}
	}
	return out
}
