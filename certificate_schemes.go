package netsec

import "errors"

// SignatureSchemeList is the wire-level list type used by the
// signature_algorithms and signature_algorithms_cert extensions.
type SignatureSchemeList = []SignatureScheme

// CertificateSignatureAlgorithms describes the two signature algorithm lists
// a TLS peer can send. SignatureAlgorithms comes from the signature_algorithms
// extension. SignatureAlgorithmsCert comes from signature_algorithms_cert.
//
// CertExtensionPresent must be set explicitly because an absent extension and
// an extension that is present with an empty list have different semantics.
// An empty signature_algorithms_cert list permits no certificate signature
// schemes; an absent one falls back to SignatureAlgorithms.
type CertificateSignatureAlgorithms struct {
	SignatureAlgorithms     []SignatureScheme
	SignatureAlgorithmsCert []SignatureScheme
	CertExtensionPresent    bool
}

// CertificateSignatureSchemePolicy is an alias for
// CertificateSignatureAlgorithms.
type CertificateSignatureSchemePolicy = CertificateSignatureAlgorithms

// NewCertificateSignatureAlgorithms builds a policy from the two peer lists.
// When certExtensionPresent is omitted, a non-nil certAlgs is treated as a
// present extension and a nil certAlgs as an absent extension.
func NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) *CertificateSignatureAlgorithms {
	present := signatureAlgorithmsCert != nil
	if len(certExtensionPresent) > 0 {
		present = certExtensionPresent[0]
	}
	return &CertificateSignatureAlgorithms{
		SignatureAlgorithms:     append([]SignatureScheme(nil), signatureAlgorithms...),
		SignatureAlgorithmsCert: append([]SignatureScheme(nil), signatureAlgorithmsCert...),
		CertExtensionPresent:    present,
	}
}

// NewCertificateSignatureAlgorithmsFromExtensions is an alias for
// NewCertificateSignatureAlgorithms.
func NewCertificateSignatureAlgorithmsFromExtensions(signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) *CertificateSignatureAlgorithms {
	return NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// NewCertificateSignatureSchemePolicy is an alias for
// NewCertificateSignatureAlgorithms.
func NewCertificateSignatureSchemePolicy(signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) *CertificateSignatureAlgorithms {
	return NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// HasSignatureAlgorithmsCert reports whether signature_algorithms_cert was
// present.
func (p *CertificateSignatureAlgorithms) HasSignatureAlgorithmsCert() bool {
	return p != nil && p.CertExtensionPresent
}

// HasCertificateExtension is an alias for HasSignatureAlgorithmsCert.
func (p *CertificateSignatureAlgorithms) HasCertificateExtension() bool {
	return p.HasSignatureAlgorithmsCert()
}

// ApplicableCertificateSchemes returns the list that governs signatures in
// certificates.
//
// If signature_algorithms_cert was present, its list is authoritative. If it
// was absent, signature_algorithms is the fallback. The result is a copy and
// preserves the peer's preference order.
func (p *CertificateSignatureAlgorithms) ApplicableCertificateSchemes() []SignatureScheme {
	if p == nil {
		return nil
	}
	if p.CertExtensionPresent {
		return append([]SignatureScheme(nil), p.SignatureAlgorithmsCert...)
	}
	return append([]SignatureScheme(nil), p.SignatureAlgorithms...)
}

// Applicable is an alias for ApplicableCertificateSchemes.
func (p *CertificateSignatureAlgorithms) Applicable() []SignatureScheme {
	return p.ApplicableCertificateSchemes()
}

// CertificateSchemes is an alias for ApplicableCertificateSchemes.
func (p *CertificateSignatureAlgorithms) CertificateSchemes() []SignatureScheme {
	return p.ApplicableCertificateSchemes()
}

// EffectiveCertificateSchemes is an alias for ApplicableCertificateSchemes.
func (p *CertificateSignatureAlgorithms) EffectiveCertificateSchemes() []SignatureScheme {
	return p.ApplicableCertificateSchemes()
}

// AcceptsCertificateScheme reports whether scheme occurs in the applicable
// certificate-signature list. The handshake list is consulted only when the
// certificate extension was not present.
func (p *CertificateSignatureAlgorithms) AcceptsCertificateScheme(scheme SignatureScheme) bool {
	if p == nil || scheme == 0 {
		return false
	}
	return ContainsSignatureScheme(p.ApplicableCertificateSchemes(), scheme)
}

// Accepts is an alias for AcceptsCertificateScheme.
func (p *CertificateSignatureAlgorithms) Accepts(scheme SignatureScheme) bool {
	return p.AcceptsCertificateScheme(scheme)
}

// AllowsCertificateScheme is an alias for AcceptsCertificateScheme.
func (p *CertificateSignatureAlgorithms) AllowsCertificateScheme(scheme SignatureScheme) bool {
	return p.AcceptsCertificateScheme(scheme)
}

// Allows is an alias for AcceptsCertificateScheme.
func (p *CertificateSignatureAlgorithms) Allows(scheme SignatureScheme) bool {
	return p.AcceptsCertificateScheme(scheme)
}

// SelectCertificateScheme chooses a scheme in the peer's preference order.
// A scheme is returned only if it occurs in both the applicable certificate
// list and local. Thus a scheme present in neither peer list, or absent from
// the caller's local support list, is never selected.
func (p *CertificateSignatureAlgorithms) SelectCertificateScheme(local []SignatureScheme) (SignatureScheme, bool) {
	if p == nil {
		return 0, false
	}
	applicable := p.ApplicableCertificateSchemes()
	for _, scheme := range applicable {
		if scheme == 0 {
			continue
		}
		if ContainsSignatureScheme(local, scheme) {
			return scheme, true
		}
	}
	return 0, false
}

// Select is an alias for SelectCertificateScheme.
func (p *CertificateSignatureAlgorithms) Select(local []SignatureScheme) (SignatureScheme, bool) {
	return p.SelectCertificateScheme(local)
}

// FilterCertificateSchemes returns the entries of local that the peer permits
// for certificate signatures, preserving the local order. This is the
// operation a TLS stack uses when deciding which certificate chains may be
// offered.
func (p *CertificateSignatureAlgorithms) FilterCertificateSchemes(local []SignatureScheme) []SignatureScheme {
	if p == nil || len(local) == 0 {
		return nil
	}
	applicable := p.ApplicableCertificateSchemes()
	out := make([]SignatureScheme, 0, len(local))
	for _, scheme := range local {
		if scheme != 0 && ContainsSignatureScheme(applicable, scheme) {
			out = append(out, scheme)
		}
	}
	return out
}

// Filter is an alias for FilterCertificateSchemes.
func (p *CertificateSignatureAlgorithms) Filter(local []SignatureScheme) []SignatureScheme {
	return p.FilterCertificateSchemes(local)
}

// ApplicableCertificateSchemes returns the certificate-signature list implied
// by the two peer extensions. See
// CertificateSignatureAlgorithms.ApplicableCertificateSchemes.
func ApplicableCertificateSchemes(signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) []SignatureScheme {
	return NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...).ApplicableCertificateSchemes()
}

// EffectiveCertificateSignatureSchemes is an alias for
// ApplicableCertificateSchemes.
func EffectiveCertificateSignatureSchemes(signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) []SignatureScheme {
	return ApplicableCertificateSchemes(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// CertificateSignatureSchemes is an alias for
// ApplicableCertificateSchemes.
func CertificateSignatureSchemes(signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) []SignatureScheme {
	return ApplicableCertificateSchemes(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// ResolveCertificateSignatureSchemes is an alias for
// ApplicableCertificateSchemes.
func ResolveCertificateSignatureSchemes(signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) []SignatureScheme {
	return ApplicableCertificateSchemes(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// AcceptsCertificateSignatureScheme reports whether scheme is in the
// applicable certificate-signature list.
func AcceptsCertificateSignatureScheme(scheme SignatureScheme, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) bool {
	return NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...).AcceptsCertificateScheme(scheme)
}

// CertificateSignatureSchemeAllowed is an alias for
// AcceptsCertificateSignatureScheme.
func CertificateSignatureSchemeAllowed(scheme SignatureScheme, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) bool {
	return AcceptsCertificateSignatureScheme(scheme, signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// CertificateSchemeAllowed is an alias for
// AcceptsCertificateSignatureScheme.
func CertificateSchemeAllowed(scheme SignatureScheme, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) bool {
	return AcceptsCertificateSignatureScheme(scheme, signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// IsCertificateSignatureSchemeAllowed is an alias for
// AcceptsCertificateSignatureScheme.
func IsCertificateSignatureSchemeAllowed(scheme SignatureScheme, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) bool {
	return AcceptsCertificateSignatureScheme(scheme, signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// AllowedCertificateScheme is an alias for
// AcceptsCertificateSignatureScheme.
func AllowedCertificateScheme(scheme SignatureScheme, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) bool {
	return AcceptsCertificateSignatureScheme(scheme, signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// SelectCertificateSignatureScheme chooses a certificate signature scheme
// from local using the peer's preference order. The returned scheme is always
// present in both the applicable peer list and local.
func SelectCertificateSignatureScheme(local, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) (SignatureScheme, bool) {
	return NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...).SelectCertificateScheme(local)
}

// SelectCertificateScheme is an alias for
// SelectCertificateSignatureScheme.
func SelectCertificateScheme(local, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) (SignatureScheme, bool) {
	return SelectCertificateSignatureScheme(local, signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// FilterCertificateSignatureSchemes returns the local schemes that the peer
// permits for certificate signatures, preserving the local order.
func FilterCertificateSignatureSchemes(local, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) []SignatureScheme {
	return NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...).FilterCertificateSchemes(local)
}

// FilterCertificateSchemes is an alias for
// FilterCertificateSignatureSchemes.
func FilterCertificateSchemes(local, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) []SignatureScheme {
	return FilterCertificateSignatureSchemes(local, signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// AllowedCertificateSignatureSchemes is an alias for
// FilterCertificateSignatureSchemes.
func AllowedCertificateSignatureSchemes(local, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) []SignatureScheme {
	return FilterCertificateSignatureSchemes(local, signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
}

// ErrNoSuitableCertificateSignatureScheme is returned when no certificate
// signature scheme is allowed by both peers.
var ErrNoSuitableCertificateSignatureScheme = errors.New("netsec: no mutually supported certificate signature scheme")

// NegotiateCertificateSignatureScheme is the error-returning form of
// SelectCertificateSignatureScheme.
func NegotiateCertificateSignatureScheme(local, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) (SignatureScheme, error) {
	scheme, ok := SelectCertificateSignatureScheme(local, signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
	if !ok {
		return 0, ErrNoSuitableCertificateSignatureScheme
	}
	return scheme, nil
}
