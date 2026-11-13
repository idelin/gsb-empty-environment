package netsec

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
)

var (
	// ErrEmptyChain is returned when a certificate path contains no certificates.
	ErrEmptyChain = errors.New("netsec: empty certificate chain")
	// ErrInvalidPolicy is returned when a chain does not satisfy policy validation.
	ErrInvalidPolicy = errors.New("netsec: certificate path policy validation failed")
)

// EvaluatePolicies runs policy validation on a certification path and returns
// the resulting valid policy sets. The path may be supplied leaf first (the
// crypto/x509 convention) or trust-anchor first.
func EvaluatePolicies(chain []*Certificate, opts ...VerifyOptions) Evaluation {
	return evaluatePolicies(normalizeChain(chain), firstOptions(opts))
}

// Evaluate is an alias for EvaluatePolicies.
func Evaluate(chain []*Certificate, opts ...VerifyOptions) Evaluation {
	return EvaluatePolicies(chain, opts...)
}

// PoliciesValid reports whether the certification path satisfies RFC 5280 and
// RFC 9618 policy processing. The path may be supplied leaf first or trust-anchor
// first.
func PoliciesValid(chain []*Certificate, opts ...VerifyOptions) bool {
	return EvaluatePolicies(chain, opts...).Valid
}

// ValidatePolicies is an alias for PoliciesValid.
func ValidatePolicies(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// VerifyPolicies is an alias for PoliciesValid.
func VerifyPolicies(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// ValidatePolicyChain is an alias for PoliciesValid.
func ValidatePolicyChain(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// VerifyPolicyChain is an alias for PoliciesValid.
func VerifyPolicyChain(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// CheckPolicies is an alias for PoliciesValid.
func CheckPolicies(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// ValidateCertificatePolicies is an alias for PoliciesValid.
func ValidateCertificatePolicies(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// VerifyCertificatePath is an alias for PoliciesValid.
func VerifyCertificatePath(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// ValidateCertificatePath is an alias for PoliciesValid.
func ValidateCertificatePath(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// CertificatePathValid is an alias for PoliciesValid.
func CertificatePathValid(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// VerifyCertificateChain is an alias for PoliciesValid.
func VerifyCertificateChain(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// ValidateCertificateChain is an alias for PoliciesValid.
func ValidateCertificateChain(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// CertificateChainValid is an alias for PoliciesValid.
func CertificateChainValid(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// VerifyCertificationPath is an alias for PoliciesValid.
func VerifyCertificationPath(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// ValidateCertificationPath is an alias for PoliciesValid.
func ValidateCertificationPath(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// CertificationPathValid is an alias for PoliciesValid.
func CertificationPathValid(chain []*Certificate, opts ...VerifyOptions) bool {
	return PoliciesValid(chain, opts...)
}

// Validate returns nil when the certification path satisfies policy validation.
func Validate(chain []*Certificate, opts ...VerifyOptions) error {
	if len(chain) == 0 {
		return ErrEmptyChain
	}
	if !PoliciesValid(chain, opts...) {
		return ErrInvalidPolicy
	}
	return nil
}

// Verify is an alias for Validate.
func Verify(chain []*Certificate, opts ...VerifyOptions) error {
	return Validate(chain, opts...)
}

// ValidateChain is an alias for Validate.
func ValidateChain(chain []*Certificate, opts ...VerifyOptions) error {
	return Validate(chain, opts...)
}

// VerifyChain is an alias for Validate.
func VerifyChain(chain []*Certificate, opts ...VerifyOptions) error {
	return Validate(chain, opts...)
}

// PolicySet returns the user-constrained valid policy set for the path.
func PolicySet(chain []*Certificate, opts ...VerifyOptions) ([]OID, error) {
	if len(chain) == 0 {
		return nil, ErrEmptyChain
	}
	result := EvaluatePolicies(chain, opts...)
	if !result.Valid {
		return nil, ErrInvalidPolicy
	}
	return result.Policies, nil
}

// ValidPolicies is an alias for PolicySet.
func ValidPolicies(chain []*Certificate, opts ...VerifyOptions) ([]OID, error) {
	return PolicySet(chain, opts...)
}

// PolicyVerifier holds the policy-validation options used for repeated path
// checks.
type PolicyVerifier struct {
	Options VerifyOptions
}

// NewPolicyVerifier creates a reusable policy verifier.
func NewPolicyVerifier(opts ...VerifyOptions) *PolicyVerifier {
	return &PolicyVerifier{Options: firstOptions(opts)}
}

// Valid reports whether chain satisfies policy processing.
func (v *PolicyVerifier) Valid(chain []*Certificate) bool {
	return PoliciesValid(chain, v.Options)
}

// Validate returns nil when chain satisfies policy processing.
func (v *PolicyVerifier) Validate(chain []*Certificate) error {
	return Validate(chain, v.Options)
}

// Evaluate returns the policy sets produced for chain.
func (v *PolicyVerifier) Evaluate(chain []*Certificate) Evaluation {
	return EvaluatePolicies(chain, v.Options)
}

// ParseCertificate parses a single DER-encoded certificate. It mirrors the
// crypto/x509 function of the same name for policy-path callers.
func ParseCertificate(der []byte) (*Certificate, error) {
	return parseCertificate(der)
}

// ParseCertificateChainPEM parses one or more PEM certificate blocks. As a
// convenience, a bare DER certificate is also accepted.
func ParseCertificateChainPEM(data []byte) ([]*Certificate, error) {
	var certs []*Certificate
	rest := data
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remaining
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := parseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	if len(certs) != 0 {
		return certs, nil
	}
	if len(data) == 0 {
		return nil, ErrEmptyChain
	}
	cert, err := parseCertificate(data)
	if err != nil {
		return nil, err
	}
	return []*Certificate{cert}, nil
}

// ValidatePEMChain parses a PEM (or DER) certification path and validates its
// policies.
func ValidatePEMChain(data []byte, opts ...VerifyOptions) (bool, error) {
	chain, err := ParseCertificateChainPEM(data)
	if err != nil {
		return false, err
	}
	return PoliciesValid(chain, opts...), nil
}

func firstOptions(opts []VerifyOptions) VerifyOptions {
	if len(opts) == 0 {
		return VerifyOptions{}
	}
	return opts[0]
}

func parseCertificate(der []byte) (*Certificate, error) {
	return x509.ParseCertificate(der)
}

func normalizeChain(chain []*Certificate) []*Certificate {
	if len(chain) < 2 {
		return chain
	}

	rootFirst := chainLink(chain[0], chain[1])
	leafFirst := chainLink(chain[1], chain[0])
	if rootFirst && !leafFirst {
		reversed := make([]*Certificate, len(chain))
		for i := range chain {
			reversed[len(chain)-1-i] = chain[i]
		}
		return reversed
	}
	return chain
}

// chainLink reports whether parent issued child.
func chainLink(parent, child *Certificate) bool {
	if len(parent.RawSubject) != 0 && len(child.RawIssuer) != 0 {
		return bytes.Equal(parent.RawSubject, child.RawIssuer)
	}
	return parent.Subject.String() == child.Issuer.String()
}
