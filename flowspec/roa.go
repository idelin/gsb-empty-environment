package flowspec

import (
	"fmt"
	"net"
	"time"
)

// RPKIStatus is the RFC 6811 route origin validation outcome.
type RPKIStatus uint8

const (
	RPKIStatusNotFound RPKIStatus = iota
	RPKIStatusValid
	RPKIStatusInvalid
)

// String returns the RFC 6811 status name.
func (s RPKIStatus) String() string {
	switch s {
	case RPKIStatusValid:
		return "Valid"
	case RPKIStatusInvalid:
		return "Invalid"
	default:
		return "NotFound"
	}
}

// AdmissionCode is a deterministic machine-readable admission result/reason.
type AdmissionCode string

const (
	AdmitOK             AdmissionCode = "ok"
	AdmitNoDestPrefix   AdmissionCode = "missing-destination-prefix"
	AdmitNotFound       AdmissionCode = "roa-not-found"
	AdmitOriginMismatch AdmissionCode = "origin-as-not-authorized"
	AdmitPrefixTooWide  AdmissionCode = "prefix-longer-than-min-length" // prefixLen < MinLength
	AdmitPrefixTooLong  AdmissionCode = "prefix-exceeds-max-length"     // prefixLen > MaxLength
	AdmitROAExpired     AdmissionCode = "roa-expired"
)

// ROA is a Validated ROA / VRP-like authorization. Prefix is the ROA prefix;
// MaxLength is the maximum authorized announced length. MinLength optionally
// raises the lower bound; when zero it defaults to the ROA prefix length,
// yielding the standard "prefix length <= announced length <= MaxLength" rule.
type ROA struct {
	Prefix    net.IPNet
	OriginAS  uint32
	MinLength int
	MaxLength int

	// Optional validity window. A zero Now passed to Admit ignores expiry.
	NotBefore time.Time
	NotAfter  time.Time
}

// effectiveMin returns the lower permitted length.
func (r ROA) effectiveMin() int {
	if r.MinLength > 0 {
		return r.MinLength
	}
	ones, _ := r.Prefix.Mask.Size()
	return ones
}

// lengthValid reports whether plen lies in [MinLength, MaxLength].
func (r ROA) lengthValid(plen int) bool {
	max := r.MaxLength
	if max == 0 {
		if ones, bits := r.Prefix.Mask.Size(); bits != 0 {
			max = ones
		}
	}
	return plen >= r.effectiveMin() && plen <= max
}

// temporallyValid reports whether the ROA is inside its validity window at now.
// A zero now disables the check; zero endpoints are unbounded.
func (r ROA) temporallyValid(now time.Time) bool {
	if now.IsZero() {
		return true
	}
	if !r.NotBefore.IsZero() && now.Before(r.NotBefore) {
		return false
	}
	if !r.NotAfter.IsZero() && now.After(r.NotAfter) {
		return false
	}
	return true
}

// AdmissionDecision is the result of ROA origin/length admission.
type AdmissionDecision struct {
	Allowed bool
	Status  RPKIStatus
	Code    AdmissionCode
	Detail  string

	OriginAS uint32
	Prefix   net.IPNet

	// MatchedROA is set for a Valid decision.
	MatchedROA *ROA
	// AuthorizedAS lists ROA origin ASNs that cover the prefix/length when the
	// announcing AS does not match (cross-tenant attribution).
	AuthorizedAS []uint32
}

// Admit enforces destination-prefix ROA authorization for a rule.
func Admit(rule *Rule, roas []ROA, now time.Time) AdmissionDecision {
	if rule == nil {
		return AdmissionDecision{Allowed: false, Status: RPKIStatusInvalid, Code: AdmitNoDestPrefix, Detail: "nil rule"}
	}
	prefix, ok := rule.DestPrefix()
	if !ok {
		return AdmissionDecision{
			Allowed:  false,
			Status:   RPKIStatusInvalid,
			Code:     AdmitNoDestPrefix,
			OriginAS: rule.OriginAS,
			Detail:   "FlowSpec rule carries no type-1 destination prefix",
		}
	}
	return AdmitPrefix(prefix, rule.OriginAS, roas, now)
}

// AdmitPrefix is the core RPKI authorization check for one destination prefix.
func AdmitPrefix(prefix net.IPNet, originAS uint32, roas []ROA, now time.Time) AdmissionDecision {
	ones, queryBits := prefix.Mask.Size()
	networkIP := prefix.IP.Mask(prefix.Mask)

	base := AdmissionDecision{Allowed: false, OriginAS: originAS, Prefix: prefix}

	var (
		lengthOKWrongAS []*ROA
		sameASBadLen    []*ROA
		coveringAny     []*ROA
		expiredMatch    []*ROA
		authorizedAS    []uint32
	)

	for i := range roas {
		roa := &roas[i]
		if _, roaBits := roa.Prefix.Mask.Size(); roaBits != 0 && queryBits != 0 && roaBits != queryBits {
			continue // different address family
		}
		// The prefixes are related (one contains the other). roa.Contains
		// handles the normal case where the ROA is at least as specific; the
		// reverse prefix.Contains catches an over-wide announcement that
		// contains the ROA, which must still resolve to a length violation.
		related := roa.Prefix.Contains(networkIP) || prefix.Contains(roa.Prefix.IP)
		if prefix.Mask != nil && roa.Prefix.Mask != nil && !related {
			continue
		}

		if !roa.temporallyValid(now) {
			expiredMatch = append(expiredMatch, roa)
			continue
		}

		coveringAny = append(coveringAny, roa)
		lenOK := roa.lengthValid(ones)
		asOK := roa.OriginAS == originAS

		if lenOK {
			authorizedAS = appendAS(authorizedAS, roa.OriginAS)
		}

		switch {
		case lenOK && asOK:
			base.Allowed = true
			base.Status = RPKIStatusValid
			base.Code = AdmitOK
			base.MatchedROA = roa
			base.Detail = fmt.Sprintf("ROA %s AS%d authorizes /%d within %d..%d",
				roa.Prefix.String(), roa.OriginAS, ones, roa.effectiveMin(), roa.MaxLength)
			return base
		case lenOK && !asOK:
			lengthOKWrongAS = append(lengthOKWrongAS, roa)
		case asOK && !lenOK:
			sameASBadLen = append(sameASBadLen, roa)
		}
	}

	base.AuthorizedAS = authorizedAS

	switch {
	case len(lengthOKWrongAS) > 0:
		base.Status = RPKIStatusInvalid
		base.Code = AdmitOriginMismatch
		as := make([]uint32, 0, len(lengthOKWrongAS))
		for _, r := range lengthOKWrongAS {
			as = appendAS(as, r.OriginAS)
		}
		base.AuthorizedAS = as
		base.Detail = fmt.Sprintf("AS%d is not authorized for %s; valid ROA origin(s): %v", originAS, prefix.String(), as)
	case len(sameASBadLen) > 0:
		base.Status = RPKIStatusInvalid
		roa := sameASBadLen[0]
		if ones < roa.effectiveMin() {
			base.Code = AdmitPrefixTooWide
			base.Detail = fmt.Sprintf("/%d is wider than minLen /%d (ROA %s AS%d, max /%d)",
				ones, roa.effectiveMin(), roa.Prefix.String(), roa.OriginAS, roa.MaxLength)
		} else {
			base.Code = AdmitPrefixTooLong
			base.Detail = fmt.Sprintf("/%d exceeds maxLength /%d (ROA %s AS%d)",
				ones, roa.MaxLength, roa.Prefix.String(), roa.OriginAS)
		}
	case len(expiredMatch) > 0:
		base.Status = RPKIStatusInvalid
		base.Code = AdmitROAExpired
		base.Detail = "all covering ROA(s) are outside their validity window"
	case len(coveringAny) > 0:
		base.Status = RPKIStatusInvalid
		base.Code = AdmitNotFound
		base.Detail = "covering ROA exists but no length/AS combination applies"
	default:
		base.Status = RPKIStatusNotFound
		base.Code = AdmitNotFound
		base.Detail = fmt.Sprintf("no ROA covers %s originated by AS%d", prefix.String(), originAS)
	}
	return base
}

func appendAS(in []uint32, as uint32) []uint32 {
	for _, x := range in {
		if x == as {
			return in
		}
	}
	return append(in, as)
}

// AdmitAll runs Admit for every rule and returns decisions aligned to indices.
func AdmitAll(rules []*Rule, roas []ROA, now time.Time) []AdmissionDecision {
	out := make([]AdmissionDecision, len(rules))
	for i, r := range rules {
		out[i] = Admit(r, roas, now)
	}
	return out
}
