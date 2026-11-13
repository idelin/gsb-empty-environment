package netsec

import (
	"crypto/elliptic"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
)

// OIDs from RFC 5480 and SEC 2.
var (
	oidPublicKeyECDSA = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}

	oidNamedCurveP224 = asn1.ObjectIdentifier{1, 3, 132, 0, 33}
	oidNamedCurveP256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 3, 1, 7}
	oidNamedCurveP384 = asn1.ObjectIdentifier{1, 3, 132, 0, 34}
	oidNamedCurveP521 = asn1.ObjectIdentifier{1, 3, 132, 0, 35}
)

// NamedCurveFromOID maps a named-curve OID to its implementation. It returns
// false for explicit parameters and for OIDs that are not supported.
func NamedCurveFromOID(oid asn1.ObjectIdentifier) (elliptic.Curve, bool) {
	switch {
	case oid.Equal(oidNamedCurveP224):
		return elliptic.P224(), true
	case oid.Equal(oidNamedCurveP256):
		return elliptic.P256(), true
	case oid.Equal(oidNamedCurveP384):
		return elliptic.P384(), true
	case oid.Equal(oidNamedCurveP521):
		return elliptic.P521(), true
	default:
		return nil, false
	}
}

// IsNamedCurve reports whether curve is one of the supported standard named
// curves. It deliberately compares the curve implementation rather than its
// parameters: a custom curve with P-256-shaped parameters is not P-256.
func IsNamedCurve(curve elliptic.Curve) bool {
	switch curve {
	case elliptic.P224(), elliptic.P256(), elliptic.P384(), elliptic.P521():
		return true
	default:
		return false
	}
}

// ValidateECParameters validates the AlgorithmIdentifier parameters of an
// ECDSA SubjectPublicKeyInfo.
//
// The only accepted form is a single OBJECT IDENTIFIER naming a supported
// curve. Explicit ECParameters (an ASN.1 SEQUENCE) are rejected outright,
// regardless of the curve they describe or the size of the public point.
func ValidateECParameters(params []byte) error {
	_, err := ParseECParameters(params)
	return err
}

// ParseECParameters validates EC algorithm parameters and returns the named
// curve OID they contain.
func ParseECParameters(params []byte) (asn1.ObjectIdentifier, error) {
	if len(params) == 0 {
		return nil, ErrMissingECParameters
	}

	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(params, &raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidECParameters, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing data after parameters", ErrInvalidECParameters)
	}

	if raw.Class != asn1.ClassUniversal {
		return nil, fmt.Errorf("%w: unexpected ASN.1 class %d", ErrInvalidECParameters, raw.Class)
	}

	switch raw.Tag {
	case asn1.TagOID:
		var oid asn1.ObjectIdentifier
		rest, err := asn1.Unmarshal(params, &oid)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidECParameters, err)
		}
		if len(rest) != 0 {
			return nil, fmt.Errorf("%w: trailing data after curve OID", ErrInvalidECParameters)
		}
		if _, ok := NamedCurveFromOID(oid); !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnsupportedECCurve, oid.String())
		}
		return oid, nil
	case asn1.TagSequence:
		return nil, ErrExplicitECParameters
	default:
		return nil, fmt.Errorf("%w: expected curve OID, got ASN.1 tag %d", ErrInvalidECParameters, raw.Tag)
	}
}

// ValidateSubjectPublicKeyInfo validates a DER-encoded SubjectPublicKeyInfo.
//
// Non-EC keys are ignored by this policy. For ECDSA keys, the curve must be
// selected by a named-curve OID and the public point must be a valid point on
// that named curve.
func ValidateSubjectPublicKeyInfo(der []byte) error {
	var spki struct {
		Algorithm pkix.AlgorithmIdentifier
		PublicKey asn1.BitString
	}
	rest, err := asn1.Unmarshal(der, &spki)
	if err != nil {
		return fmt.Errorf("%w: invalid SubjectPublicKeyInfo: %v", ErrMalformedCertificate, err)
	}
	if len(rest) != 0 {
		return fmt.Errorf("%w: trailing data after SubjectPublicKeyInfo", ErrMalformedCertificate)
	}

	return validateECSubjectPublicKeyInfo(spki.Algorithm, spki.PublicKey)
}

func validateECSubjectPublicKeyInfo(algorithm pkix.AlgorithmIdentifier, publicKey asn1.BitString) error {
	if !algorithm.Algorithm.Equal(oidPublicKeyECDSA) {
		return nil
	}

	oid, err := ParseECParameters(algorithm.Parameters.FullBytes)
	if err != nil {
		return err
	}
	curve, ok := NamedCurveFromOID(oid)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedECCurve, oid.String())
	}

	point := publicKey.RightAlign()
	x, y := elliptic.Unmarshal(curve, point)
	if x == nil || y == nil {
		return fmt.Errorf("%w: point is not on curve %s", ErrInvalidECPublicKey, oid.String())
	}

	return nil
}
