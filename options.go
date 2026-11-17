package netsec

import (
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// VerifyConfig controls signature and certificate validation.
type VerifyConfig struct {
	// Roots is the trust store used for certificate chain validation. When it
	// is nil and no TrustedCertificates are supplied, only the cryptographic
	// signatures are checked.
	Roots *x509.CertPool

	// TrustedCertificates are accepted as trust anchors. A signer certificate
	// that exactly matches one of these is trusted directly; otherwise these
	// certificates are also added to the root pool.
	TrustedCertificates []*x509.Certificate

	// Intermediates are extra intermediate certificates for chain building.
	Intermediates []*x509.Certificate

	// Content is caller-supplied detached content. ContentProvided must be set
	// for empty content to be distinguished from absent content.
	Content         []byte
	ContentProvided bool

	// CurrentTime controls certificate validity checks. Zero means time.Now().
	CurrentTime time.Time

	// RequireSignedAttributes rejects signers that use the no-signedAttrs form
	// of SignerInfo. RFC 5652 permits that form, so it is off by default.
	RequireSignedAttributes bool

	// AllowAnySigner accepts the SignedData if at least one signer verifies.
	// By default every signer must verify.
	AllowAnySigner bool

	// SkipCertificateVerification disables certificate chain and validity
	// checks. It should only be used when the caller has already established
	// trust in the signer certificate.
	SkipCertificateVerification bool
}

// VerifyOption mutates a VerifyConfig.
type VerifyOption func(*VerifyConfig)

// WithRoots sets the certificate trust store.
func WithRoots(roots *x509.CertPool) VerifyOption {
	return func(cfg *VerifyConfig) { cfg.Roots = roots }
}

// WithTrustedCertificates adds certificates that are trusted directly.
func WithTrustedCertificates(certs ...*x509.Certificate) VerifyOption {
	return func(cfg *VerifyConfig) {
		cfg.TrustedCertificates = append(cfg.TrustedCertificates, certs...)
	}
}

// WithIntermediates adds intermediate certificates for chain building.
func WithIntermediates(certs ...*x509.Certificate) VerifyOption {
	return func(cfg *VerifyConfig) {
		cfg.Intermediates = append(cfg.Intermediates, certs...)
	}
}

// WithContent supplies detached content, or verifies that content supplied by
// the caller matches encapsulated eContent.
func WithContent(content []byte) VerifyOption {
	return func(cfg *VerifyConfig) {
		cfg.Content = append([]byte{}, content...)
		cfg.ContentProvided = true
	}
}

// WithCurrentTime sets the time used for certificate validity checks.
func WithCurrentTime(now time.Time) VerifyOption {
	return func(cfg *VerifyConfig) { cfg.CurrentTime = now }
}

// RequireSignedAttributes requires every signer to use signedAttrs.
func RequireSignedAttributes() VerifyOption {
	return func(cfg *VerifyConfig) { cfg.RequireSignedAttributes = true }
}

// WithAnySigner accepts SignedData when at least one signer verifies.
func WithAnySigner() VerifyOption {
	return func(cfg *VerifyConfig) { cfg.AllowAnySigner = true }
}

// SkipCertificateVerification disables certificate chain validation.
func SkipCertificateVerification() VerifyOption {
	return func(cfg *VerifyConfig) { cfg.SkipCertificateVerification = true }
}

func parseVerifyArgs(args []any) (*VerifyConfig, error) {
	cfg := &VerifyConfig{}
	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			// Convenient for callers that pass an optional roots pointer.

		case []byte:
			cfg.Content = append([]byte{}, value...)
			cfg.ContentProvided = true

		case string:
			cfg.Content = []byte(value)
			cfg.ContentProvided = true

		case *x509.CertPool:
			cfg.Roots = value

		case []*x509.Certificate:
			cfg.TrustedCertificates = append(cfg.TrustedCertificates, value...)

		case []x509.Certificate:
			for i := range value {
				cfg.TrustedCertificates = append(cfg.TrustedCertificates, &value[i])
			}

		case *x509.Certificate:
			if value != nil {
				cfg.TrustedCertificates = append(cfg.TrustedCertificates, value)
			}

		case x509.Certificate:
			cert := value
			cfg.TrustedCertificates = append(cfg.TrustedCertificates, &cert)

		case VerifyOption:
			value(cfg)

		case VerifyConfig:
			mergeVerifyConfig(cfg, &value)

		case *VerifyConfig:
			if value != nil {
				mergeVerifyConfig(cfg, value)
			}

		case time.Time:
			cfg.CurrentTime = value

		default:
			return nil, fmt.Errorf("netsec: unsupported verify argument type %T", arg)
		}
	}
	return cfg, nil
}

func mergeVerifyConfig(dst, src *VerifyConfig) {
	if src.Roots != nil {
		dst.Roots = src.Roots
	}
	dst.TrustedCertificates = append(dst.TrustedCertificates, src.TrustedCertificates...)
	dst.Intermediates = append(dst.Intermediates, src.Intermediates...)
	if src.ContentProvided {
		dst.Content = append([]byte{}, src.Content...)
		dst.ContentProvided = true
	}
	if !src.CurrentTime.IsZero() {
		dst.CurrentTime = src.CurrentTime
	}
	if src.RequireSignedAttributes {
		dst.RequireSignedAttributes = true
	}
	if src.AllowAnySigner {
		dst.AllowAnySigner = true
	}
	if src.SkipCertificateVerification {
		dst.SkipCertificateVerification = true
	}
}

func resolveContent(sd *SignedData, cfg *VerifyConfig) ([]byte, error) {
	if sd.HasContent {
		if cfg.ContentProvided && !bytesEqual(cfg.Content, sd.Content) {
			return nil, ErrContentMismatch
		}
		return sd.Content, nil
	}
	if !cfg.ContentProvided {
		return nil, ErrDetachedContentRequired
	}
	return cfg.Content, nil
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

var errNoSigners = errors.New("netsec: SignedData contains no signers")
