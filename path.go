// Package netsec provides certificate path validation helpers.
//
// The rules enforced on top of crypto/x509 chain building:
//
//   - Every intermediate certificate in the path must have
//     basicConstraints cA=true. A certificate with cA=false must never
//     appear in the middle of a path.
//   - The end-entity (leaf) certificate must have cA=false. A leaf with
//     cA=true that could still issue certificates is rejected.
//   - The leaf is accepted only when its extended key usage matches the
//     usages requested by the caller.
package netsec

import (
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrIntermediateNotCA is returned when a certificate in the middle
	// of the path has basicConstraints cA=false (or the extension is
	// missing) and therefore must not have issued anything.
	ErrIntermediateNotCA = errors.New("netsec: intermediate certificate has basicConstraints cA=false")

	// ErrLeafIsCA is returned when the end-entity certificate has
	// basicConstraints cA=true and could still issue certificates.
	ErrLeafIsCA = errors.New("netsec: end-entity certificate has basicConstraints cA=true and may issue certificates")
)

// PathOptions configures ValidatePath.
type PathOptions struct {
	// Roots is the trust anchor pool. Required.
	Roots *x509.CertPool

	// DNSName, when non-empty, is verified against the leaf's SANs.
	DNSName string

	// KeyUsages lists the extended key usages the leaf must satisfy.
	// Defaults to ServerAuth when empty.
	KeyUsages []x509.ExtKeyUsage

	// CurrentTime overrides the validation time. Zero means time.Now().
	CurrentTime time.Time
}

// ValidatePath validates a certificate path whose end-entity certificate
// is leaf and whose intermediate certificates (ordered from leaf's issuer
// upwards, excluding the root) are intermediates.
//
// It returns the verified chains on success.
func ValidatePath(leaf *x509.Certificate, intermediates []*x509.Certificate, opts PathOptions) ([][]*x509.Certificate, error) {
	if leaf == nil {
		return nil, errors.New("netsec: nil leaf certificate")
	}
	if opts.Roots == nil {
		return nil, errors.New("netsec: nil root pool")
	}

	// Rule 1: nothing with cA=false may sit in the middle of the path.
	for i, inter := range intermediates {
		if inter == nil {
			return nil, fmt.Errorf("netsec: nil intermediate certificate at position %d", i)
		}
		if !inter.BasicConstraintsValid || !inter.IsCA {
			return nil, fmt.Errorf("%w (position %d, subject %q)", ErrIntermediateNotCA, i, inter.Subject)
		}
	}

	// Rule 2: the end-entity certificate must not be a CA.
	if leaf.IsCA {
		return nil, fmt.Errorf("%w (subject %q)", ErrLeafIsCA, leaf.Subject)
	}

	pool := x509.NewCertPool()
	for _, inter := range intermediates {
		pool.AddCert(inter)
	}

	usages := opts.KeyUsages
	if len(usages) == 0 {
		usages = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}

	// Rule 3: standard chain building plus key-usage enforcement.
	return leaf.Verify(x509.VerifyOptions{
		Roots:         opts.Roots,
		Intermediates: pool,
		DNSName:       opts.DNSName,
		KeyUsages:     usages,
		CurrentTime:   opts.CurrentTime,
	})
}
