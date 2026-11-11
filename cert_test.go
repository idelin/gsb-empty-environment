package derstrict

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"testing"
	"time"
)

func generateTestCertificate(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(20261111),
		Subject: pkix.Name{
			CommonName:   "derstrict.test",
			Organization: []string{"DER Strict"},
			Country:      []string{"US"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return der
}

// certificatePattern mirrors the top-level Certificate structure:
//
//	Certificate ::= SEQUENCE { tbsCertificate, signatureAlgorithm, signatureValue }
func certificatePattern() Pattern {
	return Pattern{
		Class: ClassUniversal,
		Tag:   TagSequence,
		Children: []Pattern{
			{Class: ClassUniversal, Tag: TagSequence},
			{Class: ClassUniversal, Tag: TagSequence},
			{Class: ClassUniversal, Tag: TagBitString},
		},
	}
}

func TestValidCertificateAccepted(t *testing.T) {
	der := generateTestCertificate(t)

	node, err := DecodeWithPattern(der, certificatePattern())
	if err != nil {
		t.Fatalf("real certificate rejected: %v", err)
	}
	if len(node.Children) != 3 {
		t.Fatalf("certificate child count = %d, want 3", len(node.Children))
	}

	// encoding/asn1 must accept the same canonical bytes.
	var raw asn1.RawValue
	if rest, err := asn1.Unmarshal(der, &raw); err != nil || len(rest) != 0 {
		t.Fatalf("encoding/asn1 rejected canonical certificate: err=%v rest=%d", err, len(rest))
	}

	// The certificate remains parseable by the standard library.
	if _, err := x509.ParseCertificate(der); err != nil {
		t.Fatalf("x509.ParseCertificate failed: %v", err)
	}
}

func TestCertificateTrailingByteRejected(t *testing.T) {
	der := generateTestCertificate(t)
	mutated := append(append([]byte{}, der...), 0x00)

	if _, err := Decode(mutated); err == nil {
		t.Fatal("derstrict accepted certificate with trailing byte")
	} else if got := OffsetOf(err); got != len(der) {
		t.Fatalf("offset = %d, want %d", got, len(der))
	}

	// encoding/asn1 silently leaves the trailing byte in rest.
	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(mutated, &raw)
	if err != nil {
		t.Fatalf("encoding/asn1 unexpectedly errored: %v", err)
	}
	if len(rest) != 1 || rest[0] != 0x00 {
		t.Fatalf("encoding/asn1 rest = %x, want single trailing byte", rest)
	}
}

func TestCertificateNonCanonicalBooleanRejected(t *testing.T) {
	der := generateTestCertificate(t)

	// Locate the cA BOOLEAN inside BasicConstraints: it is the only DER
	// BOOLEAN in a generated certificate, encoded as 01 01 ff.
	needle := []byte{0x01, 0x01, 0xff}
	offset := indexOf(der, needle)
	if offset < 0 {
		t.Fatal("cA BOOLEAN not found in certificate")
	}
	mutated := mutateAt(der, offset+2, []byte{0x7f})

	_, err := Decode(mutated)
	if err == nil {
		t.Fatal("derstrict accepted non-canonical BOOLEAN in certificate")
	}
	strictErr := err.(*Error)
	if strictErr.Op != "boolean" || strictErr.Offset != offset+2 {
		t.Fatalf("unexpected error: %v", err)
	}

	// encoding/asn1 accepts the BER-style boolean value.
	if !asn1Accepts(mutated) {
		t.Fatal("expected encoding/asn1 to accept non-canonical BOOLEAN")
	}
}

func TestCertificateNonMinimalLengthRejected(t *testing.T) {
	der := generateTestCertificate(t)

	// Find any element using the multi-octet length form with a redundant
	// leading zero and rewrite it to the same declared length using one fewer
	// octet. The byte count shrinks, so every enclosing SEQUENCE length is
	// rebuilt afterwards.
	target := findPaddedLongLength(der)
	if target.offset < 0 {
		t.Skip("certificate contains no padded long-form length")
	}
	mutated := rewritePaddedLength(t, der, target)

	_, err := Decode(mutated)
	if err == nil {
		t.Fatal("derstrict accepted non-minimal length in certificate")
	}
	strictErr := err.(*Error)
	if strictErr.Op != "length" {
		t.Fatalf("op = %q, want length: %v", strictErr.Op, err)
	}
	if got := OffsetOf(err); got < 0 || got >= len(mutated) {
		t.Fatalf("violation offset %d out of range", got)
	}
	if !asn1Accepts(mutated) {
		t.Fatal("expected encoding/asn1 to accept non-minimal length")
	}
}

type paddedLength struct {
	offset    int
	numOctets int
}

// findPaddedLongLength walks every TLV and returns the first multi-octet
// length encoding whose first value octet is zero (non-minimal).
func findPaddedLongLength(data []byte) paddedLength {
	var walk func([]byte) paddedLength
	walk = func(buf []byte) paddedLength {
		for pos := 0; pos < len(buf); {
			if pos+2 > len(buf) {
				return paddedLength{offset: -1}
			}
			identifierEnd := pos + 1
			if buf[pos]&0x1f == 0x1f {
				for identifierEnd < len(buf) && buf[identifierEnd]&0x80 != 0 {
					identifierEnd++
				}
				identifierEnd++
			}
			lengthOffset := identifierEnd
			if lengthOffset >= len(buf) {
				return paddedLength{offset: -1}
			}
			first := buf[lengthOffset]
			if first < 0x80 {
				length := int(first)
				contentStart := lengthOffset + 1
				if buf[pos]&0x20 != 0 {
					if found := walk(buf[contentStart : contentStart+length]); found.offset >= 0 {
						found.offset += contentStart
						return found
					}
				}
				pos = contentStart + length
				continue
			}
			numOctets := int(first & 0x7f)
			length := 0
			for i := 0; i < numOctets; i++ {
				length = length<<8 | int(buf[lengthOffset+1+i])
			}
			if buf[lengthOffset+1] == 0x00 {
				return paddedLength{offset: lengthOffset, numOctets: numOctets}
			}
			contentStart := lengthOffset + 1 + numOctets
			if buf[pos]&0x20 != 0 {
				if found := walk(buf[contentStart : contentStart+length]); found.offset >= 0 {
					found.offset += contentStart
					return found
				}
			}
			pos = contentStart + length
		}
		return paddedLength{offset: -1}
	}
	found := walk(data)
	return found
}

// rewritePaddedLength drops the redundant zero length octet and re-encodes
// every TLV header along the path so enclosing lengths stay consistent.
func rewritePaddedLength(t *testing.T, data []byte, target paddedLength) []byte {
	numOctets := target.numOctets
	declaredLength := 0
	for i := 0; i < numOctets; i++ {
		declaredLength = declaredLength<<8 | int(data[target.offset+1+i])
	}
	newLengthHeader := tlvHeader(0, declaredLength)[1:]
	out := append([]byte{}, data[:target.offset]...)
	out = append(out, newLengthHeader...)
	out = append(out, data[target.offset+1+numOctets:]...)
	rewrapped, err := recanonicalize(out)
	if err != nil {
		t.Fatalf("recanonicalize mutated certificate: %v", err)
	}
	return rewrapped
}

// recanonicalize re-encodes every length header while preserving identifiers
// and content, tolerating the BER-style length still present during the walk.
func recanonicalize(data []byte) ([]byte, error) {
	identifier, pos, err := scanIdentifier(data, 0)
	if err != nil {
		return nil, err
	}
	length, headerEnd, err := scanLength(data, pos)
	if err != nil {
		return nil, err
	}
	if headerEnd+length > len(data) {
		return nil, errTrailing
	}
	content := data[headerEnd : headerEnd+length]
	rest := data[headerEnd+length:]
	if len(rest) != 0 {
		return nil, errTrailing
	}
	if identifier[0]&0x20 != 0 {
		var rebuiltContent []byte
		for len(content) > 0 {
			child, err := recanonicalize(content)
			if err != nil {
				return nil, err
			}
			rebuiltContent = append(rebuiltContent, child...)
			content = content[len(child):]
		}
		content = rebuiltContent
	}
	return append(tlvHeader(identifier[0], len(content)), content...), nil
}

var errTrailing = certError("malformed TLV during recanonicalize")

type certError string

func (e certError) Error() string { return string(e) }

func scanIdentifier(data []byte, pos int) ([]byte, int, error) {
	start := pos
	pos++
	if data[start]&0x1f == 0x1f {
		for pos < len(data) && data[pos]&0x80 != 0 {
			pos++
		}
		pos++
	}
	if pos > len(data) {
		return nil, pos, errTrailing
	}
	return data[start:pos], pos, nil
}

func scanLength(data []byte, pos int) (int, int, error) {
	if pos >= len(data) {
		return 0, pos, errTrailing
	}
	first := data[pos]
	pos++
	if first < 0x80 {
		return int(first), pos, nil
	}
	numOctets := int(first & 0x7f)
	if pos+numOctets > len(data) {
		return 0, pos, errTrailing
	}
	length := 0
	for i := 0; i < numOctets; i++ {
		length = length<<8 | int(data[pos+i])
	}
	return length, pos + numOctets, nil
}
