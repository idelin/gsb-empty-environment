package netsec

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrEmptyPath is returned when a certificate path contains no certificates.
	ErrEmptyPath = errors.New("netsec: certificate path is empty")

	// ErrNilCertificate is returned when a certificate path contains a nil entry.
	ErrNilCertificate = errors.New("netsec: certificate path contains a nil certificate")

	// ErrDuplicateSubjectPublicKey is returned when the same subject distinguished
	// name and public key occur more than once in a certificate path.
	ErrDuplicateSubjectPublicKey = errors.New("netsec: duplicate subject distinguished name and public key in certificate path (certificate loop)")

	// ErrPathNotOrdered is returned when signature validation is requested for a
	// path that is not a contiguous leaf-to-anchor or anchor-to-leaf chain.
	ErrPathNotOrdered = errors.New("netsec: certificate path is not ordered from leaf to trust anchor")
)

var (
	// ErrDuplicateCertificate and ErrCertificateLoop are aliases for
	// ErrDuplicateSubjectPublicKey.
	ErrDuplicateCertificate = ErrDuplicateSubjectPublicKey
	ErrCertificateLoop      = ErrDuplicateSubjectPublicKey
)

// DuplicateCertificateError describes two positions in a certificate path that
// contain the same subject distinguished name and public key.
type DuplicateCertificateError struct {
	FirstIndex  int
	SecondIndex int
	Subject     string
}

func (e *DuplicateCertificateError) Error() string {
	if e == nil {
		return ErrDuplicateSubjectPublicKey.Error()
	}
	if e.Subject == "" {
		return fmt.Sprintf(
			"netsec: duplicate subject distinguished name and public key at positions %d and %d",
			e.FirstIndex, e.SecondIndex,
		)
	}
	return fmt.Sprintf(
		"netsec: duplicate subject distinguished name %q and public key at positions %d and %d",
		e.Subject, e.FirstIndex, e.SecondIndex,
	)
}

// Unwrap makes errors.Is(err, ErrDuplicateSubjectPublicKey) succeed.
func (e *DuplicateCertificateError) Unwrap() error {
	return ErrDuplicateSubjectPublicKey
}

// PathError identifies the certificate position at which path validation failed.
type PathError struct {
	Index int
	Err   error
}

func (e *PathError) Error() string {
	if e == nil {
		return "netsec: certificate path validation failed"
	}
	return fmt.Sprintf("netsec: certificate path position %d: %v", e.Index, e.Err)
}

// Unwrap returns the underlying error.
func (e *PathError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ValidationOptions controls optional cryptographic checks. Duplicate
// subject/public-key detection is always performed.
type ValidationOptions struct {
	// Roots is the set of trust anchors used when VerifyTrust is enabled.
	Roots *x509.CertPool

	// Intermediates is the set of additional intermediates used when
	// VerifyTrust is enabled.
	Intermediates *x509.CertPool

	// VerifySignatures checks that every adjacent pair in the path is a valid
	// issuer/subject relationship. A path may be supplied in either
	// leaf-to-anchor or anchor-to-leaf order.
	VerifySignatures bool

	// VerifyTrust performs full path verification with the first certificate
	// (or the last certificate when the path is anchor-to-leaf) as the leaf.
	VerifyTrust bool

	// MaxPathLength limits the number of intermediate certificates in the path.
	// A value of zero disables the limit.
	MaxPathLength int

	// CurrentTime is used when validity is checked. The zero value means time.Now().
	CurrentTime time.Time

	// DNSName and KeyUsages are passed to x509.Certificate.Verify when
	// VerifyTrust is enabled.
	DNSName   string
	KeyUsages []x509.ExtKeyUsage
}

// Option is an optional argument accepted by the path validation functions.
// Supported values are ValidationOptions, *ValidationOptions, *x509.VerifyOptions,
// *x509.CertPool (roots first, intermediates second), bool, int, time.Time,
// string, and *x509.Certificate.
type Option interface{}

// WithRoots sets the trust anchors used for trust verification.
func WithRoots(roots *x509.CertPool) Option {
	return ValidationOptions{Roots: roots}
}

// WithIntermediates sets the intermediate pool used for trust verification.
func WithIntermediates(intermediates *x509.CertPool) Option {
	return ValidationOptions{Intermediates: intermediates}
}

// WithSignatureVerification requests issuer/subject signature checks.
func WithSignatureVerification() Option {
	return ValidationOptions{VerifySignatures: true}
}

// WithTrustVerification requests full x509 trust-path verification.
func WithTrustVerification(roots *x509.CertPool) Option {
	return ValidationOptions{Roots: roots, VerifyTrust: true}
}

// WithMaxPathLength limits the number of intermediate certificates in the path.
func WithMaxPathLength(n int) Option {
	return ValidationOptions{MaxPathLength: n}
}

// WithCurrentTime sets the time used for validity checks.
func WithCurrentTime(t time.Time) Option {
	return ValidationOptions{CurrentTime: t}
}

// WithDNSName sets the DNS name checked during trust verification.
func WithDNSName(name string) Option {
	return ValidationOptions{DNSName: name}
}

// WithKeyUsages sets the extended key usages checked during trust verification.
func WithKeyUsages(usages ...x509.ExtKeyUsage) Option {
	return ValidationOptions{KeyUsages: usages}
}

// SubjectPublicKeyEqual reports whether a and b have the same subject
// distinguished name and the same public key. Subject alternative names and
// other certificate fields are deliberately ignored.
func SubjectPublicKeyEqual(a, b *x509.Certificate) bool {
	if a == nil || b == nil {
		return false
	}
	return bytes.Equal(subjectIdentity(a), subjectIdentity(b)) &&
		bytes.Equal(
			publicKeyIdentity(a.PublicKey, a.RawSubjectPublicKeyInfo),
			publicKeyIdentity(b.PublicKey, b.RawSubjectPublicKeyInfo),
		)
}

// SameSubjectAndPublicKey is an alias for SubjectPublicKeyEqual.
func SameSubjectAndPublicKey(a, b *x509.Certificate) bool {
	return SubjectPublicKeyEqual(a, b)
}

// AlreadyInChain reports whether candidate has the same subject and public key
// as a certificate already present in chain.
func AlreadyInChain(candidate *x509.Certificate, chain []*x509.Certificate) bool {
	if candidate == nil {
		return false
	}
	for _, cert := range chain {
		if SubjectPublicKeyEqual(candidate, cert) {
			return true
		}
	}
	return false
}

func alreadyInChain(candidate *x509.Certificate, chain []*x509.Certificate) bool {
	return AlreadyInChain(candidate, chain)
}

// FindDuplicateSubjectPublicKey searches the whole path for a repeated
// subject/public-key pair. It returns the positions of the first repetition
// and whether a duplicate was found.
func FindDuplicateSubjectPublicKey(path []*x509.Certificate) (first, second int, found bool) {
	type identity struct {
		subject   string
		publicKey string
	}

	seen := make(map[identity]int, len(path))
	for i, cert := range path {
		if cert == nil {
			continue
		}
		id := identity{
			subject:   string(subjectIdentity(cert)),
			publicKey: string(publicKeyIdentity(cert.PublicKey, cert.RawSubjectPublicKeyInfo)),
		}
		if previous, ok := seen[id]; ok {
			return previous, i, true
		}
		seen[id] = i
	}
	return -1, -1, false
}

// FindDuplicate is an alias for FindDuplicateSubjectPublicKey.
func FindDuplicate(path []*x509.Certificate) (first, second int, found bool) {
	return FindDuplicateSubjectPublicKey(path)
}

// HasDuplicateSubjectPublicKey reports whether the path contains a repeated
// subject/public-key pair.
func HasDuplicateSubjectPublicKey(path []*x509.Certificate) bool {
	_, _, found := FindDuplicateSubjectPublicKey(path)
	return found
}

// DuplicateSubjectPublicKey is an alias for HasDuplicateSubjectPublicKey.
func DuplicateSubjectPublicKey(path []*x509.Certificate) bool {
	return HasDuplicateSubjectPublicKey(path)
}

// ContainsDuplicateSubjectPublicKey is an alias for HasDuplicateSubjectPublicKey.
func ContainsDuplicateSubjectPublicKey(path []*x509.Certificate) bool {
	return HasDuplicateSubjectPublicKey(path)
}

// ContainsDuplicateCertificate is an alias for HasDuplicateSubjectPublicKey.
func ContainsDuplicateCertificate(path []*x509.Certificate) bool {
	return HasDuplicateSubjectPublicKey(path)
}

// ValidatePath validates a certificate path. The path may be supplied in
// leaf-to-anchor or anchor-to-leaf order. Duplicate subject/public-key pairs are
// always rejected. Additional signature or trust checks can be requested with
// options.
//
// ValidatePath accepts []*x509.Certificate, []x509.Certificate,
// *x509.Certificate, or x509.Certificate as its first argument.
func ValidatePath(path any, opts ...Option) error {
	certs, err := normalizePath(path)
	if err != nil {
		return err
	}
	if len(certs) == 0 {
		return ErrEmptyPath
	}
	for i, cert := range certs {
		if cert == nil {
			return &PathError{Index: i, Err: ErrNilCertificate}
		}
	}

	if first, second, found := FindDuplicateSubjectPublicKey(certs); found {
		return &DuplicateCertificateError{
			FirstIndex:  first,
			SecondIndex: second,
			Subject:     certs[first].Subject.String(),
		}
	}

	cfg, err := parseOptions(opts...)
	if err != nil {
		return err
	}
	if cfg.MaxPathLength > 0 {
		if err := checkMaxPathLength(certs, cfg.MaxPathLength); err != nil {
			return err
		}
	}
	if cfg.VerifySignatures {
		if err := verifySignatures(certs, cfg); err != nil {
			return err
		}
	}
	if cfg.VerifyTrust {
		if err := verifyTrust(certs, cfg); err != nil {
			return err
		}
	}
	return nil
}

// ValidateCertificatePath is an alias for ValidatePath.
func ValidateCertificatePath(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// VerifyPath is an alias for ValidatePath.
func VerifyPath(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// VerifyCertificatePath is an alias for ValidatePath.
func VerifyCertificatePath(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// ValidateCertificateChain is an alias for ValidatePath.
func ValidateCertificateChain(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// VerifyCertificateChain is an alias for ValidatePath.
func VerifyCertificateChain(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// CheckCertificateChain is an alias for ValidatePath.
func CheckCertificateChain(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// ValidateChain is an alias for ValidatePath.
func ValidateChain(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// VerifyChain is an alias for ValidatePath.
func VerifyChain(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// CheckChain is an alias for ValidatePath.
func CheckChain(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// CheckPath is an alias for ValidatePath.
func CheckPath(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// CheckCertificatePath is an alias for ValidatePath.
func CheckCertificatePath(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// ValidateCertPath is an alias for ValidatePath.
func ValidateCertPath(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// VerifyCertPath is an alias for ValidatePath.
func VerifyCertPath(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// CheckCertPath is an alias for ValidatePath.
func CheckCertPath(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// Validate is an alias for ValidatePath.
func Validate(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// Verify is an alias for ValidatePath.
func Verify(path any, opts ...Option) error {
	return ValidatePath(path, opts...)
}

// IsValidPath reports whether ValidatePath accepts the path.
func IsValidPath(path any, opts ...Option) bool {
	return ValidatePath(path, opts...) == nil
}

// IsValid is an alias for IsValidPath.
func IsValid(path any, opts ...Option) bool {
	return IsValidPath(path, opts...)
}

func validatePath(path []*x509.Certificate, opts ...Option) error {
	return ValidatePath(path, opts...)
}

func verifyPath(path []*x509.Certificate, opts ...Option) error {
	return ValidatePath(path, opts...)
}

func checkPath(path []*x509.Certificate, opts ...Option) error {
	return ValidatePath(path, opts...)
}

func normalizePath(path any) ([]*x509.Certificate, error) {
	switch p := path.(type) {
	case []*x509.Certificate:
		return p, nil
	case []x509.Certificate:
		normalized := make([]*x509.Certificate, len(p))
		for i := range p {
			normalized[i] = &p[i]
		}
		return normalized, nil
	case *x509.Certificate:
		if p == nil {
			return nil, ErrNilCertificate
		}
		return []*x509.Certificate{p}, nil
	case x509.Certificate:
		return []*x509.Certificate{&p}, nil
	case nil:
		return nil, ErrEmptyPath
	default:
		return nil, fmt.Errorf("netsec: unsupported certificate path type %T", path)
	}
}

func parseOptions(opts ...Option) (ValidationOptions, error) {
	var cfg ValidationOptions
	certPools := 0
	for _, opt := range opts {
		switch value := opt.(type) {
		case nil:
		case ValidationOptions:
			mergeOptions(&cfg, value)
		case *ValidationOptions:
			if value != nil {
				mergeOptions(&cfg, *value)
			}
		case x509.VerifyOptions:
			mergeVerifyOptions(&cfg, &value)
		case *x509.VerifyOptions:
			if value != nil {
				mergeVerifyOptions(&cfg, value)
			}
		case *x509.CertPool:
			if value == nil {
				continue
			}
			if certPools == 0 {
				cfg.Roots = value
			} else {
				cfg.Intermediates = value
			}
			certPools++
		case bool:
			cfg.VerifySignatures = value
			cfg.VerifyTrust = value
		case int:
			cfg.MaxPathLength = value
		case time.Time:
			cfg.CurrentTime = value
		case string:
			cfg.DNSName = value
		case *x509.Certificate:
			if value == nil {
				continue
			}
			if cfg.Roots == nil {
				cfg.Roots = x509.NewCertPool()
			}
			cfg.Roots.AddCert(value)
		case []*x509.Certificate:
			cfg.Intermediates = addCertificates(cfg.Intermediates, value)
		case []x509.Certificate:
			pool := cfg.Intermediates
			if pool == nil {
				pool = x509.NewCertPool()
			}
			for i := range value {
				pool.AddCert(&value[i])
			}
			cfg.Intermediates = pool
		default:
			return cfg, fmt.Errorf("netsec: unsupported validation option %T", opt)
		}
	}
	return cfg, nil
}

func mergeOptions(dst *ValidationOptions, src ValidationOptions) {
	if src.Roots != nil {
		dst.Roots = src.Roots
	}
	if src.Intermediates != nil {
		dst.Intermediates = src.Intermediates
	}
	if src.VerifySignatures {
		dst.VerifySignatures = true
	}
	if src.VerifyTrust {
		dst.VerifyTrust = true
	}
	if src.MaxPathLength != 0 {
		dst.MaxPathLength = src.MaxPathLength
	}
	if !src.CurrentTime.IsZero() {
		dst.CurrentTime = src.CurrentTime
	}
	if src.DNSName != "" {
		dst.DNSName = src.DNSName
	}
	if len(src.KeyUsages) > 0 {
		dst.KeyUsages = src.KeyUsages
	}
}

func mergeVerifyOptions(dst *ValidationOptions, src *x509.VerifyOptions) {
	dst.VerifyTrust = true
	if src.Roots != nil {
		dst.Roots = src.Roots
	}
	if src.Intermediates != nil {
		dst.Intermediates = src.Intermediates
	}
	if !src.CurrentTime.IsZero() {
		dst.CurrentTime = src.CurrentTime
	}
	if src.DNSName != "" {
		dst.DNSName = src.DNSName
	}
	if len(src.KeyUsages) > 0 {
		dst.KeyUsages = src.KeyUsages
	}
}

func checkMaxPathLength(path []*x509.Certificate, max int) error {
	intermediates := len(path) - 2
	if intermediates < 0 {
		intermediates = 0
	}
	if intermediates > max {
		return fmt.Errorf(
			"netsec: certificate path has %d intermediates, limit is %d",
			intermediates, max,
		)
	}
	return nil
}

func verifySignatures(path []*x509.Certificate, cfg ValidationOptions) error {
	orders := [][]*x509.Certificate{path}
	reversed := make([]*x509.Certificate, len(path))
	for i := range path {
		reversed[len(path)-1-i] = path[i]
	}
	orders = append(orders, reversed)

	var firstErr error
	for _, ordered := range orders {
		err := verifySignaturesInOrder(ordered, cfg)
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = ErrPathNotOrdered
	}
	return firstErr
}

func verifySignaturesInOrder(path []*x509.Certificate, cfg ValidationOptions) error {
	now := cfg.CurrentTime
	if now.IsZero() {
		now = time.Now()
	}
	for i := 0; i+1 < len(path); i++ {
		child, parent := path[i], path[i+1]
		if now.Before(child.NotBefore) || now.After(child.NotAfter) {
			return &PathError{
				Index: i,
				Err:   fmt.Errorf("certificate is not valid at %s", now.Format(time.RFC3339)),
			}
		}
		if !issuerMatchesSubject(child, parent) {
			return &PathError{
				Index: i,
				Err:   errors.New("certificate issuer does not match the next certificate subject"),
			}
		}
		if err := child.CheckSignatureFrom(parent); err != nil {
			return &PathError{
				Index: i,
				Err:   fmt.Errorf("certificate is not signed by the next certificate: %w", err),
			}
		}
	}
	last := path[len(path)-1]
	if now.Before(last.NotBefore) || now.After(last.NotAfter) {
		return &PathError{
			Index: len(path) - 1,
			Err:   fmt.Errorf("certificate is not valid at %s", now.Format(time.RFC3339)),
		}
	}
	return nil
}

func verifyTrust(path []*x509.Certificate, cfg ValidationOptions) error {
	opts := x509.VerifyOptions{
		Roots:         cfg.Roots,
		Intermediates: cfg.Intermediates,
		CurrentTime:   cfg.CurrentTime,
		DNSName:       cfg.DNSName,
		KeyUsages:     cfg.KeyUsages,
	}

	candidates := []*x509.Certificate{path[0], path[len(path)-1]}
	var firstErr error
	for _, leaf := range candidates {
		chains, err := leaf.Verify(opts)
		if err == nil {
			for _, chain := range chains {
				if chainContainsPath(chain, path) {
					return nil
				}
			}
			err = errors.New("verified chain does not contain the supplied certificate path")
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = x509.UnknownAuthorityError{Cert: path[0]}
	}
	return fmt.Errorf("netsec: trust path verification failed: %w", firstErr)
}

func issuerMatchesSubject(child, parent *x509.Certificate) bool {
	if len(child.RawIssuer) > 0 && len(parent.RawSubject) > 0 {
		return bytes.Equal(child.RawIssuer, parent.RawSubject)
	}
	return child.Issuer.String() == parent.Subject.String()
}

func chainContainsPath(chain, path []*x509.Certificate) bool {
	for _, want := range path {
		found := false
		for _, cert := range chain {
			if bytes.Equal(cert.Raw, want.Raw) || SubjectPublicKeyEqual(cert, want) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func subjectIdentity(cert *x509.Certificate) []byte {
	if len(cert.RawSubject) > 0 {
		return cert.RawSubject
	}
	return []byte(cert.Subject.String())
}

func addCertificates(pool *x509.CertPool, certs []*x509.Certificate) *x509.CertPool {
	if pool == nil {
		pool = x509.NewCertPool()
	}
	for _, cert := range certs {
		if cert != nil {
			pool.AddCert(cert)
		}
	}
	return pool
}

func publicKeyIdentity(key crypto.PublicKey, rawSubjectPublicKeyInfo []byte) []byte {
	if key != nil {
		if der, err := x509.MarshalPKIXPublicKey(key); err == nil {
			return der
		}
	}
	if len(rawSubjectPublicKeyInfo) > 0 {
		return rawSubjectPublicKeyInfo
	}
	if key != nil {
		return []byte(fmt.Sprintf("%T:%v", key, key))
	}
	return nil
}
