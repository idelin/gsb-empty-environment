package netsec

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"
)

var hashOIDs = map[string]crypto.Hash{
	"1.2.840.113549.2.5":      crypto.MD5,
	"1.3.14.3.2.26":           crypto.SHA1,
	"2.16.840.1.101.3.4.2.4":  crypto.SHA224,
	"2.16.840.1.101.3.4.2.1":  crypto.SHA256,
	"2.16.840.1.101.3.4.2.2":  crypto.SHA384,
	"2.16.840.1.101.3.4.2.3":  crypto.SHA512,
	"2.16.840.1.101.3.4.2.5":  crypto.SHA512_224,
	"2.16.840.1.101.3.4.2.6":  crypto.SHA512_256,
	"2.16.840.1.101.3.4.2.7":  crypto.SHA3_224,
	"2.16.840.1.101.3.4.2.8":  crypto.SHA3_256,
	"2.16.840.1.101.3.4.2.9":  crypto.SHA3_384,
	"2.16.840.1.101.3.4.2.10": crypto.SHA3_512,
}

func hashForOID(oid asn1.ObjectIdentifier) (crypto.Hash, string, error) {
	h, ok := hashOIDs[oid.String()]
	if !ok {
		return 0, "", errorf(ErrUnsupportedDigest, "unknown hash algorithm %s", oid.String())
	}
	if !h.Available() {
		return 0, "", errorf(ErrUnsupportedDigest, "%s is not available", oid.String())
	}
	return h, hashName(h), nil
}

func digestBytes(h crypto.Hash, data []byte) ([]byte, error) {
	if !h.Available() {
		return nil, errorf(ErrUnsupportedDigest, "hash %v is unavailable", h)
	}
	hasher := h.New()
	_, _ = hasher.Write(data)
	return hasher.Sum(nil), nil
}

func imprintMatches(imprint MessageImprint, data []byte) bool {
	digest, err := digestBytes(imprint.Hash, data)
	if err != nil {
		return false
	}
	return len(digest) == len(imprint.HashedMessage) &&
		subtle.ConstantTimeCompare(digest, imprint.HashedMessage) == 1
}

func splitDER(data []byte) ([]asn1.RawValue, error) {
	var values []asn1.RawValue
	for len(data) > 0 {
		var value asn1.RawValue
		rest, err := asn1.Unmarshal(data, &value)
		if err != nil {
			return nil, err
		}
		if len(rest) >= len(data) {
			return nil, errors.New("ASN.1 parser made no progress")
		}
		values = append(values, value)
		data = rest
	}
	return values, nil
}

func parseOneDER(data []byte) (asn1.RawValue, error) {
	var value asn1.RawValue
	rest, err := asn1.Unmarshal(data, &value)
	if err != nil {
		return asn1.RawValue{}, err
	}
	if len(rest) != 0 {
		return asn1.RawValue{}, errors.New("trailing data after DER value")
	}
	return value, nil
}

func parseSequence(value asn1.RawValue) ([]asn1.RawValue, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagSequence || !value.IsCompound {
		return nil, fmt.Errorf("expected SEQUENCE, got class=%d tag=%d", value.Class, value.Tag)
	}
	return splitDER(value.Bytes)
}

func parseSet(value asn1.RawValue) ([]asn1.RawValue, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagSet || !value.IsCompound {
		return nil, fmt.Errorf("expected SET, got class=%d tag=%d", value.Class, value.Tag)
	}
	return splitDER(value.Bytes)
}

func parseOID(value asn1.RawValue) (asn1.ObjectIdentifier, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagOID {
		return nil, fmt.Errorf("expected OBJECT IDENTIFIER, got class=%d tag=%d", value.Class, value.Tag)
	}
	var oid asn1.ObjectIdentifier
	rest, err := asn1.Unmarshal(value.FullBytes, &oid)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errors.New("trailing data after OBJECT IDENTIFIER")
	}
	return oid, nil
}

func parseInteger(value asn1.RawValue) (*big.Int, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagInteger {
		return nil, fmt.Errorf("expected INTEGER, got class=%d tag=%d", value.Class, value.Tag)
	}
	var number *big.Int
	rest, err := asn1.Unmarshal(value.FullBytes, &number)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 || number == nil {
		return nil, errors.New("invalid INTEGER")
	}
	return number, nil
}

func parseBoolean(value asn1.RawValue) (bool, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagBoolean {
		return false, fmt.Errorf("expected BOOLEAN, got class=%d tag=%d", value.Class, value.Tag)
	}
	var result bool
	rest, err := asn1.Unmarshal(value.FullBytes, &result)
	if err != nil {
		return false, err
	}
	if len(rest) != 0 {
		return false, errors.New("trailing data after BOOLEAN")
	}
	return result, nil
}

func parseOctetString(value asn1.RawValue) ([]byte, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagOctetString {
		return nil, fmt.Errorf("expected OCTET STRING, got class=%d tag=%d", value.Class, value.Tag)
	}
	var result []byte
	rest, err := asn1.Unmarshal(value.FullBytes, &result)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errors.New("trailing data after OCTET STRING")
	}
	return result, nil
}

func parseGeneralizedTime(value asn1.RawValue) (time.Time, error) {
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagGeneralizedTime {
		return time.Time{}, fmt.Errorf("expected GeneralizedTime, got class=%d tag=%d", value.Class, value.Tag)
	}
	var parsed time.Time
	rest, err := asn1.Unmarshal(value.FullBytes, &parsed)
	if err != nil {
		return time.Time{}, err
	}
	if len(rest) != 0 {
		return time.Time{}, errors.New("trailing data after GeneralizedTime")
	}
	return parsed, nil
}

func parseAlgorithmIdentifier(value asn1.RawValue) (AlgorithmIdentifier, error) {
	children, err := parseSequence(value)
	if err != nil {
		return AlgorithmIdentifier{}, err
	}
	if len(children) == 0 || len(children) > 2 {
		return AlgorithmIdentifier{}, errors.New("invalid AlgorithmIdentifier")
	}
	oid, err := parseOID(children[0])
	if err != nil {
		return AlgorithmIdentifier{}, err
	}
	result := AlgorithmIdentifier{OID: oid}
	if len(children) == 2 {
		result.Parameters = children[1]
	}
	return result, nil
}

func parseImplicitOctets(value asn1.RawValue) ([]byte, error) {
	if value.Class != asn1.ClassContextSpecific {
		return nil, errors.New("expected context-specific value")
	}
	return value.Bytes, nil
}

func marshalTLV(tag byte, content []byte) ([]byte, error) {
	if len(content) == 0 {
		return []byte{tag, 0}, nil
	}
	if len(content) > 0x7fffffff {
		return nil, errors.New("ASN.1 value too large")
	}
	length := len(content)
	var lengthBytes []byte
	switch {
	case length < 0x80:
		lengthBytes = []byte{byte(length)}
	default:
		var encoded [8]byte
		index := len(encoded)
		for length > 0 {
			index--
			encoded[index] = byte(length)
			length >>= 8
		}
		lengthBytes = append([]byte{0x80 | byte(len(encoded)-index)}, encoded[index:]...)
	}
	result := make([]byte, 0, 1+len(lengthBytes)+len(content))
	result = append(result, tag)
	result = append(result, lengthBytes...)
	result = append(result, content...)
	return result, nil
}

func signedAttributesDER(value asn1.RawValue) ([]byte, error) {
	return marshalTLV(0x31, value.Bytes)
}

func digestOfDER(value []byte) [32]byte {
	return sha256.Sum256(value)
}

func parseGeneralNameDirectoryName(value asn1.RawValue) ([]byte, bool) {
	if value.Class != asn1.ClassContextSpecific || value.Tag != 4 || !value.IsCompound {
		return nil, false
	}
	var inner asn1.RawValue
	rest, err := asn1.Unmarshal(value.Bytes, &inner)
	if err != nil || len(rest) != 0 || inner.Tag != asn1.TagSequence {
		return nil, false
	}
	return inner.Bytes, true
}

func equalOID(left, right asn1.ObjectIdentifier) bool {
	return left.String() == right.String()
}

func equalBytes(left, right []byte) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare(left, right) == 1
}

func certIdentity(cert *x509.Certificate) IssuerIdentity {
	if cert == nil {
		return IssuerIdentity{}
	}
	certHash := sha256.Sum256(cert.Raw)
	pubHash := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return IssuerIdentity{
		Subject:           cert.Subject.String(),
		RawSubject:        bytes.Clone(cert.RawSubject),
		CertificateSHA256: certHash,
		PublicKeySHA256:   pubHash,
	}
}
