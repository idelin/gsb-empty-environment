package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
)

// OIDSCTList is the X.509 extension OID 1.3.6.1.4.1.11129.2.4.2 that carries an
// embedded SignedCertificateTimestampList (RFC 6962 section 3.3).
var OIDSCTList = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}

// SCT versions, see RFC 9162 section 4.
const (
	Version1 uint8 = 0
)

// Log entry types, see RFC 9162 section 4.1.
const (
	EntryTypeX509Entry    uint16 = 0
	EntryTypePrecertEntry uint16 = 1
)

// SCT signature hash algorithms, see RFC 9162 section 5.1.2.
const (
	HashNone   uint8 = 0
	HashMD5    uint8 = 1
	HashSHA1   uint8 = 2
	HashSHA224 uint8 = 3
	HashSHA256 uint8 = 4
	HashSHA384 uint8 = 5
	HashSHA512 uint8 = 6
)

// SCT signature algorithms, see RFC 9162 section 5.1.2.
const (
	SigAnonymous uint8 = 0
	SigRSA       uint8 = 1
	SigDSA       uint8 = 2
	SigECDSA     uint8 = 3
	SigEd25519   uint8 = 7
)

// LogIDLength is the fixed length of a CT log identifier (RFC 9162 section 4.1):
// the SHA-256 hash of the log's SubjectPublicKeyInfo DER.
const LogIDLength = sha256.Size

// minSCTLength is the smallest possible serialized SCT: version(1) + id(32) +
// timestamp(8) + extLen(2) + sigAndHash(4) + sigLen(2).
const minSCTLength = 49

// SCT is a parsed SignedCertificateTimestamp as serialized on the wire
// (RFC 9162 section 4.1). Raw holds the original serialized bytes.
type SCT struct {
	Version            uint8
	LogID              []byte
	Timestamp          uint64
	Extensions         []byte
	HashAlgorithm      uint8
	SignatureAlgorithm uint8
	Signature          []byte
	Raw                []byte
}

// ParseSCT parses a single serialized SignedCertificateTimestamp. The bytes are
// exactly one SCT (the same form stored in crypto/x509
// Certificate.SignedCertificateTimestamps), without the outer list length prefix.
func ParseSCT(raw []byte) (*SCT, error) {
	if len(raw) < minSCTLength {
		return nil, fmt.Errorf("netsec: SCT truncated: %d bytes", len(raw))
	}
	pos := 0
	version := raw[pos]
	pos++
	if version != Version1 {
		return nil, fmt.Errorf("netsec: unsupported SCT version %d", version)
	}

	sct := &SCT{Version: version, Raw: raw}

	sct.LogID = make([]byte, LogIDLength)
	copy(sct.LogID, raw[pos:pos+LogIDLength])
	pos += LogIDLength

	sct.Timestamp = uint64(raw[pos])<<56 | uint64(raw[pos+1])<<48 |
		uint64(raw[pos+2])<<40 | uint64(raw[pos+3])<<32 |
		uint64(raw[pos+4])<<24 | uint64(raw[pos+5])<<16 |
		uint64(raw[pos+6])<<8 | uint64(raw[pos+7])
	pos += 8

	extLen := int(raw[pos])<<8 | int(raw[pos+1])
	pos += 2
	if extLen > len(raw)-pos {
		return nil, errors.New("netsec: SCT extensions length exceeds available data")
	}
	sct.Extensions = make([]byte, extLen)
	copy(sct.Extensions, raw[pos:pos+extLen])
	pos += extLen

	if len(raw)-pos < 4 {
		return nil, errors.New("netsec: SCT missing signature algorithm fields")
	}
	sct.HashAlgorithm = raw[pos]
	sct.SignatureAlgorithm = raw[pos+1]
	pos += 2
	sigLen := int(raw[pos])<<8 | int(raw[pos+1])
	pos += 2
	if sigLen != len(raw)-pos {
		return nil, fmt.Errorf("netsec: SCT signature length mismatch: header says %d, have %d", sigLen, len(raw)-pos)
	}
	sct.Signature = make([]byte, sigLen)
	copy(sct.Signature, raw[pos:])

	return sct, nil
}

// ParseSCTList parses the value of a SignedCertificateTimestampList extension
// (RFC 9162 section 4.2), i.e. the extension value including the outer
// uint16 list length prefix.
func ParseSCTList(extensionValue []byte) ([][]byte, error) {
	if len(extensionValue) < 2 {
		return nil, errors.New("netsec: SCT list truncated")
	}
	totalLen := int(extensionValue[0])<<8 | int(extensionValue[1])
	if totalLen != len(extensionValue)-2 {
		return nil, fmt.Errorf("netsec: SCT list length mismatch: header says %d, have %d", totalLen, len(extensionValue)-2)
	}

	var scts [][]byte
	pos := 2
	for pos < len(extensionValue) {
		if len(extensionValue)-pos < 2 {
			return nil, errors.New("netsec: SCT list entry length truncated")
		}
		entryLen := int(extensionValue[pos])<<8 | int(extensionValue[pos+1])
		pos += 2
		if entryLen <= 0 || entryLen > len(extensionValue)-pos {
			return nil, errors.New("netsec: SCT list entry length invalid")
		}
		entry := make([]byte, entryLen)
		copy(entry, extensionValue[pos:pos+entryLen])
		scts = append(scts, entry)
		pos += entryLen
	}
	return scts, nil
}

// MarshalSCTList serializes SCTs into the SignedCertificateTimestampList wire
// format (RFC 9162 section 4.2).
func MarshalSCTList(scts [][]byte) []byte {
	var body bytes.Buffer
	for _, sct := range scts {
		body.WriteByte(byte(len(sct) >> 8))
		body.WriteByte(byte(len(sct)))
		body.Write(sct)
	}
	out := make([]byte, 2+body.Len())
	out[0] = byte(body.Len() >> 8)
	out[1] = byte(body.Len())
	copy(out[2:], body.Bytes())
	return out
}

// SCTListExtension builds the non-critical X.509 extension carrying the
// embedded SCT list for the given serialized SCTs.
func SCTListExtension(scts [][]byte) (asn1.ObjectIdentifier, bool, []byte) {
	return OIDSCTList, false, MarshalSCTList(scts)
}

// EmbeddedSCTs extracts the individual serialized SCTs carried in a parsed
// certificate's SignedCertificateTimestampList extension. It returns no SCTs
// (and no error) when the extension is absent.
func EmbeddedSCTs(cert *x509.Certificate) ([][]byte, error) {
	if cert == nil {
		return nil, errors.New("netsec: nil certificate")
	}
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(OIDSCTList) {
			return ParseSCTList(ext.Value)
		}
	}
	return nil, nil
}

// x509SignedData builds the to-be-signed structure for an SCT over an X.509
// certificate (RFC 6962 section 3.2):
//
//	digitally-signed struct {
//	    Version sct_version;
//	    SignatureType signature_type = certificate_timestamp;
//	    uint64 timestamp;
//	    LogEntryType entry_type;
//	    select(entry_type) {
//	        case x509_entry: ASN.1Cert;
//	    } entry;
//	    CtExtensions extensions;
//	}
//
// where ASN.1Cert is a uint24 length-prefixed DER certificate.
func x509SignedData(timestamp uint64, certDER, extensions []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(Version1)
	buf.WriteByte(0) // signature_type = certificate_timestamp
	buf.Write(uint64Bytes(timestamp))
	buf.Write(uint16Bytes(EntryTypeX509Entry))
	buf.Write(uint24Bytes(len(certDER)))
	buf.Write(certDER)
	buf.Write(uint16Bytes(uint16(len(extensions))))
	buf.Write(extensions)
	return buf.Bytes()
}

func hashFor(algorithm uint8) (crypto.Hash, bool) {
	switch algorithm {
	case HashSHA256:
		return crypto.SHA256, true
	case HashSHA384:
		return crypto.SHA384, true
	case HashSHA512:
		return crypto.SHA512, true
	default:
		return 0, false
	}
}

// verifySCTSignature checks the SCT signature against a CT log public key over
// the signed data derived from certDER. It supports ECDSA (P-256/P-384/P-521),
// RSA PKCS#1 v1.5 and Ed25519 log keys as defined by RFC 9162.
func verifySCTSignature(sct *SCT, logKey crypto.PublicKey, certDER []byte) error {
	signed := x509SignedData(sct.Timestamp, certDER, sct.Extensions)

	switch sct.SignatureAlgorithm {
	case SigECDSA:
		key, ok := logKey.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("netsec: log key is %T, not ECDSA", logKey)
		}
		hash, ok := hashFor(sct.HashAlgorithm)
		if !ok {
			return fmt.Errorf("netsec: unsupported ECDSA hash algorithm %d", sct.HashAlgorithm)
		}
		digest := hash.New()
		digest.Write(signed)
		type ecdsaSig struct {
			R, S *big.Int
		}
		var parsed ecdsaSig
		if rest, err := asn1.Unmarshal(sct.Signature, &parsed); err != nil {
			return fmt.Errorf("netsec: invalid ECDSA signature encoding: %w", err)
		} else if len(rest) != 0 {
			return errors.New("netsec: trailing bytes in ECDSA signature")
		}
		if !ecdsa.Verify(key, digest.Sum(nil), parsed.R, parsed.S) {
			return errors.New("netsec: ECDSA SCT signature does not verify")
		}
		return nil

	case SigRSA:
		key, ok := logKey.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("netsec: log key is %T, not RSA", logKey)
		}
		hash, ok := hashFor(sct.HashAlgorithm)
		if !ok {
			return fmt.Errorf("netsec: unsupported RSA hash algorithm %d", sct.HashAlgorithm)
		}
		digest := hash.New()
		digest.Write(signed)
		if err := rsa.VerifyPKCS1v15(key, hash, digest.Sum(nil), sct.Signature); err != nil {
			return fmt.Errorf("netsec: RSA SCT signature does not verify: %w", err)
		}
		return nil

	case SigEd25519:
		key, ok := logKey.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("netsec: log key is %T, not Ed25519", logKey)
		}
		if sct.HashAlgorithm != HashNone {
			return fmt.Errorf("netsec: Ed25519 SCT must use hash algorithm none, got %d", sct.HashAlgorithm)
		}
		if !ed25519.Verify(key, signed, sct.Signature) {
			return errors.New("netsec: Ed25519 SCT signature does not verify")
		}
		return nil

	default:
		return fmt.Errorf("netsec: unsupported SCT signature algorithm %d", sct.SignatureAlgorithm)
	}
}

func uint16Bytes(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }

func uint24Bytes(v int) []byte {
	return []byte{byte(v >> 16), byte(v >> 8), byte(v)}
}

func uint64Bytes(v uint64) []byte {
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
	return b
}
