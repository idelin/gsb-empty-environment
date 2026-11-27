package webauthn

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"fmt"
	"math/big"
)

// ecdsaSignature is the ASN.1 DER ECDSA-Sig-Value structure.
type ecdsaSignature struct {
	R, S *big.Int
}

// verifyECDSASignature checks a WebAuthn ES256/ES384/ES512 signature. The
// signature is the raw DER ECDSA-Sig-Value produced by authenticators.
func verifyECDSASignature(publicKey *ecdsa.PublicKey, message, signature []byte, algorithm int) error {
	var digest []byte

	switch algorithm {
	case algES256:
		sum := sha256.Sum256(message)
		digest = sum[:]
	case algES384:
		sum := sha512.Sum384(message)
		digest = sum[:]
	case algES512:
		sum := sha512.Sum512(message)
		digest = sum[:]
	default:
		return fmt.Errorf("%w: unsupported signature algorithm %d", ErrSignature, algorithm)
	}

	// Reject the IEEE P1363 fixed-length (r||s) form: WebAuthn attestation
	// signatures are ASN.1 DER. A strict DER parse avoids ambiguity.
	var parsed ecdsaSignature
	rest, err := asn1.Unmarshal(signature, &parsed)
	if err != nil || len(rest) != 0 || parsed.R == nil || parsed.S == nil {
		return fmt.Errorf("%w: signature is not a DER ECDSA-Sig-Value", ErrSignature)
	}
	if !ecdsa.VerifyASN1(publicKey, digest, signature) {
		return fmt.Errorf("%w: ECDSA verification failed", ErrSignature)
	}
	return nil
}
