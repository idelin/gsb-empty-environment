package recovery

// CertificateWitness is one checkable step of an impossibility certificate:
// for an action available at a losing belief, a receipt that can actually be
// produced and whose remaining belief is also inside the losing region.
type CertificateWitness struct {
	BeliefKey  string
	ActionName string
	Receipt    Receipt
	NextKey    string
}

// Certificate is an independently checkable proof that no strategy can force
// every compatible world to the target. The losing region is closed under a
// per-action adversarial receipt, and the initial belief belongs to it.
type Certificate struct {
	Losing    map[string]bool
	Witnesses []CertificateWitness
	Initial   string
}

func (s *Solution) buildCertificate(nodes []string, edges map[string]map[string]edge) {
	if !s.losing[s.initial.key()] {
		return
	}
	cert := &Certificate{
		Losing:  map[string]bool{},
		Initial: s.initial.key(),
	}
	beliefByKey := map[string]belief{}
	for _, k := range nodes {
		if !s.losing[k] {
			continue
		}
		cert.Losing[k] = true
	}
	// Reconstruct the belief behind every losing key by replaying exploration
	// from the initial state.
	queue := []string{s.initial.key()}
	beliefByKey[s.initial.key()] = s.initial
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		b, ok := beliefByKey[key]
		if !ok {
			continue
		}
		for _, a := range s.actions {
			var obs []observation
			if a.kind == kindCommand {
				obs = s.p.applyCommit(b, a.cmd)
			} else {
				obs = s.p.applyProbe(b, a.pr)
			}
			for _, o := range obs {
				nk := o.b.key()
				if _, seen := beliefByKey[nk]; !seen {
					beliefByKey[nk] = o.b
					queue = append(queue, nk)
				}
			}
		}
	}

	for _, k := range nodes {
		if !s.losing[k] {
			continue
		}
		b := beliefByKey[k]
		for _, a := range s.actions {
			var obs []observation
			if a.kind == kindCommand {
				obs = s.p.applyCommit(b, a.cmd)
			} else {
				obs = s.p.applyProbe(b, a.pr)
			}
			found := false
			for _, o := range obs {
				nk := o.b.key()
				if s.losing[nk] {
					cert.Witnesses = append(cert.Witnesses, CertificateWitness{
						BeliefKey:  k,
						ActionName: a.name,
						Receipt:    o.receipt,
						NextKey:    nk,
					})
					found = true
					break
				}
			}
			if !found {
				// Certificate construction failed; leave it absent so Verify
				// cannot accept an incomplete proof.
				s.cert = nil
				return
			}
		}
	}
	s.cert = cert
}

// Certificate returns the impossibility certificate for an unsolvable
// problem, or nil when the problem is solvable.
func (s *Solution) Certificate() *Certificate { return s.cert }

// Verify independently replays every witness against the problem without
// trusting the planner's losing labels. It confirms:
//   - the initial belief is covered;
//   - every covered belief is not already at the target;
//   - every available action has one listed, genuinely observable receipt;
//   - each witness transitions to another covered belief.
//
// Such a closed losing region makes target completion impossible regardless
// of strategy, because the adversary always has a covered reply.
func (p *Problem) Verify(cert *Certificate) error {
	if cert == nil {
		return ErrNotSolved
	}
	if !cert.Losing[cert.Initial] {
		return ErrNotSolved
	}
	actionByName := map[string]action{}
	for _, a := range p.buildActions() {
		actionByName[a.name] = a
	}
	beliefByKey := map[string]belief{cert.Initial: p.initialBelief()}
	queue := []string{cert.Initial}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if !cert.Losing[k] {
			return ErrNotSolved
		}
		b := beliefByKey[k]
		if p.atGoal(b) {
			return ErrNotSolved
		}
		listed := map[string]CertificateWitness{}
		for _, w := range cert.Witnesses {
			if w.BeliefKey == k {
				listed[w.ActionName] = w
			}
		}
		for _, a := range p.buildActions() {
			w, ok := listed[a.name]
			if !ok {
				return ErrNotSolved
			}
			var obs []observation
			if a.kind == kindCommand {
				obs = p.applyCommit(b, a.cmd)
			} else {
				obs = p.applyProbe(b, a.pr)
			}
			match := false
			for _, o := range obs {
				if receiptKey(o.receipt) != receiptKey(w.Receipt) {
					continue
				}
				nk := o.b.key()
				if nk != w.NextKey || !cert.Losing[nk] {
					return ErrNotSolved
				}
				match = true
				if _, seen := beliefByKey[nk]; !seen {
					beliefByKey[nk] = o.b
					queue = append(queue, nk)
				}
			}
			if !match {
				return ErrNotSolved
			}
		}
	}
	return nil
}

func (p *Problem) buildActions() []action {
	var out []action
	for _, c := range p.Commands {
		out = append(out, action{kind: kindCommand, name: c.Name, cost: c.Cost, cmd: c})
	}
	for _, pr := range p.Probes {
		out = append(out, action{kind: kindProbe, name: pr.Name, cost: pr.Cost, pr: pr})
	}
	return out
}
