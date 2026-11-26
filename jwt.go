package spiffefed

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	maxJWTSVIDLifetime = 24 * time.Hour
	maxJWTBytes        = 1 << 20
)

// JWTSVID is a successfully verified JWT-SVID.
type JWTSVID struct {
	ID
	Audience  []string
	ExpiresAt time.Time
	IssuedAt  time.Time
	KeyID     string
	Algorithm string
}

// JWTSVIDVerifier validates JWT-SVIDs against a domain-isolated key ring.
//
// If Policy is set, federated subjects must also be explicitly allowed for
// the target service supplied through VerifyForService or
// VerifyForServiceAt. Without Policy, only the local trust domain (explicitly
// set, inferred when the ring contains exactly one domain, or set by Policy)
// is accepted.
type JWTSVIDVerifier struct {
	Keys             *JWTKeyRing
	Audience         string
	MaxLifetime      time.Duration
	ClockSkew        time.Duration
	Now              func() time.Time
	Policy           *FederationPolicy
	LocalTrustDomain string
}

// NewJWTSVIDVerifier constructs a verifier with a 24-hour maximum token
// lifetime and no clock skew.
func NewJWTSVIDVerifier(keys *JWTKeyRing, expectedAudience string) (*JWTSVIDVerifier, error) {
	if keys == nil {
		return nil, fmt.Errorf("%w: key ring is nil", ErrInvalidJWKS)
	}
	if expectedAudience == "" {
		return nil, fmt.Errorf("%w: expected audience is empty", ErrInvalidJWT)
	}
	if !isPrintableASCII(expectedAudience) {
		return nil, fmt.Errorf("%w: expected audience must be printable ASCII", ErrInvalidJWT)
	}
	return &JWTSVIDVerifier{
		Keys:        keys,
		Audience:    expectedAudience,
		MaxLifetime: maxJWTSVIDLifetime,
		Now:         time.Now,
	}, nil
}

// Verify validates a JWT-SVID. Federated identities require a target service;
// use VerifyForService for those identities.
func (v *JWTSVIDVerifier) Verify(token string) (JWTSVID, error) {
	return v.verify(token, "", time.Time{})
}

// VerifyAt validates a JWT-SVID at an explicit time.
func (v *JWTSVIDVerifier) VerifyAt(token string, now time.Time) (JWTSVID, error) {
	return v.verify(token, "", now)
}

// VerifyForService validates a JWT-SVID and applies the target service's
// federation allowlist when the subject is in an external trust domain.
func (v *JWTSVIDVerifier) VerifyForService(token, targetService string) (JWTSVID, error) {
	return v.verify(token, targetService, time.Time{})
}

// VerifyForServiceAt combines VerifyForService with an explicit verification
// time.
func (v *JWTSVIDVerifier) VerifyForServiceAt(token, targetService string, now time.Time) (JWTSVID, error) {
	return v.verify(token, targetService, now)
}

func (v *JWTSVIDVerifier) verify(token, targetService string, now time.Time) (JWTSVID, error) {
	if v == nil || v.Keys == nil {
		return JWTSVID{}, fmt.Errorf("%w: verifier is not configured", ErrInvalidJWT)
	}
	if v.Audience == "" {
		return JWTSVID{}, fmt.Errorf("%w: expected audience is empty", ErrInvalidJWT)
	}
	if len(token) == 0 || len(token) > maxJWTBytes {
		return JWTSVID{}, fmt.Errorf("%w: token length is invalid", ErrInvalidJWT)
	}
	if now.IsZero() {
		if v.Now != nil {
			now = v.Now()
		} else {
			now = time.Now()
		}
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return JWTSVID{}, fmt.Errorf("%w: token must have exactly three compact segments", ErrInvalidJWT)
	}
	headerBytes, err := decodeJWTBase64URL("header", parts[0])
	if err != nil {
		return JWTSVID{}, err
	}
	payloadBytes, err := decodeJWTBase64URL("payload", parts[1])
	if err != nil {
		return JWTSVID{}, err
	}
	signature, err := decodeJWTBase64URL("signature", parts[2])
	if err != nil {
		return JWTSVID{}, err
	}

	header, err := jsonObject(headerBytes)
	if err != nil {
		return JWTSVID{}, fmt.Errorf("%w: header: %v", ErrInvalidJWT, err)
	}
	alg, kid, err := validateJWTHeader(header)
	if err != nil {
		return JWTSVID{}, err
	}

	claims, err := jsonObject(payloadBytes)
	if err != nil {
		return JWTSVID{}, fmt.Errorf("%w: claims: %v", ErrInvalidJWT, err)
	}
	subjectText, err := jsonString(claims, "sub", true)
	if err != nil {
		return JWTSVID{}, fmt.Errorf("%w: %v", ErrInvalidJWT, err)
	}
	subject, err := ParseID(subjectText)
	if err != nil {
		return JWTSVID{}, fmt.Errorf("%w: sub: %w", ErrInvalidJWT, err)
	}

	audiences, err := jsonAudience(claims, "aud")
	if err != nil {
		return JWTSVID{}, fmt.Errorf("%w: %v", ErrAudience, err)
	}
	if !audienceContains(audiences, v.Audience) {
		return JWTSVID{}, fmt.Errorf("%w: expected %q", ErrAudience, v.Audience)
	}

	expSeconds, ok, err := jsonNumericDate(claims, "exp")
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("missing exp")
		}
		return JWTSVID{}, fmt.Errorf("%w: %v", ErrTokenLifetime, err)
	}
	expiresAt := time.Unix(expSeconds, 0)
	iatSeconds, hasIssuedAt, err := jsonNumericDate(claims, "iat")
	if err != nil {
		return JWTSVID{}, fmt.Errorf("%w: %v", ErrTokenLifetime, err)
	}
	nbfSeconds, hasNotBefore, err := jsonNumericDate(claims, "nbf")
	if err != nil {
		return JWTSVID{}, fmt.Errorf("%w: %v", ErrTokenLifetime, err)
	}

	localDomain := v.localTrustDomain()
	if subject.TrustDomain != localDomain {
		if v.Policy == nil {
			return JWTSVID{}, fmt.Errorf("%w: external subject %q requires a federation policy", ErrFederationDenied, subject.TrustDomain)
		}
		if targetService == "" {
			return JWTSVID{}, ErrTargetServiceRequired
		}
		if err := v.Policy.Authorize(targetService, subject.TrustDomain); err != nil {
			return JWTSVID{}, err
		}
	} else if targetService != "" && v.Policy != nil {
		if err := v.Policy.Authorize(targetService, subject.TrustDomain); err != nil {
			return JWTSVID{}, err
		}
	}

	key, ok := v.Keys.key(subject.TrustDomain, kid)
	if !ok {
		return JWTSVID{}, fmt.Errorf("%w: kid %q in trust domain %q", ErrUnknownKey, kid, subject.TrustDomain)
	}
	if _, ok := key.algorithms[alg]; !ok {
		return JWTSVID{}, fmt.Errorf("%w: alg %q is not permitted for kid %q", ErrInvalidJWT, alg, kid)
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	if err := verifyJWTSignature(alg, key.publicKey, signingInput, signature); err != nil {
		return JWTSVID{}, fmt.Errorf("%w: %v", ErrSignature, err)
	}

	maxLifetime := v.MaxLifetime
	if maxLifetime == 0 {
		maxLifetime = maxJWTSVIDLifetime
	}
	if maxLifetime < 0 {
		return JWTSVID{}, fmt.Errorf("%w: MaxLifetime is negative", ErrTokenLifetime)
	}
	if maxLifetime > maxJWTSVIDLifetime {
		return JWTSVID{}, fmt.Errorf("%w: MaxLifetime must not exceed %s", ErrTokenLifetime, maxJWTSVIDLifetime)
	}
	if v.ClockSkew < 0 {
		return JWTSVID{}, fmt.Errorf("%w: ClockSkew is negative", ErrTokenLifetime)
	}
	if !now.Before(expiresAt.Add(v.ClockSkew)) {
		return JWTSVID{}, fmt.Errorf("%w: token is expired", ErrTokenLifetime)
	}
	if expiresAt.After(now.Add(maxLifetime).Add(v.ClockSkew)) {
		return JWTSVID{}, fmt.Errorf("%w: exp is more than %s in the future", ErrTokenLifetime, maxLifetime)
	}
	if hasIssuedAt {
		issuedAt := time.Unix(iatSeconds, 0)
		if issuedAt.After(now.Add(v.ClockSkew)) {
			return JWTSVID{}, fmt.Errorf("%w: iat is in the future", ErrTokenLifetime)
		}
		if !expiresAt.After(issuedAt) {
			return JWTSVID{}, fmt.Errorf("%w: exp must be after iat", ErrTokenLifetime)
		}
		if expiresAt.Sub(issuedAt) > maxLifetime {
			return JWTSVID{}, fmt.Errorf("%w: exp-iat exceeds %s", ErrTokenLifetime, maxLifetime)
		}
	}
	if hasNotBefore {
		notBefore := time.Unix(nbfSeconds, 0)
		if notBefore.After(now.Add(v.ClockSkew)) {
			return JWTSVID{}, fmt.Errorf("%w: token is not valid yet", ErrTokenLifetime)
		}
	}

	issuedAt := time.Time{}
	if hasIssuedAt {
		issuedAt = time.Unix(iatSeconds, 0)
	}
	return JWTSVID{
		ID:        subject,
		Audience:  append([]string(nil), audiences...),
		ExpiresAt: expiresAt,
		IssuedAt:  issuedAt,
		KeyID:     kid,
		Algorithm: alg,
	}, nil
}

func (v *JWTSVIDVerifier) localTrustDomain() string {
	if v.Policy != nil {
		return v.Policy.LocalTrustDomain()
	}
	if v.LocalTrustDomain != "" {
		return v.LocalTrustDomain
	}
	if domain, ok := v.Keys.SingleDomain(); ok {
		return domain
	}
	return ""
}

func validateJWTHeader(header map[string]json.RawMessage) (string, string, error) {
	for _, forbidden := range []string{"jku", "jwk", "x5u", "x5c", "x5t", "x5t#S256"} {
		if _, present := header[forbidden]; present {
			return "", "", fmt.Errorf("%w: embedded key or key-URL header %q is forbidden", ErrInvalidJWT, forbidden)
		}
	}
	if raw, present := header["crit"]; present {
		var critical []string
		if err := json.Unmarshal(raw, &critical); err != nil {
			return "", "", fmt.Errorf("%w: crit must be a string array", ErrInvalidJWT)
		}
		if len(critical) != 0 {
			return "", "", fmt.Errorf("%w: unsupported critical headers %v", ErrInvalidJWT, critical)
		}
	}
	if raw, present := header["b64"]; present {
		var encoded bool
		if err := json.Unmarshal(raw, &encoded); err != nil || !encoded {
			return "", "", fmt.Errorf("%w: unencoded JWS payloads are forbidden", ErrInvalidJWT)
		}
	}

	alg, err := jsonString(header, "alg", true)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidJWT, err)
	}
	if !supportedJOSEAlgorithm(alg) {
		return "", "", fmt.Errorf("%w: unsupported alg %q", ErrInvalidJWT, alg)
	}
	kid, err := jsonString(header, "kid", true)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidJWT, err)
	}
	if err := validateKeyID(kid); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidJWT, err)
	}
	if typ, err := jsonString(header, "typ", false); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidJWT, err)
	} else if typ != "" && !isJWTType(typ) {
		return "", "", fmt.Errorf("%w: unsupported typ %q", ErrInvalidJWT, typ)
	}
	return alg, kid, nil
}

func isJWTType(typ string) bool {
	switch strings.ToLower(typ) {
	case "jwt", "jose", "application/jwt", "application/jose":
		return true
	default:
		return false
	}
}

func decodeJWTBase64URL(name, value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("%w: %s segment is empty", ErrInvalidJWT, name)
	}
	if strings.ContainsAny(value, "=+/ \t\r\n") {
		return nil, fmt.Errorf("%w: %s segment is not unpadded base64url", ErrInvalidJWT, name)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %s segment is not base64url: %v", ErrInvalidJWT, name, err)
	}
	return decoded, nil
}

func audienceContains(audiences []string, expected string) bool {
	for _, audience := range audiences {
		if audience == expected {
			return true
		}
	}
	return false
}

func verifyJWTSignature(alg string, publicKey crypto.PublicKey, signingInput, signature []byte) error {
	switch alg {
	case "ES256", "ES384", "ES512":
		key, ok := publicKey.(*ecdsa.PublicKey)
		if !ok || key == nil {
			return fmt.Errorf("algorithm %s requires an ECDSA key", alg)
		}
		var digest []byte
		var coordinateSize int
		switch alg {
		case "ES256":
			sum := sha256.Sum256(signingInput)
			digest, coordinateSize = sum[:], 32
		case "ES384":
			sum := sha512.Sum384(signingInput)
			digest, coordinateSize = sum[:], 48
		case "ES512":
			sum := sha512.Sum512(signingInput)
			digest, coordinateSize = sum[:], 66
		}
		if len(signature) != coordinateSize*2 {
			return fmt.Errorf("ECDSA signature must be %d bytes", coordinateSize*2)
		}
		r := new(big.Int).SetBytes(signature[:coordinateSize])
		s := new(big.Int).SetBytes(signature[coordinateSize:])
		if r.Sign() <= 0 || s.Sign() <= 0 || !ecdsa.Verify(key, digest, r, s) {
			return fmt.Errorf("ECDSA verification failed")
		}
		return nil

	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		key, ok := publicKey.(*rsa.PublicKey)
		if !ok || key == nil {
			return fmt.Errorf("algorithm %s requires an RSA key", alg)
		}
		hash, digest, err := joseDigest(alg, signingInput)
		if err != nil {
			return err
		}
		if strings.HasPrefix(alg, "PS") {
			if err := rsa.VerifyPSS(key, hash, digest, signature, nil); err != nil {
				return fmt.Errorf("RSA-PSS verification failed: %v", err)
			}
		} else if err := rsa.VerifyPKCS1v15(key, hash, digest, signature); err != nil {
			return fmt.Errorf("RSA PKCS#1 verification failed: %v", err)
		}
		return nil

	case "EdDSA":
		key, ok := publicKey.(ed25519.PublicKey)
		if !ok || len(key) != ed25519.PublicKeySize {
			return fmt.Errorf("EdDSA requires an Ed25519 key")
		}
		if !ed25519.Verify(key, signingInput, signature) {
			return fmt.Errorf("Ed25519 verification failed")
		}
		return nil
	default:
		return fmt.Errorf("unsupported alg %q", alg)
	}
}

func joseDigest(alg string, signingInput []byte) (crypto.Hash, []byte, error) {
	switch {
	case strings.HasSuffix(alg, "256"):
		sum := sha256.Sum256(signingInput)
		return crypto.SHA256, sum[:], nil
	case strings.HasSuffix(alg, "384"):
		sum := sha512.Sum384(signingInput)
		return crypto.SHA384, sum[:], nil
	case strings.HasSuffix(alg, "512"):
		sum := sha512.Sum512(signingInput)
		return crypto.SHA512, sum[:], nil
	default:
		return 0, nil, fmt.Errorf("unsupported digest for alg %q", alg)
	}
}

func isPrintableASCII(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}
