package netsec

import (
	"crypto/x509"
	"fmt"
)

// Policy states the Fulcio identity a caller is willing to accept.
//
// Every field with a non-empty expected value is mandatory: the certificate
// must carry the corresponding extension and its value must match exactly.
// Require can be used to demand presence of additional fields for which the
// caller does not constrain the value. If RequireTagRef is set, the source
// repository ref must name a tag under refs/tags/.
//
// An entirely empty Policy is rejected rather than treated as "accept
// anything".
type Policy struct {
	Issuer                 string
	BuildSignerURI         string
	SourceRepositoryURI    string
	SourceRepositoryDigest string
	SourceRepositoryRef    string
	BuildTrigger           string

	// Require lists fields that must be present even though no exact value is
	// required. Fields with non-empty expected values are required automatically.
	Require []Field

	// RequireTagRef rejects branch refs and any other non-tag ref.
	RequireTagRef bool
}

func (policy Policy) expected(field Field) string {
	switch field {
	case FieldIssuer:
		return policy.Issuer
	case FieldBuildSignerURI:
		return policy.BuildSignerURI
	case FieldSourceRepositoryURI:
		return policy.SourceRepositoryURI
	case FieldSourceRepositoryDigest:
		return policy.SourceRepositoryDigest
	case FieldSourceRepositoryRef:
		return policy.SourceRepositoryRef
	case FieldBuildTrigger:
		return policy.BuildTrigger
	default:
		return ""
	}
}

// VerifyIdentity extracts cert's Fulcio identity and checks it against policy.
// It returns the extracted identity on success.
func VerifyIdentity(cert *x509.Certificate, policy Policy) (Identity, error) {
	if err := policy.validate(); err != nil {
		return Identity{}, err
	}

	identity, err := ExtractIdentity(cert)
	if err != nil {
		return Identity{}, err
	}
	if err := policy.check(identity); err != nil {
		return identity, err
	}
	return identity, nil
}

func (policy Policy) validate() error {
	hasPresenceRequirement := false
	for _, field := range policy.Require {
		if !field.valid() {
			return fmt.Errorf("%w: unknown field %q", ErrInvalidPolicy, field)
		}
		hasPresenceRequirement = true
	}

	hasValueRequirement := false
	for _, field := range allFields() {
		expected := policy.expected(field)
		if expected == "" {
			continue
		}
		hasValueRequirement = true
		if field == FieldSourceRepositoryRef && policy.RequireTagRef && !IsTagRef(expected) {
			return fmt.Errorf(
				"%w: policy requires a tag but expects ref %q",
				ErrInvalidPolicy, expected,
			)
		}
	}
	if !hasValueRequirement && !hasPresenceRequirement && !policy.RequireTagRef {
		return ErrEmptyPolicy
	}
	return nil
}

func (policy Policy) check(identity Identity) error {
	for _, field := range allFields() {
		expected := policy.expected(field)
		actual := identity.value(field)

		if expected == "" {
			if policy.requiresPresence(field) && actual == "" {
				return fmt.Errorf("%w: %s", ErrMissingExtension, field)
			}
			continue
		}
		if actual == "" {
			return fmt.Errorf("%w: %s", ErrMissingExtension, field)
		}
		if actual != expected {
			return fmt.Errorf(
				"%w: %s expected %q, certificate has %q",
				ErrPolicyMismatch, field, expected, actual,
			)
		}
	}

	if policy.RequireTagRef && !IsTagRef(identity.SourceRepositoryRef) {
		if identity.SourceRepositoryRef == "" {
			return fmt.Errorf("%w: %s", ErrMissingExtension, FieldSourceRepositoryRef)
		}
		return fmt.Errorf(
			"%w: %s is %q",
			ErrRefNotTag, FieldSourceRepositoryRef, identity.SourceRepositoryRef,
		)
	}

	return nil
}

func (policy Policy) requiresPresence(field Field) bool {
	for _, required := range policy.Require {
		if required == field {
			return true
		}
	}
	return false
}

func allFields() []Field {
	return []Field{
		FieldIssuer,
		FieldBuildSignerURI,
		FieldSourceRepositoryURI,
		FieldSourceRepositoryDigest,
		FieldSourceRepositoryRef,
		FieldBuildTrigger,
	}
}
