package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Certificate is a delegation certificate in this package's own format. It
// deliberately is not an X.509 certificate and must not be parsed as one.
//
// A certificate grants the subject public key the intersection of Operations
// and ResourcePrefixes, valid during [NotBefore, NotAfter). MaxDelegationDepth
// is the number of further certificates the subject may issue.
type Certificate struct {
	SubjectPublicKey   ed25519.PublicKey
	IssuerID           string
	Serial             uint64
	Operations         []string
	ResourcePrefixes   []string
	MaxDelegationDepth uint32
	NotBefore          time.Time
	NotAfter           time.Time
	Signature          []byte
}

const (
	certificateMagic   = "NETSEC-DELEGATION-CERT"
	certificateVersion = uint16(1)

	maxCertificateSize = 1 << 20
	maxSetEntries      = 4096
	maxStringLength    = 1 << 16
)

const (
	fieldSubjectPublicKey = uint16(1)
	fieldIssuerID         = uint16(2)
	fieldSerial           = uint16(3)
	fieldOperations       = uint16(4)
	fieldResourcePrefixes = uint16(5)
	fieldMaxDepth         = uint16(6)
	fieldNotBefore        = uint16(7)
	fieldNotAfter         = uint16(8)
	fieldSignature        = uint16(9)
)

// NewCertificate constructs an unsigned certificate. The caller must sign it
// with the issuer's private key, normally through Sign.
func NewCertificate(
	subjectPublicKey ed25519.PublicKey,
	issuerID string,
	serial uint64,
	operations []string,
	resourcePrefixes []string,
	maxDelegationDepth uint32,
	notBefore, notAfter time.Time,
) *Certificate {
	return &Certificate{
		SubjectPublicKey:   append(ed25519.PublicKey(nil), subjectPublicKey...),
		IssuerID:           issuerID,
		Serial:             serial,
		Operations:         append([]string(nil), operations...),
		ResourcePrefixes:   append([]string(nil), resourcePrefixes...),
		MaxDelegationDepth: maxDelegationDepth,
		NotBefore:          notBefore,
		NotAfter:           notAfter,
	}
}

// KeyID returns the canonical identifier of an Ed25519 public key. Callers may
// use it as a certificate's IssuerID when the issuer has no external name.
func KeyID(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

// SubjectID returns the key identifier of the certificate subject.
func (c Certificate) SubjectID() string {
	return KeyID(c.SubjectPublicKey)
}

// SigningBytes returns the canonical length-prefixed encoding covered by the
// certificate signature. Every field is encoded as a tag, a 32-bit length and
// its value. Variable-length fields are encoded with 32-bit element lengths.
// The signature field itself is excluded.
func (c Certificate) SigningBytes() ([]byte, error) {
	encoded, err := c.signedEncoding()
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func (c Certificate) signedEncoding() ([]byte, error) {
	if err := c.validateFields(); err != nil {
		return nil, err
	}

	var b bytes.Buffer
	b.WriteString(certificateMagic)
	_ = binary.Write(&b, binary.BigEndian, certificateVersion)

	writeField(&b, fieldSubjectPublicKey, c.SubjectPublicKey)

	var issuer bytes.Buffer
	issuer.WriteString(c.IssuerID)
	writeField(&b, fieldIssuerID, issuer.Bytes())

	var serial [8]byte
	binary.BigEndian.PutUint64(serial[:], c.Serial)
	writeField(&b, fieldSerial, serial[:])

	writeField(&b, fieldOperations, encodeStringSet(c.Operations))
	writeField(&b, fieldResourcePrefixes, encodeStringSet(c.ResourcePrefixes))

	var depth [4]byte
	binary.BigEndian.PutUint32(depth[:], c.MaxDelegationDepth)
	writeField(&b, fieldMaxDepth, depth[:])

	writeField(&b, fieldNotBefore, encodeTime(c.NotBefore))
	writeField(&b, fieldNotAfter, encodeTime(c.NotAfter))
	if b.Len() > maxCertificateSize {
		return nil, fmt.Errorf("%w: encoded certificate too large", ErrInvalidCertificate)
	}
	return b.Bytes(), nil
}

func (c Certificate) validateFields() error {
	if len(c.SubjectPublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: subject public key has length %d, want %d",
			ErrInvalidCertificate, len(c.SubjectPublicKey), ed25519.PublicKeySize)
	}
	if c.IssuerID == "" {
		return fmt.Errorf("%w: empty issuer ID", ErrInvalidCertificate)
	}
	if len(c.IssuerID) > maxStringLength {
		return fmt.Errorf("%w: issuer ID too long", ErrInvalidCertificate)
	}
	if !c.NotBefore.Before(c.NotAfter) {
		return fmt.Errorf("%w: validity interval is empty or reversed", ErrInvalidCertificate)
	}
	if len(c.Operations) > maxSetEntries {
		return fmt.Errorf("%w: too many operations", ErrInvalidCertificate)
	}
	for _, operation := range c.Operations {
		if operation == "" {
			return fmt.Errorf("%w: empty operation", ErrInvalidCertificate)
		}
		if len(operation) > maxStringLength {
			return fmt.Errorf("%w: operation too long", ErrInvalidCertificate)
		}
	}
	if len(c.ResourcePrefixes) > maxSetEntries {
		return fmt.Errorf("%w: too many resource prefixes", ErrInvalidCertificate)
	}
	for _, prefix := range c.ResourcePrefixes {
		if _, err := parsePathPrefix(prefix); err != nil {
			return err
		}
	}
	return nil
}

func writeField(b *bytes.Buffer, tag uint16, value []byte) {
	var header [6]byte
	binary.BigEndian.PutUint16(header[0:2], tag)
	binary.BigEndian.PutUint32(header[2:6], uint32(len(value)))
	b.Write(header[:])
	b.Write(value)
}

func encodeStringSet(values []string) []byte {
	var b bytes.Buffer
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(values)))
	b.Write(count[:])
	for _, value := range values {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(value)))
		b.Write(length[:])
		b.WriteString(value)
	}
	return b.Bytes()
}

func encodeTime(t time.Time) []byte {
	t = t.UTC()
	var out [12]byte
	binary.BigEndian.PutUint64(out[0:8], uint64(t.Unix()))
	binary.BigEndian.PutUint32(out[8:12], uint32(t.Nanosecond()))
	return out[:]
}

func decodeTime(value []byte) (time.Time, error) {
	if len(value) != 12 {
		return time.Time{}, fmt.Errorf("%w: invalid time encoding", ErrInvalidCertificate)
	}
	seconds := int64(binary.BigEndian.Uint64(value[0:8]))
	nanos := int64(binary.BigEndian.Uint32(value[8:12]))
	if nanos < 0 || nanos >= int64(time.Second) {
		return time.Time{}, fmt.Errorf("%w: invalid nanoseconds", ErrInvalidCertificate)
	}
	return time.Unix(seconds, nanos).UTC(), nil
}

func normalizeOperations(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			return nil, fmt.Errorf("%w: empty operation", ErrInvalidCertificate)
		}
		if len(value) > maxStringLength {
			return nil, fmt.Errorf("%w: operation too long", ErrInvalidCertificate)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

// Sign computes the Ed25519 signature over the certificate's canonical field
// encoding and stores it in Signature.
func (c *Certificate) Sign(privateKey ed25519.PrivateKey) error {
	if c == nil {
		return fmt.Errorf("%w: nil certificate", ErrInvalidCertificate)
	}
	c.Signature = nil
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key has length %d, want %d",
			ErrSignature, len(privateKey), ed25519.PrivateKeySize)
	}
	message, err := c.signedEncoding()
	if err != nil {
		return err
	}
	c.Signature = ed25519.Sign(privateKey, message)
	return nil
}

// SignCertificate signs cert with the issuer private key.
func SignCertificate(cert *Certificate, privateKey ed25519.PrivateKey) error {
	return cert.Sign(privateKey)
}

// VerifySignature verifies the certificate signature with the supplied issuer
// public key.
func (c Certificate) VerifySignature(issuerPublicKey ed25519.PublicKey) error {
	if len(c.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature has length %d, want %d",
			ErrSignature, len(c.Signature), ed25519.SignatureSize)
	}
	if len(issuerPublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: issuer public key has length %d, want %d",
			ErrSignature, len(issuerPublicKey), ed25519.PublicKeySize)
	}
	message, err := c.signedEncoding()
	if err != nil {
		return err
	}
	if !ed25519.Verify(issuerPublicKey, message, c.Signature) {
		return ErrSignature
	}
	return nil
}

// MarshalBinary serializes the complete certificate, including its signature,
// using the same length-prefixed field encoding that is signed.
func (c Certificate) MarshalBinary() ([]byte, error) {
	if len(c.Signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: signature has length %d, want %d",
			ErrInvalidCertificate, len(c.Signature), ed25519.SignatureSize)
	}
	signed, err := c.signedEncoding()
	if err != nil {
		return nil, err
	}
	var tail bytes.Buffer
	writeField(&tail, fieldSignature, c.Signature)
	out := append(signed, tail.Bytes()...)
	if len(out) > maxCertificateSize {
		return nil, fmt.Errorf("%w: encoded certificate too large", ErrInvalidCertificate)
	}
	return out, nil
}

// ParseCertificate decodes a certificate produced by MarshalBinary. Unknown,
// duplicate and truncated fields are rejected.
func ParseCertificate(data []byte) (*Certificate, error) {
	if len(data) > maxCertificateSize {
		return nil, fmt.Errorf("%w: encoded certificate too large", ErrInvalidCertificate)
	}
	if !bytes.HasPrefix(data, []byte(certificateMagic)) {
		return nil, fmt.Errorf("%w: bad magic", ErrInvalidCertificate)
	}
	data = data[len(certificateMagic):]
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: truncated version", ErrInvalidCertificate)
	}
	if version := binary.BigEndian.Uint16(data[:2]); version != certificateVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidCertificate, version)
	}
	data = data[2:]

	fields := make(map[uint16][]byte, 9)
	for len(data) > 0 {
		if len(data) < 6 {
			return nil, fmt.Errorf("%w: truncated field header", ErrInvalidCertificate)
		}
		tag := binary.BigEndian.Uint16(data[0:2])
		length := binary.BigEndian.Uint32(data[2:6])
		data = data[6:]
		if uint64(length) > uint64(len(data)) {
			return nil, fmt.Errorf("%w: truncated field value", ErrInvalidCertificate)
		}
		if _, ok := fields[tag]; ok {
			return nil, fmt.Errorf("%w: duplicate field %d", ErrInvalidCertificate, tag)
		}
		switch tag {
		case fieldSubjectPublicKey, fieldIssuerID, fieldSerial, fieldOperations,
			fieldResourcePrefixes, fieldMaxDepth, fieldNotBefore, fieldNotAfter, fieldSignature:
		default:
			return nil, fmt.Errorf("%w: unknown field %d", ErrInvalidCertificate, tag)
		}
		fields[tag] = data[:length]
		data = data[length:]
	}

	for _, tag := range []uint16{
		fieldSubjectPublicKey, fieldIssuerID, fieldSerial, fieldOperations,
		fieldResourcePrefixes, fieldMaxDepth, fieldNotBefore, fieldNotAfter, fieldSignature,
	} {
		if _, ok := fields[tag]; !ok {
			return nil, fmt.Errorf("%w: missing field %d", ErrInvalidCertificate, tag)
		}
	}

	cert := &Certificate{}
	if len(fields[fieldSubjectPublicKey]) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: invalid subject public key length", ErrInvalidCertificate)
	}
	cert.SubjectPublicKey = append(ed25519.PublicKey(nil), fields[fieldSubjectPublicKey]...)
	cert.IssuerID = string(fields[fieldIssuerID])
	if len(fields[fieldSerial]) != 8 {
		return nil, fmt.Errorf("%w: invalid serial encoding", ErrInvalidCertificate)
	}
	cert.Serial = binary.BigEndian.Uint64(fields[fieldSerial])

	operations, err := decodeStringSet(fields[fieldOperations])
	if err != nil {
		return nil, err
	}
	cert.Operations = operations
	prefixValues, err := decodeStringSet(fields[fieldResourcePrefixes])
	if err != nil {
		return nil, err
	}
	cert.ResourcePrefixes = prefixValues

	if len(fields[fieldMaxDepth]) != 4 {
		return nil, fmt.Errorf("%w: invalid depth encoding", ErrInvalidCertificate)
	}
	cert.MaxDelegationDepth = binary.BigEndian.Uint32(fields[fieldMaxDepth])

	cert.NotBefore, err = decodeTime(fields[fieldNotBefore])
	if err != nil {
		return nil, err
	}
	cert.NotAfter, err = decodeTime(fields[fieldNotAfter])
	if err != nil {
		return nil, err
	}

	if len(fields[fieldSignature]) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: invalid signature length", ErrInvalidCertificate)
	}
	cert.Signature = append([]byte(nil), fields[fieldSignature]...)

	if err := cert.validateFields(); err != nil {
		return nil, err
	}
	return cert, nil
}

func decodeStringSet(data []byte) ([]string, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("%w: truncated set", ErrInvalidCertificate)
	}
	count := binary.BigEndian.Uint32(data[:4])
	if count > maxSetEntries {
		return nil, fmt.Errorf("%w: too many set entries", ErrInvalidCertificate)
	}
	data = data[4:]
	out := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		if len(data) < 4 {
			return nil, fmt.Errorf("%w: truncated set element", ErrInvalidCertificate)
		}
		length := binary.BigEndian.Uint32(data[:4])
		data = data[4:]
		if uint64(length) > uint64(len(data)) || length > maxStringLength {
			return nil, fmt.Errorf("%w: invalid set element length", ErrInvalidCertificate)
		}
		out = append(out, string(data[:length]))
		data = data[length:]
	}
	if len(data) != 0 {
		return nil, fmt.Errorf("%w: trailing set data", ErrInvalidCertificate)
	}
	return out, nil
}

// Equal reports whether two certificates have identical signed fields and
// signature. It is useful for locating a caller-supplied leaf certificate in
// a pool.
func (c Certificate) Equal(other Certificate) bool {
	if c.IssuerID != other.IssuerID || c.Serial != other.Serial ||
		c.MaxDelegationDepth != other.MaxDelegationDepth ||
		!c.NotBefore.Equal(other.NotBefore) || !c.NotAfter.Equal(other.NotAfter) ||
		!bytes.Equal(c.SubjectPublicKey, other.SubjectPublicKey) ||
		!bytes.Equal(c.Signature, other.Signature) {
		return false
	}
	if !slices.Equal(c.Operations, other.Operations) ||
		!slices.Equal(c.ResourcePrefixes, other.ResourcePrefixes) {
		return false
	}
	return true
}

func keyEncodings(publicKey ed25519.PublicKey) []string {
	sum := sha256.Sum256(publicKey)
	return []string{
		hex.EncodeToString(sum[:]),
		hex.EncodeToString(publicKey),
		base64.StdEncoding.EncodeToString(publicKey),
		base64.RawStdEncoding.EncodeToString(publicKey),
		base64.URLEncoding.EncodeToString(publicKey),
		base64.RawURLEncoding.EncodeToString(publicKey),
		base64.StdEncoding.EncodeToString(sum[:]),
		base64.RawURLEncoding.EncodeToString(sum[:]),
		string(sum[:]),
		string(publicKey),
	}
}

func issuerIDMatchesKey(id string, publicKey ed25519.PublicKey) bool {
	if id == KeyID(publicKey) || strings.EqualFold(id, KeyID(publicKey)) {
		return true
	}
	if strings.EqualFold(id, hex.EncodeToString(publicKey)) {
		return true
	}
	for _, candidate := range keyEncodings(publicKey)[1:] {
		if id == candidate {
			return true
		}
	}
	return false
}
