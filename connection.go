package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"math/big"
)

// Connection describes the transport-authenticated client identity for the
// current request. ClientCertDER is the peer certificate's DER bytes, as
// supplied by crypto/tls. JWKThumbprint is optional and, when empty, is
// derived from the certificate's public key if a token carries cnf.jkt.
type Connection struct {
	ClientCertDER []byte
	JWKThumbprint string
}

// NewConnection returns a connection carrying the given DER-encoded client
// certificate. The slice is copied so later mutation by the caller cannot
// change an authenticated connection.
func NewConnection(certDER []byte) Connection {
	return Connection{ClientCertDER: cloneBytes(certDER)}
}

// HasClientCertificate reports whether the connection has peer certificate
// bytes.
func (c Connection) HasClientCertificate() bool {
	return len(c.ClientCertDER) > 0
}

// CertificateFingerprint returns the x5t#S256 value for the connection's
// client certificate.
func (c Connection) CertificateFingerprint() (string, error) {
	if !c.HasClientCertificate() {
		return "", ErrNoClientCertificate
	}
	return CertificateFingerprint(c.ClientCertDER)
}

func (c Connection) keyThumbprint() (string, error) {
	if c.JWKThumbprint != "" {
		if err := ValidateX5TS256(c.JWKThumbprint); err != nil {
			return "", fmt.Errorf("%w: connection JWK thumbprint: %v", ErrInvalidFingerprint, err)
		}
		return c.JWKThumbprint, nil
	}
	if !c.HasClientCertificate() {
		return "", ErrJKTUnavailable
	}
	cert, err := x509.ParseCertificate(c.ClientCertDER)
	if err != nil {
		return "", fmt.Errorf("%w: cannot derive JWK thumbprint from certificate: %v", ErrJKTUnavailable, err)
	}
	return JWKThumbprint(cert.PublicKey)
}

// JWKThumbprint computes the RFC 7638 SHA-256 JWK thumbprint for a public key
// and encodes it as unpadded base64url, which is the format used by cnf.jkt.
func JWKThumbprint(publicKey crypto.PublicKey) (string, error) {
	var canonical string

	switch key := publicKey.(type) {
	case *rsa.PublicKey:
		if key.N == nil || key.E <= 0 {
			return "", fmt.Errorf("%w: invalid RSA public key", ErrJKTUnavailable)
		}
		canonical = fmt.Sprintf(`{"e":"%s","kty":"RSA","n":"%s"}`,
			base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			base64.RawURLEncoding.EncodeToString(key.N.Bytes()))
	case *ecdsa.PublicKey:
		if key.Curve == nil || key.X == nil || key.Y == nil {
			return "", fmt.Errorf("%w: invalid ECDSA public key", ErrJKTUnavailable)
		}
		curve, size, ok := jwkCurve(key.Curve)
		if !ok {
			return "", fmt.Errorf("%w: unsupported ECDSA curve", ErrJKTUnavailable)
		}
		x := make([]byte, size)
		y := make([]byte, size)
		key.X.FillBytes(x)
		key.Y.FillBytes(y)
		canonical = fmt.Sprintf(`{"crv":"%s","kty":"EC","x":"%s","y":"%s"}`,
			curve,
			base64.RawURLEncoding.EncodeToString(x),
			base64.RawURLEncoding.EncodeToString(y))
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize {
			return "", fmt.Errorf("%w: invalid Ed25519 public key", ErrJKTUnavailable)
		}
		canonical = fmt.Sprintf(`{"crv":"Ed25519","kty":"OKP","x":"%s"}`,
			base64.RawURLEncoding.EncodeToString(key))
	default:
		return "", fmt.Errorf("%w: unsupported public key type %T", ErrJKTUnavailable, publicKey)
	}

	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func jwkCurve(curve elliptic.Curve) (string, int, bool) {
	switch curve {
	case elliptic.P256():
		return "P-256", 32, true
	case elliptic.P384():
		return "P-384", 48, true
	case elliptic.P521():
		return "P-521", 66, true
	default:
		return "", 0, false
	}
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
