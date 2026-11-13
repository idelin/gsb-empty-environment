package netsec

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PathOptions describes trust anchors and certificate policy inputs for path
// validation. PathOptions can be passed to ValidateCertificatePath or
// VerifyCertificatePath.
type PathOptions struct {
	Roots                    *x509.CertPool
	RootCertificates         []*x509.Certificate
	Intermediates            *x509.CertPool
	IntermediateCertificates []*x509.Certificate
	DNSName                  string
	CurrentTime              time.Time
	KeyUsages                []x509.ExtKeyUsage
}

type pathConfig struct {
	verify        x509.VerifyOptions
	rootCerts     []*x509.Certificate
	interCerts    []*x509.Certificate
	explicitRoots bool
}

func (c *pathConfig) merge(options PathOptions) {
	if options.Roots != nil {
		c.verify.Roots = options.Roots
		c.explicitRoots = true
	}
	if len(options.RootCertificates) > 0 {
		c.rootCerts = append(c.rootCerts, options.RootCertificates...)
		c.explicitRoots = true
	}
	if options.Intermediates != nil {
		c.verify.Intermediates = options.Intermediates
	}
	if len(options.IntermediateCertificates) > 0 {
		c.interCerts = append(c.interCerts, options.IntermediateCertificates...)
	}
	if options.DNSName != "" {
		c.verify.DNSName = options.DNSName
	}
	if !options.CurrentTime.IsZero() {
		c.verify.CurrentTime = options.CurrentTime
	}
	if len(options.KeyUsages) > 0 {
		c.verify.KeyUsages = append([]x509.ExtKeyUsage(nil), options.KeyUsages...)
	}
}

func (c *pathConfig) apply(value any) error {
	switch v := value.(type) {
	case nil:
		return nil
	case PathOptions:
		c.merge(v)
	case *PathOptions:
		if v == nil {
			return errors.New("netsec: nil path options")
		}
		c.merge(*v)
	case x509.VerifyOptions:
		c.verify = v
		if v.Roots != nil {
			c.explicitRoots = true
		}
	case *x509.VerifyOptions:
		if v == nil {
			return errors.New("netsec: nil verify options")
		}
		c.verify = *v
		if v.Roots != nil {
			c.explicitRoots = true
		}
	case *x509.CertPool:
		if v == nil {
			return errors.New("netsec: nil certificate pool")
		}
		switch {
		case !c.explicitRoots:
			c.verify.Roots = v
			c.explicitRoots = true
		case c.verify.Intermediates == nil:
			c.verify.Intermediates = v
		default:
			c.verify.Roots = v
			c.explicitRoots = true
		}
	case x509.CertPool:
		return c.apply(&v)
	case *x509.Certificate:
		if v == nil {
			return ErrNilCertificate
		}
		if !c.explicitRoots && c.verify.Roots == nil && len(c.rootCerts) == 0 {
			c.rootCerts = append(c.rootCerts, v)
			c.explicitRoots = true
		} else {
			c.interCerts = append(c.interCerts, v)
		}
	case x509.Certificate:
		return c.apply(&v)
	case []*x509.Certificate:
		if c.verify.Roots == nil && len(c.rootCerts) == 0 && !c.explicitRoots {
			c.rootCerts = append(c.rootCerts, v...)
			c.explicitRoots = true
		} else {
			c.interCerts = append(c.interCerts, v...)
		}
	case []x509.Certificate:
		pointers := make([]*x509.Certificate, len(v))
		for i := range v {
			pointers[i] = &v[i]
		}
		return c.apply(pointers)
	case []byte:
		var certs []*x509.Certificate
		var err error
		if block, _ := pem.Decode(v); block != nil {
			certs, err = ParsePEMCertificates(v)
		} else {
			certs, err = ParseCertificates(v)
		}
		if err != nil {
			return err
		}
		return c.apply(certs)
	case string:
		if strings.Contains(v, "-----BEGIN") {
			certs, err := ParsePEMCertificates([]byte(v))
			if err != nil {
				return err
			}
			return c.apply(certs)
		}
		c.verify.DNSName = v
	case x509.ExtKeyUsage:
		c.verify.KeyUsages = append(c.verify.KeyUsages, v)
	case []x509.ExtKeyUsage:
		c.verify.KeyUsages = append([]x509.ExtKeyUsage(nil), v...)
	case time.Time:
		c.verify.CurrentTime = v
	default:
		return fmt.Errorf("netsec: unsupported path option %T", value)
	}
	return nil
}

func pathCertificates(value any) ([]*x509.Certificate, error) {
	switch v := value.(type) {
	case *x509.Certificate:
		if v == nil {
			return nil, ErrNilCertificate
		}
		return []*x509.Certificate{v}, nil
	case x509.Certificate:
		return []*x509.Certificate{&v}, nil
	case []*x509.Certificate:
		return v, nil
	case []x509.Certificate:
		certs := make([]*x509.Certificate, len(v))
		for i := range v {
			certs[i] = &v[i]
		}
		return certs, nil
	case [][]byte:
		certs := make([]*x509.Certificate, 0, len(v))
		for _, der := range v {
			cert, err := ParseCertificate(der)
			if err != nil {
				return nil, err
			}
			certs = append(certs, cert)
		}
		return certs, nil
	case []byte:
		if block, _ := pem.Decode(v); block != nil {
			return ParsePEMCertificates(v)
		}
		return ParseCertificates(v)
	case string:
		if strings.Contains(v, "-----BEGIN") {
			return ParsePEMCertificates([]byte(v))
		}
		return ParseCertificates([]byte(v))
	case *x509.CertPool:
		return nil, errors.New("netsec: x509.CertPool does not expose its certificates; pass a certificate slice")
	default:
		return nil, fmt.Errorf("netsec: unsupported path value %T", value)
	}
}

func poolFromCerts(certs []*x509.Certificate) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	for i, cert := range certs {
		if cert == nil {
			return nil, fmt.Errorf("netsec: nil certificate at index %d", i)
		}
		if err := CheckCertificate(cert); err != nil {
			return nil, fmt.Errorf("netsec: trust certificate at index %d: %w", i, err)
		}
		pool.AddCert(cert)
	}
	return pool, nil
}

func checkCertificateSlice(certs []*x509.Certificate, role string) error {
	for i, cert := range certs {
		if cert == nil {
			return fmt.Errorf("netsec: nil %s certificate at index %d", role, i)
		}
		if err := CheckCertificate(cert); err != nil {
			return fmt.Errorf("netsec: %s certificate at index %d: %w", role, i, err)
		}
	}
	return nil
}

func (c *pathConfig) buildOptions(chain []*x509.Certificate) (x509.VerifyOptions, error) {
	opts := c.verify

	if err := checkCertificateSlice(c.rootCerts, "trust"); err != nil {
		return opts, err
	}
	if err := checkCertificateSlice(c.interCerts, "intermediate"); err != nil {
		return opts, err
	}

	if len(c.rootCerts) > 0 {
		if opts.Roots == nil {
			opts.Roots = x509.NewCertPool()
		}
		if err := addCerts(opts.Roots, c.rootCerts); err != nil {
			return opts, err
		}
	}

	if opts.Roots == nil {
		root := chain[len(chain)-1]
		pool, err := poolFromCerts([]*x509.Certificate{root})
		if err != nil {
			return opts, err
		}
		opts.Roots = pool
	}

	if opts.Intermediates == nil {
		opts.Intermediates = x509.NewCertPool()
	}

	if err := addCerts(opts.Intermediates, c.interCerts); err != nil {
		return opts, err
	}

	if c.explicitRoots {
		if err := addCerts(opts.Intermediates, chain[1:]); err != nil {
			return opts, err
		}
	} else {
		if err := addCerts(opts.Intermediates, chain[1:max(1, len(chain)-1)]); err != nil {
			return opts, err
		}
	}

	return opts, nil
}

func addCerts(pool *x509.CertPool, certs []*x509.Certificate) error {
	for i, cert := range certs {
		if cert == nil {
			return fmt.Errorf("netsec: nil certificate at index %d", i)
		}
		if err := CheckCertificate(cert); err != nil {
			return err
		}
		pool.AddCert(cert)
	}
	return nil
}

func verifiedChainHasDuplicate(chains [][]*x509.Certificate) error {
	for i, chain := range chains {
		for j, cert := range chain {
			if err := CheckCertificate(cert); err != nil {
				return fmt.Errorf("netsec: verified chain %d certificate %d: %w", i, j, err)
			}
		}
	}
	return nil
}

// VerifyCertificatePath checks every supplied certificate for duplicate
// extension OIDs and then performs a normal crypto/x509 path verification.
// Accepted path values include *x509.Certificate, []*x509.Certificate,
// DER/PEM data, or a string containing DER/PEM data. Accepted options include
// PathOptions, x509.VerifyOptions, a root *x509.CertPool, trust-anchor
// certificate slices, a DNS name, key usages, and a verification time.
//
// If no trust anchors are supplied, the last certificate in the path is used
// as the trust anchor.
func VerifyCertificatePath(path any, options ...any) ([][]*x509.Certificate, error) {
	chain, err := pathCertificates(path)
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 {
		return nil, ErrEmptyPath
	}
	if err := CheckCertificatePath(chain); err != nil {
		return nil, err
	}

	config := &pathConfig{}
	for _, option := range options {
		if err := config.apply(option); err != nil {
			return nil, err
		}
	}

	opts, err := config.buildOptions(chain)
	if err != nil {
		return nil, err
	}

	verified, err := chain[0].Verify(opts)
	if err != nil {
		return nil, err
	}
	if err := verifiedChainHasDuplicate(verified); err != nil {
		return nil, err
	}
	return verified, nil
}

// ValidateCertificatePath verifies a certificate path. Duplicate extension
// OIDs cause an immediate failure before any path building or name
// constraints evaluation takes place.
func ValidateCertificatePath(path any, options ...any) error {
	_, err := VerifyCertificatePath(path, options...)
	return err
}

// ValidatePath is an alias for ValidateCertificatePath.
func ValidatePath(path any, options ...any) error {
	return ValidateCertificatePath(path, options...)
}

// ValidateChain is an alias for ValidateCertificatePath.
func ValidateChain(path any, options ...any) error {
	return ValidateCertificatePath(path, options...)
}

// Validate is an alias for ValidateCertificatePath.
func Validate(path any, options ...any) error {
	return ValidateCertificatePath(path, options...)
}

// ValidateCertPath is an alias for ValidateCertificatePath.
func ValidateCertPath(path any, options ...any) error {
	return ValidateCertificatePath(path, options...)
}

// ValidateCertificateChain is an alias for ValidateCertificatePath.
func ValidateCertificateChain(path any, options ...any) error {
	return ValidateCertificatePath(path, options...)
}

// VerifyPath is an alias for VerifyCertificatePath.
func VerifyPath(path any, options ...any) ([][]*x509.Certificate, error) {
	return VerifyCertificatePath(path, options...)
}

// VerifyChain is an alias for VerifyCertificatePath.
func VerifyChain(path any, options ...any) ([][]*x509.Certificate, error) {
	return VerifyCertificatePath(path, options...)
}

// Verify is an alias for VerifyCertificatePath.
func Verify(path any, options ...any) ([][]*x509.Certificate, error) {
	return VerifyCertificatePath(path, options...)
}

// VerifyCertificate is an alias for VerifyCertificatePath.
func VerifyCertificate(path any, options ...any) ([][]*x509.Certificate, error) {
	return VerifyCertificatePath(path, options...)
}

// VerifyCertificateChain is an alias for VerifyCertificatePath.
func VerifyCertificateChain(path any, options ...any) ([][]*x509.Certificate, error) {
	return VerifyCertificatePath(path, options...)
}

// Validator is a reusable path validator.
type Validator struct {
	Options PathOptions
}

// NewValidator returns a validator with the supplied trust anchors.
func NewValidator(roots ...*x509.Certificate) *Validator {
	return &Validator{Options: PathOptions{RootCertificates: roots}}
}

// Validate checks the path using the validator's options.
func (v *Validator) Validate(path any) error {
	if v == nil {
		return errors.New("netsec: nil validator")
	}
	return ValidateCertificatePath(path, v.Options)
}

// Verify checks the path using the validator's options.
func (v *Validator) Verify(path any) ([][]*x509.Certificate, error) {
	if v == nil {
		return nil, errors.New("netsec: nil validator")
	}
	return VerifyCertificatePath(path, v.Options)
}
