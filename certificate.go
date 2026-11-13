package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// Certificate type values from the OpenSSH certificate format.
const (
	UserCert uint32 = 1
	HostCert uint32 = 2
)

// CertificateSuffix is the suffix used by OpenSSH certificate key types.
const CertificateSuffix = "-cert-v01@openssh.com"

// Option is a critical option or extension entry in a certificate.
type Option struct {
	Name string
	Data []byte
}

// Permissions contains the certificate's critical options and extensions.
type Permissions struct {
	CriticalOptions []Option
	Extensions      []Option
}

// Certificate is a parsed OpenSSH user or host certificate.
//
// The field layout follows golang.org/x/crypto/ssh's Certificate so that it is
// familiar to SSH service code.
type Certificate struct {
	Nonce           []byte
	Key             PublicKey
	Serial          uint64
	CertType        uint32
	KeyId           string
	ValidPrincipals []string
	ValidAfter      uint64
	ValidBefore     uint64
	Permissions
	Reserved     []byte
	SignatureKey PublicKey
	Signature    *Signature

	certType    string
	raw         []byte
	signedBytes []byte
}

// Type returns the certificate key type, for example
// "ssh-ed25519-cert-v01@openssh.com".
func (c *Certificate) Type() string {
	return c.certType
}

// Marshal serializes the certificate from its current fields, including the
// signature. For an unmodified parsed certificate this is byte-for-byte the
// wire encoding that was received.
func (c *Certificate) Marshal() []byte {
	signed, err := c.rebuildSignedData()
	if err != nil {
		return append([]byte(nil), c.raw...)
	}
	if c.Signature == nil {
		return signed
	}
	return appendString(signed, c.Signature.Marshal())
}

// SignedBytes returns the exact byte range covered by the certificate
// signature. It ends immediately before the signature field, so it includes
// the nonce, key, serial, type, key ID, principals, validity times, critical
// options, extensions, reserved field, and signing key.
func (c *Certificate) SignedBytes() []byte {
	return append([]byte(nil), c.signedBytes...)
}

// Verify implements the PublicKey interface for the certified key. It verifies
// a user or session signature with the key inside this certificate; it does not
// verify the CA's certificate signature. Use VerifySignature or
// VerifyCertificate for that.
func (c *Certificate) Verify(data []byte, sig *Signature) error {
	if c.Key == nil {
		return fmt.Errorf("%w: certificate has no key", ErrInvalidCertificate)
	}
	return c.Key.Verify(data, sig)
}

// ParseCertificate parses an OpenSSH certificate. It accepts either the binary
// certificate blob or an authorized_keys-style certificate line.
func ParseCertificate(in []byte) (*Certificate, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrInvalidCertificate)
	}

	var lastErr error
	if cert, err := parseCertificateBlob(in); err == nil {
		return cert, nil
	} else {
		lastErr = err
	}

	trimmed := bytes.TrimRight(in, "\r\n")
	if len(trimmed) != len(in) {
		if cert, err := parseCertificateBlob(trimmed); err == nil {
			return cert, nil
		}
	}

	blob, err := decodeAuthorizedKeyLine(in, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, errors.Join(lastErr, err))
	}
	cert, err := parseCertificateBlob(blob)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
	}
	return cert, nil
}

func parseCertificateBlob(blob []byte) (*Certificate, error) {
	r := &wireReader{data: blob}

	certTypeBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: certificate type: %v", ErrInvalidCertificate, err)
	}
	certType := string(certTypeBytes)
	baseType, err := certificateBaseKeyType(certType)
	if err != nil {
		return nil, err
	}

	nonce, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: nonce: %v", ErrInvalidCertificate, err)
	}
	key, err := readCertificateKey(r, baseType)
	if err != nil {
		return nil, fmt.Errorf("%w: key: %v", ErrInvalidCertificate, err)
	}

	serial, err := r.readUint64()
	if err != nil {
		return nil, fmt.Errorf("%w: serial: %v", ErrInvalidCertificate, err)
	}
	certTypeValue, err := r.readUint32()
	if err != nil {
		return nil, fmt.Errorf("%w: certificate type value: %v", ErrInvalidCertificate, err)
	}
	if certTypeValue != UserCert && certTypeValue != HostCert {
		return nil, fmt.Errorf("%w: unknown certificate type %d", ErrInvalidCertificate, certTypeValue)
	}
	keyIDBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: key ID: %v", ErrInvalidCertificate, err)
	}
	principalsBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: principals: %v", ErrInvalidCertificate, err)
	}
	validAfter, err := r.readUint64()
	if err != nil {
		return nil, fmt.Errorf("%w: valid after: %v", ErrInvalidCertificate, err)
	}
	validBefore, err := r.readUint64()
	if err != nil {
		return nil, fmt.Errorf("%w: valid before: %v", ErrInvalidCertificate, err)
	}
	criticalBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: critical options: %v", ErrInvalidCertificate, err)
	}
	extensionsBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: extensions: %v", ErrInvalidCertificate, err)
	}
	reserved, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: reserved: %v", ErrInvalidCertificate, err)
	}
	signatureKeyBlob, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: signature key: %v", ErrInvalidCertificate, err)
	}
	signatureKey, err := parsePublicKeyBlob(signatureKeyBlob)
	if err != nil {
		return nil, fmt.Errorf("%w: signature key: %v", ErrInvalidCertificate, err)
	}

	// Everything through the signature key is covered by the signature. Record
	// the exact prefix before consuming the signature field.
	signedStart := 0
	signedEnd := r.off
	signedBytes := append([]byte(nil), blob[signedStart:signedEnd]...)

	signatureBlob, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: signature: %v", ErrInvalidCertificate, err)
	}
	if r.remaining() != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes after signature", ErrInvalidCertificate, r.remaining())
	}
	signature, err := parseSignatureBlob(signatureBlob)
	if err != nil {
		return nil, fmt.Errorf("%w: signature: %v", ErrInvalidCertificate, err)
	}

	criticalOptions, err := parseOptions(criticalBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: critical options: %v", ErrInvalidCertificate, err)
	}
	extensions, err := parseOptions(extensionsBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: extensions: %v", ErrInvalidCertificate, err)
	}

	var principals []string
	if len(principalsBytes) > 0 {
		principals = strings.Split(string(principalsBytes), ",")
	}

	return &Certificate{
		Nonce:           append([]byte(nil), nonce...),
		Key:             key,
		Serial:          serial,
		CertType:        certTypeValue,
		KeyId:           string(keyIDBytes),
		ValidPrincipals: principals,
		ValidAfter:      validAfter,
		ValidBefore:     validBefore,
		Permissions: Permissions{
			CriticalOptions: criticalOptions,
			Extensions:      extensions,
		},
		Reserved:     append([]byte(nil), reserved...),
		SignatureKey: signatureKey,
		Signature:    signature,
		certType:     certType,
		raw:          append([]byte(nil), blob...),
		signedBytes:  signedBytes,
	}, nil
}

func parseSignatureBlob(blob []byte) (*Signature, error) {
	r := &wireReader{data: blob}
	formatBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("signature format: %v", err)
	}
	sigBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("signature data: %v", err)
	}
	if r.remaining() != 0 {
		return nil, fmt.Errorf("%d trailing bytes in signature blob", r.remaining())
	}
	return &Signature{
		Format: string(formatBytes),
		Blob:   append([]byte(nil), sigBytes...),
	}, nil
}

func parseOptions(raw []byte) ([]Option, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	r := &wireReader{data: raw}
	var options []Option
	for r.remaining() > 0 {
		nameBytes, err := r.readString()
		if err != nil {
			return nil, fmt.Errorf("option name: %v", err)
		}
		if len(nameBytes) == 0 {
			return nil, errors.New("empty option name")
		}
		data, err := r.readString()
		if err != nil {
			return nil, fmt.Errorf("option %q value: %v", nameBytes, err)
		}
		options = append(options, Option{
			Name: string(nameBytes),
			Data: append([]byte(nil), data...),
		})
	}
	return options, nil
}

func marshalOptions(options []Option) []byte {
	var out []byte
	for _, option := range options {
		out = appendString(out, []byte(option.Name))
		out = appendString(out, option.Data)
	}
	return out
}

// rebuildSignedData re-encodes the current exported field values. Comparing it
// with the bytes that were actually signed catches callers that modify parsed
// fields such as CriticalOptions after parsing.
func (c *Certificate) rebuildSignedData() ([]byte, error) {
	if c.Key == nil {
		return nil, fmt.Errorf("%w: certificate has no key", ErrInvalidCertificate)
	}
	if c.SignatureKey == nil {
		return nil, fmt.Errorf("%w: certificate has no signature key", ErrInvalidCertificate)
	}

	var out []byte
	out = appendString(out, []byte(c.certType))
	out = appendString(out, c.Nonce)
	keyFields, err := marshalCertificateKeyFields(c.Key)
	if err != nil {
		return nil, err
	}
	out = append(out, keyFields...)
	out = appendUint64(out, c.Serial)
	out = appendUint32(out, c.CertType)
	out = appendString(out, []byte(c.KeyId))
	out = appendString(out, []byte(strings.Join(c.ValidPrincipals, ",")))
	out = appendUint64(out, c.ValidAfter)
	out = appendUint64(out, c.ValidBefore)
	out = appendString(out, marshalOptions(c.CriticalOptions))
	out = appendString(out, marshalOptions(c.Extensions))
	out = appendString(out, c.Reserved)
	out = appendString(out, c.SignatureKey.Marshal())
	return out, nil
}

func (c *Certificate) checkFieldsUnmodified() error {
	signed, err := c.rebuildSignedData()
	if err != nil {
		return err
	}
	if !bytes.Equal(signed, c.signedBytes) {
		return ErrCertificateFieldsModified
	}
	return nil
}

// readCertificateKey reads the key-specific fields embedded in a certificate.
// Unlike a standalone public key blob, the certificate format does not repeat
// the key type after the certificate type string.
func readCertificateKey(r *wireReader, baseType string) (PublicKey, error) {
	start := r.off
	switch baseType {
	case "ssh-ed25519":
		if _, err := r.readString(); err != nil {
			return nil, fmt.Errorf("ed25519 public key: %v", err)
		}
	case "ssh-rsa":
		if _, err := r.readMPInt(); err != nil {
			return nil, fmt.Errorf("RSA exponent: %v", err)
		}
		if _, err := r.readMPInt(); err != nil {
			return nil, fmt.Errorf("RSA modulus: %v", err)
		}
	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		if _, err := r.readString(); err != nil {
			return nil, fmt.Errorf("ECDSA curve: %v", err)
		}
		if _, err := r.readString(); err != nil {
			return nil, fmt.Errorf("ECDSA point: %v", err)
		}
	case "ssh-dss":
		for _, name := range []string{"p", "q", "g", "y"} {
			if _, err := r.readMPInt(); err != nil {
				return nil, fmt.Errorf("DSA %s: %v", name, err)
			}
		}
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, baseType)
	}
	if start == r.off {
		return nil, errors.New("empty certificate key")
	}

	keyFields := r.data[start:r.off]
	blob := appendString(nil, []byte(baseType))
	blob = append(blob, keyFields...)
	return parsePublicKeyBlob(blob)
}

// marshalCertificateKeyFields returns the key-specific fields used inside a
// certificate. A standalone SSH public key encoding is the key type followed
// by exactly these fields, so they can be recovered by removing the type.
func marshalCertificateKeyFields(key PublicKey) ([]byte, error) {
	blob := key.Marshal()
	r := &wireReader{data: blob}
	typBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: key type: %v", ErrInvalidPublicKey, err)
	}
	if string(typBytes) != key.Type() {
		return nil, fmt.Errorf("%w: key type mismatch", ErrInvalidPublicKey)
	}
	return append([]byte(nil), blob[r.off:]...), nil
}

func certificateBaseKeyType(certType string) (string, error) {
	if !strings.HasSuffix(certType, CertificateSuffix) {
		return "", fmt.Errorf("%w: key type %q is not a certificate type", ErrInvalidCertificate, certType)
	}
	base := strings.TrimSuffix(certType, CertificateSuffix)
	switch base {
	case "ssh-rsa", "ssh-dss", "ssh-ed25519",
		"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		return base, nil
	}
	return "", fmt.Errorf("%w: unsupported certificate key type %q", ErrUnsupportedAlgorithm, certType)
}
