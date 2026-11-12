package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"time"
)

// RR is a single resource record sufficient for canonical RRSIG
// construction. For the types with dedicated helpers (A/AAAA/TXT) RDATA may be
// left nil; otherwise RDATA must already be the canonical (lower-cased)
// wire-format RDATA.
type RR struct {
	Name  Name
	Type  uint16
	Class uint16
	TTL   uint32
	RDATA []byte
	A     [4]byte  // used when Type == TypeA
	AAAA  [16]byte // used when Type == TypeAAAA
	TXT   [][]byte // used when Type == TypeTXT
}

// Common DNS class/type constants.
const (
	ClassIN = uint16(1)

	TypeA      = uint16(1)
	TypeNS     = uint16(2)
	TypeCNAME  = uint16(5)
	TypeSOA    = uint16(6)
	TypeTXT    = uint16(16)
	TypeAAAA   = uint16(28)
	TypeRRSIG  = uint16(46)
	TypeNSEC   = uint16(47)
	TypeDNSKEY = uint16(48)
)

// SignedRRset is an RRset together with the RRSIG that claims to cover it and
// the authenticated NSEC denial carried alongside a wildcard-expanded answer.
type SignedRRset struct {
	RRs       []RR
	Signature RRSIG
	// Proof is required (and validated) when Signature expands a wildcard;
	// it must be nil for an exact-match signature.
	Proof *NSECProof
	// Now is the validation time. The zero value means time.Now.
	Now time.Time
}

// Verifier resolves the DNSKEY referenced by an RRSIG. Implementations are
// expected to return only keys that are themselves authenticated by a valid
// DNSKEY RRset chain.
type Verifier interface {
	LookupKey(signer Name, keyTag uint16, algorithm uint8) (DNSKEY, bool)
}

// VerifierFunc adapts a function to the Verifier interface.
type VerifierFunc func(signer Name, keyTag uint16, algorithm uint8) (DNSKEY, bool)

// LookupKey implements Verifier.
func (f VerifierFunc) LookupKey(signer Name, keyTag uint16, algorithm uint8) (DNSKEY, bool) {
	return f(signer, keyTag, algorithm)
}

// Result reports the verified interpretation of an RRSIG.
type Result struct {
	Classification Classification
	// NextCloser is the denied wildcard name "*.<closest encloser>" for a
	// wildcard expansion; zero otherwise.
	NextCloser Name
}

// Sentinel verification errors.
var (
	ErrEmptyRRset           = errors.New("netsec: cannot verify an empty RRset")
	ErrInconsistentRRset    = errors.New("netsec: RRset RRs differ in name/type/class")
	ErrTypeNotCovered       = errors.New("netsec: RRSIG type covered does not match RRset type")
	ErrSignerNotApex        = errors.New("netsec: signer name does not enclose the owner name")
	ErrKeyNotFound          = errors.New("netsec: matching DNSKEY not found")
	ErrKeyAlgorithm         = errors.New("netsec: DNSKEY algorithm does not match RRSIG")
	ErrNotZoneKey           = errors.New("netsec: DNSKEY is not a zone key")
	ErrSignatureNotValidYet = errors.New("netsec: RRSIG inception is in the future")
	ErrSignatureExpired     = errors.New("netsec: RRSIG expiration is in the past")
	ErrUnexpectedProof      = errors.New("netsec: NSEC proof supplied for a non-wildcard signature")
)

// Verify performs wildcard classification, NSEC denial enforcement and
// cryptographic signature validation for rrset.
func Verify(v Verifier, rrset SignedRRset) (Result, error) {
	if len(rrset.RRs) == 0 {
		return Result{}, ErrEmptyRRset
	}

	owner := rrset.RRs[0].Name
	rrType := rrset.RRs[0].Type
	rrClass := rrset.RRs[0].Class
	for _, rr := range rrset.RRs {
		if !rr.Name.Equal(owner) || rr.Type != rrType || rr.Class != rrClass {
			return Result{}, ErrInconsistentRRset
		}
	}

	if rrset.Signature.TypeCovered != rrType {
		return Result{}, ErrTypeNotCovered
	}
	if !owner.IsSubDomain(rrset.Signature.SignerName) {
		return Result{}, ErrSignerNotApex
	}

	cls, err := Classify(owner, rrset.Signature.Labels)
	if err != nil {
		return Result{}, err
	}

	res := Result{Classification: cls}

	if cls.Wildcard {
		if err := VerifyNSECProof(rrset.Proof, owner, cls.ClosestEncloser); err != nil {
			return Result{}, err
		}
		res.NextCloser = cls.ClosestEncloser.PrependWildcard()
	} else if rrset.Proof != nil {
		return Result{}, ErrUnexpectedProof
	}

	if err := checkValidity(rrset.Signature, rrset.Now); err != nil {
		return Result{}, err
	}

	key, ok := v.LookupKey(rrset.Signature.SignerName, rrset.Signature.KeyTag, rrset.Signature.Algorithm)
	if !ok {
		return Result{}, ErrKeyNotFound
	}
	if key.Algorithm != rrset.Signature.Algorithm {
		return Result{}, ErrKeyAlgorithm
	}
	if key.Flags&FlagZoneKey == 0 {
		return Result{}, ErrNotZoneKey
	}

	if err := verifyCryptographic(key, rrset, cls.SignedName); err != nil {
		return Result{}, err
	}
	return res, nil
}

func checkValidity(sig RRSIG, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	cur := uint32(now.Unix())
	if cur < sig.SigInception {
		return ErrSignatureNotValidYet
	}
	if cur > sig.SigExpiration {
		return ErrSignatureExpired
	}
	return nil
}

// verifyCryptographic rebuilds the signed RRSIG RDATA using the rebuilt signed
// owner name (the wildcard name for an expansion) and verifies the signature.
func verifyCryptographic(key DNSKEY, rrset SignedRRset, signedName Name) error {
	signed, err := buildSignedData(rrset, signedName)
	if err != nil {
		return err
	}

	alg := rrset.Signature.Algorithm
	var digest []byte
	if alg == AlgEd25519 {
		digest = signed
	} else {
		_, newHash, err := hashForAlgorithm(alg)
		if err != nil {
			return err
		}
		h := newHash()
		h.Write(signed)
		digest = h.Sum(nil)
	}
	return verifySignature(alg, key.PublicKey, digest, rrset.Signature.Signature)
}

// buildSignedData constructs the RFC 4034, section 3.1.8.1 signed sequence:
// RRSIG RDATA (minus Signature) followed by the canonical RRset, using the
// rebuilt signed owner name in place of the expanded owner.
func buildSignedData(rrset SignedRRset, signedName Name) ([]byte, error) {
	sig := rrset.Signature
	signerWire, err := sig.SignerName.WireEncode()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	var header [18]byte
	binary.BigEndian.PutUint16(header[0:], sig.TypeCovered)
	header[2] = sig.Algorithm
	header[3] = sig.Labels
	binary.BigEndian.PutUint32(header[4:], sig.OriginalTTL)
	binary.BigEndian.PutUint32(header[8:], sig.SigExpiration)
	binary.BigEndian.PutUint32(header[12:], sig.SigInception)
	binary.BigEndian.PutUint16(header[16:], sig.KeyTag)
	buf.Write(header[:])
	buf.Write(signerWire)

	canonical, err := canonicalRRset(rrset.RRs, signedName, sig.OriginalTTL)
	if err != nil {
		return nil, err
	}
	buf.Write(canonical)
	return buf.Bytes(), nil
}

// canonicalRRset sorts and encodes the covered RRs in canonical form
// (RFC 4034, section 6.2), substituting signedName and the original TTL.
func canonicalRRset(rrs []RR, signedName Name, originalTTL uint32) ([]byte, error) {
	encoded := make([][]byte, len(rrs))
	ownerWire, err := signedName.WireEncode()
	if err != nil {
		return nil, err
	}
	for i, rr := range rrs {
		rdata, err := canonicalRDATA(rr)
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		b.Write(ownerWire)
		var fixed [10]byte
		binary.BigEndian.PutUint16(fixed[0:], rr.Type)
		binary.BigEndian.PutUint16(fixed[2:], rr.Class)
		binary.BigEndian.PutUint32(fixed[4:], originalTTL)
		binary.BigEndian.PutUint16(fixed[8:], uint16(len(rdata)))
		b.Write(fixed[:])
		b.Write(rdata)
		encoded[i] = b.Bytes()
	}
	sort.Slice(encoded, func(i, j int) bool {
		return bytes.Compare(encoded[i], encoded[j]) < 0
	})
	var out bytes.Buffer
	for _, e := range encoded {
		out.Write(e)
	}
	return out.Bytes(), nil
}

// canonicalRDATA returns canonical wire RDATA. Well-known types are encoded
// from their typed fields; everything else uses the supplied RDATA verbatim.
func canonicalRDATA(rr RR) ([]byte, error) {
	switch rr.Type {
	case TypeA:
		out := make([]byte, 4)
		copy(out, rr.A[:])
		return out, nil
	case TypeAAAA:
		out := make([]byte, 16)
		copy(out, rr.AAAA[:])
		return out, nil
	case TypeTXT:
		var b bytes.Buffer
		for _, s := range rr.TXT {
			if len(s) > 255 {
				return nil, errors.New("netsec: TXT character-string exceeds 255 octets")
			}
			b.WriteByte(byte(len(s)))
			b.Write(s)
		}
		return b.Bytes(), nil
	default:
		if rr.RDATA == nil {
			return nil, errors.New("netsec: missing canonical RDATA for RR type")
		}
		return rr.RDATA, nil
	}
}
