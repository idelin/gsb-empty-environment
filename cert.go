package rpki

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrCertificate        = errors.New("rpki: certificate validation failed")
	ErrCertSignature      = errors.New("rpki: certificate signature invalid")
	ErrCertValidity       = errors.New("rpki: certificate outside validity interval")
	ErrCertUsage          = errors.New("rpki: certificate key usage invalid")
	ErrTALKeyMismatch     = errors.New("rpki: trust anchor key does not match TAL")
	ErrMissingRFC3779     = errors.New("rpki: certificate has no RFC 3779 resources")
	ErrUnsupportedKeyType = errors.New("rpki: only ECDSA P-256 is accepted in strict mode")
)

// CertInfo is a parsed RPKI certificate plus its RFC 3779/SIA extensions.
type CertInfo struct {
	X509              *x509.Certificate
	Claims            ResourceClaims
	HasRFC3779        bool
	CARepositories    []string
	Resources         ResourceSet
	ResourcesExpanded bool
}

// ParseCertificate parses DER and extracts RPKI-specific extensions.
func ParseCertificate(der []byte) (*CertInfo, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("%w: x509 parse: %v", ErrCertificate, err)
	}
	claims, hasResources, err := ParseResourceExtensions(cert.Extensions)
	if err != nil {
		return nil, err
	}
	var sia []string
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidSubjectInfoAccess) {
			uris, err := ParseCARepositoryURIs(ext.Value)
			if err != nil {
				return nil, err
			}
			sia = append(sia, uris...)
		}
	}
	return &CertInfo{X509: cert, Claims: claims, HasRFC3779: hasResources, CARepositories: sia}, nil
}

// StateKey returns a stable per-CA key based on SKI, falling back to SPKI hash.
func (c *CertInfo) StateKey() string {
	if len(c.X509.SubjectKeyId) > 0 {
		return "ski-" + hex.EncodeToString(c.X509.SubjectKeyId)
	}
	sum := sha256.Sum256(c.X509.RawSubjectPublicKeyInfo)
	return "spki-" + hex.EncodeToString(sum[:])
}

// VerifyOptions controls certificate chain checks.
type VerifyOptions struct {
	Now         time.Time
	RequireP256 bool
}

func (o VerifyOptions) at() time.Time {
	if o.Now.IsZero() {
		return time.Now().UTC()
	}
	return o.Now.UTC()
}

// VerifyTrustAnchor binds a self-signed certificate to the pinned TAL SPKI.
func VerifyTrustAnchor(tal TAL, cert *CertInfo, opts VerifyOptions) error {
	if cert == nil || cert.X509 == nil {
		return fmt.Errorf("%w: nil trust anchor", ErrCertificate)
	}
	if !bytes.Equal(cert.X509.RawSubjectPublicKeyInfo, tal.SubjectPublicKeyInfo) {
		return fmt.Errorf("%w: certificate SPKI differs from %s", ErrTALKeyMismatch, tal.Name)
	}
	if opts.RequireP256 {
		if err := requireP256(cert.X509); err != nil {
			return err
		}
	}
	if err := checkValidity(cert.X509, opts.at()); err != nil {
		return err
	}
	if !cert.X509.IsCA || !hasKeyUsage(cert.X509.KeyUsage, x509.KeyUsageCertSign) {
		return fmt.Errorf("%w: trust anchor must be CA with CertSign", ErrCertUsage)
	}
	if err := cert.X509.CheckSignatureFrom(cert.X509); err != nil {
		return fmt.Errorf("%w: trust anchor self-signature: %v", ErrCertSignature, err)
	}
	if !cert.HasRFC3779 {
		return ErrMissingRFC3779
	}
	resources, err := cert.Claims.Resolve(nil)
	if err != nil {
		return err
	}
	if resources.Empty() {
		return fmt.Errorf("%w: trust anchor declares empty resources", ErrMissingRFC3779)
	}
	cert.Resources = resources
	cert.ResourcesExpanded = true
	return nil
}

// VerifySubordinate verifies signature, validity, CA usage, and strict resources.
// On success cert.Resources is the child's effective inherited/explicit set.
func VerifySubordinate(cert, parent *CertInfo, opts VerifyOptions) error {
	if cert == nil || parent == nil {
		return fmt.Errorf("%w: missing cert or issuer", ErrCertificate)
	}
	if opts.RequireP256 {
		if err := requireP256(cert.X509); err != nil {
			return err
		}
		if err := requireP256(parent.X509); err != nil {
			return err
		}
	}
	if err := checkValidity(cert.X509, opts.at()); err != nil {
		return err
	}
	if err := cert.X509.CheckSignatureFrom(parent.X509); err != nil {
		return fmt.Errorf("%w: issued by %q: %v", ErrCertSignature, parent.X509.Subject, err)
	}
	if !bytes.Equal(cert.X509.RawIssuer, parent.X509.RawSubject) {
		return fmt.Errorf("%w: issuer DN mismatch", ErrCertSignature)
	}
	if !cert.HasRFC3779 {
		return ErrMissingRFC3779
	}
	resources, err := cert.Claims.Resolve(&parent.Resources)
	if err != nil {
		return err
	}
	if resources.Empty() {
		return fmt.Errorf("%w: subordinate declares empty resources", ErrMissingRFC3779)
	}
	cert.Resources = resources
	cert.ResourcesExpanded = true
	return nil
}

// VerifyEECertificate verifies an EE against its issuing CA and expands inherit.
// EE resources are not used for further delegation but are strictly bounded.
func VerifyEECertificate(ee, issuer *CertInfo, opts VerifyOptions) error {
	if ee == nil || issuer == nil {
		return fmt.Errorf("%w: missing EE or issuer", ErrCertificate)
	}
	if opts.RequireP256 {
		if err := requireP256(ee.X509); err != nil {
			return err
		}
	}
	if err := checkValidity(ee.X509, opts.at()); err != nil {
		return err
	}
	if err := ee.X509.CheckSignatureFrom(issuer.X509); err != nil {
		return fmt.Errorf("%w: EE signature: %v", ErrCertSignature, err)
	}
	if ee.X509.IsCA {
		return fmt.Errorf("%w: EE certificate must not be a CA", ErrCertUsage)
	}
	if !hasKeyUsage(ee.X509.KeyUsage, x509.KeyUsageDigitalSignature) {
		return fmt.Errorf("%w: EE lacks digitalSignature usage", ErrCertUsage)
	}
	if !ee.HasRFC3779 {
		return ErrMissingRFC3779
	}
	resources, err := ee.Claims.Resolve(&issuer.Resources)
	if err != nil {
		return err
	}
	if resources.Empty() {
		return fmt.Errorf("%w: EE declares empty resources", ErrMissingRFC3779)
	}
	ee.Resources = resources
	ee.ResourcesExpanded = true
	return nil
}

func requireP256(cert *x509.Certificate) error {
	if cert.PublicKeyAlgorithm != x509.ECDSA {
		return fmt.Errorf("%w: %s uses %s", ErrUnsupportedKeyType, cert.Subject, cert.PublicKeyAlgorithm)
	}
	key, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve == nil || key.Curve.Params() == nil || key.Curve.Params().Name != "P-256" {
		return fmt.Errorf("%w: %s uses %v", ErrUnsupportedKeyType, cert.Subject, curveNameFromCert(cert))
	}
	return nil
}

func curveNameFromCert(cert *x509.Certificate) string {
	key, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return cert.PublicKeyAlgorithm.String()
	}
	return curveName(key)
}

func checkValidity(cert *x509.Certificate, now time.Time) error {
	if now.Before(cert.NotBefore) {
		return fmt.Errorf("%w: %q not valid until %s at %s", ErrCertValidity, cert.Subject, cert.NotBefore.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if now.After(cert.NotAfter) {
		return fmt.Errorf("%w: %q expired at %s", ErrCertValidity, cert.Subject, cert.NotAfter.Format(time.RFC3339))
	}
	return nil
}

func hasKeyUsage(got, want x509.KeyUsage) bool {
	return got&want != 0
}
