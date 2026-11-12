// App Store Server signedTransactionInfo JWS verification.
package appstorejws

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Apple signing-chain marker OIDs.
const (
	// OIDLeafMarker is the extension carried by Apple-issued transaction
	// signing leaf certificates.
	OIDLeafMarker = "1.2.840.113635.100.6.11.1"
	// OIDIntermediateMarker is the extension carried by the Apple Worldwide
	// Developer Relations intermediate certificate.
	OIDIntermediateMarker = "1.2.840.113635.100.6.2.1"
)

// App Store Server environments reported in the environment claim.
const (
	EnvironmentProduction = "Production"
	EnvironmentSandbox    = "Sandbox"
	EnvironmentXcode      = "Xcode"
)

var (
	// ErrMalformedJWS is returned when the token is not a three-part compact
	// JWS or a header/payload cannot be decoded or parsed.
	ErrMalformedJWS = errors.New("appstorejws: malformed compact JWS")
	// ErrUnsupportedAlg is returned when the JOSE header alg is not ES256.
	ErrUnsupportedAlg = errors.New("appstorejws: unsupported JWS alg; only ES256 is accepted")
	// ErrInvalidX5C is returned when the x5c header does not hold exactly
	// three certificates or one of them is not valid DER.
	ErrInvalidX5C = errors.New("appstorejws: x5c must contain exactly three DER certificates")
	// ErrRootMismatch is returned when the x5c root is not identical to the
	// pinned Apple root certificate.
	ErrRootMismatch = errors.New("appstorejws: x5c root does not match pinned Apple root DER")
	// ErrMissingOID is returned when the leaf or intermediate certificate
	// lacks its required Apple marker extension.
	ErrMissingOID = errors.New("appstorejws: certificate missing required Apple marker OID")
	// ErrInvalidChain is returned when a chain signature does not verify.
	ErrInvalidChain = errors.New("appstorejws: invalid certificate chain signature")
	// ErrCertExpired is returned when a chain certificate is outside its
	// validity window at signedDate.
	ErrCertExpired = errors.New("appstorejws: certificate not valid at signedDate")
	// ErrInvalidSignature is returned when the ES256 JWS signature does not
	// verify against the leaf public key.
	ErrInvalidSignature = errors.New("appstorejws: invalid JWS signature")
	// ErrBundleIDMismatch is returned when the bundleId claim differs from
	// the configured application.
	ErrBundleIDMismatch = errors.New("appstorejws: bundleId does not match configured application")
	// ErrEnvironmentMismatch is returned when the environment claim differs
	// from the server's configured environment.
	ErrEnvironmentMismatch = errors.New("appstorejws: environment does not match server environment")
	// ErrRevoked is returned when the payload carries a revocationDate.
	ErrRevoked = errors.New("appstorejws: transaction is revoked")
	// ErrMissingClaim is returned when a required claim such as signedDate is
	// absent.
	ErrMissingClaim = errors.New("appstorejws: required claim missing")
)

// Config configures a Verifier. RootDER is the pinned Apple root certificate
// in DER form; BundleID and Environment identify this deployment.
type Config struct {
	RootDER     []byte
	BundleID    string
	Environment string
}

// Verifier is a safe-to-reuse, immutable JWS verifier bound to one pinned
// Apple root, bundle id and environment.
type Verifier struct {
	rootDER     []byte
	bundleID    string
	environment string
}

// New validates the configuration and returns a ready Verifier.
func New(cfg Config) (*Verifier, error) {
	if _, err := x509.ParseCertificate(cfg.RootDER); err != nil {
		return nil, fmt.Errorf("appstorejws: parse pinned root DER: %w", err)
	}
	if cfg.BundleID == "" {
		return nil, errors.New("appstorejws: Config.BundleID is required")
	}
	if cfg.Environment == "" {
		return nil, errors.New("appstorejws: Config.Environment is required")
	}
	return &Verifier{
		rootDER:     append([]byte(nil), cfg.RootDER...),
		bundleID:    cfg.BundleID,
		environment: cfg.Environment,
	}, nil
}

// TransactionInfo is the subset of signedTransactionInfo fields needed for
// server-side validation. Raw holds the complete decoded payload for callers
// that need additional fields.
type TransactionInfo struct {
	AppTransactionID *int64         `json:"appTransactionId,omitempty"`
	BundleID         string         `json:"bundleId"`
	Environment      string         `json:"environment"`
	OriginalID       string         `json:"originalTransactionId"`
	ProductID        string         `json:"productId"`
	RevocationDate   *int64         `json:"revocationDate,omitempty"`
	SignedDate       int64          `json:"signedDate"`
	TransactionID    string         `json:"transactionId"`
	Type             string         `json:"type"`
	Raw              map[string]any `json:"-"`
}

// Revoked reports whether the payload carries a revocationDate.
func (t *TransactionInfo) Revoked() bool {
	return t.RevocationDate != nil
}

type jwsHeader struct {
	Alg string   `json:"alg"`
	X5c []string `json:"x5c"`
}

// Verify validates a compact JWS signedTransactionInfo and returns the
// decoded transaction. A revoked transaction returns ErrRevoked together with
// the populated TransactionInfo; every other failure returns a nil
// transaction.
func (v *Verifier) Verify(token string) (*TransactionInfo, error) {
	parts := splitCompactJWS(token)
	if len(parts) != 3 {
		return nil, ErrMalformedJWS
	}

	headerJSON, err := base64URLDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrMalformedJWS, err)
	}
	var header jwsHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrMalformedJWS, err)
	}
	if header.Alg != "ES256" {
		return nil, fmt.Errorf("%w: got %q", ErrUnsupportedAlg, header.Alg)
	}
	if len(header.X5c) != 3 {
		return nil, fmt.Errorf("%w: got %d certificate(s)", ErrInvalidX5C, len(header.X5c))
	}

	certs := make([]*x509.Certificate, 3)
	for i, item := range header.X5c {
		der, err := base64AnyDecode(item)
		if err != nil {
			return nil, fmt.Errorf("%w: entry %d: %v", ErrInvalidX5C, i, err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("%w: entry %d: %v", ErrInvalidX5C, i, err)
		}
		certs[i] = cert
	}
	leaf, intermediate, root := certs[0], certs[1], certs[2]

	// Pin the trust anchor: an attacker cannot substitute a self-signed root
	// of their own.
	if root.Raw == nil || !bytesEqual(root.Raw, v.rootDER) {
		return nil, ErrRootMismatch
	}

	if !hasOID(leaf, OIDLeafMarker) {
		return nil, fmt.Errorf("%w: leaf missing %s", ErrMissingOID, OIDLeafMarker)
	}
	if !hasOID(intermediate, OIDIntermediateMarker) {
		return nil, fmt.Errorf("%w: intermediate missing %s", ErrMissingOID, OIDIntermediateMarker)
	}

	// Verify each link of the chain cryptographically.
	if err := leaf.CheckSignatureFrom(intermediate); err != nil {
		return nil, fmt.Errorf("%w: leaf not signed by intermediate: %v", ErrInvalidChain, err)
	}
	if err := intermediate.CheckSignatureFrom(root); err != nil {
		return nil, fmt.Errorf("%w: intermediate not signed by root: %v", ErrInvalidChain, err)
	}
	if err := root.CheckSignatureFrom(root); err != nil {
		return nil, fmt.Errorf("%w: root is not self-signed: %v", ErrInvalidChain, err)
	}

	payloadJSON, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformedJWS, err)
	}
	var tx TransactionInfo
	if err := json.Unmarshal(payloadJSON, &tx); err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformedJWS, err)
	}
	if err := json.Unmarshal(payloadJSON, &tx.Raw); err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformedJWS, err)
	}
	if tx.SignedDate == 0 {
		return nil, fmt.Errorf("%w: signedDate", ErrMissingClaim)
	}

	// Validity is evaluated at the signed instant so expired-but-historically
	// valid chains can be reasoned about precisely.
	signedAt := msToTime(tx.SignedDate)
	for position, cert := range certs {
		if signedAt.Before(cert.NotBefore) || signedAt.After(cert.NotAfter) {
			return nil, fmt.Errorf("%w: %s certificate window %s..%s does not cover %s",
				ErrCertExpired, chainPosition(position),
				cert.NotBefore.UTC(), cert.NotAfter.UTC(), signedAt.UTC())
		}
	}

	publicKey, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: leaf public key is not ECDSA", ErrInvalidSignature)
	}
	if publicKey.Curve.Params().Name != "P-256" {
		return nil, fmt.Errorf("%w: ES256 requires P-256, got %s", ErrInvalidSignature, publicKey.Curve.Params().Name)
	}

	signature, err := base64URLDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature: %v", ErrInvalidSignature, err)
	}
	if len(signature) != 64 {
		return nil, fmt.Errorf("%w: ES256 signature must be 64 raw bytes, got %d", ErrInvalidSignature, len(signature))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	rr := new(big.Int).SetBytes(signature[:32])
	ss := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(publicKey, digest[:], rr, ss) {
		return nil, ErrInvalidSignature
	}

	if tx.BundleID != v.bundleID {
		return nil, fmt.Errorf("%w: got %q", ErrBundleIDMismatch, tx.BundleID)
	}
	if tx.Environment != v.environment {
		return nil, fmt.Errorf("%w: got %q", ErrEnvironmentMismatch, tx.Environment)
	}

	if tx.Revoked() {
		return &tx, fmt.Errorf("%w at %d", ErrRevoked, *tx.RevocationDate)
	}
	return &tx, nil
}

func splitCompactJWS(token string) []string {
	first := -1
	last := -1
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first <= 0 || last == len(token)-1 || first == last {
		return nil
	}
	return []string{token[:first], token[first+1 : last], token[last+1:]}
}

// base64URLDecode accepts the unpadded and padded URL-safe alphabets used by
// compact JWS.
func base64URLDecode(value string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

// base64AnyDecode additionally tolerates the standard alphabet because x5c
// values are DER base64 per RFC 5280 and may appear padded in the wild.
func base64AnyDecode(value string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.URLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func hasOID(cert *x509.Certificate, oid string) bool {
	for _, ext := range cert.Extensions {
		if ext.Id.String() == oid {
			return true
		}
	}
	return false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func msToTime(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

func chainPosition(i int) string {
	switch i {
	case 0:
		return "leaf"
	case 1:
		return "intermediate"
	default:
		return "root"
	}
}
