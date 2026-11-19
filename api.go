package netsec

import (
	"crypto/x509"
	"time"
)

// Policy is another name for SecurityLevel.
type Policy = SecurityLevel

// Result and Package are aliases for Gem.
type Result = Gem
type Package = Gem

const (
	HighSecurityPolicy   = HighSecurity
	MediumSecurityPolicy = MediumSecurity
	SecurityLevelHigh    = HighSecurity
	SecurityLevelMedium  = MediumSecurity
	// LowSecurity is a compatibility alias for MediumSecurity: signatures are
	// checked when present, but wholly unsigned archives are accepted.
	LowSecurity = MediumSecurity
)

// Option configures Options in functional-option style.
type Option func(*Options)

// WithRoots sets the certificate pool that anchors signer chains.
func WithRoots(roots *x509.CertPool) Option {
	return func(options *Options) {
		options.Roots = roots
	}
}

// WithTrustRoots adds certificates to the trust-anchor set.
func WithTrustRoots(roots ...*x509.Certificate) Option {
	return func(options *Options) {
		options.RootCertificates = append(options.RootCertificates, roots...)
	}
}

// WithSecurity sets the validation policy.
func WithSecurity(level SecurityLevel) Option {
	return func(options *Options) {
		options.Security = level
		options.Level = level
	}
}

// WithSecurityLevel is an alias for WithSecurity.
func WithSecurityLevel(level SecurityLevel) Option {
	return WithSecurity(level)
}

// WithSigner supplies a signer certificate and optional intermediate chain.
// This is needed when .sig members contain bare RSA signatures without embedded
// certificates.
func WithSigner(cert *x509.Certificate, chain ...*x509.Certificate) Option {
	return func(options *Options) {
		options.Signer = cert
		options.SignerChain = append(options.SignerChain, chain...)
	}
}

// WithClock overrides the current time used for certificate validity checks.
func WithClock(clock func() time.Time) Option {
	return func(options *Options) {
		options.Clock = clock
	}
}

// Verifier holds reusable policy and trust configuration.
type Verifier struct {
	Roots    *x509.CertPool
	Security SecurityLevel
	Options  Options
}

// NewVerifier creates a HighSecurity verifier with the supplied trust roots.
func NewVerifier(roots *x509.CertPool) *Verifier {
	return &Verifier{
		Roots:    roots,
		Security: HighSecurity,
		Options:  Options{Security: HighSecurity, Roots: roots},
	}
}

// NewVerifierWithOptions creates a verifier from an Options value.
func NewVerifierWithOptions(options Options) *Verifier {
	return &Verifier{Roots: options.Roots, Security: options.Security, Options: options}
}

func (v *Verifier) options(args []any) []any {
	options := Options{}
	if v != nil {
		options = v.Options
		options.Security = v.Security
		if options.Roots == nil {
			options.Roots = v.Roots
		}
	}
	all := make([]any, 0, len(args)+1)
	all = append(all, options)
	all = append(all, args...)
	return all
}

// ValidateGem validates with the verifier's default policy and trust roots.
func (v *Verifier) ValidateGem(source any, args ...any) error {
	return ValidateGem(source, v.options(args)...)
}

// VerifyGem is an alias for ValidateGem.
func (v *Verifier) VerifyGem(source any, args ...any) error {
	return v.ValidateGem(source, args...)
}

// Validate is an alias for ValidateGem.
func (v *Verifier) Validate(source any, args ...any) error {
	return v.ValidateGem(source, args...)
}

// Verify is an alias for ValidateGem.
func (v *Verifier) Verify(source any, args ...any) error {
	return v.ValidateGem(source, args...)
}

// InspectGem validates and returns the parsed gem.
func (v *Verifier) InspectGem(source any, args ...any) (*Gem, error) {
	return InspectGem(source, v.options(args)...)
}
