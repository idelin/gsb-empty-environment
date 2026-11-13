package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// KeyBindingTyp is the only acceptable "typ" header value for a kb-jwt.
const KeyBindingTyp = "kb+jwt"

var (
	ErrMalformedPresentation = errors.New("netsec: malformed SD-JWT presentation")
	ErrMalformedJWT          = errors.New("netsec: malformed JWT")
	ErrUnexpectedTyp         = errors.New("netsec: kb-jwt typ header is not kb+jwt")
	ErrUnexpectedAlg         = errors.New("netsec: kb-jwt alg is not EdDSA")
	ErrMissingProofKey       = errors.New("netsec: kb-jwt header carries no proof key jwk")
	ErrUnsupportedKey        = errors.New("netsec: proof key is not an Ed25519 OKP key")
	ErrMissingCnf            = errors.New("netsec: issuer JWT has no cnf.jkt claim")
	ErrJKTMismatch           = errors.New("netsec: cnf.jkt does not match the proof key thumbprint")
	ErrSDHashMismatch        = errors.New("netsec: sd_hash does not match the canonical disclosure bytes")
	ErrSignatureInvalid      = errors.New("netsec: kb-jwt signature is invalid")
)

var b64 = base64.RawURLEncoding

// Presentation is a parsed SD-JWT presentation with an attached kb-jwt.
type Presentation struct {
	IssuerJWT     string
	Disclosures   []string
	KeyBindingJWT string
}

// VerifiedPresentation describes a presentation that passed VerifyKeyBinding.
type VerifiedPresentation struct {
	IssuerClaims     map[string]any
	KeyBindingClaims map[string]any
	Disclosures      []string
	HolderPublicKey  ed25519.PublicKey
}

// Thumbprint returns the RFC 7638 JWK SHA-256 thumbprint of an Ed25519 key.
func Thumbprint(publicKey ed25519.PublicKey) string {
	canonical := fmt.Sprintf(`{"crv":"Ed25519","kty":"OKP","x":"%s"}`, b64.EncodeToString(publicKey))
	sum := sha256.Sum256([]byte(canonical))
	return b64.EncodeToString(sum[:])
}

// CanonicalDisclosureBytes returns the canonical byte form a kb-jwt binds
// to: the issuer JWT followed by each disclosure, every segment terminated
// by "~". Any added, removed, reordered, or altered disclosure changes
// these bytes and therefore the expected sd_hash.
func CanonicalDisclosureBytes(issuerJWT string, disclosures []string) []byte {
	var b strings.Builder
	b.WriteString(issuerJWT)
	for _, d := range disclosures {
		b.WriteByte('~')
		b.WriteString(d)
	}
	b.WriteByte('~')
	return []byte(b.String())
}

// SDHash returns the base64url-encoded SHA-256 of the canonical disclosure
// bytes, i.e. the value a kb-jwt must carry in its sd_hash claim.
func SDHash(issuerJWT string, disclosures []string) string {
	sum := sha256.Sum256(CanonicalDisclosureBytes(issuerJWT, disclosures))
	return b64.EncodeToString(sum[:])
}

// ParsePresentation splits a compact SD-JWT presentation into its issuer
// JWT, disclosures, and trailing kb-jwt.
func ParsePresentation(s string) (*Presentation, error) {
	parts := strings.Split(s, "~")
	if len(parts) < 2 || parts[0] == "" {
		return nil, ErrMalformedPresentation
	}
	kb := parts[len(parts)-1]
	if kb == "" {
		return nil, fmt.Errorf("%w: no kb-jwt attached", ErrMalformedPresentation)
	}
	return &Presentation{
		IssuerJWT:     parts[0],
		Disclosures:   parts[1 : len(parts)-1],
		KeyBindingJWT: kb,
	}, nil
}

// AssemblePresentation joins an issuer JWT, disclosures, and a kb-jwt into
// a compact presentation.
func AssemblePresentation(issuerJWT string, disclosures []string, keyBindingJWT string) string {
	var b strings.Builder
	b.WriteString(issuerJWT)
	for _, d := range disclosures {
		b.WriteByte('~')
		b.WriteString(d)
	}
	b.WriteByte('~')
	b.WriteString(keyBindingJWT)
	return b.String()
}

// SignKeyBinding mints a kb-jwt over the given disclosures with the holder
// key. The public key is embedded in the header as a JWK so verifiers can
// resolve it self-contained; audience and nonce are optional.
func SignKeyBinding(holderKey ed25519.PrivateKey, issuerJWT string, disclosures []string, audience, nonce string) (string, error) {
	if len(holderKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("%w: bad private key size", ErrUnsupportedKey)
	}
	pub, ok := holderKey.Public().(ed25519.PublicKey)
	if !ok {
		return "", ErrUnsupportedKey
	}
	header := map[string]any{
		"alg": "EdDSA",
		"typ": KeyBindingTyp,
		"jwk": map[string]any{
			"kty": "OKP",
			"crv": "Ed25519",
			"x":   b64.EncodeToString(pub),
		},
	}
	payload := map[string]any{
		"iat":     time.Now().Unix(),
		"sd_hash": SDHash(issuerJWT, disclosures),
	}
	if audience != "" {
		payload["aud"] = audience
	}
	if nonce != "" {
		payload["nonce"] = nonce
	}
	return signCompact(header, payload, holderKey)
}

// VerifyKeyBinding verifies a compact SD-JWT presentation whose kb-jwt
// embeds the holder proof key as a header JWK.
func VerifyKeyBinding(presentation string) (*VerifiedPresentation, error) {
	p, err := ParsePresentation(presentation)
	if err != nil {
		return nil, err
	}
	_, issuerClaims, _, _, err := decodeJWT(p.IssuerJWT)
	if err != nil {
		return nil, err
	}
	kbHeader, kbClaims, signingInput, signature, err := decodeJWT(p.KeyBindingJWT)
	if err != nil {
		return nil, err
	}
	if typ, _ := kbHeader["typ"].(string); typ != KeyBindingTyp {
		return nil, fmt.Errorf("%w: got %q", ErrUnexpectedTyp, kbHeader["typ"])
	}
	if alg, _ := kbHeader["alg"].(string); alg != "EdDSA" {
		return nil, fmt.Errorf("%w: got %q", ErrUnexpectedAlg, kbHeader["alg"])
	}
	proofKey, err := proofKeyFromHeader(kbHeader)
	if err != nil {
		return nil, err
	}
	return verify(p, issuerClaims, kbClaims, signingInput, signature, proofKey)
}

// VerifyKeyBindingWithKey is VerifyKeyBinding for verifiers that already
// know the holder key out of band; the kb-jwt need not embed a JWK.
func VerifyKeyBindingWithKey(presentation string, holderKey ed25519.PublicKey) (*VerifiedPresentation, error) {
	p, err := ParsePresentation(presentation)
	if err != nil {
		return nil, err
	}
	_, issuerClaims, _, _, err := decodeJWT(p.IssuerJWT)
	if err != nil {
		return nil, err
	}
	kbHeader, kbClaims, signingInput, signature, err := decodeJWT(p.KeyBindingJWT)
	if err != nil {
		return nil, err
	}
	if typ, _ := kbHeader["typ"].(string); typ != KeyBindingTyp {
		return nil, fmt.Errorf("%w: got %q", ErrUnexpectedTyp, kbHeader["typ"])
	}
	if alg, _ := kbHeader["alg"].(string); alg != "EdDSA" {
		return nil, fmt.Errorf("%w: got %q", ErrUnexpectedAlg, kbHeader["alg"])
	}
	return verify(p, issuerClaims, kbClaims, signingInput, signature, holderKey)
}

func verify(p *Presentation, issuerClaims, kbClaims map[string]any, signingInput string, signature []byte, proofKey ed25519.PublicKey) (*VerifiedPresentation, error) {
	cnf, _ := issuerClaims["cnf"].(map[string]any)
	jkt, _ := cnf["jkt"].(string)
	if jkt == "" {
		return nil, ErrMissingCnf
	}
	if jkt != Thumbprint(proofKey) {
		return nil, ErrJKTMismatch
	}
	if got, want := kbClaims["sd_hash"], SDHash(p.IssuerJWT, p.Disclosures); got != want {
		return nil, ErrSDHashMismatch
	}
	if !ed25519.Verify(proofKey, []byte(signingInput), signature) {
		return nil, ErrSignatureInvalid
	}
	return &VerifiedPresentation{
		IssuerClaims:     issuerClaims,
		KeyBindingClaims: kbClaims,
		Disclosures:      p.Disclosures,
		HolderPublicKey:  proofKey,
	}, nil
}

func proofKeyFromHeader(header map[string]any) (ed25519.PublicKey, error) {
	jwk, ok := header["jwk"].(map[string]any)
	if !ok {
		return nil, ErrMissingProofKey
	}
	if jwk["kty"] != "OKP" || jwk["crv"] != "Ed25519" {
		return nil, ErrUnsupportedKey
	}
	x, _ := jwk["x"].(string)
	raw, err := b64.DecodeString(x)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: bad x parameter", ErrUnsupportedKey)
	}
	return ed25519.PublicKey(raw), nil
}

func signCompact(header, payload map[string]any, key ed25519.PrivateKey) (string, error) {
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	signingInput := b64.EncodeToString(headerJSON) + "." + b64.EncodeToString(payloadJSON)
	signature := ed25519.Sign(key, []byte(signingInput))
	return signingInput + "." + b64.EncodeToString(signature), nil
}

func decodeJWT(token string) (header, payload map[string]any, signingInput string, signature []byte, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, "", nil, ErrMalformedJWT
	}
	headerBytes, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("%w: header: %v", ErrMalformedJWT, err)
	}
	payloadBytes, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("%w: payload: %v", ErrMalformedJWT, err)
	}
	signature, err = b64.DecodeString(parts[2])
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("%w: signature: %v", ErrMalformedJWT, err)
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, nil, "", nil, fmt.Errorf("%w: header: %v", ErrMalformedJWT, err)
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, nil, "", nil, fmt.Errorf("%w: payload: %v", ErrMalformedJWT, err)
	}
	return header, payload, parts[0] + "." + parts[1], signature, nil
}
