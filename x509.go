package spiffefed

import (
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"strings"
	"time"
)

var oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}

// X509VerifyOptions supplies request-specific verification inputs.
type X509VerifyOptions struct {
	TargetService string
	Intermediates []*x509.Certificate
	// Now overrides the verifier clock for this call when non-zero.
	Now time.Time
}

// X509SVIDVerifier verifies SPIFFE X.509-SVID leaf certificates using only
// the bundle for the trust domain named by the certificate's URI SAN.
type X509SVIDVerifier struct {
	Bundles *BundleSet
	Policy  *FederationPolicy
	// Clock is used when X509VerifyOptions.Now is zero.
	Clock func() time.Time
	// MaxChainLength includes the leaf certificate. Zero selects the default.
	MaxChainLength int
}

// NewX509SVIDVerifier constructs a strict verifier. The local trust domain
// must have an isolated bundle in bundles.
func NewX509SVIDVerifier(bundles *BundleSet, policy *FederationPolicy) (*X509SVIDVerifier, error) {
	if bundles == nil {
		return nil, fmt.Errorf("%w: bundle set is nil", ErrInvalidTrustBundle)
	}
	if policy == nil {
		return nil, fmt.Errorf("%w: federation policy is required", ErrFederationDenied)
	}
	if _, ok := bundles.Bundle(policy.LocalTrustDomain()); !ok {
		return nil, fmt.Errorf("%w: local trust domain %q has no isolated bundle", ErrBundleNotFound, policy.LocalTrustDomain())
	}
	return &X509SVIDVerifier{
		Bundles:        bundles,
		Policy:         policy,
		Clock:          time.Now,
		MaxChainLength: 6,
	}, nil
}

// Verify validates leaf and its optional intermediate chain. It deliberately
// does not accept a leaf-provided chain: callers must pass intermediates
// explicitly from their verified transport or attestation source.
func (v *X509SVIDVerifier) Verify(leaf *x509.Certificate, opts X509VerifyOptions) (ID, error) {
	if v == nil || v.Bundles == nil || v.Policy == nil {
		return ID{}, fmt.Errorf("%w: verifier is not configured", ErrInvalidCertificate)
	}
	if leaf == nil {
		return ID{}, fmt.Errorf("%w: leaf certificate is nil", ErrInvalidCertificate)
	}

	id, err := validateX509SVIDLeaf(leaf)
	if err != nil {
		return ID{}, err
	}
	if err := v.Policy.Authorize(opts.TargetService, id.TrustDomain); err != nil {
		return ID{}, err
	}
	bundle, ok := v.Bundles.Bundle(id.TrustDomain)
	if !ok {
		return ID{}, fmt.Errorf("%w: %q", ErrBundleNotFound, id.TrustDomain)
	}

	now := opts.Now
	if now.IsZero() {
		if v.Clock != nil {
			now = v.Clock()
		} else {
			now = time.Now()
		}
	}

	intermediates := x509.NewCertPool()
	for i, cert := range opts.Intermediates {
		if cert == nil {
			return ID{}, fmt.Errorf("%w: intermediate certificate %d is nil", ErrInvalidCertificate, i)
		}
		if !cert.IsCA {
			return ID{}, fmt.Errorf("%w: intermediate certificate %d is not a CA", ErrInvalidCertificate, i)
		}
		intermediates.AddCert(cert)
	}

	chains, err := leaf.Verify(x509.VerifyOptions{
		Roots:         bundle.pool,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		return ID{}, fmt.Errorf("%w: %v", ErrCertificateNotTrusted, err)
	}

	maxChainLength := v.MaxChainLength
	if maxChainLength == 0 {
		maxChainLength = 6
	}
	if maxChainLength < 2 {
		return ID{}, fmt.Errorf("%w: MaxChainLength must be at least 2", ErrInvalidCertificate)
	}
	validChain := false
	for _, chain := range chains {
		if len(chain) <= maxChainLength {
			validChain = true
			break
		}
	}
	if !validChain {
		return ID{}, fmt.Errorf("%w: certificate chain exceeds %d certificates", ErrInvalidCertificate, maxChainLength)
	}
	return id, nil
}

// VerifyChain verifies chain[0] as the leaf and chain[1:] as intermediates.
func (v *X509SVIDVerifier) VerifyChain(chain []*x509.Certificate, opts X509VerifyOptions) (ID, error) {
	if len(chain) == 0 {
		return ID{}, fmt.Errorf("%w: certificate chain is empty", ErrInvalidCertificate)
	}
	if len(chain) > 1 {
		opts.Intermediates = append([]*x509.Certificate(nil), chain[1:]...)
	}
	return v.Verify(chain[0], opts)
}

func validateX509SVIDLeaf(leaf *x509.Certificate) (ID, error) {
	if leaf.IsCA {
		return ID{}, fmt.Errorf("%w: leaf has cA=true", ErrInvalidCertificate)
	}
	if len(leaf.UnhandledCriticalExtensions) != 0 {
		return ID{}, fmt.Errorf("%w: leaf has unhandled critical extensions", ErrInvalidCertificate)
	}

	if len(leaf.URIs) != 1 {
		return ID{}, fmt.Errorf("%w: expected exactly one URI SAN, got %d", ErrInvalidCertificate, len(leaf.URIs))
	}
	if len(leaf.DNSNames) != 0 || len(leaf.EmailAddresses) != 0 || len(leaf.IPAddresses) != 0 {
		return ID{}, fmt.Errorf("%w: non-URI SANs are forbidden in X.509-SVIDs", ErrInvalidCertificate)
	}
	if leaf.URIs[0] == nil {
		return ID{}, fmt.Errorf("%w: URI SAN is nil", ErrInvalidCertificate)
	}
	rawURI, err := singleRawURISAN(leaf)
	if err != nil {
		return ID{}, err
	}
	id, err := ParseID(rawURI)
	if err != nil {
		return ID{}, fmt.Errorf("%w: URI SAN: %w", ErrInvalidCertificate, err)
	}

	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return ID{}, fmt.Errorf("%w: KeyUsage must include digitalSignature", ErrInvalidCertificate)
	}
	const forbiddenKeyUsage = x509.KeyUsageContentCommitment |
		x509.KeyUsageDataEncipherment |
		x509.KeyUsageKeyAgreement |
		x509.KeyUsageCertSign |
		x509.KeyUsageCRLSign |
		x509.KeyUsageEncipherOnly |
		x509.KeyUsageDecipherOnly
	if leaf.KeyUsage&forbiddenKeyUsage != 0 {
		return ID{}, fmt.Errorf("%w: KeyUsage contains certificate or non-signing bits", ErrInvalidCertificate)
	}

	for _, usage := range leaf.ExtKeyUsage {
		if usage != x509.ExtKeyUsageServerAuth && usage != x509.ExtKeyUsageClientAuth {
			return ID{}, fmt.Errorf("%w: ExtKeyUsage %d is not allowed for an SVID", ErrInvalidCertificate, usage)
		}
	}
	if len(leaf.UnknownExtKeyUsage) != 0 {
		return ID{}, fmt.Errorf("%w: unknown ExtKeyUsage OIDs are forbidden", ErrInvalidCertificate)
	}

	switch leaf.SignatureAlgorithm {
	case x509.MD2WithRSA, x509.MD5WithRSA, x509.SHA1WithRSA, x509.DSAWithSHA1, x509.ECDSAWithSHA1:
		return ID{}, fmt.Errorf("%w: weak signature algorithm %q", ErrInvalidCertificate, strings.ToLower(leaf.SignatureAlgorithm.String()))
	}
	return id, nil
}

func singleRawURISAN(cert *x509.Certificate) (string, error) {
	var extValue []byte
	found := false
	for _, extension := range cert.Extensions {
		if !extension.Id.Equal(oidSubjectAltName) {
			continue
		}
		if found {
			return "", fmt.Errorf("%w: duplicate subjectAltName extensions", ErrInvalidCertificate)
		}
		found = true
		extValue = extension.Value
	}
	if !found {
		return "", fmt.Errorf("%w: subjectAltName extension is missing", ErrInvalidCertificate)
	}

	var sequence asn1.RawValue
	rest, err := asn1.Unmarshal(extValue, &sequence)
	if err != nil || len(rest) != 0 {
		return "", fmt.Errorf("%w: malformed subjectAltName extension", ErrInvalidCertificate)
	}
	if sequence.Class != asn1.ClassUniversal || sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
		return "", fmt.Errorf("%w: subjectAltName is not a SEQUENCE", ErrInvalidCertificate)
	}

	var uri string
	count := 0
	data := sequence.Bytes
	for len(data) > 0 {
		var generalName asn1.RawValue
		data, err = asn1.Unmarshal(data, &generalName)
		if err != nil {
			return "", fmt.Errorf("%w: malformed subjectAltName entry", ErrInvalidCertificate)
		}
		count++
		if count > 1 {
			return "", fmt.Errorf("%w: subjectAltName must contain only one URI", ErrInvalidCertificate)
		}
		if generalName.Class != asn1.ClassContextSpecific || generalName.Tag != 6 || generalName.IsCompound {
			return "", fmt.Errorf("%w: subjectAltName contains a non-URI entry", ErrInvalidCertificate)
		}
		uri = string(generalName.Bytes)
	}
	if count != 1 {
		return "", fmt.Errorf("%w: subjectAltName must contain exactly one URI", ErrInvalidCertificate)
	}
	return uri, nil
}
