package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// jwtHeader carries the algorithm and key id from a compact JWT.
type jwtHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

// audienceClaim accepts either a single string or a list of strings.
type audienceClaim []string

func (a *audienceClaim) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = []string{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("audience must be a string or list of strings: %w", err)
	}
	*a = list
	return nil
}

// serviceAccountRef identifies the Kubernetes service account a token was
// issued to.
type serviceAccountRef struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}

// kubernetesClaims mirrors the bound object claims injected into projected
// service account tokens.
type kubernetesClaims struct {
	Namespace      string            `json:"namespace"`
	ServiceAccount serviceAccountRef `json:"serviceaccount"`
}

// tokenClaims is the claim set understood by the validator. Audience is a
// pointer so that "claim absent" (legacy tokens) is distinguishable from an
// explicitly empty audience.
type tokenClaims struct {
	Issuer     string            `json:"iss"`
	Subject    string            `json:"sub"`
	Audience   *audienceClaim    `json:"aud"`
	ExpiresAt  int64             `json:"exp"`
	NotBefore  int64             `json:"nbf"`
	IssuedAt   int64             `json:"iat"`
	TokenID    string            `json:"jti"`
	Cluster    string            `json:"cluster,omitempty"`
	Mirrored   bool              `json:"mirrored,omitempty"`
	Generation int64             `json:"gen,omitempty"`
	Kubernetes *kubernetesClaims `json:"kubernetes.io,omitempty"`
}

// parseAndVerifyJWT splits a compact JWT, verifies its signature against the
// configured key set, and returns the decoded claims. Only an explicit
// allowlist of asymmetric algorithms is accepted.
func parseAndVerifyJWT(token string, keys KeySet) (*tokenClaims, error) {
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		return nil, fmt.Errorf("%w: expected 3 segments, got %d", ErrMalformedToken, len(segments))
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(segments[0])
	if err != nil {
		return nil, fmt.Errorf("%w: undecodable header: %v", ErrMalformedToken, err)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: unparsable header: %v", ErrMalformedToken, err)
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return nil, fmt.Errorf("%w: undecodable payload: %v", ErrMalformedToken, err)
	}
	var claims tokenClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("%w: unparsable claims: %v", ErrMalformedToken, err)
	}

	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		return nil, fmt.Errorf("%w: undecodable signature: %v", ErrMalformedToken, err)
	}

	key, err := keys.Key(header.KeyID, header.Algorithm)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}

	signed := segments[0] + "." + segments[1]
	digest := sha256.Sum256([]byte(signed))

	switch header.Algorithm {
	case "RS256":
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: key %q is not an RSA public key", ErrInvalidSignature, header.KeyID)
		}
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature); err != nil {
			return nil, fmt.Errorf("%w: RS256 verification failed", ErrInvalidSignature)
		}
	case "ES256":
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: key %q is not an ECDSA public key", ErrInvalidSignature, header.KeyID)
		}
		if !ecdsa.VerifyASN1(pub, digest[:], signature) {
			return nil, fmt.Errorf("%w: ES256 verification failed", ErrInvalidSignature)
		}
	default:
		return nil, fmt.Errorf("%w: algorithm %q is not allowed", ErrInvalidSignature, header.Algorithm)
	}

	return &claims, nil
}

// signJWT mints tokens for tests and examples; production tokens are minted
// by the cluster token issuer, never by this package.
func signJWT(alg, kid string, key crypto.Signer, claims *tokenClaims) (string, error) {
	headerJSON, err := json.Marshal(jwtHeader{Algorithm: alg, KeyID: kid})
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signed := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signed))

	var signature []byte
	switch alg {
	case "RS256":
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return "", errors.New("RS256 requires an *rsa.PrivateKey")
		}
		signature, err = rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest[:])
	case "ES256":
		ecdsaKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return "", errors.New("ES256 requires an *ecdsa.PrivateKey")
		}
		signature, err = ecdsa.SignASN1(rand.Reader, ecdsaKey, digest[:])
	default:
		return "", fmt.Errorf("unsupported algorithm %q", alg)
	}
	if err != nil {
		return "", err
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
