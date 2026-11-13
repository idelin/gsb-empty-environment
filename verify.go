package netsec

import (
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// ChainVerifier performs certificate_authorities filtering followed by normal
// X.509 path validation.
type ChainVerifier struct {
	// Roots is the trust anchor set. If nil, the system roots are used.
	Roots *x509.CertPool

	// Intermediates contains additional intermediate certificates. Chain
	// certificates are added automatically.
	Intermediates *x509.CertPool

	// DNSName, when non-empty, is checked against the leaf certificate.
	DNSName string

	// KeyUsages is passed to x509.Certificate.Verify. If empty, Go's default
	// server-auth usage applies.
	KeyUsages []x509.ExtKeyUsage

	// CurrentTime, when non-zero, overrides the verification time.
	CurrentTime time.Time
}

// Verify enforces authorities on chain[0], then verifies the chain using the
// configured trust settings. authority filtering happens before any path
// validation.
func (v ChainVerifier) Verify(chain []*x509.Certificate, authorities Authorities) ([][]*x509.Certificate, error) {
	if len(chain) == 0 || chain[0] == nil {
		return nil, ErrMissingLeafCertificate
	}
	if err := authorities.ValidateLeaf(chain[0]); err != nil {
		return nil, err
	}

	options := x509.VerifyOptions{
		Roots:         v.Roots,
		Intermediates: v.Intermediates,
		DNSName:       v.DNSName,
		KeyUsages:     v.KeyUsages,
		CurrentTime:   v.CurrentTime,
	}
	return verifyWithOptions(chain, options)
}

// VerifyExtension enforces e on chain[0], then verifies the chain.
func (v ChainVerifier) VerifyExtension(chain []*x509.Certificate, extension Extension) ([][]*x509.Certificate, error) {
	if len(chain) == 0 || chain[0] == nil {
		return nil, ErrMissingLeafCertificate
	}
	if err := extension.Validate(chain[0]); err != nil {
		return nil, err
	}
	options := x509.VerifyOptions{
		Roots:         v.Roots,
		Intermediates: v.Intermediates,
		DNSName:       v.DNSName,
		KeyUsages:     v.KeyUsages,
		CurrentTime:   v.CurrentTime,
	}
	return verifyWithOptions(chain, options)
}

// VerifyExtensionChain is like VerifyChain but distinguishes an absent
// extension from an empty one.
func VerifyExtensionChain(chain []*x509.Certificate, extension Extension, options ...any) ([][]*x509.Certificate, error) {
	if len(chain) == 0 || chain[0] == nil {
		return nil, ErrMissingLeafCertificate
	}
	if err := extension.Validate(chain[0]); err != nil {
		return nil, err
	}
	verifyOptions, err := makeVerifyOptions(options...)
	if err != nil {
		return nil, err
	}
	return verifyWithOptions(chain, verifyOptions)
}

// VerifyChain performs certificate_authorities filtering followed by normal
// path validation using this extension.
func (e Extension) VerifyChain(chain []*x509.Certificate, options ...any) ([][]*x509.Certificate, error) {
	return VerifyExtensionChain(chain, e, options...)
}

func makeVerifyOptions(options ...any) (x509.VerifyOptions, error) {
	var result x509.VerifyOptions
	for _, option := range options {
		switch value := option.(type) {
		case nil:
			continue
		case x509.VerifyOptions:
			result = value
		case *x509.VerifyOptions:
			if value != nil {
				result = *value
			}
		case *x509.CertPool:
			result.Roots = value
		case *x509.Certificate:
			if value == nil {
				continue
			}
			if result.Intermediates == nil {
				result.Intermediates = x509.NewCertPool()
			}
			result.Intermediates.AddCert(value)
		case []*x509.Certificate:
			if result.Intermediates == nil {
				result.Intermediates = x509.NewCertPool()
			}
			for _, certificate := range value {
				if certificate != nil {
					result.Intermediates.AddCert(certificate)
				}
			}
		case time.Time:
			result.CurrentTime = value
		case string:
			result.DNSName = value
		case x509.ExtKeyUsage:
			result.KeyUsages = append(result.KeyUsages, value)
		case []x509.ExtKeyUsage:
			result.KeyUsages = append(result.KeyUsages, value...)
		default:
			return x509.VerifyOptions{}, fmt.Errorf("netsec: unsupported verify option %T", option)
		}
	}
	return result, nil
}

func verifyWithOptions(chain []*x509.Certificate, options x509.VerifyOptions) ([][]*x509.Certificate, error) {
	if options.Intermediates == nil {
		options.Intermediates = x509.NewCertPool()
	}
	for _, certificate := range chain[1:] {
		if certificate != nil {
			options.Intermediates.AddCert(certificate)
		}
	}

	chains, err := chain[0].Verify(options)
	if err != nil {
		return nil, fmt.Errorf("netsec: certificate path validation failed: %w", err)
	}
	if len(chains) == 0 {
		return nil, errors.New("netsec: certificate path validation returned no chains")
	}
	return chains, nil
}
