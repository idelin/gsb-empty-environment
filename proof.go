package taskpkg

import (
	"fmt"
	"time"
)

// ProofKind identifies what a signed proof asserts.
type ProofKind uint8

const (
	// ProofAlias is an authenticated CNAME-style alias. Name is the owner,
	// Target is the alias destination, and Zone is the authority for Name.
	ProofAlias ProofKind = iota + 1
	// ProofNXDomain asserts that Name does not exist in Zone.
	ProofNXDomain
	// ProofNoData asserts that Name exists but has no record of Type.
	ProofNoData
	// ProofAnswer is a positive terminal record.
	ProofAnswer
)

func (k ProofKind) String() string {
	switch k {
	case ProofAlias:
		return "alias"
	case ProofNXDomain:
		return "nxdomain"
	case ProofNoData:
		return "nodata"
	case ProofAnswer:
		return "answer"
	default:
		return fmt.Sprintf("proof-kind(%d)", uint8(k))
	}
}

// Proof is one already signature-verified statement supplied by the parsing
// host. The resolver still validates that Zone is the real authority for Name,
// that the validity window contains the resolution time, and that the proof's
// subject and type are applicable to the current query.
//
// Type is the type asserted by the proof: CNAME for an alias, the missing type
// for NoData, and the record type for Answer. For NXDomain, empty or "*" means
// the name-wide negative, while a concrete type limits the proof to that query
// type.
//
// QType is the optional query-type binding under which the proof was obtained.
// Empty or "*" matches any query type. For aliases, a QType of CNAME also
// matches any query type because a CNAME record is inherently applicable to
// every non-CNAME lookup. A non-CNAME concrete value restricts the proof to
// that query type.
//
// ValidFrom and ValidUntil are a half-open validity interval: the proof is
// valid when ValidFrom <= now < ValidUntil. A zero bound means unbounded.
type Proof struct {
	ID         string
	Kind       ProofKind
	Name       string
	Target     string
	Zone       string
	Type       RRType
	QType      RRType
	Value      string
	ValidFrom  time.Time
	ValidUntil time.Time
}

// ValidAt reports whether now lies in the proof's half-open validity interval.
func (p Proof) ValidAt(now time.Time) bool {
	if !p.ValidFrom.IsZero() && now.Before(p.ValidFrom) {
		return false
	}
	if !p.ValidUntil.IsZero() && !now.Before(p.ValidUntil) {
		return false
	}
	return true
}

// RejectionCode explains why a supplied proof was not trusted.
type RejectionCode string

const (
	RejectMalformed    RejectionCode = "malformed"
	RejectNoAuthority  RejectionCode = "no-authority"
	RejectZoneMismatch RejectionCode = "zone-mismatch"
	RejectNotYetValid  RejectionCode = "not-yet-valid"
	RejectExpired      RejectionCode = "expired"
	RejectTypeMismatch RejectionCode = "type-mismatch"
	RejectInvalidProof RejectionCode = "invalid-proof"
	RejectUnknownKind  RejectionCode = "unknown-kind"
)

// Rejection records an unaudited proof that was not used. It is diagnostic
// only; it never contributes to resolution.
type Rejection struct {
	ProofID string
	Code    RejectionCode
	Name    string
	Zone    string
	Detail  string
}

func (r Rejection) String() string {
	if r.ProofID != "" {
		return fmt.Sprintf("proof %q: %s: %s", r.ProofID, r.Code, r.Detail)
	}
	return fmt.Sprintf("%s: %s", r.Code, r.Detail)
}
