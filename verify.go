package netsec

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"time"
)

// Options controls path validation.
type Options struct {
	// CurrentTime is the time at which validity is checked. The zero value
	// means time.Now.
	CurrentTime time.Time

	// MaxConstraintComparisons bounds the number of name/constraint
	// comparisons. Values less than or equal to zero use a safe default.
	MaxConstraintComparisons int
}

// Verify validates a certificate path ordered leaf first and root last. It
// checks signatures, validity periods, CA basic constraints, path length
// constraints, and RFC 5280 name constraints.
//
// A name constraints extension on a non-CA certificate is always an error,
// even if the certificate is otherwise valid. See VerifyChain for the same
// operation under a more explicit name.
func Verify(chain []*x509.Certificate) error {
	return VerifyChain(chain)
}

// VerifyChain validates a certificate path ordered leaf first and root last.
func VerifyChain(chain []*x509.Certificate) error {
	return VerifyChainWithOptions(chain, nil)
}

// VerifyPath is an alias for VerifyChain.
func VerifyPath(chain []*x509.Certificate) error {
	return VerifyChain(chain)
}

// VerifyCertificatePath is an alias for VerifyChain.
func VerifyCertificatePath(chain []*x509.Certificate) error {
	return VerifyChain(chain)
}

// ValidateChain is an alias for VerifyChain.
func ValidateChain(chain []*x509.Certificate) error {
	return VerifyChain(chain)
}

// VerifyChainWithOptions validates a leaf-to-root path using opts.
func VerifyChainWithOptions(chain []*x509.Certificate, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}
	now := opts.CurrentTime
	if now.IsZero() {
		now = time.Now()
	}

	chain = normalizeChain(chain)
	if len(chain) == 0 {
		return validationError(0, nil, ErrInvalidChain, "empty chain")
	}
	for i, cert := range chain {
		if cert == nil {
			return validationError(i, nil, ErrInvalidChain, "nil certificate")
		}
	}

	// Reject name constraints on non-CA certificates before doing anything
	// else. Ignoring such an extension would be unsafe for callers that
	// expect constraints to be honoured, and processing it below other
	// constraints is exactly how a wider leaf constraint could be abused.
	for i, cert := range chain {
		if HasNameConstraints(cert) && !IsCA(cert) {
			return validationError(i, cert, ErrNameConstraintsOnNonCA,
				"name constraints extension found on a non-CA certificate")
		}
	}

	if err := checkExtensions(chain); err != nil {
		return err
	}
	if err := checkValidity(chain, now); err != nil {
		return err
	}
	if err := checkIssuers(chain); err != nil {
		return err
	}
	if err := checkCAConstraints(chain); err != nil {
		return err
	}

	return CheckChainNameConstraintsWithLimit(chain, opts.MaxConstraintComparisons)
}

// VerifyCertificate builds and verifies paths for cert using Go's standard
// library, then applies this package's stricter name constraints rules to
// every returned path. In particular, a name constraints extension on the
// end-entity certificate is rejected even though crypto/x509 ignores it.
func VerifyCertificate(cert *x509.Certificate, opts *x509.VerifyOptions) ([][]*x509.Certificate, error) {
	if cert == nil {
		return nil, validationError(0, nil, ErrInvalidChain, "nil certificate")
	}
	if opts == nil {
		opts = &x509.VerifyOptions{}
	}

	chains, err := cert.Verify(*opts)
	if err != nil {
		return nil, err
	}
	for _, chain := range chains {
		if err := CheckChainNameConstraintsWithLimit(chain, opts.MaxConstraintComparisions); err != nil {
			return nil, err
		}
	}
	return chains, nil
}

// VerifyCertificateWithPools is a convenience wrapper around
// VerifyCertificate.
func VerifyCertificateWithPools(cert *x509.Certificate, intermediates, roots *x509.CertPool) ([][]*x509.Certificate, error) {
	return VerifyCertificate(cert, &x509.VerifyOptions{
		Intermediates: intermediates,
		Roots:         roots,
	})
}

// CheckNameConstraints applies only the name constraints portion of path
// validation to a leaf-to-root chain. Signatures and validity periods are not
// checked. It still rejects name constraints on non-CA certificates.
func CheckNameConstraints(chain []*x509.Certificate) error {
	return CheckChainNameConstraintsWithLimit(chain, 0)
}

// CheckChainNameConstraints is an alias for CheckNameConstraints.
func CheckChainNameConstraints(chain []*x509.Certificate) error {
	return CheckNameConstraints(chain)
}

// CheckChainNameConstraintsWithLimit is CheckNameConstraints with an explicit
// comparison budget. A limit less than or equal to zero uses the default.
func CheckChainNameConstraintsWithLimit(chain []*x509.Certificate, maxComparisons int) error {
	chain = normalizeChain(chain)
	if len(chain) == 0 {
		return validationError(0, nil, ErrInvalidChain, "empty chain")
	}
	for i, cert := range chain {
		if cert == nil {
			return validationError(i, nil, ErrInvalidChain, "nil certificate")
		}
		if HasNameConstraints(cert) && !IsCA(cert) {
			return validationError(i, cert, ErrNameConstraintsOnNonCA,
				"name constraints extension found on a non-CA certificate")
		}
	}

	checker := newConstraintChecker(maxComparisons)

	// Walk from the trust anchor towards the leaf. Every CA's constraints
	// are applied independently to all of its descendants. This is the
	// intersection required by RFC 5280: a subordinate CA can narrow the
	// permitted namespace but can never widen it again.
	for caIndex := len(chain) - 1; caIndex >= 0; caIndex-- {
		ca := chain[caIndex]
		if !HasNameConstraints(ca) {
			continue
		}
		for certIndex := 0; certIndex < caIndex; certIndex++ {
			if err := checker.checkNameConstraintsFor(ca, caIndex, chain[certIndex], certIndex); err != nil {
				return err
			}
		}
	}

	return nil
}

// normalizeChain returns chain in leaf-to-root order. The canonical order is
// leaf first, but a path that is consistently linked in the opposite
// direction is accepted as a convenience.
func normalizeChain(chain []*x509.Certificate) []*x509.Certificate {
	if len(chain) < 2 {
		return chain
	}

	leafFirst, rootFirst := true, true
	for i := 0; i < len(chain)-1; i++ {
		if chain[i] == nil || chain[i+1] == nil {
			return chain
		}
		if !bytes.Equal(chain[i].RawIssuer, chain[i+1].RawSubject) {
			leafFirst = false
		}
		if !bytes.Equal(chain[i+1].RawIssuer, chain[i].RawSubject) {
			rootFirst = false
		}
	}

	if !leafFirst && rootFirst {
		reversed := make([]*x509.Certificate, len(chain))
		for i, cert := range chain {
			reversed[len(chain)-1-i] = cert
		}
		return reversed
	}

	return chain
}

func checkExtensions(chain []*x509.Certificate) error {
	for i, cert := range chain {
		if len(cert.UnhandledCriticalExtensions) > 0 {
			return validationError(i, cert, ErrUnhandledCriticalExtension,
				fmt.Sprintf("unhandled critical extensions: %v", cert.UnhandledCriticalExtensions))
		}
	}
	return nil
}

func checkValidity(chain []*x509.Certificate, now time.Time) error {
	for i, cert := range chain {
		if now.Before(cert.NotBefore) {
			return validationError(i, cert, ErrCertificateExpired,
				fmt.Sprintf("current time %s is before notBefore %s", now.Format(time.RFC3339), cert.NotBefore.Format(time.RFC3339)))
		}
		if now.After(cert.NotAfter) {
			return validationError(i, cert, ErrCertificateExpired,
				fmt.Sprintf("current time %s is after notAfter %s", now.Format(time.RFC3339), cert.NotAfter.Format(time.RFC3339)))
		}
	}
	return nil
}

func checkIssuers(chain []*x509.Certificate) error {
	for i := 0; i < len(chain)-1; i++ {
		child, issuer := chain[i], chain[i+1]
		if !bytes.Equal(child.RawIssuer, issuer.RawSubject) {
			return validationError(i, child, ErrInvalidChain,
				fmt.Sprintf("issuer does not match certificate %d's subject", i+1))
		}
		if err := child.CheckSignatureFrom(issuer); err != nil {
			return validationError(i, child, ErrSignature, err.Error())
		}
	}
	return nil
}

func checkCAConstraints(chain []*x509.Certificate) error {
	// The final certificate is the trust anchor. Its CA bit is not part of
	// path validation (legacy v1 roots are still accepted), but every
	// intermediate that issues a certificate in the path must be a CA.
	for i := 1; i < len(chain)-1; i++ {
		cert := chain[i]
		if !IsCA(cert) {
			return validationError(i, cert, ErrNotAuthorizedToSign,
				"issuer certificate is not a CA")
		}
	}

	for i := 1; i < len(chain); i++ {
		cert := chain[i]
		if cert.MaxPathLen < 0 {
			continue
		}

		// RFC 5280 excludes self-issued certificates from pathLen
		// accounting.
		intermediates := 0
		for j := 1; j < i; j++ {
			if !isSelfIssued(chain[j]) {
				intermediates++
			}
		}
		if intermediates > cert.MaxPathLen {
			return validationError(i, cert, ErrPathLengthExceeded,
				fmt.Sprintf("%d intermediate CA certificates below a limit of %d", intermediates, cert.MaxPathLen))
		}
	}

	return nil
}
