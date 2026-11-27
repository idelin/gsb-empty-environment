package dnssec

import "time"

// Validator carries the resolver-side validation configuration.
type Validator struct {
	// Now is the validation instant. The zero value means time.Now.
	Now time.Time
	// Skew widens the RRSIG validity window on both sides.
	Skew time.Duration
}

// DefaultSkew is applied when Validator.Skew is zero. RFC 4035 allows a
// small clock-skew tolerance; 300 seconds is a common resolver default.
const DefaultSkew = 5 * time.Minute

func (v *Validator) now() time.Time {
	if v.Now.IsZero() {
		return time.Now()
	}
	return v.Now
}

func (v *Validator) skew() time.Duration {
	if v.Skew < 0 {
		return 0
	}
	if v.Skew == 0 {
		return DefaultSkew
	}
	return v.Skew
}

// RRSetResult reports the outcome of verifying one RRset.
type RRSetResult struct {
	Status       Status
	WildcardName Name
	UsedRRSIG    *RRSIG
	Reason       string
}

// VerifyRRSet cryptographically validates rrset against its RRSIG records
// using the trusted zone DNSKEYs. It performs:
//
//  1. canonical ordering/de-duplication of the RRset (RFC 4034 section 6),
//  2. signer-name, labels and owner consistency,
//  3. RFC 1982 inception/expiration window checking,
//  4. ECDSA signature verification with the matching ZONE key.
//
// At least one RRSIG must validate. A wildcard-expanded answer is detected
// (Labels < owner label count) and the source wildcard name is returned; the
// caller MUST additionally obtain a denial-of-existence proof showing that no
// more specific name blocks the wildcard (see VerifyDenial).
func (v *Validator) VerifyRRSet(rrset, rrsigs, zoneKeys []RR) (*RRSetResult, error) {
	if len(rrset) == 0 {
		return nil, ErrBogus("empty RRset cannot be verified")
	}
	owner := rrset[0].Name
	typ := rrset[0].Type
	class := rrset[0].Class
	for _, rr := range rrset {
		if !rr.Name.Equal(owner) || rr.Type != typ || rr.Class != class {
			return nil, ErrBogus("RRset mixes owner name, type or class")
		}
	}

	now := nowUnixSeconds(v.now())
	skew := v.skew()

	var lastErr error
	for _, sigRR := range rrsigs {
		sig, err := ParseRRSIG(sigRR.Rdata)
		if err != nil {
			lastErr = ErrBogus("malformed RRSIG: " + err.Error())
			continue
		}
		if sig.TypeCovered != typ {
			continue
		}
		if !owner.IsSubdomain(sig.Signer) {
			lastErr = ErrBogus("RRSIG signer is not an ancestor of the owner name")
			continue
		}
		if int(sig.Labels) > owner.LabelCount() {
			lastErr = ErrBogus("RRSIG labels exceed owner name label count")
			continue
		}
		if !InValidityWindow(sig.Inception, sig.Expiration, now, skew) {
			lastErr = ErrBogus("current time is outside RRSIG validity window")
			continue
		}
		key, err := FindMatchingDNSKEY(zoneKeys, sig.KeyTag)
		if err != nil {
			lastErr = err
			continue
		}
		canonical, err := CanonicalRRSet(rrset, sig.OrigTTL)
		if err != nil {
			lastErr = err
			continue
		}
		if err := VerifySignature(key.Algorithm, key.PublicKey, SignedData(sig, canonical), sig.Signature); err != nil {
			lastErr = err
			continue
		}

		res := &RRSetResult{Status: StatusSecure, UsedRRSIG: sig}
		if int(sig.Labels) < owner.LabelCount() {
			// Wildcard expansion: the rightmost sig.Labels labels form the
			// suffix and the wildcard is "*." + that suffix.
			suffix, err := owner.TrimToLabels(int(sig.Labels))
			if err != nil {
				return nil, ErrBogus("RRSIG labels field is inconsistent with owner name")
			}
			wild, err := suffix.PrefixLabel("*")
			if err != nil {
				return nil, err
			}
			res.WildcardName = wild
		}
		return res, nil
	}
	if lastErr == nil {
		lastErr = ErrBogus("no RRSIG covers the RRset")
	}
	return &RRSetResult{Status: StatusBogus, Reason: lastErr.Error()}, lastErr
}

// VerifyRRSetWithWildcard combines RRset verification with the RFC 4035
// section 5.3 wildcard rule. When the signature indicates wildcard synthesis,
// proof is consulted and must show that qname itself does not exist (an NSEC
// match/coverage or NSEC3 coverage of qname); otherwise the answer is Bogus.
func (v *Validator) VerifyRRSetWithWildcard(rrset, rrsigs, zoneKeys []RR, qname Name, proof *DenialResult) (*RRSetResult, error) {
	res, err := v.VerifyRRSet(rrset, rrsigs, zoneKeys)
	if err != nil {
		return res, err
	}
	if res.WildcardName.IsRoot() {
		return res, nil
	}
	if proof == nil || proof.Status == StatusUnknown {
		return nil, ErrBogus("wildcard answer without proof that the exact query name does not exist")
	}
	if proof.Status == StatusBogus {
		return nil, ErrBogus("wildcard answer backed by a bogus denial proof")
	}
	if !proof.CoversName(qname) {
		return nil, ErrBogus("wildcard answer: no NSEC/NSEC3 proof covers the exact query name")
	}
	return res, nil
}

// VerifyAllRRSIGs validates every RRSIG covering the given RRset owner/type
// and fails Bogus if any of them cannot be validated. Denial proofs require
// this stricter treatment because every supplied NSEC/NSEC3 RRSIG is part of
// the authenticated evidence.
func (v *Validator) VerifyAllRRSIGs(rrset, rrsigs, zoneKeys []RR) error {
	if len(rrsigs) == 0 {
		return ErrBogus("denial proof RRset has no RRSIG")
	}
	if _, err := v.VerifyRRSet(rrset, rrsigs, zoneKeys); err != nil {
		return err
	}
	for _, sigRR := range rrsigs {
		sig, perr := ParseRRSIG(sigRR.Rdata)
		if perr != nil {
			return ErrBogus("malformed RRSIG: " + perr.Error())
		}
		key, kerr := FindMatchingDNSKEY(zoneKeys, sig.KeyTag)
		if kerr != nil {
			return kerr
		}
		canonical, cerr := CanonicalRRSet(rrset, sig.OrigTTL)
		if cerr != nil {
			return cerr
		}
		if err := VerifySignature(key.Algorithm, key.PublicKey, SignedData(sig, canonical), sig.Signature); err != nil {
			return err
		}
	}
	return nil
}
