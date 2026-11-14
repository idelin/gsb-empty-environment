package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"fmt"
)

// Algorithm identifies the detached-signature scheme used by a binding or a
// revocation certificate. Leaving it empty enables automatic detection from
// the public key type.
type Algorithm string

const (
	AlgorithmEd25519 Algorithm = "ed25519"
	AlgorithmRSA     Algorithm = "rsa-sha256"     // RSASSA-PKCS1-v1_5
	AlgorithmRSAPSS  Algorithm = "rsa-pss-sha256" // RSASSA-PSS
	AlgorithmECDSA   Algorithm = "ecdsa-sha256"   // ASN.1/DER signature
)

// BindingStatement returns the canonical bytes covered by a self-signature
// that binds userID to fingerprint.
func BindingStatement(fingerprint, userID string) []byte {
	fp := NormalizeShortID(fingerprint)
	return []byte("netsec.identity.binding.v1\x00" + fp + "\x00" + normalizeUserID(userID))
}

// RevocationStatement returns the canonical bytes covered by a revocation
// certificate. An empty userID revokes every binding of the key.
func RevocationStatement(fingerprint, userID string) []byte {
	fp := NormalizeShortID(fingerprint)
	return []byte("netsec.identity.revocation.v1\x00" + fp + "\x00" + normalizeUserID(userID))
}

// SignMessage signs message with signer and returns the detached signature and
// the algorithm used. For RSA keys, PKCS#1 v1.5 with SHA-256 is used.
func SignMessage(signer crypto.Signer, message []byte) ([]byte, Algorithm, error) {
	if signer == nil {
		return nil, "", fmt.Errorf("%w: nil signer", ErrInvalidPublicKey)
	}
	switch public := signer.Public().(type) {
	case ed25519.PublicKey:
		signature, err := signer.Sign(rand.Reader, message, crypto.Hash(0))
		if err != nil {
			return nil, "", fmt.Errorf("netsec: sign: %w", err)
		}
		return signature, AlgorithmEd25519, nil
	case *rsa.PublicKey:
		digest := sha256.Sum256(message)
		signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
		if err != nil {
			return nil, "", fmt.Errorf("netsec: sign: %w", err)
		}
		return signature, AlgorithmRSA, nil
	case *ecdsa.PublicKey:
		digest := sha256.Sum256(message)
		signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
		if err != nil {
			return nil, "", fmt.Errorf("netsec: sign: %w", err)
		}
		return signature, AlgorithmECDSA, nil
	default:
		return nil, "", fmt.Errorf("%w: unsupported signer type %T", ErrInvalidPublicKey, public)
	}
}

// VerifyMessage verifies a detached signature over message. The optional
// algorithm enables strict scheme selection; when it is omitted, RSA
// PKCS#1 v1.5 is tried before RSA-PSS.
func VerifyMessage(publicKey any, message, signature []byte, algorithms ...Algorithm) error {
	algorithm := Algorithm("")
	if len(algorithms) > 0 {
		algorithm = algorithms[0]
	}
	key, _, err := normalizePublicKey(publicKey)
	if err != nil {
		return err
	}
	if len(signature) == 0 {
		return fmt.Errorf("%w: empty signature", ErrInvalidSignature)
	}

	switch public := key.(type) {
	case ed25519.PublicKey:
		if algorithm != "" && algorithm != AlgorithmEd25519 {
			return fmt.Errorf("%w: algorithm %q does not match Ed25519", ErrInvalidSignature, algorithm)
		}
		if len(signature) != ed25519.SignatureSize || !ed25519.Verify(public, message, signature) {
			return ErrInvalidSignature
		}
		return nil
	case *rsa.PublicKey:
		digest := sha256.Sum256(message)
		switch algorithm {
		case AlgorithmRSAPSS:
			if err := rsa.VerifyPSS(public, crypto.SHA256, digest[:], signature, nil); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
			}
			return nil
		case AlgorithmRSA, "":
			if err := rsa.VerifyPKCS1v15(public, crypto.SHA256, digest[:], signature); err == nil {
				return nil
			}
			if algorithm == "" {
				if err := rsa.VerifyPSS(public, crypto.SHA256, digest[:], signature, nil); err == nil {
					return nil
				}
			}
			return ErrInvalidSignature
		default:
			return fmt.Errorf("%w: algorithm %q does not match RSA key", ErrInvalidSignature, algorithm)
		}
	case *ecdsa.PublicKey:
		if algorithm != "" && algorithm != AlgorithmECDSA {
			return fmt.Errorf("%w: algorithm %q does not match ECDSA key", ErrInvalidSignature, algorithm)
		}
		digest := sha256.Sum256(message)
		if !ecdsa.VerifyASN1(public, digest[:], signature) {
			return ErrInvalidSignature
		}
		return nil
	default:
		return fmt.Errorf("%w: cannot verify with %T", ErrInvalidPublicKey, key)
	}
}

// SignatureAlgorithmForKey reports the signing algorithm used by SignMessage
// for the supplied public key.
func SignatureAlgorithmForKey(publicKey any) (Algorithm, error) {
	key, _, err := normalizePublicKey(publicKey)
	if err != nil {
		return "", err
	}
	switch key.(type) {
	case ed25519.PublicKey:
		return AlgorithmEd25519, nil
	case *rsa.PublicKey:
		return AlgorithmRSA, nil
	case *ecdsa.PublicKey:
		return AlgorithmECDSA, nil
	default:
		return "", fmt.Errorf("%w: unsupported key type %T", ErrInvalidPublicKey, key)
	}
}

func normalizeUserID(userID string) string {
	return trimSpace(userID)
}
