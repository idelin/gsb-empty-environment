// Package admission implements the AS_PATH and ROA joint admission check used
// by a BGP route server import policy.
//
// It rejects routes whose AS_PATH is structurally untrusted (empty AS_PATH on
// eBGP sessions, mismatched first AS, private/reserved AS numbers, excessive
// prepending) and combines that with RPKI origin validation (RFC 6483),
// including the undetermined-origin treatment of AS_SET routes (RFC 6811).
package admission

import (
	"fmt"
	"net/netip"
	"strings"
)

// Reason identifies why an admission decision was taken.
type Reason string

const (
	ReasonEmptyPathOnEBGP Reason = "EMPTY_AS_PATH_ON_EBGP"
	ReasonFirstASMismatch Reason = "FIRST_AS_MISMATCH"
	ReasonPrivateAS       Reason = "PRIVATE_AS_IN_PATH"
	ReasonReservedAS      Reason = "RESERVED_AS_IN_PATH"
	ReasonPrependExceeded Reason = "PREPEND_LIMIT_EXCEEDED"
	ReasonRPKIInvalid     Reason = "RPKI_INVALID"
	ReasonInvalidPrefix   Reason = "INVALID_PREFIX"
)

// Session describes the BGP session on which an UPDATE was received.
type Session struct {
	PeerAS uint32
	// EBGP is false for iBGP sessions, where an empty AS_PATH is legitimate
	// (e.g. locally originated routes) and the peer-AS first-hop check does
	// not apply.
	EBGP bool
}

// ROATable is the set of validated ROAs available to the route server.
type ROATable struct {
	roas []ROA
}

// NewROATable builds a table from already-constructed ROA entries.
func NewROATable(roas ...ROA) ROATable {
	copied := make([]ROA, len(roas))
	copy(copied, roas)
	return ROATable{roas: copied}
}

// Decision is the admission result returned to the import policy.
type Decision struct {
	// Rejected is true when the route must not be accepted.
	Rejected bool
	// RPKI is VALID, NOT_FOUND or INVALID. It is empty when the prefix could
	// not be parsed.
	RPKI string
	// OriginAS is the derived origin. It is zero when the origin is
	// undetermined (empty path or trailing AS_SET).
	OriginAS uint32
	// OriginKnown is false for undetermined origins per RFC 6811.
	OriginKnown bool
	// Reasons lists every violated rule and RPKI outcome, each with a detail
	// string explaining the conclusion.
	Reasons []Reason
	Details []string
}

func (d Decision) String() string {
	verdict := "ACCEPT"
	if d.Rejected {
		verdict = "REJECT"
	}
	origin := "NONE"
	if d.OriginKnown {
		origin = fmt.Sprintf("AS%d", d.OriginAS)
	}
	return fmt.Sprintf("%s rpki=%s origin=%s reasons=%v details=%q",
		verdict, d.RPKI, origin, d.Reasons, strings.Join(d.Details, "; "))
}

// Admit applies the joint AS_PATH/ROA import policy to one UPDATE.
//
// The AS_PATH is validated even when ROA data is missing; pass an empty
// ROATable if RPKI data is unavailable, in which case parseable routes with a
// sound AS_PATH evaluate to NOT_FOUND and are accepted.
func (t ROATable) Admit(sess Session, prefix string, path ASPath) Decision {
	var d Decision

	p, err := netip.ParsePrefix(prefix)
	if err != nil {
		d.Rejected = true
		d.Reasons = append(d.Reasons, ReasonInvalidPrefix)
		d.Details = append(d.Details, fmt.Sprintf("cannot parse prefix %q: %v", prefix, err))
		return d
	}
	p = p.Masked()

	origin, originKnown, violations := validatePath(sess, path)
	d.OriginAS = origin
	d.OriginKnown = originKnown
	for _, v := range violations {
		d.Reasons = append(d.Reasons, v.reason)
		d.Details = append(d.Details, v.detail)
	}

	result := t.validate(p, origin, originKnown)
	d.RPKI = result.state
	d.Details = append(d.Details, result.detail)
	if result.state == RPKIInvalid {
		d.Reasons = append(d.Reasons, ReasonRPKIInvalid)
	}

	d.Rejected = len(d.Reasons) > 0
	return d
}
