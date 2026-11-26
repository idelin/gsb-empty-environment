// Package nsec3 implements verification of NSEC3 (RFC 5155) negative-DNSSEC
// proofs: NXDOMAIN, NODATA, unsigned delegations (Opt-Out) and wildcard
// expansions. It provides the building blocks (canonical name encoding,
// salted iterated SHA-1, base32hex owner names, RFC 4034 type bitmaps and
// ring interval coverage) as well as the high-level Verifier that combines
// them into proof checks.
package nsec3

import (
	"fmt"
)

// ProofKind classifies the proof a Verifier actually established.
type ProofKind int

const (
	// ProofNone is the zero value; no proof was accepted.
	ProofNone ProofKind = iota
	// ProofNXDOMAIN: the QNAME does not exist (RFC 5155 section 8.3).
	ProofNXDOMAIN
	// ProofNODATAExact: an NSEC3 record matches the QNAME hash and its
	// bitmap denies the qtype (and CNAME).
	ProofNODATAExact
	// ProofNODATAENT: the QNAME is an empty non-terminal: the closest
	// encloser matches and the next closer name is merely covered.
	ProofNODATAENT
	// ProofNODATAOptOut: an Opt-Out NSEC3 record covers an unsigned
	// delegation below a signed encloser (DS queries only).
	ProofNODATAOptOut
	// ProofUnsignedDelegation: same Opt-Out mechanism exposed through
	// the dedicated unsigned-delegation entry point.
	ProofUnsignedDelegation
	// ProofWildcard: a wildcard answer is proven safe because the next
	// closer name is covered.
	ProofWildcard
)

// Proof carries the records that established a negative response, so
// callers can audit which part of the chain was used.
type Proof struct {
	Kind            ProofKind
	ClosestEncloser string
	NextCloser      string
	Wildcard        string
	// Encloser is the exact-matching record for the closest encloser.
	Encloser *Record
	// NextCloserRecord covers (never matches) the next closer name.
	NextCloserRecord *Record
	// WildcardRecord covers the hash of "*.<closest encloser>".
	WildcardRecord *Record
	// Exact is the record matching the QNAME hash for exact-match
	// NODATA proofs.
	Exact *Record
}

// Verifier holds the NSEC3 records that make up one signed answer's
// denial proof. All records must belong to the same hash chain, i.e.
// share algorithm, iterations and salt.
type Verifier struct {
	params  Params
	records []*Record
	byOwner map[string]*Record // key: hex-less raw hash string
}

// NewVerifier validates and indexes the supplied NSEC3 records. It rejects
// unknown hash algorithms, iteration counts above MaxIterations, malformed
// records, duplicate owner hashes and parameter mismatches across records
// (which would indicate a cross-zone / spliced-chain attack).
func NewVerifier(records []*Record) (*Verifier, error) {
	if len(records) == 0 {
		return nil, ErrNoRecords
	}
	v := &Verifier{
		records: make([]*Record, 0, len(records)),
		byOwner: make(map[string]*Record, len(records)),
	}
	for i, r := range records {
		if r == nil {
			return nil, fmt.Errorf("%w: record %d is nil", ErrInvalidRecord, i)
		}
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("record %d: %w", i, err)
		}
		if i == 0 {
			v.params = r.Params()
		} else if !r.Params().Equal(v.params) {
			return nil, fmt.Errorf("%w: record %d", ErrParamMismatch, i)
		}
		if len(r.OwnerHash) == 0 {
			return nil, fmt.Errorf("%w: record %d has no owner hash", ErrInvalidRecord, i)
		}
		if len(r.OwnerHash) != len(r.NextHash) {
			return nil, fmt.Errorf("%w: record %d owner/next hash length mismatch",
				ErrInvalidRecord, i)
		}
		key := string(r.OwnerHash)
		if _, exists := v.byOwner[key]; exists {
			return nil, fmt.Errorf("%w: %x", ErrDuplicateOwner, r.OwnerHash)
		}
		v.byOwner[key] = r
		v.records = append(v.records, r)
	}
	return v, nil
}

// Params returns the shared hash-chain parameters.
func (v *Verifier) Params() Params { return v.params }

// hashOf computes a name hash using the verifier's chain parameters.
func (v *Verifier) hashOf(name string) ([]byte, error) {
	return HashName(v.params.Algorithm, v.params.Iterations, v.params.Salt, name)
}

// exactMatch returns the record whose owner hash equals h, if any.
func (v *Verifier) exactMatch(h []byte) *Record {
	return v.byOwner[string(h)]
}

// covering returns any record whose interval covers h.
func (v *Verifier) covering(h []byte) *Record {
	for _, r := range v.records {
		if RecordCoversHash(r, h) {
			return r
		}
	}
	return nil
}

// closestEncloser walks the strict ancestors of qname (closest first) and
// returns the first whose hash has an exact-matching NSEC3 owner, together
// with the corresponding next closer name.
func (v *Verifier) closestEncloser(qname string) (ce string, nc string, rec *Record, err error) {
	anc, err := ancestors(qname)
	if err != nil {
		return "", "", nil, err
	}
	for _, candidate := range anc {
		h, err := v.hashOf(candidate)
		if err != nil {
			return "", "", nil, err
		}
		if r := v.exactMatch(h); r != nil {
			nc, err := nextCloserName(qname, candidate)
			if err != nil {
				return "", "", nil, err
			}
			return candidate, nc, r, nil
		}
	}
	return "", "", nil, ErrNoClosestEncloser
}

// VerifyNXDOMAIN verifies an NXDOMAIN response per RFC 5155 section 8.3
// (and the worked example in appendix B.1). All three elements are
// mandatory:
//
//  1. an NSEC3 record whose owner hash exactly matches the closest
//     encloser (an existing ancestor of the QNAME);
//  2. an NSEC3 record covering the next closer name;
//  3. an NSEC3 record covering the hash of "*.<closest encloser>".
func (v *Verifier) VerifyNXDOMAIN(qname string) (*Proof, error) {
	ce, nc, ceRec, err := v.closestEncloser(qname)
	if err != nil {
		return nil, err
	}
	ncHash, err := v.hashOf(nc)
	if err != nil {
		return nil, err
	}
	ncRec := v.covering(ncHash)
	if ncRec == nil {
		return nil, fmt.Errorf("%w: %s", ErrNextCloserNotCovered, nc)
	}
	wild, err := wildcardName(ce)
	if err != nil {
		return nil, err
	}
	wildHash, err := v.hashOf(wild)
	if err != nil {
		return nil, err
	}
	// A wildcard that itself exists would have an exact owner; the
	// NXDOMAIN proof requires the name to be *covered*, never matched.
	if r := v.exactMatch(wildHash); r != nil {
		return nil, fmt.Errorf("%w: wildcard %s has an exact owner", ErrWildcardNotCovered, wild)
	}
	wildRec := v.covering(wildHash)
	if wildRec == nil {
		return nil, fmt.Errorf("%w: %s", ErrWildcardNotCovered, wild)
	}
	return &Proof{
		Kind:             ProofNXDOMAIN,
		ClosestEncloser:  ce,
		NextCloser:       nc,
		Wildcard:         wild,
		Encloser:         ceRec,
		NextCloserRecord: ncRec,
		WildcardRecord:   wildRec,
	}, nil
}

// VerifyNODATA verifies a NODATA response (NOERROR with an empty answer
// section) per RFC 5155 section 8.4. qtype is the requested DNS type.
//
// Three independent proof paths are accepted:
//
//  1. Exact match: an NSEC3 owner equals the QNAME hash, its bitmap omits
//     qtype and CNAME; for DS queries it must additionally omit SOA,
//     because an apex SOA means the DS question is about a delegation and
//     must be proven via Opt-Out instead.
//  2. Empty non-terminal: the closest encloser matches exactly and the
//     next closer name is covered (the name exists but has no owner RR).
//  3. Opt-Out delegation (DS queries only): the next closer name is
//     covered by a record with the Opt-Out flag set; using such a record
//     without the flag, or for a non-DS query, is rejected.
func (v *Verifier) VerifyNODATA(qname string, qtype uint16) (*Proof, error) {
	qhash, err := v.hashOf(qname)
	if err != nil {
		return nil, err
	}
	// Path 1: exact match at the QNAME.
	if r := v.exactMatch(qhash); r != nil {
		if r.HasType(qtype) {
			return nil, fmt.Errorf("%w: type %d at %s", ErrTypePresent, qtype, qname)
		}
		if r.HasType(TypeCNAME) {
			return nil, fmt.Errorf("%w: %s", ErrCNAMEPresent, qname)
		}
		if qtype == TypeDS && r.HasType(TypeSOA) {
			return nil, fmt.Errorf("%w: %s", ErrSOAPresent, qname)
		}
		return &Proof{Kind: ProofNODATAExact, Exact: r}, nil
	}

	ce, nc, ceRec, err := v.closestEncloser(qname)
	if err != nil {
		return nil, err
	}
	ncHash, err := v.hashOf(nc)
	if err != nil {
		return nil, err
	}
	ncRec := v.covering(ncHash)
	if ncRec == nil {
		return nil, fmt.Errorf("%w: %s", ErrNextCloserNotCovered, nc)
	}

	// Path 3: Opt-Out only proves anything for DS queries.
	if ncRec.OptOut() {
		if qtype != TypeDS {
			return nil, fmt.Errorf("%w: type %d", ErrOptOutNotApplicable, qtype)
		}
		return &Proof{
			Kind:             ProofNODATAOptOut,
			ClosestEncloser:  ce,
			NextCloser:       nc,
			Encloser:         ceRec,
			NextCloserRecord: ncRec,
		}, nil
	}

	// Path 2: ordinary empty non-terminal NODATA.
	if qtype == TypeDS {
		// A DS denial without Opt-Out cannot rely on a plain cover.
		return nil, fmt.Errorf("%w: covering record for %s", ErrOptOutRequired, nc)
	}
	return &Proof{
		Kind:             ProofNODATAENT,
		ClosestEncloser:  ce,
		NextCloser:       nc,
		Encloser:         ceRec,
		NextCloserRecord: ncRec,
	}, nil
}

// VerifyUnsignedDelegation verifies that the name below qname's closest
// encloser is an unsigned delegation covered by an Opt-Out NSEC3 record
// (RFC 5155 sections 3.2 and 8.4).
//
// The next closer name must be covered by a record with Opt-Out set; a
// plain covering record is explicitly rejected so a signed child zone
// cannot be downgraded to insecure. The closest encloser must not itself
// be a delegation point (an NS bit), since delegation semantics then
// belong to that parent name instead.
func (v *Verifier) VerifyUnsignedDelegation(qname string) (*Proof, error) {
	ce, nc, ceRec, err := v.closestEncloser(qname)
	if err != nil {
		return nil, err
	}
	// A zone apex legitimately carries an NS RRset together with SOA; it
	// is only a *delegation point* when NS is present without SOA.
	if ceRec.HasType(TypeNS) && !ceRec.HasType(TypeSOA) {
		return nil, fmt.Errorf("%w: %s", ErrDelegationPoint, ce)
	}
	ncHash, err := v.hashOf(nc)
	if err != nil {
		return nil, err
	}
	ncRec := v.covering(ncHash)
	if ncRec == nil {
		return nil, fmt.Errorf("%w: %s", ErrNextCloserNotCovered, nc)
	}
	if !ncRec.OptOut() {
		return nil, fmt.Errorf("%w: covering record for %s", ErrOptOutRequired, nc)
	}
	return &Proof{
		Kind:             ProofUnsignedDelegation,
		ClosestEncloser:  ce,
		NextCloser:       nc,
		Encloser:         ceRec,
		NextCloserRecord: ncRec,
	}, nil
}

// VerifyWildcardAnswer verifies that an answer synthesized from a wildcard
// is legitimate (RFC 5155 section 8.5 / appendix B.2). The proof must show
// the closest encloser exists (exact owner match) and that the next closer
// name is covered: coverage proves no more-specific name exists which
// would have shadowed the wildcard. wildcardName is the wildcard owner
// used in the answer, e.g. "*.example."; when non-empty its hash is
// checked as well.
func (v *Verifier) VerifyWildcardAnswer(qname, wildcardNameUsed string) (*Proof, error) {
	ce, nc, ceRec, err := v.closestEncloser(qname)
	if err != nil {
		return nil, err
	}
	ncHash, err := v.hashOf(nc)
	if err != nil {
		return nil, err
	}
	ncRec := v.covering(ncHash)
	if ncRec == nil {
		return nil, fmt.Errorf("%w: %s", ErrNextCloserNotCovered, nc)
	}
	p := &Proof{
		Kind:             ProofWildcard,
		ClosestEncloser:  ce,
		NextCloser:       nc,
		Encloser:         ceRec,
		NextCloserRecord: ncRec,
	}
	if wildcardNameUsed != "" {
		wild, err := LowerName(wildcardNameUsed)
		if err != nil {
			return nil, err
		}
		wildHash, err := v.hashOf(wild)
		if err != nil {
			return nil, err
		}
		r := v.exactMatch(wildHash)
		if r == nil {
			r = v.covering(wildHash)
		}
		if r == nil {
			return nil, fmt.Errorf("%w: %s", ErrWildcardNotCovered, wild)
		}
		p.Wildcard = wild
		p.WildcardRecord = r
	}
	return p, nil
}
