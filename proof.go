package dnssec

import (
	"errors"
	"fmt"
	"time"
)

// NODATAProofKind identifies the three RFC 5155 NODATA evidence forms.
type NODATAProofKind int

const (
	NODATAExact NODATAProofKind = iota + 1
	NODATAEmptyNonTerminal
	NODATAOptOutDelegation
)

// NXDOMAINProof is the complete three-link NXDOMAIN evidence chain.
type NXDOMAINProof struct {
	QName              Name
	ClosestEncloser    Name
	ClosestEncloserRR  *NSEC3Record
	NextCloser         Name
	NextCloserRR       *NSEC3Record
	Wildcard           Name
	WildcardCoverageRR *NSEC3Record
}

// NODATAProof describes why the requested type is absent.
type NODATAProof struct {
	QName      Name
	QType      uint16
	Kind       NODATAProofKind
	NSEC3      *NSEC3Record
	OptOut     bool
	Delegation bool
}

// WildcardAnswerProof proves that a wildcard answer did not shadow an exact
// owner. It combines RRSIG Labels wildcard reconstruction with the NSEC3
// closest-encloser and next-closer denial chain.
type WildcardAnswerProof struct {
	QName             Name
	Wildcard          Name
	ClosestEncloser   Name
	ClosestEncloserRR *NSEC3Record
	NextCloser        Name
	NextCloserRR      *NSEC3Record
}

func validateQueryInSet(set *NSEC3Set, qname Name) error {
	if set == nil {
		return errors.New("dnssec: nil NSEC3 set")
	}
	if len(set.Records) == 0 {
		return errors.New("dnssec: empty NSEC3 set")
	}
	if !IsSubdomainOf(qname, set.Zone) {
		return fmt.Errorf("dnssec: query %s is outside zone %s", qname, set.Zone)
	}
	return nil
}

// ValidateNXDOMAIN checks all three required links for NSEC3-authenticated
// NXDOMAIN: an exact closest-encloser match, coverage of the next-closer name,
// and coverage of *.closest_encloser. Opt-Out covering records cannot be used
// for NXDOMAIN denial.
func ValidateNXDOMAIN(set *NSEC3Set, qname Name) (*NXDOMAINProof, error) {
	if err := validateQueryInSet(set, qname); err != nil {
		return nil, err
	}
	queryHash, err := set.Hash(qname)
	if err != nil {
		return nil, err
	}
	if _, ok := set.FindExactHash(queryHash); ok {
		return nil, fmt.Errorf("dnssec: NXDOMAIN rejected: %s has an exact NSEC3 owner", qname)
	}

	ce, ceRR, err := set.ClosestEncloser(qname)
	if err != nil {
		return nil, fmt.Errorf("dnssec: NXDOMAIN closest encloser: %w", err)
	}
	if EqualNames(ce, qname) {
		return nil, fmt.Errorf("dnssec: NXDOMAIN rejected: closest encloser equals query %s", qname)
	}

	nextCloser, err := NextCloser(qname, ce)
	if err != nil {
		return nil, err
	}
	nxRR, nextHash, ok, err := set.FindCoverageName(nextCloser)
	if err != nil {
		return nil, err
	}
	if _, exists := set.FindExactHash(nextHash); exists {
		return nil, fmt.Errorf("dnssec: NXDOMAIN rejected: next-closer %s exists as an exact NSEC3 owner", nextCloser)
	}
	if !ok {
		return nil, fmt.Errorf("dnssec: NXDOMAIN rejected: next-closer %s is not covered by any NSEC3", nextCloser)
	}
	if nxRR.NSEC3.Flags&NSEC3FlagOptOut != 0 {
		return nil, fmt.Errorf("dnssec: NXDOMAIN rejected: next-closer %s is covered only by an Opt-Out NSEC3", nextCloser)
	}

	wildcard := ce.Wildcard()
	wcRR, wildcardHash, ok, err := set.FindCoverageName(wildcard)
	if err != nil {
		return nil, err
	}
	if _, exists := set.FindExactHash(wildcardHash); exists {
		return nil, fmt.Errorf("dnssec: NXDOMAIN rejected: wildcard %s exists as an exact NSEC3 owner", wildcard)
	}
	if !ok {
		return nil, fmt.Errorf("dnssec: NXDOMAIN rejected: wildcard %s is not covered by any NSEC3", wildcard)
	}
	if wcRR.NSEC3.Flags&NSEC3FlagOptOut != 0 {
		return nil, fmt.Errorf("dnssec: NXDOMAIN rejected: wildcard %s is covered only by an Opt-Out NSEC3", wildcard)
	}

	return &NXDOMAINProof{
		QName:              qname,
		ClosestEncloser:    ce,
		ClosestEncloserRR:  ceRR,
		NextCloser:         nextCloser,
		NextCloserRR:       nxRR,
		Wildcard:           wildcard,
		WildcardCoverageRR: wcRR,
	}, nil
}

// ValidateNODATA validates exact-node, empty-non-terminal, and Opt-Out
// unsigned-delegation NODATA evidence. The optional delegation argument must
// be true for an unsigned delegation. Opt-Out coverage is never accepted for
// a non-delegation query, and a delegation NODATA proof is limited to DS.
func ValidateNODATA(set *NSEC3Set, qname Name, qtype uint16, delegation ...bool) (*NODATAProof, error) {
	if err := validateQueryInSet(set, qname); err != nil {
		return nil, err
	}
	isDelegation := len(delegation) > 0 && delegation[0]

	exactRR, hash, exact, err := set.FindExactName(qname)
	if err != nil {
		return nil, err
	}
	if exact {
		if typeBitmapContains(exactRR.NSEC3.Types, qtype) {
			return nil, fmt.Errorf("dnssec: NODATA rejected: type %d exists at %s", qtype, qname)
		}
		if qtype != TypeCNAME && typeBitmapContains(exactRR.NSEC3.Types, TypeCNAME) {
			return nil, fmt.Errorf("dnssec: NODATA rejected: CNAME exists at %s and must be returned for type %d", qname, qtype)
		}
		kind := NODATAExact
		if len(exactRR.NSEC3.Types) == 0 {
			kind = NODATAEmptyNonTerminal
		}
		return &NODATAProof{
			QName:      qname,
			QType:      qtype,
			Kind:       kind,
			NSEC3:      exactRR,
			Delegation: isDelegation,
		}, nil
	}

	coverRR, ok := set.FindCoverage(hash)
	if !ok {
		return nil, fmt.Errorf("dnssec: NODATA rejected: no exact match and no covering NSEC3 for %s", qname)
	}
	if coverRR.NSEC3.Flags&NSEC3FlagOptOut == 0 {
		return nil, fmt.Errorf("dnssec: NODATA rejected: non-Opt-Out coverage of %s proves non-existence, not NODATA", qname)
	}

	// Opt-Out is meaningful only at an unsigned delegation boundary. The
	// resolver must establish that context; it cannot be inferred from an
	// arbitrary name inside an Opt-Out span.
	if !isDelegation {
		return nil, fmt.Errorf("dnssec: NODATA rejected: Opt-Out coverage for non-delegation query %s", qname)
	}
	if qtype != TypeDS {
		return nil, fmt.Errorf("dnssec: NODATA rejected: Opt-Out delegation proof is only valid for a DS query, got type %d", qtype)
	}

	return &NODATAProof{
		QName:      qname,
		QType:      qtype,
		Kind:       NODATAOptOutDelegation,
		NSEC3:      coverRR,
		OptOut:     true,
		Delegation: true,
	}, nil
}

// ValidateWildcardAnswer checks a wildcard-expanded positive answer. labels
// is the RRSIG Labels field. The proof requires the closest encloser to exist
// exactly, the exact QNAME to be absent, and the next-closer (equal to QNAME
// for a one-label wildcard expansion) to be denied by a non-Opt-Out NSEC3.
func ValidateWildcardAnswer(set *NSEC3Set, qname Name, labels uint8) (*WildcardAnswerProof, error) {
	if err := validateQueryInSet(set, qname); err != nil {
		return nil, err
	}
	if int(labels) >= qname.LabelCount() {
		return nil, fmt.Errorf("dnssec: wildcard proof rejected: RRSIG labels %d is not below owner label count %d", labels, qname.LabelCount())
	}
	if int(labels)+1 != qname.LabelCount() {
		return nil, fmt.Errorf("dnssec: wildcard proof rejected: labels %d cannot expand to %d-label query", labels, qname.LabelCount())
	}
	ce, err := qname.Ancestor(int(labels))
	if err != nil {
		return nil, err
	}
	if !IsSubdomainOf(ce, set.Zone) {
		return nil, fmt.Errorf("dnssec: wildcard proof rejected: closest encloser %s is outside zone", ce)
	}

	qhash, err := set.Hash(qname)
	if err != nil {
		return nil, err
	}
	if _, ok := set.FindExactHash(qhash); ok {
		return nil, fmt.Errorf("dnssec: wildcard proof rejected: exact owner %s exists; wildcard must not shadow it", qname)
	}

	ceRR, _, ceExact, err := set.FindExactName(ce)
	if err != nil {
		return nil, err
	}
	if !ceExact {
		return nil, fmt.Errorf("dnssec: wildcard proof rejected: closest encloser %s has no exact NSEC3", ce)
	}

	nextCloser, err := NextCloser(qname, ce)
	if err != nil {
		return nil, err
	}
	nxRR, _, covered, err := set.FindCoverageName(nextCloser)
	if err != nil {
		return nil, err
	}
	if !covered {
		return nil, fmt.Errorf("dnssec: wildcard proof rejected: next-closer %s is not denied", nextCloser)
	}
	if nxRR.NSEC3.Flags&NSEC3FlagOptOut != 0 {
		return nil, fmt.Errorf("dnssec: wildcard proof rejected: next-closer %s is covered only by Opt-Out evidence", nextCloser)
	}

	return &WildcardAnswerProof{
		QName:             qname,
		Wildcard:          ce.Wildcard(),
		ClosestEncloser:   ce,
		ClosestEncloserRR: ceRR,
		NextCloser:        nextCloser,
		NextCloserRR:      nxRR,
	}, nil
}

// SignedNSEC3 is an NSEC3 RR together with the RRSIGs that should
// authenticate its RRset.
type SignedNSEC3 struct {
	Record     NSEC3Record
	Signatures []RRSIG
}

// Validator authenticates NSEC3 evidence with ECDSA-P256 zone keys before
// applying the RFC 5155 proof rules.
type Validator struct {
	Zone          Name
	Keys          []DNSKEY
	Now           time.Time
	MaxIterations uint16
}

// NewValidator returns a validator using the default NSEC3 iteration limit.
func NewValidator(zone Name, keys []DNSKEY, now time.Time) *Validator {
	return &Validator{
		Zone:          Name{Labels: append([]string(nil), zone.Labels...)},
		Keys:          append([]DNSKEY(nil), keys...),
		Now:           now,
		MaxIterations: DefaultMaxIterations,
	}
}

// NewValidatorWithLimit returns a validator with an explicit iteration limit.
func NewValidatorWithLimit(zone Name, keys []DNSKEY, now time.Time, maxIterations uint16) *Validator {
	v := NewValidator(zone, keys, now)
	if maxIterations > ProtocolMaxIterations {
		maxIterations = ProtocolMaxIterations
	}
	v.MaxIterations = maxIterations
	return v
}

// AuthenticateNSEC3 verifies every supplied NSEC3 RRset signature and builds
// a parameter-consistent NSEC3 set. Wildcard-indicated RRSIG labels are not
// accepted for NSEC3 owners, whose names must be signed at their full label
// count.
func (v *Validator) AuthenticateNSEC3(evidence []SignedNSEC3) (*NSEC3Set, error) {
	if v == nil {
		return nil, errors.New("dnssec: nil validator")
	}
	if len(evidence) == 0 {
		return nil, errors.New("dnssec: no NSEC3 evidence")
	}
	if len(v.Keys) == 0 {
		return nil, errors.New("dnssec: no zone DNSKEYs")
	}
	records := make([]NSEC3Record, 0, len(evidence))
	for i, signed := range evidence {
		if len(signed.Signatures) == 0 {
			return nil, fmt.Errorf("dnssec: NSEC3 evidence %d has no RRSIG", i)
		}
		rr := signed.Record.RR()
		if rr.Class == 0 {
			rr.Class = ClassINET
		}
		sigIndex, _, err := VerifyAnyRRSIG([]RR{rr}, signed.Signatures, v.Keys, v.Now, v.Zone)
		if err != nil {
			return nil, fmt.Errorf("dnssec: NSEC3 evidence %d signature: %w", i, err)
		}
		sig := signed.Signatures[sigIndex]
		if sig.TypeCovered != TypeNSEC3 {
			return nil, fmt.Errorf("dnssec: NSEC3 evidence %d is covered by RRSIG type %d", i, sig.TypeCovered)
		}
		if int(sig.Labels) != signed.Record.Owner.LabelCount() {
			return nil, fmt.Errorf("dnssec: NSEC3 evidence %d uses wildcard RRSIG labels %d for owner with %d labels", i, sig.Labels, signed.Record.Owner.LabelCount())
		}
		records = append(records, signed.Record)
	}
	set, err := NewNSEC3Set(v.Zone, records, v.MaxIterations)
	if err != nil {
		return nil, err
	}
	return set, nil
}

// NXDOMAIN authenticates evidence and validates an NXDOMAIN proof.
func (v *Validator) NXDOMAIN(qname Name, evidence []SignedNSEC3) (*NXDOMAINProof, error) {
	set, err := v.AuthenticateNSEC3(evidence)
	if err != nil {
		return nil, err
	}
	return ValidateNXDOMAIN(set, qname)
}

// NODATA authenticates evidence and validates a NODATA proof. Pass true as
// delegation only when the resolver has established that qname is an
// unsigned delegation boundary.
func (v *Validator) NODATA(qname Name, qtype uint16, evidence []SignedNSEC3, delegation ...bool) (*NODATAProof, error) {
	set, err := v.AuthenticateNSEC3(evidence)
	if err != nil {
		return nil, err
	}
	return ValidateNODATA(set, qname, qtype, delegation...)
}

// WildcardAnswer verifies a positive RRset against one of sigs, requires the
// selected signature to indicate a one-label wildcard expansion, and then
// verifies the NSEC3 next-closer denial proof. This prevents a valid wildcard
// signature from shadowing a real exact owner.
func (v *Validator) WildcardAnswer(qname Name, rrset []RR, sigs []RRSIG, evidence []SignedNSEC3) (*WildcardAnswerProof, error) {
	if v == nil {
		return nil, errors.New("dnssec: nil validator")
	}
	if len(rrset) == 0 {
		return nil, errors.New("dnssec: empty wildcard RRset")
	}
	if !EqualNames(rrset[0].Name, qname) {
		return nil, fmt.Errorf("dnssec: wildcard RRset owner %s is not query %s", rrset[0].Name, qname)
	}
	if len(v.Keys) == 0 {
		return nil, errors.New("dnssec: no zone DNSKEYs")
	}

	var selected *RRSIG
	var last error
	for i := range sigs {
		sig := &sigs[i]
		if int(sig.Labels) >= qname.LabelCount() {
			continue
		}
		for _, key := range v.Keys {
			if sig.Algorithm != key.Algorithm || sig.KeyTag != key.KeyTag() {
				continue
			}
			if err := VerifyRRSIG(rrset, *sig, key, v.Now, v.Zone); err != nil {
				last = err
				continue
			}
			selected = sig
			break
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		if last == nil {
			last = errors.New("dnssec: no wildcard-indicated RRSIG matched a zone DNSKEY")
		}
		return nil, last
	}

	set, err := v.AuthenticateNSEC3(evidence)
	if err != nil {
		return nil, err
	}
	return ValidateWildcardAnswer(set, qname, selected.Labels)
}
