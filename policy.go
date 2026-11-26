package hsm

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound             = errors.New("hsm: object not found")
	ErrDuplicateID          = errors.New("hsm: duplicate object identifier")
	ErrPermission           = errors.New("hsm: caller is not permitted")
	ErrPolicy               = errors.New("hsm: policy violation")
	ErrInvalidAttributes    = errors.New("hsm: invalid attribute vector")
	ErrMonotonicity         = errors.New("hsm: attribute monotonicity violation")
	ErrInvalidKey           = errors.New("hsm: invalid key")
	ErrUnsupportedMechanism = errors.New("hsm: unsupported mechanism")
	ErrCrypto               = errors.New("hsm: cryptographic operation failed")
	ErrNotExtractable       = errors.New("hsm: key is not extractable")
	ErrUntrustedWrapper     = errors.New("hsm: wrapping key is not trusted")
	ErrRoleConflict         = errors.New("hsm: conflicting cryptographic roles")
	ErrMechanismMismatch    = errors.New("hsm: mechanism does not match wrapped object")
	ErrTrustedPromotion     = errors.New("hsm: CKA_TRUSTED cannot be promoted through this API")
	ErrRolePromotion        = errors.New("hsm: cryptographic role mutation is disabled")
)

// Policy controls the security posture of the HSM emulation. Its zero value
// is deliberately strict. Every boolean enables a known non-conforming or
// legacy behaviour and exists so that attacks can be reproduced safely.
type Policy struct {
	// AllowWrapOfNonExtractable permits C_WrapKey for a key whose
	// CKA_EXTRACTABLE attribute is false.
	AllowWrapOfNonExtractable bool `json:"allow_wrap_of_non_extractable"`
	// AllowRoleOverlap permits a key to combine WRAP+DECRYPT or
	// UNWRAP+ENCRYPT. A strict policy rejects each use as well as each
	// transition that would create such a combination.
	AllowRoleOverlap bool `json:"allow_role_overlap"`
	// AllowRoleMutation permits SetAttributeValue, CopyObject, and unwrap
	// templates to add or remove cryptographic usage roles. Strict mode
	// keeps this vector immutable after import.
	AllowRoleMutation bool `json:"allow_role_mutation"`
	// AllowUntrustedWrapWithTrusted bypasses the requirement that a
	// CKA_WRAP_WITH_TRUSTED target be wrapped by a trusted key.
	AllowUntrustedWrapWithTrusted bool `json:"allow_untrusted_wrap_with_trusted"`
	// AllowUnwrapTemplateDowngrade lets an unwrap template make a wrapped
	// key less sensitive or more extractable than its source.
	AllowUnwrapTemplateDowngrade bool `json:"allow_unwrap_template_downgrade"`
	// AllowUntrustedPermissiveUnwrap permits an untrusted unwrapping key to
	// create an object with CKA_SENSITIVE=false or CKA_EXTRACTABLE=true.
	AllowUntrustedPermissiveUnwrap bool `json:"allow_untrusted_permissive_unwrap"`
	// AllowMechanismConfusion disables binding between a wrapped blob and
	// the mechanism used to create it. This models HSMs that dispatch by
	// key object instead of by an authenticated wrapping envelope.
	AllowMechanismConfusion bool `json:"allow_mechanism_confusion"`
	// AllowTrustedPromotion permits an application to set CKA_TRUSTED=true
	// through SetAttributeValue or CopyObject when it also holds the
	// CanSetTrusted permission.
	AllowTrustedPromotion bool `json:"allow_trusted_promotion"`
	// AllowDeriveMaterialCopy models a broken C_DeriveKey implementation
	// which returns the base key material rather than independently derived
	// material.
	AllowDeriveMaterialCopy bool `json:"allow_derive_material_copy"`
	// AllowPermissiveDerivation permits a derived key to be created with
	// CKA_SENSITIVE=false or CKA_EXTRACTABLE=true. Strict mode requires new
	// derived material to remain protected.
	AllowPermissiveDerivation bool `json:"allow_permissive_derivation"`
	// AllowDuplicateKeyMaterial permits two independently imported objects
	// with equal secret material but different policy vectors. Strict mode
	// rejects that role-splitting configuration.
	AllowDuplicateKeyMaterial bool `json:"allow_duplicate_key_material"`
}

// HardenedPolicy returns the strict, zero-value policy.
func HardenedPolicy() Policy { return Policy{} }

// Hardened is an alias for the zero-value hardened policy.
var Hardened = Policy{}

// InsecurePolicy enables every modelled legacy weakness. It is intended only
// for attack reproduction and regression tests.
func InsecurePolicy() Policy {
	return Policy{
		AllowWrapOfNonExtractable:      true,
		AllowRoleOverlap:               true,
		AllowRoleMutation:              true,
		AllowUntrustedWrapWithTrusted:  true,
		AllowUnwrapTemplateDowngrade:   true,
		AllowUntrustedPermissiveUnwrap: true,
		AllowMechanismConfusion:        true,
		AllowTrustedPromotion:          true,
		AllowDeriveMaterialCopy:        true,
		AllowPermissiveDerivation:      true,
		AllowDuplicateKeyMaterial:      true,
	}
}

// IsHardened reports whether p has no permissive compatibility switches on.
func (p Policy) IsHardened() bool { return p == Policy{} }

// ValidateAttributes applies attribute invariants and, for a strict policy,
// the WRAP/DECRYPT and UNWRAP/ENCRYPT separation rules.
func (p Policy) ValidateAttributes(a Attributes) error {
	if err := validateAttributeInvariants(a); err != nil {
		return err
	}
	return p.validateRoleOverlap(a)
}

// CheckOperation reports whether the usage-role vector permits op.
func (p Policy) CheckOperation(a Attributes, op Operation) error {
	return p.operationRoleAllowed(a, op)
}

// ValidateTransition checks monotonicity, history flags, role mutation, and
// role separation for an attribute update.
func (p Policy) ValidateTransition(old, next Attributes) error {
	if err := validateMonotonicTransition(old, next); err != nil {
		return err
	}
	if err := validateAttributeInvariants(next); err != nil {
		return err
	}
	if err := p.validateRoleMutation(old, next); err != nil {
		return err
	}
	return p.validateRoleOverlap(next)
}

// ApplyPatch applies patch to old and validates the resulting state under p.
// CKA_ALWAYS_SENSITIVE and CKA_NEVER_EXTRACTABLE are creation-time history
// flags and cannot be changed through this helper.
func (p Policy) ApplyPatch(old Attributes, patch AttributePatch) (Attributes, error) {
	next := patch.Apply(old)
	if patch.AlwaysSensitive != nil && next.AlwaysSensitive != old.AlwaysSensitive {
		return Attributes{}, fmt.Errorf("%w: CKA_ALWAYS_SENSITIVE is read-only", ErrMonotonicity)
	}
	if patch.NeverExtractable != nil && next.NeverExtractable != old.NeverExtractable {
		return Attributes{}, fmt.Errorf("%w: CKA_NEVER_EXTRACTABLE is read-only", ErrMonotonicity)
	}
	if err := p.ValidateTransition(old, next); err != nil {
		return Attributes{}, err
	}
	return next, nil
}

// String lists enabled compatibility switches.
func (p Policy) String() string {
	if p.IsHardened() {
		return "hardened"
	}
	var on []string
	if p.AllowWrapOfNonExtractable {
		on = append(on, "wrap-nonextractable")
	}
	if p.AllowRoleOverlap {
		on = append(on, "role-overlap")
	}
	if p.AllowRoleMutation {
		on = append(on, "role-mutation")
	}
	if p.AllowUntrustedWrapWithTrusted {
		on = append(on, "untrusted-wrap-with-trusted")
	}
	if p.AllowUnwrapTemplateDowngrade {
		on = append(on, "unwrap-template-downgrade")
	}
	if p.AllowUntrustedPermissiveUnwrap {
		on = append(on, "untrusted-permissive-unwrap")
	}
	if p.AllowMechanismConfusion {
		on = append(on, "mechanism-confusion")
	}
	if p.AllowTrustedPromotion {
		on = append(on, "trusted-promotion")
	}
	if p.AllowDeriveMaterialCopy {
		on = append(on, "derive-material-copy")
	}
	if p.AllowPermissiveDerivation {
		on = append(on, "permissive-derivation")
	}
	if p.AllowDuplicateKeyMaterial {
		on = append(on, "duplicate-material")
	}
	return "insecure(" + strings.Join(on, ",") + ")"
}

func (p Policy) validateRoleOverlap(a Attributes) error {
	if p.AllowRoleOverlap {
		return nil
	}
	if hasWrapDecryptConflict(a) {
		return fmt.Errorf("%w: CKA_WRAP and CKA_DECRYPT cannot both be true", ErrRoleConflict)
	}
	if hasUnwrapEncryptConflict(a) {
		return fmt.Errorf("%w: CKA_UNWRAP and CKA_ENCRYPT cannot both be true", ErrRoleConflict)
	}
	return nil
}

func (p Policy) operationRoleAllowed(a Attributes, op Operation) error {
	if p.AllowRoleOverlap {
		return nil
	}
	switch op {
	case OpWrapKey, OpDecrypt:
		if hasWrapDecryptConflict(a) {
			return fmt.Errorf("%w: key combines CKA_WRAP and CKA_DECRYPT", ErrRoleConflict)
		}
	case OpUnwrapKey, OpEncrypt:
		if hasUnwrapEncryptConflict(a) {
			return fmt.Errorf("%w: key combines CKA_UNWRAP and CKA_ENCRYPT", ErrRoleConflict)
		}
	}
	return nil
}

func (p Policy) validateRoleMutation(old, next Attributes) error {
	if p.AllowRoleMutation {
		return nil
	}
	if usageRolesChanged(old, next) {
		return fmt.Errorf("%w: usage roles %s -> %s", ErrRolePromotion,
			old.UsageRoles().shortString(), next.UsageRoles().shortString())
	}
	return nil
}

func (a Attributes) shortString() string {
	var roles []string
	if a.Encrypt {
		roles = append(roles, "encrypt")
	}
	if a.Decrypt {
		roles = append(roles, "decrypt")
	}
	if a.Wrap {
		roles = append(roles, "wrap")
	}
	if a.Unwrap {
		roles = append(roles, "unwrap")
	}
	if a.Derive {
		roles = append(roles, "derive")
	}
	if len(roles) == 0 {
		return "none"
	}
	return strings.Join(roles, "+")
}

// Operation names an API call exposed by the model.
type Operation string

const (
	OpWrapKey           Operation = "WrapKey"
	OpUnwrapKey         Operation = "UnwrapKey"
	OpEncrypt           Operation = "Encrypt"
	OpDecrypt           Operation = "Decrypt"
	OpDeriveKey         Operation = "DeriveKey"
	OpSetAttributeValue Operation = "SetAttributeValue"
	OpCopyObject        Operation = "CopyObject"
)

var allOperations = []Operation{
	OpWrapKey, OpUnwrapKey, OpEncrypt, OpDecrypt, OpDeriveKey,
	OpSetAttributeValue, OpCopyObject,
}

// APISet is a bit mask used to grant a subset of the modelled APIs.
type APISet uint16

const APINone APISet = 0

const (
	APIWrapKey APISet = 1 << iota
	APIUnwrapKey
	APIEncrypt
	APIDecrypt
	APIDeriveKey
	APISetAttributeValue
	APICopyObject
)

const APIAll = APIWrapKey | APIUnwrapKey | APIEncrypt | APIDecrypt |
	APIDeriveKey | APISetAttributeValue | APICopyObject

// Bit returns the permission bit for op.
func (op Operation) Bit() APISet {
	switch op {
	case OpWrapKey:
		return APIWrapKey
	case OpUnwrapKey:
		return APIUnwrapKey
	case OpEncrypt:
		return APIEncrypt
	case OpDecrypt:
		return APIDecrypt
	case OpDeriveKey:
		return APIDeriveKey
	case OpSetAttributeValue:
		return APISetAttributeValue
	case OpCopyObject:
		return APICopyObject
	default:
		return APINone
	}
}

// Has reports whether s includes op.
func (s APISet) Has(op Operation) bool { return s&op.Bit() != 0 }

// String returns a compact list of granted operations.
func (s APISet) String() string {
	if s == APINone {
		return "none"
	}
	if s == APIAll {
		return "all"
	}
	var parts []string
	for _, op := range allOperations {
		if s.Has(op) {
			parts = append(parts, string(op))
		}
	}
	return strings.Join(parts, "|")
}

// Permissions is the caller capability set used by the online adjudicator and
// the attack search. An empty KeyIDs slice means every key is addressable.
type Permissions struct {
	APIs          APISet `json:"apis"`
	KeyIDs        []ID   `json:"key_ids,omitempty"`
	CanSetTrusted bool   `json:"can_set_trusted,omitempty"`
}

// AllPermissions grants all APIs and all key objects.
func AllPermissions() Permissions { return Permissions{APIs: APIAll} }

// PermissionsFor grants a specific API subset on all keys.
func PermissionsFor(apis APISet) Permissions { return Permissions{APIs: apis} }

// WithTrustedSetter additionally grants the SO-only trusted promotion bit.
func (p Permissions) WithTrustedSetter(v bool) Permissions {
	p.CanSetTrusted = v
	return p
}

// Allows reports whether the caller may invoke op on keyID.
func (p Permissions) Allows(op Operation, keyID ID) bool {
	if !p.APIs.Has(op) {
		return false
	}
	if len(p.KeyIDs) == 0 {
		return true
	}
	for _, id := range p.KeyIDs {
		if id == keyID {
			return true
		}
	}
	return false
}

func (p Permissions) clone() Permissions {
	p.KeyIDs = append([]ID(nil), p.KeyIDs...)
	return p
}
