package nestedjwt

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/asn1"
	"fmt"
	"math/big"
	"strings"
)

// verifyJWS validates a compact 3-segment JWS: header policy, kid-bound key
// resolution and asymmetric signature verification. It returns the protected
// header and the raw payload bytes (claims JSON, or a nested compact token).
func (v *Verifier) verifyJWS(segments []string) (JOSEHeader, []byte, error) {
	encodedHeader, encodedPayload, encodedSignature := segments[0], segments[1], segments[2]

	header, _, err := parseProtectedHeader(encodedHeader)
	if err != nil {
		return JOSEHeader{}, nil, err
	}

	alg := header.Algorithm
	switch {
	case strings.EqualFold(alg, "none"):
		return JOSEHeader{}, nil, ErrNoneAlgorithm
	case isSymmetricJWS(alg):
		return JOSEHeader{}, nil, fmt.Errorf("%w: %q", ErrSymmetricAlgorithm, alg)
	case !contains(v.policy.AllowedSigning, alg):
		return JOSEHeader{}, nil, fmt.Errorf("%w: JWS alg %q", ErrUnsupportedAlgorithm, alg)
	}

	if v.policy.RequireKID && header.KeyID == "" {
		return JOSEHeader{}, nil, fmt.Errorf("%w: inner JWS", ErrMissingKeyID)
	}

	publicKey, err := v.keys.VerificationKey(header.KeyID)
	if err != nil {
		return JOSEHeader{}, nil, err
	}

	payload, err := b64.DecodeString(encodedPayload)
	if err != nil {
		return JOSEHeader{}, nil, fmt.Errorf("%w: payload is not base64url: %v", ErrMalformedToken, err)
	}
	signature, err := b64.DecodeString(encodedSignature)
	if err != nil {
		return JOSEHeader{}, nil, fmt.Errorf("%w: signature is not base64url: %v", ErrMalformedToken, err)
	}
	if len(signature) == 0 {
		return JOSEHeader{}, nil, fmt.Errorf("%w: empty signature", ErrSignature)
	}

	signingInput := []byte(encodedHeader + "." + segments[1])
	digest := sha256.Sum256(signingInput)

	switch alg {
	case "ES256":
		ecKey, ok := publicKey.(*ecdsa.PublicKey)
		if !ok {
			return JOSEHeader{}, nil, fmt.Errorf("%w: ES256 requires *ecdsa.PublicKey, got %T", ErrSignature, publicKey)
		}
		if err := verifyECDSA(ecKey, digest[:], signature); err != nil {
			return JOSEHeader{}, nil, err
		}
	case "RS256", "PS256":
		rsaKey, ok := publicKey.(*rsa.PublicKey)
		if !ok {
			return JOSEHeader{}, nil, fmt.Errorf("%w: %s requires *rsa.PublicKey, got %T", ErrSignature, alg, publicKey)
		}
		if alg == "RS256" {
			err = rsa.VerifyPKCS1v15(rsaKey, crypto.SHA256, digest[:], signature)
		} else {
			err = rsa.VerifyPSS(rsaKey, crypto.SHA256, digest[:], signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		}
		if err != nil {
			return JOSEHeader{}, nil, fmt.Errorf("%w: %v", ErrSignature, err)
		}
	default:
		return JOSEHeader{}, nil, fmt.Errorf("%w: JWS alg %q is not implemented", ErrUnsupportedAlgorithm, alg)
	}

	return header, payload, nil
}

// isSymmetricJWS reports HMAC-style MAC algorithms, which cannot authenticate
// a client because any party sharing the MAC key could forge signatures.
func isSymmetricJWS(alg string) bool {
	switch alg {
	case "HS256", "HS384", "HS512":
		return true
	default:
		return false
	}
}

type ecdsaSignature struct {
	R, S *big.Int
}

func verifyECDSA(key *ecdsa.PublicKey, digest, signature []byte) error {
	// RFC 7515 appendix A.3: ECDSA signatures are DER-encoded Ecdsa-Sig-Value.
	var parsed ecdsaSignature
	if rest, err := asn1.Unmarshal(signature, &parsed); err != nil || len(rest) != 0 || parsed.R == nil || parsed.S == nil {
		return fmt.Errorf("%w: ES256 signature is not valid DER", ErrSignature)
	}
	if !ecdsa.Verify(key, digest, parsed.R, parsed.S) {
		return ErrSignature
	}
	return nil
}
