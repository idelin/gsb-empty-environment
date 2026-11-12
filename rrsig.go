package netsec

import "errors"

// Algorithm numbers used by DNSSEC (RFC 4034, Appendix A.1 and later RFCs).
const (
	AlgRSASHA1   = 5
	AlgRSASHA256 = 8
	AlgRSASHA512 = 10
	AlgECDSAP256 = 13
	AlgECDSAP384 = 14
	AlgEd25519   = 15
)

// RRSIG carries the fields of an RRSIG RDATA that are relevant to signature
// validation and wildcard-expansion classification. It deliberately mirrors
// the on-the-wire field names from RFC 4034, section 3.
type RRSIG struct {
	// TypeCovered is the RR type whose RRset is signed.
	TypeCovered uint16
	// Algorithm is the DNSKEY algorithm number.
	Algorithm uint8
	// Labels is the RRSIG "Labels" field: the label count of the original
	// signer owner name, counting a leading wildcard label.
	Labels uint8
	// OriginalTTL is the TTL of the covered RRset as it appears in the zone.
	OriginalTTL uint32
	// SigExpiration / SigInception are seconds since the UNIX epoch.
	SigExpiration uint32
	SigInception  uint32
	// KeyTag identifies the DNSKEY that produced the signature.
	KeyTag uint16
	// SignerName is the zone apex / signer owner name.
	SignerName Name
	// Signature is the raw cryptographic signature octets.
	Signature []byte
}

// Classification is the verdict of inspecting an RRSIG's Labels field against
// the owner name of the covered RRset.
type Classification struct {
	// Wildcard is true exactly when the RRSIG proves the answer was produced
	// by wildcard expansion rather than a concrete owner name.
	Wildcard bool
	// OwnerLabels is the owner label count used for the comparison: root is
	// excluded and a leading "*" label is excluded.
	OwnerLabels int
	// SigLabels is the RRSIG Labels field as an int.
	SigLabels int
	// ClosestEncloser is the nearest enclosing name from which the wildcard
	// was expanded. It is zero for the non-wildcard case.
	ClosestEncloser Name
	// SignedName is the owner name that must actually be verified. For
	// wildcard expansion it is "*.<ClosestEncloser>"; otherwise it is the
	// RRset owner name itself.
	SignedName Name
}

// Sentinel errors returned by Classify. They can be matched with errors.Is.
var (
	// ErrLabelsExceedOwner means the RRSIG Labels field names more labels than
	// the owner name has, i.e. the signature cannot possibly cover this name
	// and must be treated as a forgery.
	ErrLabelsExceedOwner = errors.New("netsec: RRSIG labels exceed owner label count (forged)")
	// ErrWildcardOnExactName means Labels equals the owner count while the
	// owner name literally begins with "*", an impossible exact-match form.
	ErrWildcardOnExactName = errors.New("netsec: equal labels but owner name starts with '*'")
)

// Classify applies the wildcard-expansion rule to an RRSIG covering an RRset
// whose owner name is owner.
//
// Label counts exclude the root label and, for the owner name, exclude a
// leading literal "*" label. The three outcomes are:
//
//   - sigLabels >  ownerLabels: forgery, ErrLabelsExceedOwner.
//   - sigLabels == ownerLabels: exact match; owner must not start with "*".
//   - sigLabels <  ownerLabels: wildcard expansion; the signed name is rebuilt
//     as "*.<closest encloser>" and the caller must additionally hold an NSEC
//     proof that the next-closer name does not exist.
func Classify(owner Name, sigLabels uint8) (Classification, error) {
	ownerLabels := owner.EffectiveLabelCount()
	sigLabelsInt := int(sigLabels)

	c := Classification{
		OwnerLabels: ownerLabels,
		SigLabels:   sigLabelsInt,
	}

	switch {
	case sigLabelsInt > ownerLabels:
		return Classification{}, ErrLabelsExceedOwner

	case sigLabelsInt == ownerLabels:
		if owner.IsWildcard() {
			return Classification{}, ErrWildcardOnExactName
		}
		c.Wildcard = false
		c.ClosestEncloser = Name{}
		c.SignedName = Name{Labels: append([]string(nil), owner.Labels...)}
		return c, nil

	default:
		// sigLabels < ownerLabels: wildcard expansion.
		//
		// The RRSIG Labels field counts the leading "*" of the wildcard owner,
		// so the closest encloser has sigLabels-1 labels.
		encloserLabels := sigLabelsInt - 1
		if encloserLabels < 0 {
			// Labels == 0 cannot cover any name below the root.
			return Classification{}, ErrLabelsExceedOwner
		}
		encloser, err := owner.ClosestEncloser(encloserLabels)
		if err != nil {
			return Classification{}, err
		}
		c.Wildcard = true
		c.ClosestEncloser = encloser
		c.SignedName = encloser.PrependWildcard()
		return c, nil
	}
}
