package netsec

import (
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
)

// ErrNameConstraint is returned (wrapped) by VerifyPath when a
// certificate in the path carries a DNS name that violates the name
// constraints imposed by an issuing CA above it.
var ErrNameConstraint = errors.New("netsec: name violates name constraints")

// VerifyPath verifies a certificate path ordered leaf-first and ending
// in a self-signed trust anchor. It checks each signature along the
// path and enforces the DNS name constraints (permitted and excluded
// subtrees) imposed by every CA in the path.
//
// A self-issued intermediate certificate — one whose subject equals its
// issuer and whose signature verifies with its own public key, i.e. the
// same key — is exempt from the name-constraint check exactly once, for
// itself. The exemption does not extend to any other certificate: the
// constraints accumulated above it still apply to every certificate
// below it, and the self-issued certificate's own constraints are added
// on top. The exemption never applies to the leaf (final) certificate.
//
// VerifyPath does not check validity periods or revocation status.
func VerifyPath(chain []*x509.Certificate) error {
	if len(chain) == 0 {
		return errors.New("netsec: empty certificate path")
	}
	root := chain[len(chain)-1]
	if !IsSelfIssued(root) {
		return fmt.Errorf("netsec: path does not end in a self-signed trust anchor: %q", root.Subject)
	}
	for i := 0; i < len(chain)-1; i++ {
		if err := chain[i].CheckSignatureFrom(chain[i+1]); err != nil {
			return fmt.Errorf("netsec: signature check failed for %q: %w", chain[i].Subject, err)
		}
	}

	var constraints []nameConstraint
	for i := len(chain) - 1; i >= 0; i-- {
		cert := chain[i]
		isAnchor := i == len(chain)-1
		// The trust anchor has no issuer above it. A self-issued
		// intermediate skips the check for itself only; the leaf is
		// always checked.
		if !isAnchor && (i == 0 || !IsSelfIssued(cert)) {
			if err := checkConstraints(cert, constraints); err != nil {
				return err
			}
		}
		if len(cert.PermittedDNSDomains) > 0 || len(cert.ExcludedDNSDomains) > 0 {
			constraints = append(constraints, nameConstraint{
				issuer:    cert.Subject.String(),
				permitted: cert.PermittedDNSDomains,
				excluded:  cert.ExcludedDNSDomains,
			})
		}
	}
	return nil
}

// IsSelfIssued reports whether cert is self-issued: its subject and
// issuer are identical and its signature verifies with its own public
// key, meaning the certificate was issued to the same entity using the
// same key. A certificate that merely reuses the issuer's name with a
// different key is not self-issued.
func IsSelfIssued(cert *x509.Certificate) bool {
	if !bytes.Equal(cert.RawSubject, cert.RawIssuer) {
		return false
	}
	return cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil
}

type nameConstraint struct {
	issuer    string
	permitted []string
	excluded  []string
}

func checkConstraints(cert *x509.Certificate, constraints []nameConstraint) error {
	for _, name := range cert.DNSNames {
		for _, c := range constraints {
			if matchAny(name, c.excluded) {
				return fmt.Errorf("%w: %q on %q is excluded by %q", ErrNameConstraint, name, cert.Subject, c.issuer)
			}
			if len(c.permitted) > 0 && !matchAny(name, c.permitted) {
				return fmt.Errorf("%w: %q on %q is not permitted by %q", ErrNameConstraint, name, cert.Subject, c.issuer)
			}
		}
	}
	return nil
}

func matchAny(name string, domains []string) bool {
	for _, domain := range domains {
		if matchDomain(name, domain) {
			return true
		}
	}
	return false
}

// matchDomain reports whether name falls inside the constrained domain,
// matching the domain itself or any of its subdomains.
func matchDomain(name, constraint string) bool {
	name = strings.ToLower(name)
	constraint = strings.TrimPrefix(strings.ToLower(constraint), ".")
	return name == constraint || strings.HasSuffix(name, "."+constraint)
}
