package netsec

import (
	"bytes"
	"crypto"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// PublicKey is a parsed SSH public key. It intentionally mirrors the small
// part of ssh.PublicKey that certificate verification needs.
type PublicKey interface {
	Type() string
	Marshal() []byte
	Verify(data []byte, sig *Signature) error
}

// Signature is an SSH signature blob.
type Signature struct {
	Format string
	Blob   []byte
	Rest   []byte
}

// Marshal returns the SSH wire representation of the signature.
func (s *Signature) Marshal() []byte {
	if s == nil {
		return nil
	}
	out := appendString(nil, []byte(s.Format))
	out = appendString(out, s.Blob)
	return append(out, s.Rest...)
}

type publicKey struct {
	typ        string
	blob       []byte
	rsaKey     *rsa.PublicKey
	ecdsaKey   *ecdsa.PublicKey
	ed25519Key ed25519.PublicKey
	dsaKey     *dsa.PublicKey
}

func (k *publicKey) Type() string {
	return k.typ
}

func (k *publicKey) Marshal() []byte {
	return append([]byte(nil), k.blob...)
}

func (k *publicKey) Verify(data []byte, sig *Signature) error {
	if sig == nil || sig.Format == "" {
		return fmt.Errorf("%w: missing signature", ErrInvalidSignature)
	}

	switch k.typ {
	case "ssh-ed25519":
		if sig.Format != "ssh-ed25519" {
			return fmt.Errorf("%w: signature algorithm %q does not match key type %q", ErrUnsupportedAlgorithm, sig.Format, k.typ)
		}
		if len(sig.Blob) != ed25519.SignatureSize {
			return fmt.Errorf("%w: invalid ed25519 signature length %d", ErrInvalidSignature, len(sig.Blob))
		}
		if !ed25519.Verify(k.ed25519Key, data, sig.Blob) {
			return fmt.Errorf("%w: ed25519 verification failed", ErrSignatureMismatch)
		}
		return nil

	case "ssh-rsa":
		var hash crypto.Hash
		var digest []byte
		switch sig.Format {
		case "ssh-rsa":
			sum := sha1.Sum(data)
			hash, digest = crypto.SHA1, sum[:]
		case "rsa-sha2-256":
			sum := sha256.Sum256(data)
			hash, digest = crypto.SHA256, sum[:]
		case "rsa-sha2-512":
			sum := sha512.Sum512(data)
			hash, digest = crypto.SHA512, sum[:]
		default:
			return fmt.Errorf("%w: unsupported RSA signature algorithm %q", ErrUnsupportedAlgorithm, sig.Format)
		}
		if err := rsa.VerifyPKCS1v15(k.rsaKey, hash, digest, sig.Blob); err != nil {
			return fmt.Errorf("%w: RSA verification failed", ErrSignatureMismatch)
		}
		return nil

	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		if sig.Format != k.typ {
			return fmt.Errorf("%w: signature algorithm %q does not match key type %q", ErrUnsupportedAlgorithm, sig.Format, k.typ)
		}
		var digest []byte
		switch k.typ {
		case "ecdsa-sha2-nistp256":
			sum := sha256.Sum256(data)
			digest = sum[:]
		case "ecdsa-sha2-nistp384":
			sum := sha512.Sum384(data)
			digest = sum[:]
		case "ecdsa-sha2-nistp521":
			sum := sha512.Sum512(data)
			digest = sum[:]
		}
		if !verifyECDSASignature(k.ecdsaKey, digest, sig.Blob) {
			return fmt.Errorf("%w: ECDSA verification failed", ErrSignatureMismatch)
		}
		return nil

	case "ssh-dss":
		if sig.Format != "ssh-dss" {
			return fmt.Errorf("%w: signature algorithm %q does not match key type %q", ErrUnsupportedAlgorithm, sig.Format, k.typ)
		}
		var parsed struct {
			R, S *big.Int
		}
		rest, err := asn1.Unmarshal(sig.Blob, &parsed)
		if err != nil || len(rest) != 0 || parsed.R == nil || parsed.S == nil {
			return fmt.Errorf("%w: invalid DSA signature", ErrInvalidSignature)
		}
		sum := sha1.Sum(data)
		if !dsa.Verify(k.dsaKey, sum[:], parsed.R, parsed.S) {
			return fmt.Errorf("%w: DSA verification failed", ErrSignatureMismatch)
		}
		return nil
	}

	return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, k.typ)
}

func verifyECDSASignature(key *ecdsa.PublicKey, digest, blob []byte) bool {
	// OpenSSH encodes ECDSA signatures as two SSH strings containing the
	// unsigned big-endian values of r and s.
	r := &wireReader{data: blob}
	rBytes, rErr := r.readString()
	sBytes, sErr := r.readString()
	if rErr == nil && sErr == nil && r.remaining() == 0 && len(rBytes) > 0 && len(sBytes) > 0 {
		rr := new(big.Int).SetBytes(rBytes)
		ss := new(big.Int).SetBytes(sBytes)
		if rr.Sign() > 0 && ss.Sign() > 0 {
			return ecdsa.Verify(key, digest, rr, ss)
		}
	}

	// Accept DER as well for callers that construct signatures with the
	// standard library instead of the SSH wire encoding.
	return ecdsa.VerifyASN1(key, digest, blob)
}

// ParsePublicKey parses an SSH public key from its binary wire form, an
// authorized_keys line, or a PEM-encoded public key.
func ParsePublicKey(in []byte) (PublicKey, error) {
	if key, err := parsePublicKeyBlob(in); err == nil {
		return key, nil
	}

	trimmed := bytes.TrimRight(in, "\r\n")
	if len(trimmed) != len(in) {
		if key, err := parsePublicKeyBlob(trimmed); err == nil {
			return key, nil
		}
	}

	if block, _ := pem.Decode(in); block != nil {
		key, err := parsePEMPublicKey(block)
		if err != nil {
			return nil, err
		}
		return key, nil
	}

	blob, err := decodeAuthorizedKeyLine(in, false)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPublicKey, err)
	}
	key, err := parsePublicKeyBlob(blob)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPublicKey, err)
	}
	return key, nil
}

// NewPublicKey converts a standard-library public key into an SSH public key.
func NewPublicKey(pub any) (PublicKey, error) {
	var blob []byte

	switch k := pub.(type) {
	case PublicKey:
		return k, nil
	case *rsa.PublicKey:
		if k == nil || k.N == nil || k.E <= 0 {
			return nil, fmt.Errorf("%w: invalid RSA key", ErrInvalidPublicKey)
		}
		blob = appendString(nil, []byte("ssh-rsa"))
		blob = appendMPInt(blob, big.NewInt(int64(k.E)))
		blob = appendMPInt(blob, k.N)
	case rsa.PublicKey:
		return NewPublicKey(&k)
	case *ecdsa.PublicKey:
		if k == nil || k.Curve == nil || k.X == nil || k.Y == nil {
			return nil, fmt.Errorf("%w: invalid ECDSA key", ErrInvalidPublicKey)
		}
		var keyType string
		var curveName string
		switch k.Curve.Params().Name {
		case "P-256":
			keyType = "ecdsa-sha2-nistp256"
			curveName = "nistp256"
		case "P-384":
			keyType = "ecdsa-sha2-nistp384"
			curveName = "nistp384"
		case "P-521":
			keyType = "ecdsa-sha2-nistp521"
			curveName = "nistp521"
		default:
			return nil, fmt.Errorf("%w: unsupported ECDSA curve %q", ErrUnsupportedAlgorithm, k.Curve.Params().Name)
		}
		blob = appendString(nil, []byte(keyType))
		blob = appendString(blob, []byte(curveName))
		blob = appendString(blob, elliptic.Marshal(k.Curve, k.X, k.Y))
	case ecdsa.PublicKey:
		return NewPublicKey(&k)
	case ed25519.PublicKey:
		if len(k) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: invalid ed25519 key length %d", ErrInvalidPublicKey, len(k))
		}
		blob = appendString(nil, []byte("ssh-ed25519"))
		blob = appendString(blob, k)
	case *dsa.PublicKey:
		if k == nil {
			return nil, fmt.Errorf("%w: invalid DSA key", ErrInvalidPublicKey)
		}
		blob = appendString(nil, []byte("ssh-dss"))
		blob = appendMPInt(blob, k.P)
		blob = appendMPInt(blob, k.Q)
		blob = appendMPInt(blob, k.G)
		blob = appendMPInt(blob, k.Y)
	default:
		return nil, fmt.Errorf("%w: unsupported public key type %T", ErrUnsupportedAlgorithm, pub)
	}

	return parsePublicKeyBlob(blob)
}

func parsePEMPublicKey(block *pem.Block) (PublicKey, error) {
	var pub any
	var err error

	switch block.Type {
	case "PUBLIC KEY":
		pub, err = x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		pub, err = x509.ParsePKCS1PublicKey(block.Bytes)
	case "CERTIFICATE":
		var cert *x509.Certificate
		cert, err = x509.ParseCertificate(block.Bytes)
		if err == nil {
			pub = cert.PublicKey
		}
	default:
		pub, err = x509.ParsePKIXPublicKey(block.Bytes)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: PEM public key: %v", ErrInvalidPublicKey, err)
	}
	key, err := NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("%w: PEM public key: %v", ErrInvalidPublicKey, err)
	}
	return key, nil
}

func parsePublicKeyBlob(blob []byte) (PublicKey, error) {
	r := &wireReader{data: blob}
	typBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("%w: key type: %v", ErrInvalidPublicKey, err)
	}
	typ := string(typBytes)
	key := &publicKey{typ: typ, blob: append([]byte(nil), blob...)}

	switch typ {
	case "ssh-rsa":
		e, err := r.readMPInt()
		if err != nil {
			return nil, fmt.Errorf("%w: RSA exponent: %v", ErrInvalidPublicKey, err)
		}
		n, err := r.readMPInt()
		if err != nil {
			return nil, fmt.Errorf("%w: RSA modulus: %v", ErrInvalidPublicKey, err)
		}
		if e.Sign() <= 0 || n.Sign() <= 0 {
			return nil, fmt.Errorf("%w: invalid RSA parameters", ErrInvalidPublicKey)
		}
		if !e.IsInt64() || e.Int64() > int64(^uint32(0)) {
			return nil, fmt.Errorf("%w: invalid RSA exponent", ErrInvalidPublicKey)
		}
		key.rsaKey = &rsa.PublicKey{N: n, E: int(e.Int64())}

	case "ssh-ed25519":
		pub, err := r.readString()
		if err != nil {
			return nil, fmt.Errorf("%w: ed25519 public key: %v", ErrInvalidPublicKey, err)
		}
		if len(pub) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: invalid ed25519 key length %d", ErrInvalidPublicKey, len(pub))
		}
		key.ed25519Key = ed25519.PublicKey(append([]byte(nil), pub...))

	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		curveNameBytes, err := r.readString()
		if err != nil {
			return nil, fmt.Errorf("%w: ECDSA curve: %v", ErrInvalidPublicKey, err)
		}
		var curve elliptic.Curve
		var expectedName string
		switch typ {
		case "ecdsa-sha2-nistp256":
			curve, expectedName = elliptic.P256(), "nistp256"
		case "ecdsa-sha2-nistp384":
			curve, expectedName = elliptic.P384(), "nistp384"
		case "ecdsa-sha2-nistp521":
			curve, expectedName = elliptic.P521(), "nistp521"
		}
		if string(curveNameBytes) != expectedName {
			return nil, fmt.Errorf("%w: curve %q does not match key type %q", ErrInvalidPublicKey, curveNameBytes, typ)
		}
		point, err := r.readString()
		if err != nil {
			return nil, fmt.Errorf("%w: ECDSA point: %v", ErrInvalidPublicKey, err)
		}
		x, y := elliptic.Unmarshal(curve, point)
		if x == nil {
			return nil, fmt.Errorf("%w: invalid ECDSA point", ErrInvalidPublicKey)
		}
		key.ecdsaKey = &ecdsa.PublicKey{Curve: curve, X: x, Y: y}

	case "ssh-dss":
		p, err := r.readMPInt()
		if err != nil {
			return nil, fmt.Errorf("%w: DSA p: %v", ErrInvalidPublicKey, err)
		}
		q, err := r.readMPInt()
		if err != nil {
			return nil, fmt.Errorf("%w: DSA q: %v", ErrInvalidPublicKey, err)
		}
		g, err := r.readMPInt()
		if err != nil {
			return nil, fmt.Errorf("%w: DSA g: %v", ErrInvalidPublicKey, err)
		}
		y, err := r.readMPInt()
		if err != nil {
			return nil, fmt.Errorf("%w: DSA y: %v", ErrInvalidPublicKey, err)
		}
		key.dsaKey = &dsa.PublicKey{Parameters: dsa.Parameters{P: p, Q: q, G: g}, Y: y}

	default:
		if strings.HasSuffix(typ, CertificateSuffix) {
			return nil, fmt.Errorf("%w: certificate used where a public key is required", ErrInvalidPublicKey)
		}
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, typ)
	}

	if r.remaining() != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes in public key", ErrInvalidPublicKey, r.remaining())
	}
	return key, nil
}

func decodeAuthorizedKeyLine(in []byte, requireCert bool) ([]byte, error) {
	for _, line := range bytes.Split(in, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || bytes.HasPrefix(line, []byte("#")) {
			continue
		}
		fields := strings.Fields(string(line))
		if len(fields) == 1 {
			blob, err := decodeBase64(fields[0])
			if err == nil {
				blobType, typeErr := wireString(blob)
				if typeErr == nil {
					isCert := strings.HasSuffix(blobType, CertificateSuffix)
					if requireCert == isCert && (requireCert || looksLikeSSHKeyType(blobType)) {
						return blob, nil
					}
				}
			}
		}
		for i := 0; i+1 < len(fields); i++ {
			typ := fields[i]
			if !looksLikeSSHKeyType(typ) {
				continue
			}
			blob, err := decodeBase64(fields[i+1])
			if err != nil {
				continue
			}
			blobType, err := wireString(blob)
			if err != nil || blobType != typ {
				continue
			}
			isCert := strings.HasSuffix(blobType, CertificateSuffix)
			if requireCert != isCert {
				continue
			}
			return blob, nil
		}
	}
	if requireCert {
		return nil, errors.New("no SSH certificate found")
	}
	return nil, errors.New("no SSH public key found")
}

func looksLikeSSHKeyType(s string) bool {
	return strings.HasPrefix(s, "ssh-") ||
		strings.HasPrefix(s, "ecdsa-sha2-") ||
		strings.HasPrefix(s, "sk-")
}

func decodeBase64(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

func wireString(blob []byte) (string, error) {
	r := &wireReader{data: blob}
	b, err := r.readString()
	if err != nil {
		return "", err
	}
	return string(b), nil
}
