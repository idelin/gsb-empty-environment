package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// ValidateCertificateAuthorities checks leaf's issuer against the advertised
// authorities. It accepts the leaf and authorities in either order and can
// also accept a []*x509.Certificate chain, [][]byte authority names, []byte
// single authority, Extension, or *Extension.
//
// If verification options (roots, intermediates, DNS name, usage, or time) are
// supplied, normal X.509 path validation follows the issuer check.
func ValidateCertificateAuthorities(args ...any) error {
	leaf, chain, authorities, options, err := parseValidationArgs(args...)
	if err != nil {
		return err
	}
	if err := authorities.ValidateLeaf(leaf); err != nil {
		return err
	}
	if len(options) != 0 {
		if len(chain) == 0 {
			chain = []*x509.Certificate{leaf}
		}
		callArgs := append([]any{authorities}, options...)
		_, err := VerifyChain(chain, callArgs...)
		return err
	}
	return nil
}

// VerifyCertificateAuthorities is an alias for
// ValidateCertificateAuthorities.
func VerifyCertificateAuthorities(args ...any) error {
	return ValidateCertificateAuthorities(args...)
}

// CheckCertificateAuthorities is an alias for
// ValidateCertificateAuthorities.
func CheckCertificateAuthorities(args ...any) error {
	return ValidateCertificateAuthorities(args...)
}

// ValidateIssuer is an alias for ValidateCertificateAuthorities.
func ValidateIssuer(args ...any) error {
	return ValidateCertificateAuthorities(args...)
}

// ValidateCertificate is an alias for ValidateCertificateAuthorities.
func ValidateCertificate(args ...any) error {
	return ValidateCertificateAuthorities(args...)
}

// VerifyCertificate is an alias for ValidateCertificateAuthorities.
func VerifyCertificate(args ...any) error {
	return ValidateCertificateAuthorities(args...)
}

// Verify performs issuer filtering and path validation when given a
// certificate chain, or issuer filtering when given a single leaf and an
// authority list.
func Verify(args ...any) error {
	if len(args) != 0 {
		switch value := args[0].(type) {
		case []*x509.Certificate, tls.Certificate, *tls.Certificate, []tls.Certificate:
			return ValidateChain(args[0], args[1:]...)
		case []byte:
			if _, err := chainFrom(value); err == nil {
				return ValidateChain(args[0], args[1:]...)
			}
		case [][]byte:
			if _, err := chainFrom(value); err == nil {
				return ValidateChain(args[0], args[1:]...)
			}
		}
	}
	return ValidateCertificateAuthorities(args...)
}

// Check is an alias for Verify.
func Check(args ...any) error {
	return Verify(args...)
}

// FilterCertificateAuthorities returns the certificates whose leaf issuer is
// permitted by the supplied authority list. Arguments may be given in either
// order and may be []tls.Certificate, tls.Certificate, []*x509.Certificate,
// *x509.Certificate, Authorities, [][]byte, []byte, Extension, or *Extension.
func FilterCertificateAuthorities(args ...any) ([]tls.Certificate, error) {
	var certificates []tls.Certificate
	var authorities Authorities
	authoritiesSet := false

	for _, arg := range args {
		switch value := arg.(type) {
		case []tls.Certificate:
			certificates = append(certificates, value...)
		case tls.Certificate:
			certificates = append(certificates, value)
		case *tls.Certificate:
			if value != nil {
				certificates = append(certificates, *value)
			}
		case []*x509.Certificate:
			for _, certificate := range value {
				if certificate != nil {
					certificates = append(certificates, tls.Certificate{
						Certificate: [][]byte{certificate.Raw},
						Leaf:        certificate,
					})
				}
			}
		case *x509.Certificate:
			if value != nil {
				certificates = append(certificates, tls.Certificate{
					Certificate: [][]byte{value.Raw},
					Leaf:        value,
				})
			}
		case Authorities:
			authorities = value
			authoritiesSet = true
		case [][]byte:
			authorities = Authorities(value)
			authoritiesSet = true
		case []byte:
			if certificate, err := x509.ParseCertificate(value); err == nil {
				certificates = append(certificates, tls.Certificate{
					Certificate: [][]byte{value},
					Leaf:        certificate,
				})
			} else {
				authorities = coerceAuthorityBytes(value)
				authoritiesSet = true
			}
		case Extension:
			if value.Present {
				authorities = presentAuthorities(value.Authorities)
			} else {
				authorities = nil
			}
			authoritiesSet = true
		case *Extension:
			if value != nil && value.Present {
				authorities = presentAuthorities(value.Authorities)
			} else {
				authorities = nil
			}
			authoritiesSet = true
		default:
			return nil, fmt.Errorf("netsec: unsupported certificate filter argument %T", arg)
		}
	}
	if !authoritiesSet {
		authorities = nil
	}
	return FilterCertificates(certificates, authorities)
}

// IssuerAuthorized reports whether issuer is present in authorities. It
// accepts issuer and authorities in either order. A nil/absent authorities
// means no filtering was requested and reports true.
func IssuerAuthorized(args ...any) bool {
	var issuer []byte
	var authorities Authorities
	authoritiesSet := false
	for _, arg := range args {
		switch value := arg.(type) {
		case []byte:
			if issuer == nil {
				issuer = value
			}
		case Authorities:
			authorities = value
			authoritiesSet = true
		case [][]byte:
			authorities = Authorities(value)
			authoritiesSet = true
		case *Extension:
			if value != nil && value.Present {
				authorities = presentAuthorities(value.Authorities)
				authoritiesSet = true
			}
		case Extension:
			if value.Present {
				authorities = presentAuthorities(value.Authorities)
				authoritiesSet = true
			}
		}
	}
	if !authoritiesSet {
		return true
	}
	return authorities.Contains(issuer)
}

// VerifyChain enforces authorities on the leaf (the first element when a chain
// is supplied) and then verifies the chain.
//
// The first argument may be *x509.Certificate, []*x509.Certificate,
// tls.Certificate, or []tls.Certificate. Additional arguments may contain
// Authorities, [][]byte, []byte, Extension, *Extension, x509.VerifyOptions,
// *x509.VerifyOptions, *x509.CertPool, []*x509.Certificate intermediates, or
// time.Time, in any order.
func VerifyChain(leafOrChain any, args ...any) ([][]*x509.Certificate, error) {
	chain, err := chainFrom(leafOrChain)
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 || chain[0] == nil {
		return nil, ErrMissingLeafCertificate
	}

	authorities, options, err := parseAuthoritiesAndOptions(args...)
	if err != nil {
		return nil, err
	}
	if err := authorities.ValidateLeaf(chain[0]); err != nil {
		return nil, err
	}
	verifyOptions, err := makeVerifyOptions(options...)
	if err != nil {
		return nil, err
	}
	return verifyWithOptions(chain, verifyOptions)
}

// ValidateChain enforces the extension and path validation but discards the
// verified paths.
func ValidateChain(leafOrChain any, args ...any) error {
	_, err := VerifyChain(leafOrChain, args...)
	return err
}

// VerifyCertificateChain enforces issuer filtering and X.509 path validation.
func VerifyCertificateChain(leafOrChain any, args ...any) error {
	return ValidateChain(leafOrChain, args...)
}

// VerifyCertificateChainPaths is like VerifyCertificateChain but returns the
// verified paths.
func VerifyCertificateChainPaths(leafOrChain any, args ...any) ([][]*x509.Certificate, error) {
	return VerifyChain(leafOrChain, args...)
}

// ValidateCertificateChain is an alias for ValidateChain.
func ValidateCertificateChain(leafOrChain any, args ...any) error {
	return ValidateChain(leafOrChain, args...)
}

func parseValidationArgs(args ...any) (*x509.Certificate, []*x509.Certificate, Authorities, []any, error) {
	var leaf *x509.Certificate
	var chain []*x509.Certificate
	var authorities Authorities
	authoritiesSet := false
	var options []any

	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			if !authoritiesSet {
				authorities = nil
				authoritiesSet = true
			}
		case *x509.Certificate:
			if leaf == nil {
				leaf = value
				if len(chain) == 0 {
					chain = []*x509.Certificate{value}
				}
			} else if !authoritiesSet && value != nil {
				authorities = Authorities{value.RawSubject}
				authoritiesSet = true
			} else {
				options = append(options, value)
			}
		case []*x509.Certificate:
			if leaf == nil && len(value) > 0 {
				chain = value
				leaf = value[0]
			} else {
				options = append(options, value)
			}
		case *tls.Certificate:
			parsed, err := chainFrom(value)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			if len(parsed) > 0 {
				chain = parsed
				leaf = parsed[0]
			}
		case tls.Certificate:
			parsed, err := chainFrom(value)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			if len(parsed) > 0 {
				chain = parsed
				leaf = parsed[0]
			}
		case Authorities:
			authorities = value
			authoritiesSet = true
		case [][]byte:
			authorities = Authorities(value)
			authoritiesSet = true
		case []byte:
			if leaf == nil {
				if certificate, err := x509.ParseCertificate(value); err == nil {
					chain = []*x509.Certificate{certificate}
					leaf = certificate
					continue
				}
			}
			if !authoritiesSet {
				authorities = coerceAuthorityBytes(value)
				authoritiesSet = true
			} else {
				options = append(options, value)
			}
		case Extension:
			if value.Present {
				authorities = presentAuthorities(value.Authorities)
			} else {
				authorities = nil
			}
			authoritiesSet = true
		case *Extension:
			if value != nil && value.Present {
				authorities = presentAuthorities(value.Authorities)
			} else {
				authorities = nil
			}
			authoritiesSet = true
		default:
			options = append(options, arg)
		}
	}

	if leaf == nil {
		return nil, nil, nil, nil, ErrMissingLeafCertificate
	}
	if !authoritiesSet {
		authorities = nil
	}
	return leaf, chain, authorities, options, nil
}

func parseAuthoritiesAndOptions(args ...any) (Authorities, []any, error) {
	var authorities Authorities
	authoritiesSet := false
	var options []any

	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			if !authoritiesSet {
				authorities = nil
				authoritiesSet = true
			}
		case Authorities:
			authorities = value
			authoritiesSet = true
		case [][]byte:
			authorities = Authorities(value)
			authoritiesSet = true
		case []byte:
			if certificate, err := x509.ParseCertificate(value); err == nil {
				options = append(options, certificate)
			} else if !authoritiesSet {
				authorities = coerceAuthorityBytes(value)
				authoritiesSet = true
			} else {
				options = append(options, value)
			}
		case Extension:
			if value.Present {
				authorities = presentAuthorities(value.Authorities)
			} else {
				authorities = nil
			}
			authoritiesSet = true
		case *Extension:
			if value != nil && value.Present {
				authorities = presentAuthorities(value.Authorities)
			} else {
				authorities = nil
			}
			authoritiesSet = true
		case []*x509.Certificate:
			options = append(options, value)
		case *x509.Certificate:
			options = append(options, value)
		default:
			options = append(options, arg)
		}
	}
	if !authoritiesSet {
		authorities = nil
	}
	return authorities, options, nil
}

func chainFrom(value any) ([]*x509.Certificate, error) {
	switch chain := value.(type) {
	case nil:
		return nil, ErrMissingLeafCertificate
	case *x509.Certificate:
		if chain == nil {
			return nil, ErrMissingLeafCertificate
		}
		return []*x509.Certificate{chain}, nil
	case []*x509.Certificate:
		if len(chain) == 0 {
			return nil, ErrMissingLeafCertificate
		}
		return chain, nil
	case []byte:
		certificate, err := x509.ParseCertificate(chain)
		if err != nil {
			return nil, fmt.Errorf("netsec: parse leaf certificate: %w", err)
		}
		return []*x509.Certificate{certificate}, nil
	case [][]byte:
		if len(chain) == 0 {
			return nil, ErrMissingLeafCertificate
		}
		parsed := make([]*x509.Certificate, 0, len(chain))
		for i, raw := range chain {
			certificate, err := x509.ParseCertificate(raw)
			if err != nil {
				return nil, fmt.Errorf("netsec: parse certificate %d: %w", i, err)
			}
			parsed = append(parsed, certificate)
		}
		return parsed, nil
	case *tls.Certificate:
		return chainFromTLSCertificate(chain)
	case tls.Certificate:
		return chainFromTLSCertificate(&chain)
	case []tls.Certificate:
		if len(chain) == 0 {
			return nil, ErrMissingLeafCertificate
		}
		return chainFromTLSCertificate(&chain[0])
	default:
		return nil, fmt.Errorf("netsec: unsupported certificate chain type %T", value)
	}
}

func chainFromTLSCertificate(certificate *tls.Certificate) ([]*x509.Certificate, error) {
	if certificate == nil {
		return nil, ErrMissingLeafCertificate
	}
	if len(certificate.Certificate) == 0 {
		if certificate.Leaf == nil {
			return nil, ErrMissingLeafCertificate
		}
		return []*x509.Certificate{certificate.Leaf}, nil
	}
	chain := make([]*x509.Certificate, 0, len(certificate.Certificate))
	for i, raw := range certificate.Certificate {
		if i == 0 && certificate.Leaf != nil {
			chain = append(chain, certificate.Leaf)
			continue
		}
		parsed, err := x509.ParseCertificate(raw)
		if err != nil {
			return nil, fmt.Errorf("netsec: parse certificate %d: %w", i, err)
		}
		chain = append(chain, parsed)
	}
	return chain, nil
}

func coerceAuthorityBytes(value []byte) Authorities {
	if authorities, err := ParseCertificateAuthorities(value); err == nil {
		return authorities
	}
	if authorities, err := ParseCertificateAuthoritiesExtension(value); err == nil {
		return authorities
	}
	return Authorities{value}
}

func presentAuthorities(authorities Authorities) Authorities {
	if authorities == nil {
		return Authorities{}
	}
	return authorities
}
