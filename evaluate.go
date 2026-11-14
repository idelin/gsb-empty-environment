package netsec

import (
	"fmt"
	"math"
	"time"
)

// negativeFact is the type-independent part of a proven negative answer. For
// NXDOMAIN it applies to every ordinary query type. For NODATA it is tested
// against the authenticated bitmap at every cache hit.
type negativeFact struct {
	kind             ResultKind
	types            TypeBitmap
	delegation       bool
	optOutDS         bool
	emptyNonTerminal bool
}

func (f negativeFact) supports(qtype uint16) bool {
	if !validQuestionType(qtype) {
		return false
	}
	if f.kind == ResultNXDOMAIN {
		return true
	}
	if f.kind != ResultNODATA {
		return false
	}
	if f.optOutDS {
		return qtype == TypeDS
	}
	if f.delegation {
		if qtype != TypeDS {
			return false
		}
		return !f.types.Has(TypeDS)
	}
	if f.types.Has(qtype) {
		return false
	}
	if f.types.Has(TypeCNAME) && qtype != TypeCNAME {
		return false
	}
	return true
}

type validatedProof struct {
	key               CacheKey
	policyGeneration  uint64
	policyFingerprint [32]byte
	fact              negativeFact
	validFrom         time.Time
	expiresAt         time.Time
	witnesses         []Witness
}

// EvaluateResponse validates a per-view authenticated denial response and
// returns the negative conclusion it actually proves. It does not read or
// mutate a cache.
func EvaluateResponse(resp *Response, policy Policy, now time.Time) (Answer, error) {
	policy = policy.withDefaults()
	if err := validatePolicy(policy); err != nil {
		return Answer{}, err
	}
	answer, _, err := evaluateResponse(resp, policy, now)
	return answer, err
}

func evaluateResponse(resp *Response, policy Policy, now time.Time) (Answer, *validatedProof, error) {
	if resp == nil {
		return Answer{}, nil, fmt.Errorf("%w: nil response", ErrMalformed)
	}
	att := resp.Attestation
	if att.View == "" || att.View != policy.View {
		return Answer{}, nil, fmt.Errorf("%w: proof view %q, policy view %q", ErrViewMismatch, att.View, policy.View)
	}
	if att.PolicyGeneration != policy.Generation {
		return Answer{}, nil, fmt.Errorf("%w: proof generation %d, policy generation %d", ErrPolicyMismatch, att.PolicyGeneration, policy.Generation)
	}
	if att.KeyGeneration != policy.KeyGeneration {
		return Answer{}, nil, fmt.Errorf("%w: proof generation %d, policy generation %d", ErrKeyGeneration, att.KeyGeneration, policy.KeyGeneration)
	}
	if !att.ChainVerified || !att.SignaturesVerified {
		return Answer{}, nil, fmt.Errorf("%w: unauthenticated signature chain", ErrMalformed)
	}
	if att.Apex.IsZero() {
		return Answer{}, nil, fmt.Errorf("%w: zero apex", ErrMalformed)
	}
	if len(policy.Apexes) > 0 && !containsName(policy.Apexes, att.Apex) {
		return Answer{}, nil, fmt.Errorf("%w: apex not allowed by view policy", ErrPolicyMismatch)
	}
	question := resp.Question
	if question.Name.IsZero() || !question.Name.IsSubdomainOf(att.Apex) {
		return Answer{}, nil, fmt.Errorf("%w: question is outside the authenticated apex", ErrMalformed)
	}
	if !validQuestionType(question.Type) {
		return Answer{}, nil, fmt.Errorf("%w: unsupported question type %d", ErrMalformed, question.Type)
	}
	class := att.Class
	if class == 0 {
		class = question.Class
	}
	if class == 0 {
		class = ClassIN
	}
	if question.Class == 0 {
		question.Class = class
	}
	if question.Class != class {
		return Answer{}, nil, fmt.Errorf("%w: question class differs from attestation", ErrMalformed)
	}

	params := att.Parameters.Clone()
	if err := params.validate(policy.MaxNSEC3Iterations); err != nil {
		return Answer{}, nil, err
	}
	if len(policy.AllowedParameters) > 0 {
		allowed := false
		for _, p := range policy.AllowedParameters {
			if p.Equal(params) {
				allowed = true
				break
			}
		}
		if !allowed {
			return Answer{}, nil, fmt.Errorf("%w: parameter set not allowed by view policy", ErrPolicyMismatch)
		}
	}
	if att.ValidFrom.IsZero() || att.ValidUntil.IsZero() || !att.ValidUntil.After(att.ValidFrom) {
		return Answer{}, nil, fmt.Errorf("%w: invalid attestation validity interval", ErrMalformed)
	}
	if now.Before(att.ValidFrom) || !now.Before(att.ValidUntil) {
		return Answer{}, nil, fmt.Errorf("%w: attestation outside validity interval", ErrProofExpired)
	}
	validFrom := att.ValidFrom
	validUntil := att.ValidUntil

	var nsec []NSECRecord
	var nsec3 []NSEC3Record
	nsecByOwner := make(map[string]NSECRecord)
	nsec3ByHash := make(map[string]NSEC3Record)
	minTTL := uint32(math.MaxUint32)

	switch params.Kind {
	case ProofNSEC:
		if len(resp.NSEC) == 0 || len(resp.NSEC3) != 0 {
			return Answer{}, nil, fmt.Errorf("%w: NSEC proof record mix", ErrMalformed)
		}
		nsec = make([]NSECRecord, 0, len(resp.NSEC))
		for _, input := range resp.NSEC {
			if err := validateBitmap(input.Types); err != nil {
				return Answer{}, nil, err
			}
			rec := cloneNSEC(input)
			if rec.Class != class {
				return Answer{}, nil, fmt.Errorf("%w: NSEC class mismatch", ErrMalformed)
			}
			if rec.Owner.IsZero() || !rec.Owner.IsSubdomainOf(att.Apex) {
				return Answer{}, nil, fmt.Errorf("%w: NSEC owner outside apex", ErrMalformed)
			}
			if rec.Next.IsZero() || !rec.Next.IsSubdomainOf(att.Apex) {
				return Answer{}, nil, fmt.Errorf("%w: NSEC next name outside apex", ErrMalformed)
			}
			if err := validateRRSIG(rec.RRSIG, TypeNSEC, att.Apex, att.KeyGeneration, policy, now); err != nil {
				return Answer{}, nil, err
			}
			if rec.RRSIG.ValidFrom.After(validFrom) {
				validFrom = rec.RRSIG.ValidFrom
			}
			if rec.RRSIG.ValidUntil.Before(validUntil) {
				validUntil = rec.RRSIG.ValidUntil
			}
			if rec.TTL < minTTL {
				minTTL = rec.TTL
			}
			if _, dup := nsecByOwner[rec.Owner.wire]; dup {
				return Answer{}, nil, fmt.Errorf("%w: duplicate NSEC owner %s", ErrMalformed, rec.Owner)
			}
			nsecByOwner[rec.Owner.wire] = rec
			nsec = append(nsec, rec)
		}
	case ProofNSEC3:
		if len(resp.NSEC3) == 0 || len(resp.NSEC) != 0 {
			return Answer{}, nil, fmt.Errorf("%w: NSEC3 proof record mix", ErrMalformed)
		}
		nsec3 = make([]NSEC3Record, 0, len(resp.NSEC3))
		for _, input := range resp.NSEC3 {
			if err := validateBitmap(input.Types); err != nil {
				return Answer{}, nil, err
			}
			rec := cloneNSEC3(input)
			if rec.Class != class {
				return Answer{}, nil, fmt.Errorf("%w: NSEC3 class mismatch", ErrMalformed)
			}
			if !rec.Parameters().Equal(params) {
				return Answer{}, nil, fmt.Errorf("%w: NSEC3 parameter set differs from attestation", ErrMalformed)
			}
			if rec.OptOut && !policy.AllowNSEC3OptOut {
				return Answer{}, nil, fmt.Errorf("%w: opt-out disabled by view policy", ErrPolicyMismatch)
			}
			if len(rec.Salt) > 255 || len(rec.NextHashedOwner) != 20 {
				return Answer{}, nil, fmt.Errorf("%w: invalid NSEC3 salt or next hash length", ErrMalformed)
			}
			hash, err := rec.OwnerHash(att.Apex)
			if err != nil {
				return Answer{}, nil, err
			}
			if err := validateRRSIG(rec.RRSIG, TypeNSEC3, att.Apex, att.KeyGeneration, policy, now); err != nil {
				return Answer{}, nil, err
			}
			if rec.RRSIG.ValidFrom.After(validFrom) {
				validFrom = rec.RRSIG.ValidFrom
			}
			if rec.RRSIG.ValidUntil.Before(validUntil) {
				validUntil = rec.RRSIG.ValidUntil
			}
			if rec.TTL < minTTL {
				minTTL = rec.TTL
			}
			key := string(hash)
			if _, dup := nsec3ByHash[key]; dup {
				return Answer{}, nil, fmt.Errorf("%w: duplicate NSEC3 owner hash", ErrMalformed)
			}
			nsec3ByHash[key] = rec
			nsec3 = append(nsec3, rec)
		}
	default:
		return Answer{}, nil, fmt.Errorf("%w: unknown proof kind", ErrMalformed)
	}

	delegations := make(map[string]Delegation)
	for _, d := range resp.Delegations {
		if d.Name.IsZero() || !d.Name.IsProperSubdomainOf(att.Apex) {
			return Answer{}, nil, fmt.Errorf("%w: delegation outside apex", ErrMalformed)
		}
		if err := mergeDelegation(delegations, d); err != nil {
			return Answer{}, nil, err
		}
	}
	for _, rec := range nsec {
		if rec.Types.Has(TypeNS) && !rec.Types.Has(TypeSOA) {
			d := Delegation{Name: rec.Owner, Signed: rec.Types.Has(TypeDS)}
			if err := mergeDelegation(delegations, d); err != nil {
				return Answer{}, nil, err
			}
		}
	}
	for _, rec := range nsec {
		if d, ok := delegationAbove(rec.Owner, delegations); ok && !rec.Owner.Equal(d.Name) {
			return Answer{}, nil, fmt.Errorf("%w: NSEC owner below delegation %s", ErrMalformed, d.Name)
		}
	}

	existing := make(map[string]bool)
	if params.Kind == ProofNSEC {
		existing[att.Apex.wire] = true
		for _, rec := range nsec {
			cur := rec.Owner
			for {
				existing[cur.wire] = true
				if cur.Equal(att.Apex) {
					break
				}
				parent, ok := cur.Parent()
				if !ok || !parent.IsSubdomainOf(att.Apex) {
					break
				}
				cur = parent
			}
		}
	}

	var fact negativeFact
	var witnesses []Witness
	var err error
	if params.Kind == ProofNSEC {
		fact, witnesses, err = synthesizeNSEC(question, att.Apex, nsec, nsecByOwner, existing, delegations)
	} else {
		fact, witnesses, err = synthesizeNSEC3(question, att.Apex, params, nsec3, nsec3ByHash, delegations)
	}
	if err != nil {
		return Answer{}, nil, err
	}
	if !fact.supports(question.Type) {
		return Answer{}, nil, fmt.Errorf("%w: synthesized fact does not cover query type %d", ErrUnproven, question.Type)
	}

	ttl := minTTL
	if resp.NegativeTTL > 0 && resp.NegativeTTL < ttl {
		ttl = resp.NegativeTTL
	}
	expiresAt := validUntil
	if ttl != math.MaxUint32 {
		ttlExpiry := now.Add(time.Duration(ttl) * time.Second)
		if ttlExpiry.Before(expiresAt) {
			expiresAt = ttlExpiry
		}
	}
	if !expiresAt.After(now) {
		return Answer{}, nil, fmt.Errorf("%w: proof has no remaining TTL", ErrProofExpired)
	}
	if validFrom.Before(att.ValidFrom) {
		validFrom = att.ValidFrom
	}

	key := CacheKey{
		View:          att.View,
		Apex:          att.Apex,
		Class:         class,
		Parameters:    params,
		KeyGeneration: att.KeyGeneration,
		QueryName:     question.Name,
	}
	answer := Answer{
		Result:           fact.kind,
		View:             att.View,
		Apex:             att.Apex,
		Class:            class,
		QueryName:        question.Name,
		QueryType:        question.Type,
		Parameters:       params.Clone(),
		KeyGeneration:    att.KeyGeneration,
		PolicyGeneration: att.PolicyGeneration,
		ValidFrom:        validFrom,
		ValidUntil:       expiresAt,
		Witnesses:        cloneWitnesses(witnesses),
	}
	proof := &validatedProof{
		key:               key,
		policyGeneration:  att.PolicyGeneration,
		policyFingerprint: policy.fingerprint(),
		fact:              fact,
		validFrom:         validFrom,
		expiresAt:         expiresAt,
		witnesses:         cloneWitnesses(witnesses),
	}
	return answer, proof, nil
}

func validateRRSIG(sig RRSIG, covered uint16, apex Name, keyGeneration uint64, policy Policy, now time.Time) error {
	if !sig.Verified {
		return fmt.Errorf("%w: RRSIG is not marked verified", ErrMalformed)
	}
	if sig.TypeCovered != covered {
		return fmt.Errorf("%w: RRSIG covers type %d, expected %d", ErrMalformed, sig.TypeCovered, covered)
	}
	if !sig.Signer.Equal(apex) {
		return fmt.Errorf("%w: RRSIG signer is not the zone apex", ErrMalformed)
	}
	if sig.KeyGeneration != keyGeneration {
		return fmt.Errorf("%w: RRSIG key generation differs", ErrKeyGeneration)
	}
	if sig.Algorithm == 0 {
		return fmt.Errorf("%w: zero RRSIG algorithm", ErrMalformed)
	}
	if len(policy.AllowedAlgorithms) > 0 {
		allowed := false
		for _, a := range policy.AllowedAlgorithms {
			if a == sig.Algorithm {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("%w: signature algorithm not allowed", ErrPolicyMismatch)
		}
	}
	if sig.ValidFrom.IsZero() || sig.ValidUntil.IsZero() || !sig.ValidUntil.After(sig.ValidFrom) {
		return fmt.Errorf("%w: invalid RRSIG validity interval", ErrMalformed)
	}
	if now.Before(sig.ValidFrom) || !now.Before(sig.ValidUntil) {
		return fmt.Errorf("%w: RRSIG outside validity interval", ErrProofExpired)
	}
	return nil
}

func validateBitmap(b TypeBitmap) error {
	for _, t := range b.Types {
		if t == 0 {
			return fmt.Errorf("%w: type zero in denial bitmap", ErrMalformed)
		}
	}
	return nil
}

func containsName(names []Name, target Name) bool {
	for _, n := range names {
		if n.Equal(target) {
			return true
		}
	}
	return false
}

func mergeDelegation(m map[string]Delegation, d Delegation) error {
	if old, ok := m[d.Name.wire]; ok {
		if old.Signed != d.Signed {
			return fmt.Errorf("%w: contradictory delegation state for %s", ErrMalformed, d.Name)
		}
		return nil
	}
	m[d.Name.wire] = d
	return nil
}

func delegationAbove(name Name, m map[string]Delegation) (Delegation, bool) {
	var best Delegation
	bestLabels := -1
	for _, d := range m {
		if name.IsSubdomainOf(d.Name) && d.Name.LabelCount() > bestLabels {
			best = d
			bestLabels = d.Name.LabelCount()
		}
	}
	return best, bestLabels >= 0
}

func cloneNSEC(r NSECRecord) NSECRecord {
	r.Types = r.Types.Canonical()
	r.RRSIG = r.RRSIG.clone()
	return r
}

func cloneNSEC3(r NSEC3Record) NSEC3Record {
	r.Salt = append([]byte(nil), r.Salt...)
	r.NextHashedOwner = append([]byte(nil), r.NextHashedOwner...)
	r.Types = r.Types.Canonical()
	r.RRSIG = r.RRSIG.clone()
	return r
}

func nsecWitness(role WitnessRole, rec NSECRecord) Witness {
	rec = cloneNSEC(rec)
	return Witness{
		Role: role,
		RRSet: RRSetWitness{
			Owner: rec.Owner,
			Type:  TypeNSEC,
			Class: rec.Class,
			TTL:   rec.TTL,
			RDATA: rec.RDATA(),
			RRSIG: rec.RRSIG.clone(),
		},
		NSEC: &rec,
	}
}

func nsec3Witness(role WitnessRole, rec NSEC3Record) Witness {
	rec = cloneNSEC3(rec)
	return Witness{
		Role: role,
		RRSet: RRSetWitness{
			Owner: rec.Owner,
			Type:  TypeNSEC3,
			Class: rec.Class,
			TTL:   rec.TTL,
			RDATA: rec.RDATA(),
			RRSIG: rec.RRSIG.clone(),
		},
		NSEC3: &rec,
	}
}

func findNSECCover(target Name, records []NSECRecord) (*NSECRecord, error) {
	var found *NSECRecord
	for i := range records {
		rec := &records[i]
		if !NSECCoveredBy(rec.Owner, rec.Next, target) {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%w: overlapping NSEC coverage for %s", ErrMalformed, target)
		}
		found = rec
	}
	if found == nil {
		return nil, nil
	}
	return found, nil
}

func findENTEvidence(name Name, records []NSECRecord) *NSECRecord {
	var best *NSECRecord
	for i := range records {
		rec := &records[i]
		if !rec.Owner.IsProperSubdomainOf(name) {
			continue
		}
		if best == nil || CompareNames(rec.Owner, best.Owner) < 0 {
			best = rec
		}
	}
	return best
}

func synthesizeNSEC(
	q Question,
	apex Name,
	records []NSECRecord,
	byOwner map[string]NSECRecord,
	existing map[string]bool,
	delegations map[string]Delegation,
) (negativeFact, []Witness, error) {
	if d, ok := delegationAbove(q.Name, delegations); ok {
		if !q.Name.Equal(d.Name) {
			return negativeFact{}, nil, fmt.Errorf("%w: %s is below delegation %s", ErrDelegation, q.Name, d.Name)
		}
		if q.Type != TypeDS {
			return negativeFact{}, nil, fmt.Errorf("%w: non-DS query at delegation %s", ErrDelegation, d.Name)
		}
		if d.Signed {
			return negativeFact{}, nil, fmt.Errorf("%w: signed delegation has a DS RRset", ErrPositive)
		}
		rec, ok := byOwner[q.Name.wire]
		if !ok {
			return negativeFact{}, nil, fmt.Errorf("%w: unsigned delegation DS absence needs an exact NSEC", ErrUnproven)
		}
		if !rec.Types.Has(TypeNS) || rec.Types.Has(TypeSOA) {
			return negativeFact{}, nil, fmt.Errorf("%w: delegation NSEC bitmap lacks NS or contains SOA", ErrMalformed)
		}
		if rec.Types.Has(TypeCNAME) {
			return negativeFact{}, nil, fmt.Errorf("%w: delegation NSEC bitmap contains CNAME", ErrMalformed)
		}
		if rec.Types.Has(TypeDS) {
			return negativeFact{}, nil, fmt.Errorf("%w: NSEC bitmap contains DS", ErrPositive)
		}
		return negativeFact{kind: ResultNODATA, types: rec.Types, delegation: true},
			[]Witness{nsecWitness(WitnessExactMatch, rec)}, nil
	}

	if rec, ok := byOwner[q.Name.wire]; ok {
		if rec.Types.Has(q.Type) {
			return negativeFact{}, nil, fmt.Errorf("%w: type %d exists at %s", ErrPositive, q.Type, q.Name)
		}
		if rec.Types.Has(TypeCNAME) {
			return negativeFact{}, nil, fmt.Errorf("%w: CNAME exists at %s", ErrPositive, q.Name)
		}
		return negativeFact{kind: ResultNODATA, types: rec.Types},
			[]Witness{nsecWitness(WitnessExactMatch, rec)}, nil
	}

	if existing[q.Name.wire] {
		if q.Name.Equal(apex) {
			return negativeFact{}, nil, fmt.Errorf("%w: apex has no exact NSEC", ErrUnproven)
		}
		evidence := findENTEvidence(q.Name, records)
		if evidence == nil {
			return negativeFact{}, nil, fmt.Errorf("%w: no evidence of empty non-terminal %s", ErrUnproven, q.Name)
		}
		return negativeFact{kind: ResultNODATA, emptyNonTerminal: true},
			[]Witness{nsecWitness(WitnessEmptyNonTerminal, *evidence)}, nil
	}

	ce := apex
	for depth := q.Name.LabelCount() - 1; depth >= apex.LabelCount(); depth-- {
		candidate, ok := q.Name.AncestorAtDepth(depth)
		if !ok {
			break
		}
		if existing[candidate.wire] {
			ce = candidate
			break
		}
	}
	if ce.Equal(q.Name) {
		return negativeFact{}, nil, fmt.Errorf("%w: closest encloser equals existing question", ErrUnproven)
	}
	nextCloser, ok := q.Name.AncestorAtDepth(ce.LabelCount() + 1)
	if !ok || nextCloser.Equal(ce) {
		return negativeFact{}, nil, fmt.Errorf("%w: cannot derive next closer name", ErrUnproven)
	}
	nextCover, err := findNSECCover(nextCloser, records)
	if err != nil {
		return negativeFact{}, nil, err
	}
	if nextCover == nil {
		return negativeFact{}, nil, fmt.Errorf("%w: no NSEC covers next closer %s", ErrUnproven, nextCloser)
	}

	wildcard, err := WildcardName(ce)
	if err != nil {
		return negativeFact{}, nil, err
	}
	if existing[wildcard.wire] {
		if rec, ok := byOwner[wildcard.wire]; ok {
			if rec.Types.Has(TypeNS) && !rec.Types.Has(TypeSOA) {
				if rec.Types.Has(TypeCNAME) {
					return negativeFact{}, nil, fmt.Errorf("%w: wildcard delegation bitmap contains CNAME", ErrMalformed)
				}
				if q.Type != TypeDS {
					return negativeFact{}, nil, fmt.Errorf("%w: wildcard delegation exists", ErrDelegation)
				}
				if rec.Types.Has(TypeDS) {
					return negativeFact{}, nil, fmt.Errorf("%w: wildcard delegation bitmap contains DS", ErrPositive)
				}
				return negativeFact{kind: ResultNODATA, types: rec.Types, delegation: true},
					[]Witness{
						nsecWitness(WitnessNextCloserCover, *nextCover),
						nsecWitness(WitnessWildcardMatch, rec),
					}, nil
			}
			if rec.Types.Has(q.Type) {
				return negativeFact{}, nil, fmt.Errorf("%w: %w: wildcard answers type %d", ErrWildcard, ErrPositive, q.Type)
			}
			if rec.Types.Has(TypeCNAME) {
				return negativeFact{}, nil, fmt.Errorf("%w: %w: wildcard has CNAME", ErrWildcard, ErrPositive)
			}
			witnesses := []Witness{nsecWitness(WitnessNextCloserCover, *nextCover)}
			if ceWitness, ok := nsecClosestWitness(ce, apex, byOwner, records); ok {
				witnesses = append(witnesses, ceWitness)
			}
			witnesses = append(witnesses, nsecWitness(WitnessWildcardMatch, rec))
			return negativeFact{kind: ResultNODATA, types: rec.Types}, witnesses, nil
		}
		evidence := findENTEvidence(wildcard, records)
		if evidence == nil {
			return negativeFact{}, nil, fmt.Errorf("%w: wildcard %s existence not evidenced", ErrUnproven, wildcard)
		}
		return negativeFact{kind: ResultNODATA, emptyNonTerminal: true},
			[]Witness{
				nsecWitness(WitnessNextCloserCover, *nextCover),
				nsecWitness(WitnessWildcardMatch, *evidence),
			}, nil
	}

	wildcardCover, err := findNSECCover(wildcard, records)
	if err != nil {
		return negativeFact{}, nil, err
	}
	if wildcardCover == nil {
		return negativeFact{}, nil, fmt.Errorf("%w: no NSEC covers wildcard %s", ErrUnproven, wildcard)
	}
	witnesses := []Witness{
		nsecWitness(WitnessNextCloserCover, *nextCover),
		nsecWitness(WitnessWildcardCover, *wildcardCover),
	}
	if ceWitness, ok := nsecClosestWitness(ce, apex, byOwner, records); ok {
		witnesses = append(witnesses, ceWitness)
	}
	return negativeFact{kind: ResultNXDOMAIN}, witnesses, nil
}

func nsecClosestWitness(ce, apex Name, byOwner map[string]NSECRecord, records []NSECRecord) (Witness, bool) {
	if rec, ok := byOwner[ce.wire]; ok {
		return nsecWitness(WitnessClosestEncloser, rec), true
	}
	if !ce.Equal(apex) {
		if evidence := findENTEvidence(ce, records); evidence != nil {
			return nsecWitness(WitnessClosestEncloser, *evidence), true
		}
	}
	return Witness{}, false
}

type nsec3Hasher struct {
	params Parameters
	cache  map[string][]byte
}

func (h *nsec3Hasher) hash(n Name) ([]byte, error) {
	if got, ok := h.cache[n.wire]; ok {
		return got, nil
	}
	got, err := h.params.HashName(n)
	if err != nil {
		return nil, err
	}
	h.cache[n.wire] = got
	return got, nil
}

func synthesizeNSEC3(
	q Question,
	apex Name,
	params Parameters,
	records []NSEC3Record,
	byHash map[string]NSEC3Record,
	delegations map[string]Delegation,
) (negativeFact, []Witness, error) {
	hasher := &nsec3Hasher{params: params, cache: make(map[string][]byte)}
	ownerHashes := make(map[int][]byte, len(records))
	for i := range records {
		hash, err := records[i].OwnerHash(apex)
		if err != nil {
			return negativeFact{}, nil, err
		}
		ownerHashes[i] = hash
	}
	findCover := func(target []byte) (*NSEC3Record, error) {
		var found *NSEC3Record
		for i := range records {
			if !NSEC3CoveredBy(ownerHashes[i], records[i].NextHashedOwner, target) {
				continue
			}
			if found != nil {
				return nil, fmt.Errorf("%w: overlapping NSEC3 coverage", ErrMalformed)
			}
			found = &records[i]
		}
		return found, nil
	}
	match := func(n Name) (*NSEC3Record, []byte, error) {
		hash, err := hasher.hash(n)
		if err != nil {
			return nil, nil, err
		}
		rec, ok := byHash[string(hash)]
		if !ok {
			return nil, hash, nil
		}
		return &rec, hash, nil
	}

	var delegation *Delegation
	if d, ok := delegationAbove(q.Name, delegations); ok {
		if !q.Name.Equal(d.Name) {
			return negativeFact{}, nil, fmt.Errorf("%w: %s is below delegation %s", ErrDelegation, q.Name, d.Name)
		}
		if q.Type != TypeDS {
			return negativeFact{}, nil, fmt.Errorf("%w: non-DS query at delegation %s", ErrDelegation, d.Name)
		}
		if d.Signed {
			return negativeFact{}, nil, fmt.Errorf("%w: signed delegation has a DS RRset", ErrPositive)
		}
		delegation = &d
	}

	if rec, _, err := match(q.Name); err != nil {
		return negativeFact{}, nil, err
	} else if rec != nil {
		isDelegation := rec.Types.Has(TypeNS) && !rec.Types.Has(TypeSOA)
		if isDelegation && rec.Types.Has(TypeCNAME) {
			return negativeFact{}, nil, fmt.Errorf("%w: delegation NSEC3 bitmap contains CNAME", ErrMalformed)
		}
		if delegation != nil {
			if !isDelegation {
				return negativeFact{}, nil, fmt.Errorf("%w: unsigned delegation lacks NSEC3 NS bitmap", ErrMalformed)
			}
			if rec.Types.Has(TypeDS) {
				return negativeFact{}, nil, fmt.Errorf("%w: NSEC3 bitmap contains DS", ErrPositive)
			}
			return negativeFact{kind: ResultNODATA, types: rec.Types, delegation: true},
				[]Witness{nsec3Witness(WitnessExactMatch, *rec)}, nil
		}
		if isDelegation {
			if q.Type != TypeDS {
				return negativeFact{}, nil, fmt.Errorf("%w: non-DS query at delegation %s", ErrDelegation, q.Name)
			}
			if rec.Types.Has(TypeDS) {
				return negativeFact{}, nil, fmt.Errorf("%w: NSEC3 bitmap contains DS", ErrPositive)
			}
			return negativeFact{kind: ResultNODATA, types: rec.Types, delegation: true},
				[]Witness{nsec3Witness(WitnessExactMatch, *rec)}, nil
		}
		if rec.Types.Has(q.Type) {
			return negativeFact{}, nil, fmt.Errorf("%w: type %d exists at %s", ErrPositive, q.Type, q.Name)
		}
		if rec.Types.Has(TypeCNAME) {
			return negativeFact{}, nil, fmt.Errorf("%w: CNAME exists at %s", ErrPositive, q.Name)
		}
		fact := negativeFact{kind: ResultNODATA, types: rec.Types}
		if rec.Types.Empty() {
			fact.emptyNonTerminal = true
		}
		return fact, []Witness{nsec3Witness(WitnessExactMatch, *rec)}, nil
	}

	var ce Name
	var ceRec *NSEC3Record
	for depth := q.Name.LabelCount() - 1; depth >= apex.LabelCount(); depth-- {
		candidate, ok := q.Name.AncestorAtDepth(depth)
		if !ok {
			break
		}
		rec, _, err := match(candidate)
		if err != nil {
			return negativeFact{}, nil, err
		}
		if rec != nil {
			ce = candidate
			ceRec = rec
			break
		}
	}
	if ceRec == nil {
		return negativeFact{}, nil, fmt.Errorf("%w: no matching NSEC3 closest encloser", ErrUnproven)
	}
	if ceRec.Types.Has(TypeNS) && !ceRec.Types.Has(TypeSOA) {
		return negativeFact{}, nil, fmt.Errorf("%w: closest encloser is a delegation", ErrDelegation)
	}
	nextCloser, ok := q.Name.AncestorAtDepth(ce.LabelCount() + 1)
	if !ok || nextCloser.Equal(ce) {
		return negativeFact{}, nil, fmt.Errorf("%w: cannot derive NSEC3 next closer", ErrUnproven)
	}
	nextRec, nextHash, err := match(nextCloser)
	if err != nil {
		return negativeFact{}, nil, err
	}
	if nextRec != nil {
		return negativeFact{}, nil, fmt.Errorf("%w: next closer exists in NSEC3 chain", ErrUnproven)
	}
	nextCover, err := findCover(nextHash)
	if err != nil {
		return negativeFact{}, nil, err
	}
	if nextCover == nil {
		return negativeFact{}, nil, fmt.Errorf("%w: no NSEC3 covers next closer", ErrUnproven)
	}

	if delegation != nil {
		if !nextCloser.Equal(q.Name) || !nextCover.OptOut {
			return negativeFact{}, nil, fmt.Errorf("%w: unsigned delegation DS absence needs opt-out coverage of its name", ErrUnproven)
		}
		return negativeFact{kind: ResultNODATA, optOutDS: true},
			[]Witness{
				nsec3Witness(WitnessClosestEncloser, *ceRec),
				nsec3Witness(WitnessOptOutCover, *nextCover),
			}, nil
	}
	if nextCover.OptOut {
		// RFC 8198: a covering Opt-Out NSEC3 does not prove non-existence and
		// cannot drive aggressive negative caching. The one narrow exception
		// used here is the RFC 5155 section 8.6 DS NODATA proof at a possible
		// unsigned delegation; everything else fails closed.
		if q.Type == TypeDS && nextCloser.Equal(q.Name) {
			return negativeFact{kind: ResultNODATA, optOutDS: true},
				[]Witness{
					nsec3Witness(WitnessClosestEncloser, *ceRec),
					nsec3Witness(WitnessOptOutCover, *nextCover),
				}, nil
		}
		return negativeFact{}, nil, fmt.Errorf("%w: opt-out covers next closer %s", ErrOptOut, nextCloser)
	}

	wildcard, err := WildcardName(ce)
	if err != nil {
		return negativeFact{}, nil, err
	}
	wildcardRec, wildcardHash, err := match(wildcard)
	if err != nil {
		return negativeFact{}, nil, err
	}
	if wildcardRec != nil {
		if wildcardRec.Types.Has(TypeNS) && !wildcardRec.Types.Has(TypeSOA) {
			if wildcardRec.Types.Has(TypeCNAME) {
				return negativeFact{}, nil, fmt.Errorf("%w: wildcard delegation NSEC3 bitmap contains CNAME", ErrMalformed)
			}
			if q.Type != TypeDS {
				return negativeFact{}, nil, fmt.Errorf("%w: wildcard delegation exists", ErrDelegation)
			}
			if wildcardRec.Types.Has(TypeDS) {
				return negativeFact{}, nil, fmt.Errorf("%w: wildcard NSEC3 bitmap contains DS", ErrPositive)
			}
			return negativeFact{kind: ResultNODATA, types: wildcardRec.Types, delegation: true},
				[]Witness{
					nsec3Witness(WitnessClosestEncloser, *ceRec),
					nsec3Witness(WitnessNextCloserCover, *nextCover),
					nsec3Witness(WitnessWildcardMatch, *wildcardRec),
				}, nil
		}
		if wildcardRec.Types.Has(q.Type) {
			return negativeFact{}, nil, fmt.Errorf("%w: %w: wildcard answers type %d", ErrWildcard, ErrPositive, q.Type)
		}
		if wildcardRec.Types.Has(TypeCNAME) {
			return negativeFact{}, nil, fmt.Errorf("%w: %w: wildcard has CNAME", ErrWildcard, ErrPositive)
		}
		return negativeFact{kind: ResultNODATA, types: wildcardRec.Types},
			[]Witness{
				nsec3Witness(WitnessClosestEncloser, *ceRec),
				nsec3Witness(WitnessNextCloserCover, *nextCover),
				nsec3Witness(WitnessWildcardMatch, *wildcardRec),
			}, nil
	}

	wildcardCover, err := findCover(wildcardHash)
	if err != nil {
		return negativeFact{}, nil, err
	}
	if wildcardCover == nil {
		return negativeFact{}, nil, fmt.Errorf("%w: no NSEC3 covers wildcard", ErrUnproven)
	}
	if wildcardCover.OptOut {
		return negativeFact{}, nil, fmt.Errorf("%w: opt-out covers wildcard %s", ErrOptOut, wildcard)
	}
	return negativeFact{kind: ResultNXDOMAIN},
		[]Witness{
			nsec3Witness(WitnessClosestEncloser, *ceRec),
			nsec3Witness(WitnessNextCloserCover, *nextCover),
			nsec3Witness(WitnessWildcardCover, *wildcardCover),
		}, nil
}
