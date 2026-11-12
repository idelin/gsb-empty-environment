package netsec

import (
	"context"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FulcioIdentityOID is the first Fulcio OID used for certificate identity
// attributes.  Callers that use a different certificate extension must set
// Policy.IdentityOID explicitly.
var FulcioIdentityOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}

// DefaultIdentityOID is used when a Policy does not specify an extension.
var DefaultIdentityOID = FulcioIdentityOID

// Artifact identifies the single artifact to which an attestation must bind.
type Artifact struct {
	Name   string
	Digest map[string]string
}

// Policy is the admission host's complete trust and binding policy.
//
// The repository, commit and builder fields describe what must appear in the
// in-toto predicate.  Identity is the exact value that must appear in the
// certificate extension selected by IdentityOID.  If Identity is empty, the
// predicate Builder is also used as the certificate identity.
type Policy struct {
	// TargetName is optional.  When set, the sole in-toto subject must have
	// exactly this name.
	TargetName    string
	TargetDigest  map[string]string
	SubjectDigest map[string]string

	Artifact       Artifact
	ArtifactName   string
	ArtifactDigest string

	Repository         string
	ExpectedRepository string
	Commit             string
	ExpectedCommit     string
	Builder            string
	ExpectedBuilder    string
	BuilderID          string
	BuilderIdentity    string

	Identity               string
	ExpectedIdentity       string
	SignerIdentity         string
	ExpectedSignerIdentity string
	CertIdentity           string
	CertificateIdentity    string
	IdentityOID            asn1.ObjectIdentifier
	IdentityExtension      asn1.ObjectIdentifier
	IdentityExtensionOID   asn1.ObjectIdentifier
	CertIdentityOID        asn1.ObjectIdentifier
	CertificateIdentityOID asn1.ObjectIdentifier

	Roots        *x509.CertPool
	TrustRoots   *x509.CertPool
	TrustedRoots *x509.CertPool
	CertPool     *x509.CertPool

	Time           time.Time
	VerifyTime     time.Time
	CurrentTime    time.Time
	EvaluationTime time.Time
	At             time.Time

	PredicateType         string
	ExpectedPredicateType string

	Publish        func(*VerifiedStatement) error
	OnPublish      func(*VerifiedStatement) error
	PublishContext func(context.Context, *VerifiedStatement) error
}

// VerifiedStatement is returned only after every DSSE, certificate and
// in-toto binding check has succeeded.
type VerifiedStatement struct {
	Statement

	Payload          []byte
	PayloadType      string
	Signer           *x509.Certificate
	Certificate      *x509.Certificate
	CertificateChain []*x509.Certificate
	SignerIdentity   string
	Identity         string
}

// VerifyRequest groups the data normally supplied by an admission host.
type VerifyRequest struct {
	Envelope     Envelope
	Certificates []*x509.Certificate
	Policy       Policy
}

// Verifier binds a policy to a certificate chain and can be reused by a
// long-running admission service.
type Verifier struct {
	Policy       Policy
	Certificates []*x509.Certificate
}

// NewVerifier creates a reusable verifier for a policy and certificate chain.
func NewVerifier(policy Policy, certificates []*x509.Certificate) *Verifier {
	return &Verifier{Policy: policy, Certificates: certificates}
}

func (p Policy) trustedRoots() *x509.CertPool {
	if p.Roots != nil {
		return p.Roots
	}
	if p.TrustRoots != nil {
		return p.TrustRoots
	}
	if p.TrustedRoots != nil {
		return p.TrustedRoots
	}
	return p.CertPool
}

func (p Policy) verificationTime() time.Time {
	if !p.Time.IsZero() {
		return p.Time
	}
	if !p.VerifyTime.IsZero() {
		return p.VerifyTime
	}
	if !p.CurrentTime.IsZero() {
		return p.CurrentTime
	}
	if !p.EvaluationTime.IsZero() {
		return p.EvaluationTime
	}
	if !p.At.IsZero() {
		return p.At
	}
	return time.Now()
}

func (p Policy) identityOID() (asn1.ObjectIdentifier, error) {
	switch {
	case len(p.IdentityOID) != 0:
		return p.IdentityOID, nil
	case len(p.IdentityExtension) != 0:
		return p.IdentityExtension, nil
	case len(p.IdentityExtensionOID) != 0:
		return p.IdentityExtensionOID, nil
	case len(p.CertIdentityOID) != 0:
		return p.CertIdentityOID, nil
	case len(p.CertificateIdentityOID) != 0:
		return p.CertificateIdentityOID, nil
	case len(DefaultIdentityOID) != 0:
		return DefaultIdentityOID, nil
	default:
		return nil, errors.New("netsec: no certificate identity extension OID configured")
	}
}

func (p Policy) expectedCertificateIdentity() string {
	for _, value := range []string{
		p.Identity,
		p.ExpectedIdentity,
		p.SignerIdentity,
		p.ExpectedSignerIdentity,
		p.CertIdentity,
		p.CertificateIdentity,
		p.BuilderIdentity,
		p.BuilderID,
		p.Builder,
		p.ExpectedBuilder,
	} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (p Policy) expectedBuilder() string {
	for _, value := range []string{p.Builder, p.ExpectedBuilder, p.BuilderID, p.BuilderIdentity} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return p.expectedCertificateIdentity()
}

func (p Policy) expectedPredicateType() string {
	if p.PredicateType != "" {
		return p.PredicateType
	}
	return p.ExpectedPredicateType
}

func (p Policy) effectiveTarget() (string, map[string]string, error) {
	name := p.TargetName
	if name == "" {
		name = p.ArtifactName
	}
	if name == "" {
		name = p.Artifact.Name
	}

	if len(p.TargetDigest) != 0 {
		return name, p.TargetDigest, nil
	}
	if len(p.SubjectDigest) != 0 {
		return name, p.SubjectDigest, nil
	}
	if len(p.Artifact.Digest) != 0 {
		return name, p.Artifact.Digest, nil
	}

	digest := p.ArtifactDigest
	if digest == "" {
		return "", nil, errors.New("netsec: policy has no target artifact digest")
	}
	algorithm := "sha256"
	value := digest
	if separator := strings.IndexByte(digest, ':'); separator >= 0 {
		algorithm = digest[:separator]
		value = digest[separator+1:]
	} else if separator := strings.IndexByte(digest, '='); separator >= 0 {
		algorithm = digest[:separator]
		value = digest[separator+1:]
	}
	algorithm = strings.ToLower(strings.TrimSpace(algorithm))
	value = strings.TrimSpace(value)
	if algorithm == "" || value == "" {
		return "", nil, fmt.Errorf("netsec: invalid artifact digest %q", digest)
	}
	return name, map[string]string{algorithm: value}, nil
}

func (p Policy) notify(ctx context.Context, statement *VerifiedStatement) error {
	switch {
	case p.PublishContext != nil:
		return p.PublishContext(ctx, statement)
	case p.Publish != nil:
		return p.Publish(statement)
	case p.OnPublish != nil:
		return p.OnPublish(statement)
	default:
		return nil
	}
}

// Verify verifies a DSSE envelope using the supplied certificate chain.
func (v Verifier) Verify(ctx context.Context, env Envelope) (*VerifiedStatement, error) {
	return Verify(ctx, env, v.Certificates, v.Policy)
}

// VerifyBytes verifies a JSON-encoded DSSE envelope with the verifier's policy.
func (v Verifier) VerifyBytes(ctx context.Context, envelopeJSON []byte) (*VerifiedStatement, error) {
	return VerifyBytes(ctx, envelopeJSON, v.Certificates, v.Policy)
}
