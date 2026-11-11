// Package nameconstraint implements RFC 5280 name-constraint enforcement for
// certificate chains, independently of crypto/tls's built-in verification.
//
// The verifier supports dNSName, iPAddress and rfc822Name constraints and
// applies them to every subordinate certificate in a chain (not only the
// leaf). Excluded subtrees always take precedence over permitted subtrees,
// an explicitly empty permitted-subtree set denies every name of the given
// type, wildcard DNS SANs are matched per RFC 6125 and certificates without
// a subjectAltName extension are never matched against the common name.
package nameconstraint

import (
	"crypto/x509"
	"errors"
)

// Reason identifies the category of a name-constraint failure.
type Reason int

const (
	ReasonUnknown Reason = iota
	// ReasonExcluded means a name matched an excludedSubtree constraint.
	ReasonExcluded
	// ReasonNotPermitted means a name matched none of the permittedSubtrees.
	ReasonNotPermitted
	// ReasonMalformedConstraint means a NameConstraints extension was invalid.
	ReasonMalformedConstraint
	// ReasonMalformedName means a SAN value could not be parsed.
	ReasonMalformedName
	// ReasonNoChain means VerifyPeerCertificate was given no usable chain.
	ReasonNoChain
)

// ConstraintError describes a name-constraint violation. Cert is the
// certificate asserting the offending name, Issuer is the CA certificate
// whose constraint was violated.
type ConstraintError struct {
	Reason   Reason
	NameType string
	Name     string
	Detail   string
	Cert     *x509.Certificate
	Issuer   *x509.Certificate
}

func (e *ConstraintError) Error() string {
	if e.Detail != "" {
		return "nameconstraint: " + e.Detail
	}
	return "nameconstraint: constraint violation"
}

// VerifyChain enforces name constraints against a single certificate chain.
// The chain must be ordered leaf-first, ending with the trust anchor. The
// trust anchor's own constraints are applied to its subordinates as well.
// A nil or empty chain is an error.
func VerifyChain(chain []*x509.Certificate) error {
	if len(chain) == 0 {
		return &ConstraintError{Reason: ReasonNoChain, Detail: "empty certificate chain"}
	}

	// Each certificate at index i is subordinate to every certificate at
	// index j > i. Walk the chain leaf-first and evaluate the constraints of
	// every ancestor against the SANs of the current certificate.
	for i := 0; i < len(chain); i++ {
		subject := chain[i]
		for j := i + 1; j < len(chain); j++ {
			issuer := chain[j]
			nc, present, err := parseNameConstraints(issuer)
			if err != nil {
				return &ConstraintError{
					Reason: ReasonMalformedConstraint,
					Detail: "malformed name constraints in " + issuer.Subject.String() + ": " + err.Error(),
					Issuer: issuer,
				}
			}
			if !present {
				continue
			}
			if err := enforceCertificate(subject, nc); err != nil {
				if ce, ok := err.(*ConstraintError); ok {
					ce.Cert = subject
					ce.Issuer = issuer
				}
				return err
			}
		}
	}
	return nil
}

// enforceCertificate checks every SAN value in cert against nc. Certificates
// without a SAN extension carry no names and therefore impose nothing; the
// common name is deliberately never consulted.
func enforceCertificate(cert *x509.Certificate, nc *nameConstraints) error {
	hasSAN := false
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidExtensionSubjectAltName) {
			hasSAN = true
			break
		}
	}
	if !hasSAN {
		return nil
	}

	for _, dns := range cert.DNSNames {
		if err := matchDNS(dns, nc); err != nil {
			return err
		}
	}
	for _, ip := range cert.IPAddresses {
		if err := matchIP(ip, nc); err != nil {
			return err
		}
	}
	for _, email := range cert.EmailAddresses {
		if err := matchEmail(email, nc); err != nil {
			return err
		}
	}
	return nil
}

// VerifyPeerCertificate is a callback suitable for assignment to
// tls.Config.VerifyPeerCertificate.
//
// When the normal TLS path built verified chains (the common case where
// InsecureSkipVerify is false), every chain is evaluated and the callback
// succeeds if at least one chain satisfies all name constraints. When
// InsecureSkipVerify is set, rawCerts is parsed and treated as a single
// presented chain instead.
func VerifyPeerCertificate(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	if len(verifiedChains) > 0 {
		var errs []error
		for _, chain := range verifiedChains {
			if err := VerifyChain(chain); err == nil {
				return nil
			} else {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	if len(rawCerts) == 0 {
		return &ConstraintError{Reason: ReasonNoChain, Detail: "peer presented no certificates"}
	}
	chain := make([]*x509.Certificate, 0, len(rawCerts))
	for _, raw := range rawCerts {
		cert, err := parseCertificateAny(raw)
		if err != nil {
			return err
		}
		chain = append(chain, cert)
	}
	return VerifyChain(chain)
}
