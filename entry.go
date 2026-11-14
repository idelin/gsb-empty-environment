package netsec

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
)

// Log entry types (RFC 6962 s3.1).
const (
	logEntryX509    uint16 = 0
	logEntryPrecert uint16 = 1

	// signatureTypeCertificateTimestamp is the v1 signature_type for SCTs.
	signatureTypeCertificateTimestamp byte = 0
)

// oidExtensionSCT is the X.509 extension carrying an embedded SCT list.
var oidExtensionSCT = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}

// oidExtensionPoison is the CT poison extension present in precertificates.
var oidExtensionPoison = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 3}

// ErrNoSCTExtension is returned when a certificate carries no SCT extension
// from which a precertificate TBSCertificate could be rebuilt.
var ErrNoSCTExtension = errors.New("netsec: certificate has no SCT extension to strip")

// IssuerKeyHash returns the SHA-256 of the issuer's DER SubjectPublicKeyInfo,
// as committed to by a precert_entry SCT.
func IssuerKeyHash(issuer *x509.Certificate) [32]byte {
	return sha256.Sum256(issuer.RawSubjectPublicKeyInfo)
}

// x509Entry builds the signed LogEntry for a final certificate.
func x509Entry(certDER []byte) []byte {
	out := make([]byte, 0, len(certDER)+5)
	out = appendUint16(out, logEntryX509)
	out = appendUint24(out, uint32(len(certDER)))
	return append(out, certDER...)
}

// precertEntry builds the signed LogEntry for a precertificate.
func precertEntry(issuerKeyHash [32]byte, tbs []byte) []byte {
	out := make([]byte, 0, len(tbs)+37)
	out = appendUint16(out, logEntryPrecert)
	out = append(out, issuerKeyHash[:]...)
	out = appendUint24(out, uint32(len(tbs)))
	return append(out, tbs...)
}

// signatureInput builds the TLS-plaintext structure the log signed:
// version || signature_type || timestamp || entry || extensions.
func signatureInput(sct *SCT, entry []byte) []byte {
	out := make([]byte, 0, len(entry)+12+len(sct.Extensions))
	out = append(out, byte(sct.Version), signatureTypeCertificateTimestamp)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], sct.Timestamp)
	out = append(out, ts[:]...)
	out = append(out, entry...)
	out = appendUint16(out, uint16(len(sct.Extensions)))
	return append(out, sct.Extensions...)
}

func appendUint16(b []byte, v uint16) []byte {
	return append(b, byte(v>>8), byte(v))
}

func appendUint24(b []byte, v uint32) []byte {
	return append(b, byte(v>>16), byte(v>>8), byte(v))
}

// PrecertTBSCertificate rebuilds the TBSCertificate of the precertificate
// that corresponds to leaf: the final certificate's TBS with the embedded
// SCT list extension (and the CT poison extension, if any) removed.
func PrecertTBSCertificate(leaf *x509.Certificate) ([]byte, error) {
	return stripCTExtensions(leaf.RawTBSCertificate)
}

// SCTListExtension returns the raw SCT list embedded in cert, if present.
func SCTListExtension(cert *x509.Certificate) ([]byte, bool) {
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidExtensionSCT) {
			return ext.Value, true
		}
	}
	return nil, false
}

// stripCTExtensions removes the SCT and poison extensions from a DER
// TBSCertificate and re-encodes it. All other elements are copied verbatim
// so the result is byte-identical to the originally logged precert TBS.
func stripCTExtensions(tbs []byte) ([]byte, error) {
	if len(tbs) == 0 || tbs[0] != 0x30 {
		return nil, fmt.Errorf("%w: TBSCertificate is not a SEQUENCE", ErrMalformedSCTList)
	}
	contentLen, headerLen, err := derLength(tbs)
	if err != nil {
		return nil, err
	}
	body := tbs[headerLen : headerLen+contentLen]
	var elements [][]byte
	for len(body) > 0 {
		cl, hl, err := derLength(body)
		if err != nil {
			return nil, err
		}
		elements = append(elements, body[:hl+cl])
		body = body[hl+cl:]
	}
	stripped := false
	for i, el := range elements {
		if el[0] != 0xa3 { // [3] EXPLICIT extensions
			continue
		}
		rebuilt, found, err := filterExtensions(el)
		if err != nil {
			return nil, err
		}
		if found {
			elements[i] = rebuilt
			stripped = true
		}
	}
	if !stripped {
		return nil, ErrNoSCTExtension
	}
	var out []byte
	for _, el := range elements {
		out = append(out, el...)
	}
	return append(derTLV(0x30, len(out)), out...), nil
}

// filterExtensions rewrites a [3] EXPLICIT extensions element without the
// SCT and poison extensions. It reports whether any extension was removed.
func filterExtensions(el []byte) ([]byte, bool, error) {
	cl, hl, err := derLength(el)
	if err != nil {
		return nil, false, err
	}
	inner := el[hl : hl+cl]
	if len(inner) == 0 || inner[0] != 0x30 {
		return nil, false, fmt.Errorf("%w: extensions is not a SEQUENCE", ErrMalformedSCTList)
	}
	seqLen, seqHdr, err := derLength(inner)
	if err != nil {
		return nil, false, err
	}
	sctOID, _ := asn1.Marshal(oidExtensionSCT)
	poisonOID, _ := asn1.Marshal(oidExtensionPoison)
	var kept []byte
	removed := false
	body := inner[seqHdr : seqHdr+seqLen]
	for len(body) > 0 {
		cl, hl, err := derLength(body)
		if err != nil {
			return nil, false, err
		}
		ext := body[:hl+cl]
		body = body[hl+cl:]
		oid, err := extensionOID(ext)
		if err != nil {
			return nil, false, err
		}
		if bytesEqual(oid, sctOID) || bytesEqual(oid, poisonOID) {
			removed = true
			continue
		}
		kept = append(kept, ext...)
	}
	if !removed {
		return el, false, nil
	}
	seq := append(derTLV(0x30, len(kept)), kept...)
	return append(derTLV(0xa3, len(seq)), seq...), true, nil
}

// extensionOID returns the DER-encoded OID of one Extension TLV.
func extensionOID(ext []byte) ([]byte, error) {
	if len(ext) == 0 || ext[0] != 0x30 {
		return nil, fmt.Errorf("%w: extension is not a SEQUENCE", ErrMalformedSCTList)
	}
	cl, hl, err := derLength(ext)
	if err != nil {
		return nil, err
	}
	body := ext[hl : hl+cl]
	if len(body) == 0 || body[0] != 0x06 {
		return nil, fmt.Errorf("%w: extension missing OID", ErrMalformedSCTList)
	}
	ol, oh, err := derLength(body)
	if err != nil {
		return nil, err
	}
	return body[:oh+ol], nil
}

// derLength parses the DER length of the TLV at b and returns the content
// length and the total header (tag + length) size.
func derLength(b []byte) (contentLen, headerLen int, err error) {
	if len(b) < 2 {
		return 0, 0, fmt.Errorf("%w: truncated DER", ErrMalformedSCTList)
	}
	headerLen = 2
	switch {
	case b[1]&0x80 == 0:
		contentLen = int(b[1])
	default:
		n := int(b[1] & 0x7f)
		if n == 0 || n > 4 || len(b) < 2+n {
			return 0, 0, fmt.Errorf("%w: bad DER length", ErrMalformedSCTList)
		}
		headerLen = 2 + n
		for i := 0; i < n; i++ {
			contentLen = contentLen<<8 | int(b[2+i])
		}
	}
	if len(b) < headerLen+contentLen {
		return 0, 0, fmt.Errorf("%w: truncated DER content", ErrMalformedSCTList)
	}
	return contentLen, headerLen, nil
}

// derTLV encodes a DER tag-and-length header for the given content length.
func derTLV(tag byte, contentLen int) []byte {
	out := []byte{tag}
	switch {
	case contentLen < 0x80:
		out = append(out, byte(contentLen))
	case contentLen < 0x100:
		out = append(out, 0x81, byte(contentLen))
	case contentLen < 0x10000:
		out = append(out, 0x82, byte(contentLen>>8), byte(contentLen))
	default:
		out = append(out, 0x83, byte(contentLen>>16), byte(contentLen>>8), byte(contentLen))
	}
	return out
}

func bytesEqual(a, b []byte) bool {
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
