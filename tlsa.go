package netsec

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
)

// TLSA certificate usage values (RFC 6698 section 2.1.1). Only the DANE
// usages are supported by this package.
const (
	// UsageDANETA (2) pins a trust anchor: the matched certificate must be a
	// CA in the peer's chain and the leaf must build a path to it.
	UsageDANETA TLSAUsage = 2
	// UsageDANEEE (3) pins the end-entity certificate directly. Neither the
	// DNS name nor the validity period is checked.
	UsageDANEEE TLSAUsage = 3
)

// TLSA selector values (RFC 6698 section 2.1.2).
const (
	// SelectorCert selects the full DER-encoded certificate.
	SelectorCert TLSASelector = 0
	// SelectorSPKI selects the DER-encoded SubjectPublicKeyInfo.
	SelectorSPKI TLSASelector = 1
)

// TLSA matching type values (RFC 6698 section 2.1.3).
const (
	// MatchingFull compares the selected data byte-for-byte.
	MatchingFull TLSAMatching = 0
	// MatchingSHA256 compares the SHA-256 digest of the selected data.
	MatchingSHA256 TLSAMatching = 1
	// MatchingSHA512 compares the SHA-512 digest of the selected data.
	MatchingSHA512 TLSAMatching = 2
)

// TLSAUsage is the certificate usage field of a TLSA record.
type TLSAUsage uint8

// TLSASelector is the selector field of a TLSA record.
type TLSASelector uint8

// TLSAMatching is the matching type field of a TLSA record.
type TLSAMatching uint8

// TLSARecord is a DNSSEC-validated TLSA RDATA set. The caller is responsible
// for DNSSEC validation; this package only performs the certificate matching.
type TLSARecord struct {
	Usage        TLSAUsage
	Selector     TLSASelector
	MatchingType TLSAMatching
	// Data is the certificate association data carried in the RDATA.
	Data []byte
}

// usable reports whether every field of the record has a value this package
// implements. Records with unrecognized fields are ignored per RFC 7218.
func (r TLSARecord) usable() bool {
	if len(r.Data) == 0 {
		return false
	}
	switch r.Usage {
	case UsageDANETA, UsageDANEEE:
	default:
		return false
	}
	switch r.Selector {
	case SelectorCert, SelectorSPKI:
	default:
		return false
	}
	switch r.MatchingType {
	case MatchingFull:
	case MatchingSHA256:
		if len(r.Data) != sha256.Size {
			return false
		}
	case MatchingSHA512:
		if len(r.Data) != sha512.Size {
			return false
		}
	default:
		return false
	}
	return true
}

// selectedData returns exactly the certificate bytes the selector names. The
// selector is interpreted first and in full: a full-cert selector never sees
// the SPKI and vice versa, so a selector mismatch can never be masked by a
// later byte comparison.
func selectedData(cert *x509.Certificate, selector TLSASelector) ([]byte, bool) {
	switch selector {
	case SelectorCert:
		return cert.Raw, true
	case SelectorSPKI:
		return cert.RawSubjectPublicKeyInfo, true
	default:
		return nil, false
	}
}

// MatchCertificate reports whether cert satisfies the TLSA record. The
// selector determines which certificate bytes are extracted and the matching
// type determines how those bytes are compared; there is deliberately no
// fallback to a raw byte comparison when a digest matching type is in use.
func MatchCertificate(cert *x509.Certificate, record TLSARecord) bool {
	data, ok := selectedData(cert, record.Selector)
	if !ok {
		return false
	}
	switch record.MatchingType {
	case MatchingFull:
		return bytes.Equal(data, record.Data)
	case MatchingSHA256:
		sum := sha256.Sum256(data)
		return bytes.Equal(sum[:], record.Data)
	case MatchingSHA512:
		sum := sha512.Sum512(data)
		return bytes.Equal(sum[:], record.Data)
	default:
		return false
	}
}
