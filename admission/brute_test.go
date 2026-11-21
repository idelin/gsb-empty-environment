package admission

// bruteForce is an independent, straightforward enumerator used only by tests.
// It enumerates every complete pairing (perfect matching) consistent with the
// groups and quotas, which is deliberately exponential and only runs on tiny
// inputs.
func bruteForce(p *problem) bool {
	usedL := map[string]bool{}
	usedR := map[string]bool{}
	vec := emptyVec()

	var recGroup func(gi int) bool
	recGroup = func(gi int) bool {
		if gi == len(p.groups) {
			return len(usedL) == len(p.left) && len(usedR) == len(p.right) && p.withinQuota(vec)
		}

		// Reject the whole decision group.
		if recGroup(gi + 1) {
			return true
		}

		// Accept it only when none of its edges touches a used batch.
		members := p.groups[gi].edges
		for _, member := range members {
			me := p.edges[member]
			if usedL[me.L] || usedR[me.R] {
				return false
			}
		}
		for _, member := range members {
			me := p.edges[member]
			usedL[me.L] = true
			usedR[me.R] = true
			vec = addEdge(vec, me)
		}
		ok := recGroup(gi + 1)
		for _, member := range members {
			me := p.edges[member]
			delete(usedL, me.L)
			delete(usedR, me.R)
			vec.total--
			vec.counts[me.JointCategory]--
			if vec.counts[me.JointCategory] == 0 {
				delete(vec.counts, me.JointCategory)
			}
		}
		return ok
	}
	return recGroup(0)
}

// bruteAmbiguity independently counts possible joint categories and
// counterpart-side categories for a batch by enumerating all perfect
// matchings.
func bruteAmbiguity(p *problem, batchID string) (joint, counterpart int) {
	usedL := map[string]bool{}
	usedR := map[string]bool{}
	acceptedGroups := map[int]bool{}
	vec := emptyVec()
	jointSeen := map[string]bool{}
	counterpartSeen := map[string]bool{}

	var recGroup func(gi int)
	recGroup = func(gi int) {
		if gi == len(p.groups) {
			if len(usedL) != len(p.left) || len(usedR) != len(p.right) || !p.withinQuota(vec) {
				return
			}
			for ei2, e2 := range p.edges {
				if !acceptedGroups[p.edgeGroup[ei2]] {
					continue
				}
				if e2.L != batchID && e2.R != batchID {
					continue
				}
				jointSeen[e2.JointCategory] = true
				cp := e2.R
				if batchID == e2.R {
					cp = e2.L
				}
				counterpartSeen[p.batches[cp].Category] = true
			}
			return
		}

		// Reject branch.
		recGroup(gi + 1)

		members := p.groups[gi].edges
		for _, member := range members {
			me := p.edges[member]
			if usedL[me.L] || usedR[me.R] {
				return
			}
		}
		for _, member := range members {
			me := p.edges[member]
			usedL[me.L] = true
			usedR[me.R] = true
			vec = addEdge(vec, me)
		}
		acceptedGroups[gi] = true
		recGroup(gi + 1)
		delete(acceptedGroups, gi)
		for _, member := range members {
			me := p.edges[member]
			delete(usedL, me.L)
			delete(usedR, me.R)
			vec.total--
			vec.counts[me.JointCategory]--
			if vec.counts[me.JointCategory] == 0 {
				delete(vec.counts, me.JointCategory)
			}
		}
	}
	recGroup(0)
	return len(jointSeen), len(counterpartSeen)
}
