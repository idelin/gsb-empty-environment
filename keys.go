package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
)

func hmacSecret(key any) ([]byte, error) {
	switch value := key.(type) {
	case []byte:
		if len(value) == 0 {
			return nil, fmt.Errorf("%w: empty HMAC secret", ErrInvalidKey)
		}
		if looksLikeAsymmetricKeyMaterial(value) {
			return nil, fmt.Errorf("%w: refusing to use an asymmetric key as an HMAC secret", ErrInvalidKey)
		}
		return value, nil
	case string:
		if value == "" {
			return nil, fmt.Errorf("%w: empty HMAC secret", ErrInvalidKey)
		}
		if strings.Contains(value, "-----BEGIN ") {
			return nil, fmt.Errorf("%w: refusing to use an asymmetric key as an HMAC secret", ErrInvalidKey)
		}
		return []byte(value), nil
	case ed25519.PublicKey, ed25519.PrivateKey:
		return nil, fmt.Errorf("%w: Ed25519 key cannot be used for HMAC", ErrInvalidKey)
	case *rsa.PublicKey, *rsa.PrivateKey, *ecdsa.PublicKey, *ecdsa.PrivateKey:
		return nil, fmt.Errorf("%w: asymmetric key cannot be used for HMAC", ErrInvalidKey)
	case crypto.Signer, *x509.Certificate:
		return nil, fmt.Errorf("%w: asymmetric key cannot be used for HMAC", ErrInvalidKey)
	default:
		return nil, fmt.Errorf("%w: HMAC requires a byte slice or string", ErrInvalidKey)
	}
}

func rsaVerificationKey(key any) (*rsa.PublicKey, error) {
	switch value := key.(type) {
	case *rsa.PublicKey:
		if value == nil || value.N == nil || value.E <= 0 {
			return nil, fmt.Errorf("%w: invalid RSA public key", ErrInvalidKey)
		}
		return value, nil
	case rsa.PublicKey:
		return rsaVerificationKey(&value)
	case *rsa.PrivateKey:
		if value == nil {
			return nil, fmt.Errorf("%w: invalid RSA private key", ErrInvalidKey)
		}
		return &value.PublicKey, nil
	case *x509.Certificate:
		if value == nil {
			return nil, fmt.Errorf("%w: invalid certificate", ErrInvalidKey)
		}
		return rsaVerificationKey(value.PublicKey)
	case crypto.Signer:
		return rsaVerificationKey(value.Public())
	case []byte:
		parsed, err := parseAsymmetricKey(value)
		if err != nil {
			return nil, err
		}
		return rsaVerificationKey(parsed)
	case string:
		parsed, err := parseAsymmetricKey([]byte(value))
		if err != nil {
			return nil, err
		}
		return rsaVerificationKey(parsed)
	default:
		return nil, fmt.Errorf("%w: RSA verification requires an RSA public key", ErrInvalidKey)
	}
}

func ecdsaVerificationKey(key any, curveBits int) (*ecdsa.PublicKey, error) {
	var publicKey *ecdsa.PublicKey

	switch value := key.(type) {
	case *ecdsa.PublicKey:
		if value == nil {
			return nil, fmt.Errorf("%w: invalid ECDSA public key", ErrInvalidKey)
		}
		publicKey = value
	case ecdsa.PublicKey:
		publicKey = &value
	case *ecdsa.PrivateKey:
		if value == nil {
			return nil, fmt.Errorf("%w: invalid ECDSA private key", ErrInvalidKey)
		}
		publicKey = &value.PublicKey
	case *x509.Certificate:
		if value == nil {
			return nil, fmt.Errorf("%w: invalid certificate", ErrInvalidKey)
		}
		return ecdsaVerificationKey(value.PublicKey, curveBits)
	case crypto.Signer:
		return ecdsaVerificationKey(value.Public(), curveBits)
	case []byte:
		parsed, err := parseAsymmetricKey(value)
		if err != nil {
			return nil, err
		}
		return ecdsaVerificationKey(parsed, curveBits)
	case string:
		parsed, err := parseAsymmetricKey([]byte(value))
		if err != nil {
			return nil, err
		}
		return ecdsaVerificationKey(parsed, curveBits)
	default:
		return nil, fmt.Errorf("%w: ECDSA verification requires an ECDSA public key", ErrInvalidKey)
	}

	if publicKey.Curve == nil || publicKey.X == nil || publicKey.Y == nil {
		return nil, fmt.Errorf("%w: invalid ECDSA public key", ErrInvalidKey)
	}
	bits := publicKey.Curve.Params().BitSize
	if bits != curveBits {
		return nil, fmt.Errorf("%w: curve has %d bits, algorithm requires %d", ErrInvalidKey, bits, curveBits)
	}
	return publicKey, nil
}

func ed25519VerificationKey(key any) (ed25519.PublicKey, error) {
	switch value := key.(type) {
	case ed25519.PublicKey:
		if len(value) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: invalid Ed25519 public key", ErrInvalidKey)
		}
		return value, nil
	case ed25519.PrivateKey:
		if len(value) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("%w: invalid Ed25519 private key", ErrInvalidKey)
		}
		return value.Public().(ed25519.PublicKey), nil
	case *x509.Certificate:
		if value == nil {
			return nil, fmt.Errorf("%w: invalid certificate", ErrInvalidKey)
		}
		return ed25519VerificationKey(value.PublicKey)
	case crypto.Signer:
		return ed25519VerificationKey(value.Public())
	case []byte:
		if len(value) == ed25519.PublicKeySize {
			return ed25519.PublicKey(value), nil
		}
		parsed, err := parseAsymmetricKey(value)
		if err != nil {
			return nil, err
		}
		return ed25519VerificationKey(parsed)
	case string:
		if len(value) == ed25519.PublicKeySize {
			return ed25519.PublicKey([]byte(value)), nil
		}
		parsed, err := parseAsymmetricKey([]byte(value))
		if err != nil {
			return nil, err
		}
		return ed25519VerificationKey(parsed)
	default:
		return nil, fmt.Errorf("%w: EdDSA requires an Ed25519 public key", ErrInvalidKey)
	}
}

func parseAsymmetricKey(data []byte) (any, error) {
	if block, _ := pem.Decode(data); block != nil {
		switch block.Type {
		case "PUBLIC KEY":
			key, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
			}
			return key, nil
		case "RSA PUBLIC KEY":
			key, err := x509.ParsePKCS1PublicKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
			}
			return key, nil
		case "CERTIFICATE":
			certificate, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
			}
			return certificate, nil
		case "PRIVATE KEY":
			key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
			}
			return key, nil
		case "RSA PRIVATE KEY":
			key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
			}
			return key, nil
		case "EC PRIVATE KEY":
			key, err := x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
			}
			return key, nil
		default:
			return nil, fmt.Errorf("%w: unsupported PEM block %q", ErrInvalidKey, block.Type)
		}
	}

	if key, err := x509.ParsePKIXPublicKey(data); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PublicKey(data); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("%w: cannot parse public key", ErrInvalidKey)
}

func verifyECDSASignature(publicKey *ecdsa.PublicKey, digest, signature []byte, curveBits int) bool {
	componentSize := (curveBits + 7) / 8
	if len(signature) == 2*componentSize {
		r := new(big.Int).SetBytes(signature[:componentSize])
		s := new(big.Int).SetBytes(signature[componentSize:])
		if ecdsa.Verify(publicKey, digest, r, s) {
			return true
		}
	}

	var parsed struct {
		R *big.Int
		S *big.Int
	}
	if rest, err := asn1.Unmarshal(signature, &parsed); err == nil && len(rest) == 0 {
		if parsed.R != nil && parsed.S != nil && ecdsa.Verify(publicKey, digest, parsed.R, parsed.S) {
			return true
		}
	}
	return false
}

func verifyEd25519(publicKey ed25519.PublicKey, message, signature []byte) bool {
	return ed25519.Verify(publicKey, message, signature)
}

func looksLikePEM(data []byte) bool {
	return bytes.Contains(data, []byte("-----BEGIN "))
}

func looksLikeAsymmetricKeyMaterial(data []byte) bool {
	if looksLikePEM(data) {
		return true
	}
	if _, err := x509.ParsePKIXPublicKey(data); err == nil {
		return true
	}
	if _, err := x509.ParsePKCS1PublicKey(data); err == nil {
		return true
	}
	if _, err := x509.ParsePKCS8PrivateKey(data); err == nil {
		return true
	}
	return false
}
