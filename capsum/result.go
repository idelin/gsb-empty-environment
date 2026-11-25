package capsum

import "prescreen.local/ps-2237843e01/pathlang"

func (a *analysis) buildResult() *Result {
	x := a.x
	res := &Result{
		Program:    x.prog.Name,
		Alphabet:   append([]string(nil), x.alphabet...),
		Components: append([]string(nil), x.componentAlphabet...),
		Points:     map[string]*PointResult{},
		Handles:    map[string]*HandleSummary{},
		Budget:     a.budget,
		Iterations: a.iterations,
	}

	for gid, d := range x.generators {
		res.GeneratorRecords = append(res.GeneratorRecords, GeneratorRecord{
			ID:    gid,
			Grant: a.generatorGrant(gid),
			DFA:   d,
		})
	}

	for _, node := range x.prog.Nodes {
		pr := &PointResult{Node: node}
		var may, must dnf
		first := true
		for k, st := range a.states {
			if k.node != node {
				continue
			}
			pr.ReachedBy = append(pr.ReachedBy, k.mask)
			eff := effective(st)
			if first {
				may, must = eff, eff
				first = false
			} else {
				may = unionDNF(may, eff)
				must = intersectDNF(must, eff)
			}
		}
		sortMasks(pr.ReachedBy)
		switch {
		case first && a.budgetExhausted:
			pr.Status = StatusUnknown
		case first:
			pr.Status = StatusUnreachable
		case a.budgetExhausted:
			// Budget ran out: never claim settled may/must languages.
			pr.Status = StatusUnknown
		default:
			pr.Status = StatusReachable
			pr.May = x.evaluate(may)
			pr.Must = x.evaluate(must)
		}
		res.Points[node] = pr
	}

	for hid, name := range x.handles {
		frozen := a.handles[hid]
		hs := &HandleSummary{Name: name}
		if len(frozen) > 0 {
			hs.Present = true
			hs.May = x.evaluate(frozen)
			hs.Must = x.evaluate(a.handleMust(hid))
		}
		res.Handles[name] = hs
	}
	res.BudgetExhausted = a.budgetExhausted
	return res
}

// handleMust intersects the frozen languages contributed by distinct post
// sources of a handle, because a resource is guaranteed carried only when
// every posting source freezes it.
func (a *analysis) handleMust(hid int) dnf {
	var acc dnf
	first := true
	for ei := range a.x.prog.Edges {
		e := &a.x.prog.Edges[ei]
		if e.Kind != EdgePost || a.x.handleIndex[e.Handle] != hid {
			continue
		}
		var nodeEff dnf
		nodeFirst := true
		for k, st := range a.states {
			if k.node != e.From {
				continue
			}
			eff := effective(st)
			if nodeFirst {
				nodeEff, nodeFirst = eff, false
			} else {
				nodeEff = unionDNF(nodeEff, eff)
			}
		}
		if nodeFirst {
			continue
		}
		if first {
			acc, first = nodeEff, false
		} else {
			acc = intersectDNF(acc, nodeEff)
		}
	}
	if first {
		return emptyDNF()
	}
	return acc
}

func (a *analysis) generatorGrant(gid int) Grant {
	key := ""
	for k, id := range a.x.genIndex {
		if id == gid {
			key = k
			break
		}
	}
	for _, dm := range a.x.domains {
		for _, g := range dm.Grants {
			if generatorKey(g) == key {
				return g
			}
		}
	}
	return Grant{}
}

// CheckAction tests a requested file operation against the may/must languages
// of a reachable program point using DFA containment, not sample evaluation.
func (r *Result) CheckAction(node string, action *FileAction) (guaranteed, possible bool, status Status) {
	pr, ok := r.Points[node]
	if !ok {
		return false, false, StatusUnreachable
	}
	if pr.Status != StatusReachable {
		return false, false, pr.Status
	}
	for _, c := range action.Path {
		if !contains(r.Components, c) {
			// The action names a component outside the finite alphabet; the
			// language question is undecided for this program view.
			return false, false, StatusUnknown
		}
	}
	if !contains(r.Alphabet, action.Root) {
		return false, false, StatusUnknown
	}
	self, err := pathlang.BuildSelf(r.Alphabet, r.Components, action.Root, action.Path)
	if err != nil {
		return false, false, StatusUnknown
	}
	target := pathlang.Minimize(self)
	possible = !pathlang.IsEmpty(pathlang.Intersect(target, pr.May))
	return pathlang.Subset(target, pr.Must), possible, pr.Status
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
