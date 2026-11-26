package capmig

import "sort"

// evaluateTicket computes the support mask of one ticket across every
// admissible history and assembles its audit trail.
func (e *engine) evaluateTicket(
	t Ticket,
	coverCache map[string][]uint64,
	coverKey func(string, string, requirement) string,
) TicketReport {
	targetPhysical := e.idx.resolveObject(t.NewObject)

	report := TicketReport{
		ID:                  t.ID,
		Subject:             t.Subject,
		TargetPhysical:      targetPhysical,
		TargetActions:       append([]string(nil), t.NewActions...),
		AdmissibleHistories: e.admissibleCount(),
		Chain:               map[string][]ChainHop{},
	}

	reqs := make([]requirement, 0, len(t.NewActions))
	for _, a := range t.NewActions {
		reqs = append(reqs, requirement{physical: targetPhysical, action: a})
	}

	// Static coverage analysis: which ancestor tokens can cover each pair.
	support := append([]uint64(nil), e.admissible...)
	for _, req := range reqs {
		basis := RequirementBasis{PhysicalObject: req.physical, Action: req.action}
		reqCover := make([]uint64, len(e.admissible))

		for _, token := range t.AncestorTokens {
			gi := e.idx.grants[token]
			g := e.in.Grants[gi]
			grantPhysical := e.idx.resolveObject(g.Object)
			objPath, objOK := e.idx.objectReachable(grantPhysical, req.physical)
			actPath, actOK := e.idx.actionCovers(g.Action, req.action)
			if !objOK || !actOK {
				continue
			}

			key := coverKey(token, t.Subject, req)
			cover, ok := coverCache[key]
			if !ok {
				cover = e.coverageMaskFast(gi, e.idx.subjects[t.Subject])
				coverCache[key] = cover
			}
			for i := range reqCover {
				reqCover[i] |= cover[i]
			}

			basis.CoveringTokens = append(basis.CoveringTokens, TokenCoverage{
				Token:       token,
				ObjectPath:  objPath,
				ActionPath:  actPath,
				Subject:     g.Subject,
				HolderInAll: allAdmissibleHave(e, cover),
				AliveInAll:  e.tokenAliveInAll(gi),
			})
		}

		if len(basis.CoveringTokens) == 0 {
			basis.Unsupported = true
			for i := range reqCover {
				reqCover[i] = 0
			}
		}
		report.Requirements = append(report.Requirements, basis)
		for i := range support {
			support[i] &= reqCover[i]
		}
	}

	// Confine support to admissible histories.
	for i := range support {
		support[i] &= e.admissible[i]
	}

	report.SupportingHistories = onesCount(support)
	report.Chain = e.buildChains(t)
	report.BarrierRejects = e.ticketBarrierRejects(t)
	report.TouchesRevocation = e.ticketRevocations(t)
	report.CausalFailures = e.ticketCausalFailures(t)
	report.EffectiveVariables = e.ticketEffectiveVariables(t)
	report.ReauthCandidates = e.ticketReauthCandidates(t)

	switch {
	case e.admissibleCount() == 0:
		report.Decision = DecisionDeny
	case report.SupportingHistories == 0:
		report.Decision = DecisionDeny
	case report.SupportingHistories == e.admissibleCount():
		report.Decision = DecisionMigrate
	default:
		report.Decision = DecisionReauth
	}

	if report.Decision != DecisionMigrate {
		report.Counterexamples = e.buildCounterexamples(t, support, reqs)
	}

	sort.Strings(report.BarrierRejects)
	sort.Strings(report.TouchesRevocation)
	sort.Strings(report.CausalFailures)
	sort.Strings(report.EffectiveVariables)
	sort.Strings(report.ReauthCandidates)
	sort.Slice(report.Requirements, func(i, j int) bool {
		if report.Requirements[i].PhysicalObject != report.Requirements[j].PhysicalObject {
			return report.Requirements[i].PhysicalObject < report.Requirements[j].PhysicalObject
		}
		return report.Requirements[i].Action < report.Requirements[j].Action
	})
	return report
}

func allAdmissibleHave(e *engine, cover []uint64) bool {
	for i := range e.admissible {
		if cover[i]&e.admissible[i] != e.admissible[i] {
			return false
		}
	}
	return true
}

func (e *engine) tokenAliveInAll(gi int) bool {
	for w := range e.admissible {
		if e.tokenBits[gi].revoked[w]&e.admissible[w] != 0 {
			return false
		}
	}
	return true
}

func (e *engine) buildChains(t Ticket) map[string][]ChainHop {
	out := map[string][]ChainHop{}
	for _, token := range t.AncestorTokens {
		var hops []ChainHop
		for _, hi := range e.idx.handoffs {
			h := e.in.Handoffs[hi]
			if h.Token != token {
				continue
			}
			successes := onesCount(andWords(e.hopSuccessBits[hi], e.admissible))
			hops = append(hops, ChainHop{
				HandoffID:         handoffID(e.in, hi),
				From:              h.From,
				To:                h.To,
				Epoch:             h.Epoch,
				LostConfirmation:  h.LostConfirmation,
				RejectedByBarrier: e.rejected[hi],
				SuccessHistories:  successes,
			})
		}
		out[token] = hops
	}
	return out
}

func (e *engine) ticketBarrierRejects(t Ticket) []string {
	seen := map[string]bool{}
	var out []string
	for _, hi := range e.idx.handoffs {
		h := e.in.Handoffs[hi]
		if !e.rejected[hi] || !contains(t.AncestorTokens, h.Token) {
			continue
		}
		id := handoffID(e.in, hi)
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (e *engine) ticketRevocations(t Ticket) []string {
	var out []string
	for _, token := range t.AncestorTokens {
		if _, ok := e.idx.revocations[token]; ok {
			out = append(out, token)
		}
	}
	return out
}

func (e *engine) ticketCausalFailures(t Ticket) []string {
	seen := map[string]bool{}
	var out []string
	for _, hi := range e.idx.handoffs {
		h := e.in.Handoffs[hi]
		if e.rejected[hi] || !contains(t.AncestorTokens, h.Token) {
			continue
		}
		// A causal failure exists if some admissible history misses the hop.
		missed := andNotWords(e.admissible, e.hopSuccessBits[hi])
		if onesCount(missed) == 0 {
			continue
		}
		id := handoffID(e.in, hi)
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (e *engine) ticketEffectiveVariables(t Ticket) []string {
	seen := map[string]bool{}
	var out []string
	for _, hi := range e.idx.ambiguousHops {
		h := e.in.Handoffs[hi]
		if !contains(t.AncestorTokens, h.Token) {
			continue
		}
		varIdx := e.ambiguousOfHop[hi]
		if e.variableEffective(varIdx, hi) {
			id := handoffID(e.in, hi)
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

func (e *engine) ticketReauthCandidates(t Ticket) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, token := range t.AncestorTokens {
		gi := e.idx.grants[token]
		add(e.in.Grants[gi].Subject)
		for _, hi := range e.idx.handoffs {
			h := e.in.Handoffs[hi]
			if h.Token != token {
				continue
			}
			add(h.From)
			add(h.To)
		}
	}
	return out
}

func (e *engine) buildCounterexamples(t Ticket, support []uint64, reqs []requirement) []Counterexample {
	fail := make([]uint64, len(support))
	for i := range fail {
		fail[i] = e.admissible[i] &^ support[i]
	}
	masks := firstSetBits(fail, 3)
	out := make([]Counterexample, 0, len(masks))
	for _, mask := range masks {
		choices := map[string]bool{}
		for varIdx, hi := range e.idx.ambiguousHops {
			choices[handoffID(e.in, hi)] = mask&(1<<uint(varIdx)) != 0
		}
		out = append(out, Counterexample{
			Mask:            mask,
			VariableChoices: choices,
			FailedReason:    e.failureReason(t, mask, reqs),
		})
	}
	return out
}

func (e *engine) failureReason(t Ticket, mask uint64, reqs []requirement) string {
	want := e.idx.subjects[t.Subject]
	word, bit := mask/64, uint(mask%64)
	for _, req := range reqs {
		for _, token := range t.AncestorTokens {
			gi := e.idx.grants[token]
			g := e.in.Grants[gi]
			grantPhysical := e.idx.resolveObject(g.Object)
			if _, ok := e.idx.objectReachable(grantPhysical, req.physical); !ok {
				continue
			}
			if _, ok := e.idx.actionCovers(g.Action, req.action); !ok {
				continue
			}
			revoked := e.tokenBits[gi].revoked[word]&(1<<bit) != 0
			holds := e.tokenBits[gi].holder[want][word]&(1<<bit) != 0
			switch {
			case revoked:
				return "token " + token + " revoked before migration for " + req.action
			case !holds:
				return "token " + token + " held by another subject for " + req.action
			}
		}
	}
	return "no ancestor token covers the full requirement set"
}

// variableEffective reports whether flipping the variable changes the success
// bit of its handoff in at least one pair of admissible twin histories.
func (e *engine) variableEffective(varIdx, hi int) bool {
	flipped := xorWords(e.hopSuccessBits[hi], e.varOneWords[varIdx])
	diff := xorWords(e.hopSuccessBits[hi], flipped)
	return onesCount(andWords(diff, e.admissible)) != 0
}

// verifyJointConsistency is a structural invariant: all tickets are evaluated
// against the same enumeration masks, so one ambiguous handoff can never be
// resolved differently for different derived tickets. The check additionally
// confirms every handoff's success is a pure deterministic function of the
// single global mask (no per-ticket hidden state exists in the engine).
func verifyJointConsistency(in *Input, e *engine) bool {
	for mask := uint64(0); mask < e.numHist; mask++ {
		first := e.simulate(mask)
		for hi := range e.in.Handoffs {
			if e.hopSucceeded(first, hi) != e.hopSucceededMask(mask, hi) {
				return false
			}
		}
		for gi := range e.in.Grants {
			word, bit := mask/64, uint(mask%64)
			tb := e.tokenBits[gi]
			revoked := tb.revoked[word]&(1<<bit) != 0
			holder := -1
			for s, row := range tb.holder {
				if row[word]&(1<<bit) != 0 {
					holder = s
					break
				}
			}
			if first.states[gi].revoked != revoked || first.states[gi].holder != holder {
				return false
			}
		}
	}
	return true
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
