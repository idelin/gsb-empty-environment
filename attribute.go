package hsm

import (
	"fmt"
	"strings"
)

// Attributes is the complete PKCS#11 attribute vector used by this package.
// The fields intentionally have the same names as the CKA_* attributes.
type Attributes struct {
	Sensitive        bool `json:"CKA_SENSITIVE"`
	Extractable      bool `json:"CKA_EXTRACTABLE"`
	Encrypt          bool `json:"CKA_ENCRYPT"`
	Decrypt          bool `json:"CKA_DECRYPT"`
	Wrap             bool `json:"CKA_WRAP"`
	Unwrap           bool `json:"CKA_UNWRAP"`
	Derive           bool `json:"CKA_DERIVE"`
	Trusted          bool `json:"CKA_TRUSTED"`
	WrapWithTrusted  bool `json:"CKA_WRAP_WITH_TRUSTED"`
	AlwaysSensitive  bool `json:"CKA_ALWAYS_SENSITIVE"`
	NeverExtractable bool `json:"CKA_NEVER_EXTRACTABLE"`
}

// AttributePatch changes a subset of an attribute vector. A nil field is
// omitted, matching the PKCS#11 template semantics.
type AttributePatch struct {
	Sensitive        *bool `json:"CKA_SENSITIVE,omitempty"`
	Extractable      *bool `json:"CKA_EXTRACTABLE,omitempty"`
	Encrypt          *bool `json:"CKA_ENCRYPT,omitempty"`
	Decrypt          *bool `json:"CKA_DECRYPT,omitempty"`
	Wrap             *bool `json:"CKA_WRAP,omitempty"`
	Unwrap           *bool `json:"CKA_UNWRAP,omitempty"`
	Derive           *bool `json:"CKA_DERIVE,omitempty"`
	Trusted          *bool `json:"CKA_TRUSTED,omitempty"`
	WrapWithTrusted  *bool `json:"CKA_WRAP_WITH_TRUSTED,omitempty"`
	AlwaysSensitive  *bool `json:"CKA_ALWAYS_SENSITIVE,omitempty"`
	NeverExtractable *bool `json:"CKA_NEVER_EXTRACTABLE,omitempty"`
}

// BoolPtr is a convenience helper for constructing an AttributePatch.
func BoolPtr(v bool) *bool { return &v }

// Apply returns a with every non-nil patch field applied.
func (p AttributePatch) Apply(a Attributes) Attributes {
	if p.Sensitive != nil {
		a.Sensitive = *p.Sensitive
	}
	if p.Extractable != nil {
		a.Extractable = *p.Extractable
	}
	if p.Encrypt != nil {
		a.Encrypt = *p.Encrypt
	}
	if p.Decrypt != nil {
		a.Decrypt = *p.Decrypt
	}
	if p.Wrap != nil {
		a.Wrap = *p.Wrap
	}
	if p.Unwrap != nil {
		a.Unwrap = *p.Unwrap
	}
	if p.Derive != nil {
		a.Derive = *p.Derive
	}
	if p.Trusted != nil {
		a.Trusted = *p.Trusted
	}
	if p.WrapWithTrusted != nil {
		a.WrapWithTrusted = *p.WrapWithTrusted
	}
	if p.AlwaysSensitive != nil {
		a.AlwaysSensitive = *p.AlwaysSensitive
	}
	if p.NeverExtractable != nil {
		a.NeverExtractable = *p.NeverExtractable
	}
	return a
}

// IsZero reports whether the patch contains no changes.
func (p AttributePatch) IsZero() bool {
	return p.Sensitive == nil &&
		p.Extractable == nil &&
		p.Encrypt == nil &&
		p.Decrypt == nil &&
		p.Wrap == nil &&
		p.Unwrap == nil &&
		p.Derive == nil &&
		p.Trusted == nil &&
		p.WrapWithTrusted == nil &&
		p.AlwaysSensitive == nil &&
		p.NeverExtractable == nil
}

// FullPatch returns an AttributePatch that applies every field of a.
func FullPatch(a Attributes) AttributePatch {
	return AttributePatch{
		Sensitive:        BoolPtr(a.Sensitive),
		Extractable:      BoolPtr(a.Extractable),
		Encrypt:          BoolPtr(a.Encrypt),
		Decrypt:          BoolPtr(a.Decrypt),
		Wrap:             BoolPtr(a.Wrap),
		Unwrap:           BoolPtr(a.Unwrap),
		Derive:           BoolPtr(a.Derive),
		Trusted:          BoolPtr(a.Trusted),
		WrapWithTrusted:  BoolPtr(a.WrapWithTrusted),
		AlwaysSensitive:  BoolPtr(a.AlwaysSensitive),
		NeverExtractable: BoolPtr(a.NeverExtractable),
	}
}

// Equal compares every attribute in the vector.
func (a Attributes) Equal(b Attributes) bool { return a == b }

// Validate checks invariants that must hold for every stored attribute
// vector, independently of a particular policy profile.
func (a Attributes) Validate() error { return validateAttributeInvariants(a) }

// String returns a stable representation suitable for diffs and audit logs.
func (a Attributes) String() string {
	values := []struct {
		name string
		on   bool
	}{
		{"CKA_SENSITIVE", a.Sensitive},
		{"CKA_EXTRACTABLE", a.Extractable},
		{"CKA_ENCRYPT", a.Encrypt},
		{"CKA_DECRYPT", a.Decrypt},
		{"CKA_WRAP", a.Wrap},
		{"CKA_UNWRAP", a.Unwrap},
		{"CKA_DERIVE", a.Derive},
		{"CKA_TRUSTED", a.Trusted},
		{"CKA_WRAP_WITH_TRUSTED", a.WrapWithTrusted},
		{"CKA_ALWAYS_SENSITIVE", a.AlwaysSensitive},
		{"CKA_NEVER_EXTRACTABLE", a.NeverExtractable},
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%s=%t", v.name, v.on))
	}
	return strings.Join(parts, ",")
}

// UsageRoles returns only the attributes that grant cryptographic use.
func (a Attributes) UsageRoles() Attributes {
	return Attributes{
		Encrypt: a.Encrypt,
		Decrypt: a.Decrypt,
		Wrap:    a.Wrap,
		Unwrap:  a.Unwrap,
		Derive:  a.Derive,
	}
}

func validateAttributeInvariants(a Attributes) error {
	if a.AlwaysSensitive && !a.Sensitive {
		return fmt.Errorf("%w: CKA_ALWAYS_SENSITIVE=true requires CKA_SENSITIVE=true", ErrInvalidAttributes)
	}
	if a.NeverExtractable && a.Extractable {
		return fmt.Errorf("%w: CKA_NEVER_EXTRACTABLE=true contradicts CKA_EXTRACTABLE=true", ErrInvalidAttributes)
	}
	return nil
}

func validateMonotonicTransition(old, next Attributes) error {
	if old.Sensitive && !next.Sensitive {
		return fmt.Errorf("%w: CKA_SENSITIVE cannot change from true to false", ErrMonotonicity)
	}
	if !old.Extractable && next.Extractable {
		return fmt.Errorf("%w: CKA_EXTRACTABLE cannot change from false to true", ErrMonotonicity)
	}
	if old.AlwaysSensitive && !next.AlwaysSensitive {
		return fmt.Errorf("%w: CKA_ALWAYS_SENSITIVE is a read-only history flag", ErrMonotonicity)
	}
	if old.NeverExtractable && !next.NeverExtractable {
		return fmt.Errorf("%w: CKA_NEVER_EXTRACTABLE is a read-only history flag", ErrMonotonicity)
	}
	if old.WrapWithTrusted && !next.WrapWithTrusted {
		return fmt.Errorf("%w: CKA_WRAP_WITH_TRUSTED cannot be cleared once set", ErrMonotonicity)
	}
	return nil
}

func usageRolesChanged(old, next Attributes) bool {
	return old.UsageRoles() != next.UsageRoles()
}

func hasWrapDecryptConflict(a Attributes) bool { return a.Wrap && a.Decrypt }

func hasUnwrapEncryptConflict(a Attributes) bool { return a.Unwrap && a.Encrypt }
