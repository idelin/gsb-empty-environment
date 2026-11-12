package netsec

import (
	"bytes"
	"crypto"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/x509"
	"fmt"
	"io"
	"time"
)

const defaultRequestTTL = 2 * time.Minute

// Identity is the verified result of an exported authenticator.
type Identity struct {
	Certificate    *x509.Certificate
	Chain          []*x509.Certificate
	VerifiedChains [][]*x509.Certificate
	RequestContext []byte
}

// Verifier validates exported authenticators for one TLS connection. Keeping a
// verifier (and its pending request ledger) per connection makes request
// consumption unambiguous.
type Verifier struct {
	roots     *x509.CertPool
	role      Role
	hash      crypto.Hash
	keyUsages []x509.ExtKeyUsage
	ledger    *requestLedger
	now       func() time.Time
}

type verifierConfig struct {
	role      Role
	hash      crypto.Hash
	keyUsages []x509.ExtKeyUsage
	ttl       time.Duration
	now       func() time.Time
	rng       io.Reader
}

// VerifierOption configures a Verifier.
type VerifierOption func(*verifierConfig) error

// WithRole sets the endpoint role that created the authenticator. The default
// is RoleServer.
func WithRole(role Role) VerifierOption {
	return func(config *verifierConfig) error {
		if _, _, err := role.ExporterLabels(); err != nil {
			return err
		}
		config.role = role
		return nil
	}
}

// WithHash sets the TLS authenticator hash. The default is SHA-256.
func WithHash(hash crypto.Hash) VerifierOption {
	return func(config *verifierConfig) error {
		if !hash.Available() {
			return fmt.Errorf("netsec: hash %v is unavailable", hash)
		}
		config.hash = hash
		return nil
	}
}

// WithKeyUsages sets the acceptable X.509 extended key usages. The default
// accepts any usage; applications should narrow this for their endpoint role.
func WithKeyUsages(usages ...x509.ExtKeyUsage) VerifierOption {
	return func(config *verifierConfig) error {
		config.keyUsages = append([]x509.ExtKeyUsage(nil), usages...)
		return nil
	}
}

// WithRequestTTL sets how long an issued request remains pending. A non-positive
// TTL disables expiry.
func WithRequestTTL(ttl time.Duration) VerifierOption {
	return func(config *verifierConfig) error {
		config.ttl = ttl
		return nil
	}
}

// WithClock supplies a clock for request expiry and certificate validation.
func WithClock(now func() time.Time) VerifierOption {
	return func(config *verifierConfig) error {
		if now == nil {
			return fmt.Errorf("netsec: nil clock")
		}
		config.now = now
		return nil
	}
}

// WithRandomSource supplies the request-context generator.
func WithRandomSource(rng io.Reader) VerifierOption {
	return func(config *verifierConfig) error {
		if rng == nil {
			return fmt.Errorf("netsec: nil random source")
		}
		config.rng = rng
		return nil
	}
}

// NewVerifier constructs a verifier bound to an explicit trust store.
func NewVerifier(roots *x509.CertPool, options ...VerifierOption) (*Verifier, error) {
	if roots == nil {
		return nil, fmt.Errorf("netsec: nil certificate pool")
	}
	config := verifierConfig{
		role:      RoleServer,
		hash:      crypto.SHA256,
		keyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		ttl:       defaultRequestTTL,
		now:       time.Now,
		rng:       defaultRNG,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}
	if !config.hash.Available() {
		return nil, fmt.Errorf("netsec: authenticator hash %v is unavailable", config.hash)
	}
	return &Verifier{
		roots:     roots,
		role:      config.role,
		hash:      config.hash,
		keyUsages: config.keyUsages,
		ledger:    newRequestLedger(config.ttl, config.rng, config.now, config.hash),
		now:       config.now,
	}, nil
}

// IssueRequest creates a fresh, unpredictable request that can be redeemed
// exactly once by Verify.
func (v *Verifier) IssueRequest() (Request, error) {
	if v == nil || v.ledger == nil {
		return Request{}, ErrRequestNotFound
	}
	return v.ledger.issue(v.role)
}

// Verify validates a Certificate || CertificateVerify || Finished sequence.
// The request is consumed before any attacker-controlled bytes are parsed, so
// malformed, replayed, and forged messages cannot reuse it.
func (v *Verifier) Verify(raw []byte, request Request, exporter Exporter) (*Identity, error) {
	if v == nil || v.ledger == nil {
		return nil, ErrRequestNotFound
	}
	if request.ledger != v.ledger {
		return nil, ErrRequestNotFound
	}
	if err := request.redeem(v.now()); err != nil {
		return nil, err
	}
	if exporter == nil {
		return nil, fmt.Errorf("%w: nil exporter", ErrExporter)
	}

	authenticator, err := ParseAuthenticator(raw)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(authenticator.CertificateRequestContext, request.context) {
		return nil, ErrRequestContextMismatch
	}
	if len(authenticator.FinishedVerifyData) != v.hash.Size() {
		return nil, fmt.Errorf("%w: Finished length does not match authenticator hash", ErrMalformed)
	}

	certificates := make([]*x509.Certificate, 0, len(authenticator.CertificateChainDER))
	for _, der := range authenticator.CertificateChainDER {
		certificate, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCertificate, err)
		}
		certificates = append(certificates, certificate)
	}
	leaf := certificates[0]

	intermediates := x509.NewCertPool()
	for _, certificate := range certificates[1:] {
		intermediates.AddCert(certificate)
	}
	verifiedChains, err := leaf.Verify(x509.VerifyOptions{
		Roots:         v.roots,
		Intermediates: intermediates,
		KeyUsages:     v.keyUsages,
		CurrentTime:   v.now(),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCertificate, err)
	}

	handshakeLabel, finishedLabel, err := v.role.ExporterLabels()
	if err != nil {
		return nil, err
	}
	handshakeContext, err := exportKeyMaterial(exporter, handshakeLabel, v.hash.Size())
	if err != nil {
		return nil, err
	}
	finishedKey, err := exportKeyMaterial(exporter, finishedLabel, v.hash.Size())
	if err != nil {
		return nil, err
	}

	verifyInput, err := CertificateVerifyInput(handshakeContext, request.wire, authenticator.Certificate, v.hash)
	if err != nil {
		return nil, err
	}
	if err := verifyCertificateSignature(leaf, authenticator.SignatureScheme, authenticator.Signature, verifyInput); err != nil {
		return nil, err
	}

	finishedTranscript, err := FinishedTranscript(
		handshakeContext,
		request.wire,
		authenticator.Certificate,
		authenticator.CertificateVerify,
		v.hash,
	)
	if err != nil {
		return nil, err
	}
	if err := verifyFinished(finishedKey, finishedTranscript, authenticator.FinishedVerifyData, v.hash); err != nil {
		return nil, err
	}

	return &Identity{
		Certificate:    leaf,
		Chain:          certificates,
		VerifiedChains: verifiedChains,
		RequestContext: request.Context(),
	}, nil
}
