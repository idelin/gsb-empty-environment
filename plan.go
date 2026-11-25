package handoff

import (
	"net/netip"
	"sort"
)

// Phase identifies the online planner phase.
type Phase string

const (
	PhaseWithdrawConflicts Phase = "withdraw_equal_length_conflicts"
	PhasePreposition       Phase = "preposition_new_prefixes"
	PhaseCut               Phase = "cut_authorization"
	PhaseDone              Phase = "done"
)

// AckRequirement is an egress-bound exact-digest acknowledgement obligation.
type AckRequirement struct {
	Egress      EgressID
	StatementID string
	Digest      Digest
	Reason      string
	Classes     []string
}

// Plan is a safe-by-construction handoff and its replayable protocol.
type Plan struct {
	Spec                 Spec
	TransferPrefixes     []netip.Prefix
	NewClaims            []Claim
	ConflictClaimDigests []Digest
	WithdrawStatement    *Statement
	PrepositionStatement Statement
	CutEvent             EventID
	Protocol             *Protocol
	Requirements         [][]AckRequirement
}

// BuildSafePlan constructs the conservative ACK-barrier handoff.
//
// The generated order is:
//  1. withdraw old equal-length claims that would create a hard conflict;
//  2. wait for every affected egress to ACK that exact withdrawal;
//  3. pre-position new-tenant prefixes while the old aggregate still wins or
//     remains an admissible shorter fallback;
//  4. wait for every egress to ACK the exact merged new statement;
//  5. execute the authorization cut.
func BuildSafePlan(spec Spec) (*Plan, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	transfer := MergePrefixes(spec.Transfer)
	newClaims := make([]Claim, 0, len(transfer))
	for _, p := range transfer {
		newClaims = append(newClaims, Claim{Prefix: p, Tenant: spec.NewTenant})
	}

	allInitial := collectInitialClaims(spec)
	conflicts := findEqualLengthConflicts(newClaims, allInitial)

	protocol, err := NewProtocol(spec)
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		Spec:                 spec,
		TransferPrefixes:     transfer,
		NewClaims:            newClaims,
		ConflictClaimDigests: conflicts,
		Protocol:             protocol,
	}

	nextBarrier := EventID("")
	if len(conflicts) > 0 {
		ws := Statement{
			PlanID:     spec.PlanID,
			ID:         "withdraw-equal-length-conflicts",
			Generation: 1,
			Withdraws:  conflicts,
			Nonce:      1,
		}.Canonical()
		plan.WithdrawStatement = &ws
		_, ack := protocol.AddStatementBroadcast(ws)
		_ = ack
		barrier := EventID("barrier:withdraw-acks")
		protocol.Events[barrier] = Event{ID: barrier, Kind: EventAck, Seq: -1}
		protocol.EventOrder = append(protocol.EventOrder, barrier)
		protocol.RequireAcksBefore(ws.ID, barrier)
		nextBarrier = barrier

		ps := Statement{
			PlanID:     spec.PlanID,
			ID:         "preposition-new",
			Generation: 2,
			Announces:  newClaims,
			Nonce:      2,
		}.Canonical()
		plan.PrepositionStatement = ps
		_, newAck := protocol.AddStatementBroadcast(ps)
		_ = newAck
		if nextBarrier != "" {
			for _, id := range protocol.EventOrder {
				ev := protocol.Events[id]
				if ev.Kind == EventDeliver && ev.StatementID == ps.ID {
					protocol.AddPrecedence(nextBarrier, id)
				}
			}
		}
		cut := protocol.AddCut("main", cutOps(spec, transfer)...)
		protocol.RequireAcksBefore(ps.ID, cut)
		plan.CutEvent = cut
	} else {
		ps := Statement{
			PlanID:     spec.PlanID,
			ID:         "preposition-new",
			Generation: 2,
			Announces:  newClaims,
			Nonce:      2,
		}.Canonical()
		plan.PrepositionStatement = ps
		protocol.AddStatementBroadcast(ps)
		cut := protocol.AddCut("main", cutOps(spec, transfer)...)
		protocol.RequireAcksBefore(ps.ID, cut)
		plan.CutEvent = cut
	}

	plan.Requirements = plan.buildRequirements()
	return plan, nil
}

func collectInitialClaims(spec Spec) []Claim {
	byDigest := make(map[Digest]Claim)
	add := func(c Claim) {
		c = c.Canonical()
		byDigest[c.Digest()] = c
	}
	for _, c := range spec.InitialClaims {
		add(c)
	}
	for _, claims := range spec.InitialViews {
		for _, c := range claims {
			add(c)
		}
	}
	out := make([]Claim, 0, len(byDigest))
	for _, c := range byDigest {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Digest().String() < out[j].Digest().String() })
	return out
}

func findEqualLengthConflicts(newClaims, initial []Claim) []Digest {
	seen := make(map[Digest]bool)
	var out []Digest
	for _, nc := range newClaims {
		for _, ic := range initial {
			if ic.Tenant == nc.Tenant {
				continue
			}
			if ic.Prefix.Bits() != nc.Prefix.Bits() {
				continue
			}
			if PrefixesOverlap(ic.Prefix, nc.Prefix) {
				d := ic.Digest()
				if !seen[d] {
					seen[d] = true
					out = append(out, d)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// PrefixesOverlap reports whether two prefixes share any address.
func PrefixesOverlap(a, b netip.Prefix) bool {
	if a.Addr().BitLen() != b.Addr().BitLen() {
		return false
	}
	la, ha, _ := prefixRange(a)
	lb, hb, _ := prefixRange(b)
	return la.Cmp(hb) < 0 && lb.Cmp(ha) < 0
}

func cutOps(spec Spec, transfer []netip.Prefix) []AuthzOp {
	ops := make([]AuthzOp, 0, 2*len(transfer))
	for _, p := range transfer {
		ops = append(ops, AuthzOp{Grant: false, Tenant: spec.OldTenant, Prefix: p})
	}
	for _, p := range transfer {
		ops = append(ops, AuthzOp{Grant: true, Tenant: spec.NewTenant, Prefix: p})
	}
	return ops
}

func (p *Plan) buildRequirements() [][]AckRequirement {
	var phases [][]AckRequirement
	if p.WithdrawStatement != nil {
		stmt := *p.WithdrawStatement
		var reqs []AckRequirement
		for _, e := range p.Spec.Egresses {
			reqs = append(reqs, AckRequirement{
				Egress: e, StatementID: stmt.ID, Digest: stmt.Digest(),
				Reason: "remove equal-length old-tenant conflict before pre-positioning",
			})
		}
		phases = append(phases, reqs)
	}
	var reqs []AckRequirement
	for _, e := range p.Spec.Egresses {
		reqs = append(reqs, AckRequirement{
			Egress: e, StatementID: p.PrepositionStatement.ID,
			Digest: p.PrepositionStatement.Digest(),
			Reason: "prove this egress has the exact merged new-tenant statement before cut",
		})
	}
	phases = append(phases, reqs)
	return phases
}

// MissingAcks returns current-phase obligations not satisfied by book.
func (p *Plan) MissingAcks(book *ReceiptBook) []AckRequirement {
	phase := p.CurrentPhase(book)
	switch phase {
	case PhaseWithdrawConflicts:
		return missingFrom(p.Requirements[0], book)
	case PhasePreposition:
		idx := len(p.Requirements) - 1
		return missingFrom(p.Requirements[idx], book)
	default:
		return nil
	}
}

func missingFrom(reqs []AckRequirement, book *ReceiptBook) []AckRequirement {
	var out []AckRequirement
	for _, req := range reqs {
		// Generation check is intentionally exact; replay of an old digest is
		// accepted by Has only for that old digest and cannot match req.Digest.
		if book == nil || !book.Has(req.Egress, req.Digest) {
			out = append(out, req)
		}
	}
	return out
}

// CurrentPhase computes the online phase from observed exact receipts.
func (p *Plan) CurrentPhase(book *ReceiptBook) Phase {
	if p.WithdrawStatement != nil {
		d := p.WithdrawStatement.Digest()
		for _, e := range p.Spec.Egresses {
			if book == nil || !book.HasCurrentGeneration(e, *p.WithdrawStatement) ||
				!book.Has(e, d) {
				return PhaseWithdrawConflicts
			}
		}
	}
	ps := p.PrepositionStatement
	for _, e := range p.Spec.Egresses {
		if book == nil || !book.HasCurrentGeneration(e, ps) {
			return PhasePreposition
		}
	}
	return PhaseCut
}

// Batch is the next online action batch.
type Batch struct {
	Phase     Phase
	Statement *Statement
	CutEvent  EventID
	Missing   []AckRequirement
	Done      bool
}

// NextBatch selects the next safe action from observed receipts. It never
// advances through an ACK barrier on a replayed or wrong-generation receipt.
func (p *Plan) NextBatch(book *ReceiptBook) Batch {
	phase := p.CurrentPhase(book)
	switch phase {
	case PhaseWithdrawConflicts:
		reqs := missingFrom(p.Requirements[0], book)
		return Batch{Phase: phase, Statement: p.WithdrawStatement, Missing: reqs}
	case PhasePreposition:
		idx := len(p.Requirements) - 1
		reqs := missingFrom(p.Requirements[idx], book)
		return Batch{Phase: phase, Statement: &p.PrepositionStatement, Missing: reqs}
	case PhaseCut:
		return Batch{Phase: PhaseCut, CutEvent: p.CutEvent}
	default:
		return Batch{Phase: PhaseDone, Done: true}
	}
}

// ApplyCut mutates actual authorization after the controller executes Batch.Cut.
func (p *Plan) ApplyCut(authz *Authorization) {
	for _, op := range cutOps(p.Spec, p.TransferPrefixes) {
		if op.Grant {
			authz.Grant(op.Tenant, op.Prefix)
		} else {
			authz.Revoke(op.Tenant, op.Prefix)
		}
	}
}

// Certificate exhaustively issues the independently replayable safety proof.
func (p *Plan) Certificate(opts VerifyOptions) (*Certificate, error) {
	return IssueCertificate(p.Protocol, opts)
}

// CriticalClasses returns equivalence classes intersecting critical prefixes.
func (p *Plan) CriticalClasses() ([]netip.Prefix, error) {
	relevant := append([]netip.Prefix(nil), p.Spec.Critical...)
	relevant = append(relevant, p.Spec.Transfer...)
	for _, c := range collectInitialClaims(p.Spec) {
		relevant = append(relevant, c.Prefix)
		relevant = append(relevant, c.Exclusions...)
	}
	for _, c := range p.NewClaims {
		relevant = append(relevant, c.Prefix)
		relevant = append(relevant, c.Exclusions...)
	}
	classes, err := EquivalenceClasses(p.Spec.Domain, relevant...)
	if err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, cl := range classes {
		for _, crit := range p.Spec.Critical {
			if PrefixesOverlap(cl, crit) {
				out = append(out, cl)
				break
			}
		}
	}
	return out, nil
}
