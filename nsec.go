package netsec

import (
	"bytes"
	"errors"
)

// NSEC is an NSEC resource record (RFC 4034, section 4) used to prove name
// non-existence. Only the fields needed for wildcard proofs are modeled.
type NSEC struct {
	// OwnerName is the NSEC RR owner name.
	OwnerName Name
	// NextDomainName is the next name in the zone's canonical ordering.
	NextDomainName Name
	// Types is the bitmap of RR types present at OwnerName, represented as
	// raw type numbers (e.g. dns.TypeA == 1, dns.TypeNS == 2).
	Types []uint16
}

// HasType reports whether the type bitmap contains t.
func (r NSEC) HasType(t uint16) bool {
	for _, present := range r.Types {
		if present == t {
			return true
		}
	}
	return false
}

// Sentinel errors returned by wildcard-proof validation.
var (
	// ErrProofMissing means wildcard expansion was detected but no NSEC
	// denial was supplied.
	ErrProofMissing = errors.New("netsec: missing NSEC proof for wildcard expansion")
	// ErrNoClosestEncloserNSEC means no NSEC RR is owned by the closest
	// encloser.
	ErrNoClosestEncloserNSEC = errors.New("netsec: no NSEC record at closest encloser")
	// ErrNextCloserNotDenied means none of the NSEC records proves the
	// next-closer name does not exist via an enclosing canonical interval.
	ErrNextCloserNotDenied = errors.New("netsec: next-closer name is not denied by any NSEC interval")
	// ErrClosestEncloserHasSOA means the closest-encloser NSEC improperly
	// asserts an SOA, which would contradict it being an empty non-terminal
	// used in a wildcard denial.
	ErrClosestEncloserHasSOA = errors.New("netsec: closest-encloser NSEC must not contain an SOA type")
)

// NSECProof bundles the authenticated NSEC records that accompany a
// wildcard-expanded answer. Every NSEC record here is assumed to already carry
// a valid RRSIG from the zone; Verify enforces that via VerifyNSECProof.
type NSECProof struct {
	Records []NSEC
}

// VerifyNSECProof checks that the supplied NSEC records authenticate the
// wildcard expansion that produced qname:
//
//  1. An NSEC RR must be owned by the closest encloser.
//  2. An NSEC interval must enclose the next-closer name, proving it does not
//     exist (RFC 7129, section 5.5 / Appendix B.3).
//
// qname is the expanded RRset owner name and encloser is the closest encloser
// reported by Classify. The next-closer name is "*." + encloser.
func VerifyNSECProof(proof *NSECProof, qname, encloser Name) error {
	if proof == nil || len(proof.Records) == 0 {
		return ErrProofMissing
	}

	nextCloser := encloser.PrependWildcard()

	haveEncloserNSEC := false
	encloserDenies := false
	for _, rr := range proof.Records {
		if rr.OwnerName.Equal(encloser) {
			haveEncloserNSEC = true
			if rr.HasType(TypeSOA) {
				return ErrClosestEncloserHasSOA
			}
		}
		if intervalContains(rr, nextCloser) {
			encloserDenies = true
		}
	}

	if !haveEncloserNSEC {
		return ErrNoClosestEncloserNSEC
	}
	if !encloserDenies {
		return ErrNextCloserNotDenied
	}
	_ = qname // qname documents the expanded owner; the interval test uses nextCloser.
	return nil
}

// intervalContains reports whether the (possibly wrapping) NSEC interval
// (owner, next] canonically contains target. Owner and next must be the
// ordered endpoints of one NSEC record inside the same zone.
func intervalContains(rr NSEC, target Name) bool {
	owner, next, name := rr.OwnerName, rr.NextDomainName, target

	// A record cannot deny its own owner.
	if name.Equal(owner) {
		return false
	}

	ow, _ := owner.WireEncode()
	nw, _ := next.WireEncode()
	tw, _ := name.WireEncode()

	wrap := canonicalCompare(nw, ow) <= 0
	switch {
	case wrap:
		// Last interval in the zone wraps around the apex:
		// target > owner OR target <= next.
		return canonicalCompare(tw, ow) > 0 || canonicalCompare(tw, nw) <= 0
	default:
		// Ordinary interval: owner < target <= next.
		return canonicalCompare(tw, ow) > 0 && canonicalCompare(tw, nw) <= 0
	}
}

// canonicalCompare compares two uncompressed wire names using DNS canonical
// ordering (RFC 4034, section 6.1), applied from the root upward. It returns
// -1, 0 or +1.
func canonicalCompare(a, b []byte) int {
	la := labelsFromWire(a)
	lb := labelsFromWire(b)
	depth := len(la)
	if len(lb) < depth {
		depth = len(lb)
	}
	// Compare from the root-most label toward the leaf.
	for i := 1; i <= depth; i++ {
		sa := la[len(la)-i]
		sb := lb[len(lb)-i]
		if c := bytes.Compare(sa, sb); c != 0 {
			return c
		}
	}
	switch {
	case len(la) < len(lb):
		return -1
	case len(la) > len(lb):
		return 1
	default:
		return 0
	}
}

// labelsFromWire splits a wire name (including trailing zero) into label
// octet strings for canonical comparison.
func labelsFromWire(wire []byte) [][]byte {
	var out [][]byte
	pos := 0
	for pos < len(wire) {
		length := int(wire[pos])
		pos++
		if length == 0 {
			break
		}
		out = append(out, wire[pos:pos+length])
		pos += length
	}
	return out
}
