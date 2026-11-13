package netsec

import (
	"bytes"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// CertType identifies the purpose of an Ed25519 certificate.
type CertType uint8

// CertKeyType identifies the key certified by an Ed25519 certificate.
type CertKeyType uint8

// SigningCertificate is a parsed Tor Ed25519 certificate (cert-spec.txt,
// section 4) binding a blinded key to a short-lived descriptor signing key.
type SigningCertificate struct {
	// Version is the certificate version byte (currently 1).
	Version uint8

	// Type is the certificate type. Only CertTypeSigningKey is admitted.
	Type CertType

	// KeyType is the certified-key type. Only CertKeyTypeEd25519 is admitted.
	KeyType CertKeyType

	// Expiration is the certificate expiration time, derived from the on-wire
	// hour count since the Unix epoch.
	Expiration time.Time

	// SigningKey is the certified Ed25519 descriptor signing key.
	SigningKey ed25519PublicKey

	// Extensions holds the raw type/value pairs of certificate extensions.
	Extensions []CertExtension

	// Raw is the complete certificate as it appeared on the wire.
	Raw []byte

	// SignedPart is everything covered by Signature: the header, certified
	// key and extensions, i.e. Raw minus the trailing 64-byte signature.
	SignedPart []byte

	// Signature is the blinded-key Ed25519 signature over SignedPart.
	Signature []byte
}

// CertExtension is one extension entry inside a SigningCertificate.
type CertExtension struct {
	Type  uint8
	Flags uint8
	Value []byte
}

// CertSignedPart returns the exact bytes covered by the certificate
// signature. Verifier implementations sign or verify this material with the
// current-period blinded key.
func (c *SigningCertificate) CertSignedPart() []byte {
	return c.SignedPart
}

// Descriptor is a parsed onion-service descriptor relevant to HSDir
// admission. It is intentionally a subset of the full descriptor grammar.
type Descriptor struct {
	// Version is the hs-descriptor version. Only version 3 is admitted.
	Version uint64

	// LifetimeSeconds is the advertised descriptor lifetime.
	LifetimeSeconds uint64

	// DescriptorEnd is the advertised descriptor expiration, when present.
	DescriptorEnd time.Time

	// Certificate is the descriptor-signing-key Ed25519 certificate.
	Certificate *SigningCertificate

	// RevisionCounter is the monotonically increasing revision value.
	RevisionCounter uint64

	// SignedBody is the exact descriptor text covered by Signature, i.e.
	// every byte preceding the "descriptor-signing-key-signature" line.
	SignedBody []byte

	// Signature is the detached Ed25519 signature over
	// SignaturePrefix + SignedBody.
	Signature []byte

	// Raw is the complete unmodified descriptor text.
	Raw []byte
}

// On-wire field names from rend-spec-v3.
const (
	fieldHSDirVersion       = "hs-descriptor"
	fieldDescriptorLifetime = "descriptor-lifetime"
	fieldDescriptorEnd      = "descriptor-end-time"
	fieldDescriptorSignKey  = "descriptor-signing-key-cert"
	fieldRevisionCounter    = "revision-counter"
	fieldSignature          = "descriptor-signing-key-signature"
	fieldSuperencrypted     = "superencrypted"
)

// Ed25519 certificate wire sizes (cert-spec.txt section 4).
const (
	certHeaderSize   = 9 // version(1) + type(1) + expiration(4) + key type(1) + key length(2)
	certKeyLength    = ed25519PublicKeySize
	certSignatureLen = 64
)

// ed25519PublicKey is an alias to avoid forcing callers to import
// crypto/ed25519 merely to reference key material.
type ed25519PublicKey = []byte

const ed25519PublicKeySize = 32

// ParseDescriptor parses the textual form of a v3 onion service descriptor,
// including its embedded PEM-encoded Ed25519 certificate and detached
// signature. It performs structural validation only; all cryptographic checks
// are deferred to the injected Verifier.
//
// Scalar fields use "key value" lines, while descriptor-signing-key-cert,
// descriptor-signing-key-signature and superencrypted occupy their own line
// followed by PEM/base64 payload lines.
func ParseDescriptor(raw []byte) (*Descriptor, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrMalformedDescriptor)
	}

	desc := &Descriptor{Raw: append([]byte(nil), raw...)}
	var certPEM, signatureText []byte
	var haveVersion, haveCert, haveRevision, haveSignature bool

	lines := bytes.Split(raw, []byte("\n"))
	signedBodyLength := -1
	for index := 0; index < len(lines); {
		line := lines[index]
		switch {
		case string(line) == fieldSignature:
			// Everything before this field line is the signed body; the
			// newline ending the previous line is part of the body.
			signedBodyLength = 0
			for _, previous := range lines[:index] {
				signedBodyLength += len(previous) + 1
			}
			if index+1 >= len(lines) {
				return nil, fmt.Errorf("%w: %s has no payload",
					ErrMalformedDescriptor, fieldSignature)
			}
			index++
			signatureText = append([]byte(nil), lines[index]...)
			haveSignature = true
			index++
		case string(line) == fieldDescriptorSignKey:
			start := index
			for index < len(lines) && string(lines[index]) != "-----END ED25519 CERT-----" {
				index++
			}
			if index >= len(lines) {
				return nil, fmt.Errorf("%w: unterminated %s PEM block",
					ErrMalformedDescriptor, fieldDescriptorSignKey)
			}
			certPEM = bytes.Join(lines[start:index+1], []byte("\n"))
			haveCert = true
			index++
		case string(line) == fieldSuperencrypted:
			for index < len(lines) && string(lines[index]) != "-----END MESSAGE-----" {
				index++
			}
			if index < len(lines) {
				index++
			}
		default:
			if key, value, ok := splitField(line); ok {
				switch key {
				case fieldHSDirVersion:
					version, err := parseUint(value)
					if err != nil {
						return nil, fmt.Errorf("%w: version: %v", ErrMalformedDescriptor, err)
					}
					desc.Version = version
					haveVersion = true
				case fieldDescriptorLifetime:
					lifetime, err := parseUint(value)
					if err != nil {
						return nil, fmt.Errorf("%w: lifetime: %v", ErrMalformedDescriptor, err)
					}
					desc.LifetimeSeconds = lifetime
				case fieldDescriptorEnd:
					when, err := time.Parse("2006-01-02 15:04:05",
						string(bytes.TrimSpace(value)))
					if err != nil {
						return nil, fmt.Errorf("%w: descriptor-end-time: %v",
							ErrMalformedDescriptor, err)
					}
					desc.DescriptorEnd = when.UTC()
				case fieldRevisionCounter:
					counter, err := parseUint(value)
					if err != nil {
						return nil, fmt.Errorf("%w: revision-counter: %v",
							ErrMalformedDescriptor, err)
					}
					desc.RevisionCounter = counter
					haveRevision = true
				}
			}
			index++
		}
	}

	if !haveVersion {
		return nil, fmt.Errorf("%w: missing %s field", ErrMalformedDescriptor, fieldHSDirVersion)
	}
	if !haveRevision {
		return nil, fmt.Errorf("%w: missing %s field",
			ErrMalformedDescriptor, fieldRevisionCounter)
	}
	if !haveCert {
		return nil, fmt.Errorf("%w: missing %s field",
			ErrMalformedDescriptor, fieldDescriptorSignKey)
	}
	if !haveSignature || signedBodyLength < 0 || signedBodyLength > len(raw) {
		return nil, fmt.Errorf("%w: missing %s field",
			ErrMalformedDescriptor, fieldSignature)
	}

	signature, err := decodeBase64(signatureText)
	if err != nil {
		return nil, fmt.Errorf("%w: signature: %v", ErrMalformedDescriptor, err)
	}
	desc.Signature = signature
	desc.SignedBody = append([]byte(nil), raw[:signedBodyLength]...)

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("%w: undecodable %s PEM block",
			ErrMalformedDescriptor, fieldDescriptorSignKey)
	}
	cert, err := ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	desc.Certificate = cert
	return desc, nil
}

// ParseCertificate parses the binary form of a Tor Ed25519 certificate.
//
// Layout (cert-spec.txt section 4):
//
//	version                  [1 byte]
//	cert type                [1 byte]
//	expiration date/hour     [4 bytes, big-endian hours since epoch]
//	cert key type            [1 byte]
//	certified key length     [2 bytes, big-endian]
//	certified key            [N bytes]
//	number of extensions     [1 byte]
//	extensions               [variable]
//	signature                [64 bytes]
func ParseCertificate(raw []byte) (*SigningCertificate, error) {
	if len(raw) < certHeaderSize+1+certSignatureLen {
		return nil, fmt.Errorf("%w: certificate too short", ErrMalformedDescriptor)
	}

	cert := &SigningCertificate{Raw: append([]byte(nil), raw...)}
	cert.Version = raw[0]
	cert.Type = CertType(raw[1])
	expirationHours := uint64(raw[2])<<24 | uint64(raw[3])<<16 |
		uint64(raw[4])<<8 | uint64(raw[5])
	cert.Expiration = time.Unix(int64(expirationHours)*3600, 0).UTC()
	cert.KeyType = CertKeyType(raw[6])
	keyLength := int(raw[7])<<8 | int(raw[8])

	pos := certHeaderSize
	if keyLength != certKeyLength || pos+keyLength > len(raw) {
		return nil, fmt.Errorf("%w: bad certified key length %d",
			ErrMalformedDescriptor, keyLength)
	}
	cert.SigningKey = append([]byte(nil), raw[pos:pos+keyLength]...)
	pos += keyLength

	if pos >= len(raw) {
		return nil, fmt.Errorf("%w: certificate truncated before extensions",
			ErrMalformedDescriptor)
	}
	extensionCount := int(raw[pos])
	pos++

	for i := 0; i < extensionCount; i++ {
		if pos+4 > len(raw) {
			return nil, fmt.Errorf("%w: certificate truncated in extension header",
				ErrMalformedDescriptor)
		}
		extension := CertExtension{
			Type:  raw[pos],
			Flags: raw[pos+1],
		}
		extensionLength := int(raw[pos+2])<<8 | int(raw[pos+3])
		pos += 4
		if pos+extensionLength > len(raw) {
			return nil, fmt.Errorf("%w: certificate truncated in extension value",
				ErrMalformedDescriptor)
		}
		extension.Value = append([]byte(nil), raw[pos:pos+extensionLength]...)
		pos += extensionLength
		cert.Extensions = append(cert.Extensions, extension)
	}

	if len(raw)-pos != certSignatureLen {
		return nil, fmt.Errorf("%w: certificate must end with a %d-byte signature",
			ErrMalformedDescriptor, certSignatureLen)
	}
	cert.SignedPart = append([]byte(nil), raw[:pos]...)
	cert.Signature = append([]byte(nil), raw[pos:]...)
	return cert, nil
}

func splitField(line []byte) (string, []byte, bool) {
	separator := bytes.IndexByte(line, ' ')
	if separator <= 0 {
		return "", nil, false
	}
	return string(line[:separator]), line[separator+1:], true
}

func parseUint(value []byte) (uint64, error) {
	var number uint64
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return 0, errors.New("empty integer")
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("invalid integer %q", value)
		}
		number = number*10 + uint64(digit-'0')
	}
	return number, nil
}

func decodeBase64(value []byte) ([]byte, error) {
	value = bytes.TrimSpace(value)
	if decoded, err := base64.StdEncoding.DecodeString(string(value)); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(string(value))
}
