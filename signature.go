package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
)

var ErrUnsupportedKey = errors.New("unsupported public key type")

func cloneTrustedKeys(keys []crypto.PublicKey) []crypto.PublicKey {
	cloned := make([]crypto.PublicKey, 0, len(keys))
	for _, key := range keys {
		if key == nil {
			continue
		}
		switch pub := key.(type) {
		case *ed25519.PublicKey:
			if pub == nil {
				continue
			}
		case *rsa.PublicKey:
			if pub == nil {
				continue
			}
		case *ecdsa.PublicKey:
			if pub == nil {
				continue
			}
		}
		cloned = append(cloned, clonePublicKey(key))
	}
	return cloned
}

func clonePublicKey(key crypto.PublicKey) crypto.PublicKey {
	switch pub := key.(type) {
	case ed25519.PublicKey:
		return append(ed25519.PublicKey(nil), pub...)
	case *ed25519.PublicKey:
		if pub == nil {
			return (*ed25519.PublicKey)(nil)
		}
		copy := append(ed25519.PublicKey(nil), (*pub)...)
		return &copy
	case *rsa.PublicKey:
		if pub == nil {
			return (*rsa.PublicKey)(nil)
		}
		copy := *pub
		copy.N = new(big.Int).Set(pub.N)
		return &copy
	case *ecdsa.PublicKey:
		if pub == nil {
			return (*ecdsa.PublicKey)(nil)
		}
		copy := *pub
		copy.X = new(big.Int).Set(pub.X)
		copy.Y = new(big.Int).Set(pub.Y)
		return &copy
	default:
		return key
	}
}

// SignPortable creates a type-5 portable signature over PortableSignatureInput.
// Ed25519 signs the message directly; RSA and ECDSA sign its SHA-256 digest.
func SignPortable(state FileState, signer crypto.Signer) ([]byte, error) {
	if signer == nil {
		return nil, ErrUnsupportedKey
	}
	message := PortableSignatureInput(state)

	var (
		signature []byte
		err       error
	)
	switch pub := signer.Public().(type) {
	case ed25519.PublicKey, *ed25519.PublicKey:
		signature, err = signer.Sign(rand.Reader, message, crypto.Hash(0))
	case *rsa.PublicKey, *ecdsa.PublicKey:
		digest := sha256.Sum256(message)
		signature, err = signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedKey, pub)
	}
	if err != nil {
		return nil, err
	}
	if len(signature) == 0 {
		return nil, errors.New("signer returned an empty signature")
	}
	return append([]byte{EVMTypePortableSignature}, signature...), nil
}

func SealPortableSignature(state FileState, signer crypto.Signer) ([]byte, error) {
	return SignPortable(state, signer)
}

func verifySignature(message, signature []byte, keys []crypto.PublicKey) (bool, bool) {
	supported := false
	digest := sha256.Sum256(message)

	for _, key := range keys {
		switch pub := key.(type) {
		case ed25519.PublicKey:
			supported = true
			if ed25519.Verify(pub, message, signature) {
				return true, true
			}
		case *ed25519.PublicKey:
			supported = true
			if pub != nil && ed25519.Verify(*pub, message, signature) {
				return true, true
			}
		case *rsa.PublicKey:
			supported = true
			if pub == nil {
				continue
			}
			if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature) == nil {
				return true, true
			}
			if rsa.VerifyPSS(pub, crypto.SHA256, digest[:], signature, nil) == nil {
				return true, true
			}
		case *ecdsa.PublicKey:
			supported = true
			if pub == nil {
				continue
			}
			if ecdsa.VerifyASN1(pub, digest[:], signature) {
				return true, true
			}
			if verifyECDSARaw(pub, digest[:], signature) {
				return true, true
			}
		}
	}
	return false, supported
}

func verifyECDSARaw(pub *ecdsa.PublicKey, digest, signature []byte) bool {
	keySize := (pub.Curve.Params().BitSize + 7) / 8
	if len(signature) != 2*keySize {
		return false
	}
	r := new(big.Int).SetBytes(signature[:keySize])
	s := new(big.Int).SetBytes(signature[keySize:])
	return ecdsa.Verify(pub, digest, r, s)
}

// ParsePublicKey accepts a PKIX PEM key, an RSA PKCS#1 PEM key, an X.509
// certificate, or a raw 32-byte Ed25519 public key.
func ParsePublicKey(data []byte) (crypto.PublicKey, error) {
	if block, _ := pem.Decode(data); block != nil {
		if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
			return key, nil
		}
		if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
			return key, nil
		}
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			return cert.PublicKey, nil
		}
		return nil, errors.New("unable to parse public key PEM block")
	}
	if len(data) == ed25519.PublicKeySize {
		return ed25519.PublicKey(append([]byte(nil), data...)), nil
	}
	return nil, errors.New("unable to parse public key")
}
