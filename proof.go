package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ConfirmationKey is the cnf object to write into the issued credential.
type ConfirmationKey struct {
	JKT string `json:"jkt"`
}

type proofHeader struct {
	Alg string          `json:"alg"`
	Typ string          `json:"typ,omitempty"`
	JWK json.RawMessage `json:"jwk"`
}

// ConfirmationKeyFromVerifiedProof verifies the proof signature and returns
// cnf.jkt derived from the verified public signing key.
func ConfirmationKeyFromVerifiedProof(proof string) (ConfirmationKey, error) {
	thumbprint, err := verifyProof(proof)
	if err != nil {
		return ConfirmationKey{}, err
	}
	return ConfirmationKey{JKT: thumbprint}, nil
}

// VerifyProofBinding verifies the proof signature and requires the verified
// signing key's JWK thumbprint to equal the cnf.jkt intended for issuance.
func VerifyProofBinding(proof string, cnf ConfirmationKey) error {
	if cnf.JKT == "" {
		return fmt.Errorf("%w: empty cnf.jkt", ErrInvalidProof)
	}

	thumbprint, err := verifyProof(proof)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(thumbprint), []byte(cnf.JKT)) != 1 {
		return fmt.Errorf("%w: proof key thumbprint %q, credential cnf.jkt %q",
			ErrMismatchedConfirmationKey, thumbprint, cnf.JKT)
	}
	return nil
}

func verifyProof(proof string) (string, error) {
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: JWT must have three compact-serialization parts", ErrInvalidProof)
	}

	headerBytes, err := decodeBase64URL(parts[0])
	if err != nil {
		return "", fmt.Errorf("%w: invalid JWT header: %v", ErrInvalidProof, err)
	}
	claimsBytes, err := decodeBase64URL(parts[1])
	if err != nil {
		return "", fmt.Errorf("%w: invalid JWT claims: %v", ErrInvalidProof, err)
	}
	signatureBytes, err := decodeBase64URL(parts[2])
	if err != nil {
		return "", fmt.Errorf("%w: invalid JWT signature: %v", ErrInvalidProof, err)
	}
	if len(signatureBytes) == 0 {
		return "", fmt.Errorf("%w: empty JWT signature", ErrInvalidProof)
	}

	var claims any
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return "", fmt.Errorf("%w: invalid JWT claims: %v", ErrInvalidProof, err)
	}
	if _, ok := claims.(map[string]any); !ok {
		return "", fmt.Errorf("%w: JWT claims must be a JSON object", ErrInvalidProof)
	}

	var header proofHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return "", fmt.Errorf("%w: invalid JWT header: %v", ErrInvalidProof, err)
	}
	if header.Alg == "" || header.Alg == "none" {
		return "", fmt.Errorf("%w: alg %q is not accepted", ErrInvalidProof, header.Alg)
	}
	if len(header.JWK) == 0 {
		return "", fmt.Errorf("%w: JWT header requires a public jwk", ErrInvalidProof)
	}

	thumbprint, err := ThumbprintJWK(header.JWK)
	if err != nil {
		return "", err
	}
	publicKey, err := publicKeyFromJWK(header.JWK)
	if err != nil {
		return "", err
	}

	signingInput := []byte(parts[0] + "." + parts[1])
	if err := verifySignature(header.Alg, publicKey, signingInput, signatureBytes); err != nil {
		return "", err
	}
	return thumbprint, nil
}

func verifySignature(alg string, publicKey crypto.PublicKey, signingInput, signature []byte) error {
	switch alg {
	case "RS256", "RS384", "RS512":
		rsaKey, ok := publicKey.(*rsa.PublicKey)
		if !ok {
			return algorithmKeyMismatchError(alg)
		}
		hash, err := hashForAlgorithm(alg)
		if err != nil {
			return err
		}
		digest := hashBytes(hash, signingInput)
		if err := rsa.VerifyPKCS1v15(rsaKey, hash, digest, signature); err != nil {
			return invalidSignatureError(err)
		}

	case "PS256", "PS384", "PS512":
		rsaKey, ok := publicKey.(*rsa.PublicKey)
		if !ok {
			return algorithmKeyMismatchError(alg)
		}
		hash, err := hashForAlgorithm(alg)
		if err != nil {
			return err
		}
		digest := hashBytes(hash, signingInput)
		if err := rsa.VerifyPSS(rsaKey, hash, digest, signature, nil); err != nil {
			return invalidSignatureError(err)
		}

	case "ES256", "ES384", "ES512":
		ecKey, ok := publicKey.(*ecdsa.PublicKey)
		if !ok {
			return algorithmKeyMismatchError(alg)
		}
		expectedCurve, err := curveForECAlgorithm(alg)
		if err != nil {
			return err
		}
		if ecKey.Curve != expectedCurve {
			return algorithmKeyMismatchError(alg)
		}

		coordinateSize := (ecKey.Curve.Params().BitSize + 7) / 8
		if len(signature) != 2*coordinateSize {
			return fmt.Errorf("%w: invalid ECDSA signature length", ErrInvalidProof)
		}
		hash, err := hashForAlgorithm(alg)
		if err != nil {
			return err
		}
		digest := hashBytes(hash, signingInput)
		r := new(big.Int).SetBytes(signature[:coordinateSize])
		s := new(big.Int).SetBytes(signature[coordinateSize:])
		if !ecdsa.Verify(ecKey, digest, r, s) {
			return invalidSignatureError(errors.New("ecdsa verification failed"))
		}

	case "EdDSA":
		edKey, ok := publicKey.(ed25519.PublicKey)
		if !ok {
			return algorithmKeyMismatchError(alg)
		}
		if !ed25519.Verify(edKey, signingInput, signature) {
			return invalidSignatureError(errors.New("ed25519 verification failed"))
		}

	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
	return nil
}

func hashForAlgorithm(alg string) (crypto.Hash, error) {
	switch alg {
	case "RS256", "PS256", "ES256":
		return crypto.SHA256, nil
	case "RS384", "PS384", "ES384":
		return crypto.SHA384, nil
	case "RS512", "PS512", "ES512":
		return crypto.SHA512, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
}

func curveForECAlgorithm(alg string) (elliptic.Curve, error) {
	switch alg {
	case "ES256":
		return elliptic.P256(), nil
	case "ES384":
		return elliptic.P384(), nil
	case "ES512":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
}

func hashBytes(hash crypto.Hash, input []byte) []byte {
	hasher := hash.New()
	hasher.Write(input)
	return hasher.Sum(nil)
}

func algorithmKeyMismatchError(alg string) error {
	return fmt.Errorf("%w: JWT alg %q does not match jwk key type or curve", ErrInvalidProof, alg)
}

func invalidSignatureError(err error) error {
	return fmt.Errorf("%w: signature verification failed: %v", ErrInvalidProof, err)
}
