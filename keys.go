package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"math/big"
	"strings"
)

// PublicKey is an SSH wire-format public key blob, for example the bytes
// returned by ssh.PublicKey.Marshal().
type PublicKey struct {
	Blob []byte
}

// parseSSHKeyType returns the algorithm name at the start of an SSH public
// key blob and the reader positioned at the algorithm-specific fields.
func parseSSHKeyType(blob []byte) (string, *wireReader, error) {
	if len(blob) == 0 {
		return "", nil, fmt.Errorf("empty ssh public key")
	}
	r := newWireReader(blob)
	typ, err := r.readStringValue()
	if err != nil {
		return "", nil, fmt.Errorf("parse ssh public key type: %w", err)
	}
	if typ == "" {
		return "", nil, fmt.Errorf("empty ssh public key type")
	}
	if r.remaining() == 0 {
		return "", nil, fmt.Errorf("ssh public key %q has no key material", typ)
	}
	return typ, r, nil
}

// ValidatePublicKeyBlob checks the outer SSH string framing of a public key.
// It deliberately does not require support for the key algorithm: a host key
// is an identity that may be compared byte-for-byte even when this package
// cannot verify signatures with it.
func ValidatePublicKeyBlob(blob []byte) error {
	_, _, err := parseSSHKeyType(blob)
	return err
}

func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}

func verifySSHSignature(keyBlob, signatureBlob, data []byte) error {
	if len(signatureBlob) == 0 {
		return fmt.Errorf("missing ssh signature")
	}

	kr := newWireReader(keyBlob)
	keyType, err := kr.readStringValue()
	if err != nil {
		return fmt.Errorf("parse public key: %w", err)
	}

	sr := newWireReader(signatureBlob)
	algorithm, err := sr.readStringValue()
	if err != nil {
		return fmt.Errorf("parse signature algorithm: %w", err)
	}
	sig, err := sr.readString()
	if err != nil {
		return fmt.Errorf("parse signature body: %w", err)
	}
	if err := sr.expectEnd(); err != nil {
		return fmt.Errorf("parse signature: %w", err)
	}

	switch keyType {
	case "ssh-ed25519":
		if algorithm != "ssh-ed25519" {
			return fmt.Errorf("signature algorithm %q cannot be used with %q", algorithm, keyType)
		}
		pubBytes, err := kr.readString()
		if err != nil {
			return fmt.Errorf("parse ed25519 public key: %w", err)
		}
		if err := kr.expectEnd(); err != nil {
			return fmt.Errorf("parse ed25519 public key: %w", err)
		}
		if len(pubBytes) != ed25519.PublicKeySize {
			return fmt.Errorf("invalid ed25519 public key length %d", len(pubBytes))
		}
		if len(sig) != ed25519.SignatureSize {
			return fmt.Errorf("invalid ed25519 signature length %d", len(sig))
		}
		if !ed25519.Verify(ed25519.PublicKey(pubBytes), data, sig) {
			return fmt.Errorf("ed25519 signature verification failed")
		}
		return nil

	case "ssh-rsa":
		pub, err := parseRSAPublicKey(kr)
		if err != nil {
			return err
		}
		var hash crypto.Hash
		var digest []byte
		switch algorithm {
		case "rsa-sha2-256":
			h := sha256.Sum256(data)
			hash, digest = crypto.SHA256, h[:]
		case "rsa-sha2-512":
			h := sha512.Sum512(data)
			hash, digest = crypto.SHA512, h[:]
		case "ssh-rsa":
			h := sha1.Sum(data)
			hash, digest = crypto.SHA1, h[:]
		default:
			return fmt.Errorf("signature algorithm %q cannot be used with %q", algorithm, keyType)
		}
		if err := rsa.VerifyPKCS1v15(pub, hash, digest, sig); err != nil {
			return fmt.Errorf("rsa signature verification failed: %w", err)
		}
		return nil

	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		pub, curveName, err := parseECDSAPublicKey(kr, keyType)
		if err != nil {
			return err
		}
		if algorithm != keyType {
			return fmt.Errorf("signature algorithm %q does not match %q", algorithm, keyType)
		}
		sigReader := newWireReader(sig)
		rBytes, err := sigReader.readString()
		if err != nil {
			return fmt.Errorf("parse ecdsa signature r: %w", err)
		}
		sBytes, err := sigReader.readString()
		if err != nil {
			return fmt.Errorf("parse ecdsa signature s: %w", err)
		}
		if err := sigReader.expectEnd(); err != nil {
			return fmt.Errorf("parse ecdsa signature: %w", err)
		}
		r := new(big.Int).SetBytes(rBytes)
		s := new(big.Int).SetBytes(sBytes)
		var digest []byte
		switch curveName {
		case "nistp256":
			h := sha256.Sum256(data)
			digest = h[:]
		case "nistp384":
			h := sha512.Sum384(data)
			digest = h[:]
		case "nistp521":
			h := sha512.Sum512(data)
			digest = h[:]
		default:
			return fmt.Errorf("unsupported ecdsa curve %q", curveName)
		}
		if !ecdsa.Verify(pub, digest, r, s) {
			return fmt.Errorf("ecdsa signature verification failed")
		}
		return nil

	default:
		return fmt.Errorf("unsupported host key algorithm %q for signature verification", keyType)
	}
}

func parseRSAPublicKey(r *wireReader) (*rsa.PublicKey, error) {
	eBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("parse rsa public exponent: %w", err)
	}
	nBytes, err := r.readString()
	if err != nil {
		return nil, fmt.Errorf("parse rsa modulus: %w", err)
	}
	if err := r.expectEnd(); err != nil {
		return nil, fmt.Errorf("parse rsa public key: %w", err)
	}
	e := new(big.Int).SetBytes(eBytes)
	n := new(big.Int).SetBytes(nBytes)
	if e.Sign() <= 0 || n.Sign() <= 0 || e.BitLen() > 32 {
		return nil, fmt.Errorf("invalid rsa public key")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func parseECDSAPublicKey(r *wireReader, keyType string) (*ecdsa.PublicKey, string, error) {
	curveName, err := r.readStringValue()
	if err != nil {
		return nil, "", fmt.Errorf("parse ecdsa curve: %w", err)
	}
	if curveName != strings.TrimPrefix(keyType, "ecdsa-sha2-") {
		return nil, "", fmt.Errorf("ecdsa curve %q does not match key type %q", curveName, keyType)
	}
	point, err := r.readString()
	if err != nil {
		return nil, "", fmt.Errorf("parse ecdsa point: %w", err)
	}
	if err := r.expectEnd(); err != nil {
		return nil, "", fmt.Errorf("parse ecdsa public key: %w", err)
	}
	var curve elliptic.Curve
	switch keyType {
	case "ecdsa-sha2-nistp256":
		curve = elliptic.P256()
	case "ecdsa-sha2-nistp384":
		curve = elliptic.P384()
	case "ecdsa-sha2-nistp521":
		curve = elliptic.P521()
	}
	x, y := elliptic.Unmarshal(curve, point)
	if x == nil || y == nil {
		return nil, "", fmt.Errorf("invalid ecdsa public key point")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, curveName, nil
}

func equalPublicKeys(a, b []byte) bool {
	return bytes.Equal(a, b)
}
