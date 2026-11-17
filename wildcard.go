package netsec

import (
	"errors"
	"strings"
)

// RRSIG carries the subset of an RRSIG resource record (RFC 4034 section 3)
// needed to decide whether the covered RRset is the product of wildcard
// expansion.
type RRSIG struct {
	// TypeCovered is the RR type that this RRSIG covers.
	TypeCovered uint16
	// Labels is the RRSIG Labels field: the number of labels in the
	// original signed owner name, not counting the root label and not
	// counting the "*" label of a wildcard name.
	Labels uint8
	// OriginalTTL is the RRSIG Original TTL field.
	OriginalTTL uint32
	// SignatureExpiration and SignatureInception are absolute Unix-style
	// inception/expiration times as stored in the RRSIG.
	SignatureExpiration uint32
	SignatureInception  uint32
	// KeyTag identifies the DNSKEY that generated the signature.
	KeyTag uint16
	// SignerName is the signer field of the RRSIG (the zone apex name).
	SignerName string
	// Signature holds the raw cryptographic signature bytes.
	Signature []byte
}

// NSEC is the subset of an NSEC resource record (RFC 4034 section 4) needed
// to validate a wildcard answer.
type NSEC struct {
	// OwnerName is the owner name of the NSEC RR, in absolute text form.
	OwnerName string
	// NextDomainName is the Next Domain Name field of the NSEC RR, in
	// absolute text form.
	NextDomainName string
}

// NSECProof bundles the two authenticated NSEC records that RFC 4035
// section 5.3.4 demands for a wildcard answer:
//
//   - ClosestEncloser must be owned by the closest encloser, proving that
//     the closest encloser exists.
//   - NextCloser must cover (in NSEC interval terms) the next closer name,
//     proving that no name closer than the wildcard exists.
//
// A single NSEC RR commonly fulfils both roles, in which case the same
// NSEC value may be assigned to both fields. The NSEC RRsets themselves are
// assumed to have already been cryptographically validated by the caller.
type NSECProof struct {
	ClosestEncloser *NSEC
	NextCloser      *NSEC
}

// WildcardStatus classifies an RRSIG against its owner name.
type WildcardStatus int

const (
	// RRSIGExact means Labels equals the number of owner-name labels: the
	// RRset was signed under the owner name itself, no wildcard expansion.
	RRSIGExact WildcardStatus = iota
	// RRSIGWildcardExpanded means Labels is smaller than the number of
	// owner-name labels: the answer was synthesized from a wildcard and the
	// signature must be verified against the reconstructed wildcard name.
	RRSIGWildcardExpanded
	// RRSIGForged means Labels is larger than the number of owner-name
	// labels, which is impossible for a legitimately generated signature.
	RRSIGForged
)

// WildcardResult is the verdict produced by AnalyzeRRSIG.
//
// For an exact match only Status and OwnerLabels are meaningful. For a
// wildcard expansion the reconstruction and NSEC-proof fields are populated
// so the caller can verify the signature against SignedName and use
// NextCloserName/ClosestEncloserName when checking the NSEC chain.
type WildcardResult struct {
	// Status is the classification of the RRSIG Labels field.
	Status WildcardStatus
	// OwnerLabels is the number of labels in the owner name, not counting
	// the root label (and counting a literal "*" label like any other).
	OwnerLabels int
	// ClosestEncloserName is the nearest ancestor of the owner name that
	// exists, in absolute lowercase text form (e.g. "example.").
	ClosestEncloserName string
	// NextCloserName is the name one label closer to the owner than the
	// closest encloser, whose non-existence the NextCloser NSEC must prove
	// (e.g. "host.example." for owner "a.b.host.example.").
	NextCloserName string
	// SignedName is the wildcard name that must be reconstructed and used
	// as the signed owner name when verifying the RRSIG, i.e.
	// "*." + ClosestEncloserName (e.g. "*.example.").
	SignedName string
}

// Sentinel errors returned by AnalyzeRRSIG.
var (
	// ErrInvalidName indicates a malformed absolute domain name.
	ErrInvalidName = errors.New("netsec: invalid domain name")
	// ErrLabelsOutOfRange indicates that RRSIG Labels exceeds the number of
	// owner-name labels, which makes the signature a forgery.
	ErrLabelsOutOfRange = errors.New("netsec: RRSIG labels greater than owner name label count")
	// ErrWildcardOwnerOnExact indicates that Labels equals the owner label
	// count while the owner name itself starts with "*", which is illegal:
	// a wildcard owner must carry the "*" label inside its RRSIG Labels.
	ErrWildcardOwnerOnExact = errors.New("netsec: exact-label RRSIG owner name must not start with '*'")
	// ErrMissingNSECProof indicates that a wildcard-expanded answer was not
	// accompanied by the required NSEC closest-encloser / next-closer proof.
	ErrMissingNSECProof = errors.New("netsec: wildcard expansion requires an NSEC proof")
	// ErrNSECNoClosestEncloser indicates that no supplied NSEC is owned by
	// the closest encloser.
	ErrNSECNoClosestEncloser = errors.New("netsec: NSEC proof missing closest encloser record")
	// ErrNSECNoNextCloser indicates that no supplied NSEC covers the next
	// closer name.
	ErrNSECNoNextCloser = errors.New("netsec: NSEC proof does not cover next closer name")
)

// AnalyzeRRSIG determines whether the RRSIG over ownerName represents a
// wildcard expansion and, when it does, validates the accompanying NSEC
// proof and reconstructs the name under which the signature must be
// verified.
//
// The rules implemented are:
//
//  1. If sig.Labels is greater than the owner name's label count (root label
//     not counted), the RRSIG is forged and ErrLabelsOutOfRange is returned.
//  2. If sig.Labels equals the owner label count, the owner name must not
//     start with "*"; otherwise ErrWildcardOwnerOnExact is returned. The
//     signature is verified against the owner name directly.
//  3. If sig.Labels is smaller than the owner label count, the answer is a
//     wildcard expansion. The signed name is reconstructed as
//     "*.<closest encloser>", the closest encloser being the sig.Labels
//     rightmost labels of the owner. proof must contain an NSEC owned by the
//     closest encloser and an NSEC that covers the next closer name; the
//     caller then verifies the RRSIG against the reconstructed SignedName.
//
// Names must be supplied in absolute (root-terminated) text form.
func AnalyzeRRSIG(ownerName string, sig *RRSIG, proof *NSECProof) (*WildcardResult, error) {
	if sig == nil {
		return nil, errors.New("netsec: nil RRSIG")
	}
	ownerLabels := splitLabels(ownerName)
	if ownerLabels == nil {
		return nil, ErrInvalidName
	}

	result := &WildcardResult{
		OwnerLabels: len(ownerLabels),
	}

	switch {
	case int(sig.Labels) > len(ownerLabels):
		result.Status = RRSIGForged
		return result, ErrLabelsOutOfRange

	case int(sig.Labels) == len(ownerLabels):
		result.Status = RRSIGExact
		if ownerLabels[0] == "*" {
			return result, ErrWildcardOwnerOnExact
		}
		return result, nil

	default:
		result.Status = RRSIGWildcardExpanded
	}

	// Wildcard expansion: the closest encloser is the sig.Labels rightmost
	// labels of the owner name; the wildcard sits one label above it.
	encloserLabels := ownerLabels[len(ownerLabels)-int(sig.Labels):]
	nextCloserLabels := ownerLabels[len(ownerLabels)-int(sig.Labels)-1:]

	result.ClosestEncloserName = joinName(encloserLabels)
	result.NextCloserName = joinName(nextCloserLabels)
	result.SignedName = joinName(append([]string{"*"}, encloserLabels...))

	if proof == nil {
		return result, ErrMissingNSECProof
	}
	if proof.ClosestEncloser == nil || proof.NextCloser == nil {
		return result, ErrMissingNSECProof
	}
	if splitLabels(proof.ClosestEncloser.OwnerName) == nil ||
		splitLabels(proof.ClosestEncloser.NextDomainName) == nil ||
		splitLabels(proof.NextCloser.OwnerName) == nil ||
		splitLabels(proof.NextCloser.NextDomainName) == nil {
		return result, ErrInvalidName
	}

	if !nameEquals(proof.ClosestEncloser.OwnerName, result.ClosestEncloserName) {
		return result, ErrNSECNoClosestEncloser
	}
	if !CanonicallyCovers(proof.NextCloser, result.NextCloserName) {
		return result, ErrNSECNoNextCloser
	}

	return result, nil
}

// LabelCount returns the number of labels in an absolute domain name, not
// counting the root label. A literal "*" label is counted like any other
// label. It returns -1 for a malformed name.
func LabelCount(name string) int {
	labels := splitLabels(name)
	if labels == nil {
		return -1
	}
	return len(labels)
}

// CanonicallyCovers reports whether nsec proves the non-existence of name
// per RFC 4034 section 4.1.1: name must fall strictly inside the NSEC interval
// (owner name, next domain name) in canonical DNS name order, including the
// wrap-around case where the next domain name sorts before the owner name at
// the zone apex.
func CanonicallyCovers(nsec *NSEC, name string) bool {
	if nsec == nil {
		return false
	}
	owner := splitLabels(nsec.OwnerName)
	next := splitLabels(nsec.NextDomainName)
	target := splitLabels(name)
	if owner == nil || next == nil || target == nil {
		return false
	}

	// The interval is open: owner and next names themselves exist and are
	// therefore never covered.
	if equalCanonical(target, owner) || equalCanonical(target, next) {
		return false
	}
	if canonicalLess(next, owner) {
		// Wrap-around NSEC at the zone apex: the interval spans from owner
		// to the end of the name space and from the start up to next.
		return canonicalLess(owner, target) || canonicalLess(target, next)
	}
	return canonicalLess(owner, target) && canonicalLess(target, next)
}

// splitLabels breaks an absolute domain name into lowercase labels without
// the root label. It returns nil for malformed names (non-absolute names,
// empty labels, or a label longer than 63 octets).
func splitLabels(name string) []string {
	if name == "" || !strings.HasSuffix(name, ".") {
		return nil
	}
	body := name[:len(name)-1]
	if body == "" {
		return []string{}
	}
	parts := strings.Split(body, ".")
	labels := make([]string, len(parts))
	for i, part := range parts {
		if part == "" || len(part) > 63 {
			return nil
		}
		labels[i] = strings.ToLower(part)
	}
	return labels
}

// joinName renders labels back into an absolute, lowercase text name.
func joinName(labels []string) string {
	if len(labels) == 0 {
		return "."
	}
	return strings.Join(labels, ".") + "."
}

// nameEquals compares two absolute domain names case-insensitively.
func nameEquals(a, b string) bool {
	la := splitLabels(a)
	lb := splitLabels(b)
	if la == nil || lb == nil || len(la) != len(lb) {
		return false
	}
	for i := range la {
		if la[i] != lb[i] {
			return false
		}
	}
	return true
}

// canonicalLess implements the canonical DNS name ordering of RFC 4034
// section 6.1: labels are compared from the root upward, label octets are
// compared case-insensitively, and a name sorts before any name that it is a
// proper prefix of.
func canonicalLess(a, b []string) bool {
	i, j := len(a)-1, len(b)-1
	for i >= 0 && j >= 0 {
		switch {
		case a[i] < b[j]:
			return true
		case a[i] > b[j]:
			return false
		}
		i--
		j--
	}
	// The shorter name (ran out of labels first) sorts earlier.
	return i < j
}

// equalCanonical reports whether two already-split names are identical in
// canonical form.
func equalCanonical(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
