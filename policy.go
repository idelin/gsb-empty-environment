package netsec

import (
	"errors"
	"fmt"
)

// Policy violations. Validation errors wrap one of these sentinels, so callers
// can use errors.Is to identify the rejected policy rule.
var (
	ErrWrapDecryptConflict     = errors.New("netsec: a key cannot combine CKA_WRAP with CKA_DECRYPT")
	ErrUnwrapEncryptConflict   = errors.New("netsec: a key cannot combine CKA_UNWRAP with CKA_ENCRYPT")
	ErrSensitiveIrreversible   = errors.New("netsec: CKA_SENSITIVE cannot be changed from true to false")
	ErrExtractableIrreversible = errors.New("netsec: CKA_EXTRACTABLE cannot be changed from false to true")
	ErrUntrustedWrappingKey    = errors.New("netsec: a wrapping key must be trusted when the wrapped key has CKA_WRAP_WITH_TRUSTED")
	ErrInvalidAttributeValue   = errors.New("netsec: invalid PKCS #11 attribute value")
	ErrDuplicateAttribute      = errors.New("netsec: duplicate attribute with conflicting values")
	ErrAttributeTypeMismatch   = errors.New("netsec: attribute type mismatch")
)

// Compatibility aliases for callers that use alternate names.
var (
	ErrSensitiveDowngrade    = ErrSensitiveIrreversible
	ErrExtractableUpgrade    = ErrExtractableIrreversible
	ErrWrappingKeyNotTrusted = ErrUntrustedWrappingKey
)

func policyError(kind error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", kind, fmt.Sprintf(format, args...))
}

func validateState(set attributeSet) error {
	if err := validateKnownBooleans(set); err != nil {
		return err
	}

	wrap, err := booleanValue(set, CKA_WRAP)
	if err != nil {
		return err
	}
	decrypt, err := booleanValue(set, CKA_DECRYPT)
	if err != nil {
		return err
	}
	if wrap && decrypt {
		return policyError(ErrWrapDecryptConflict,
			"CKA_WRAP and CKA_DECRYPT are both true")
	}

	unwrap, err := booleanValue(set, CKA_UNWRAP)
	if err != nil {
		return err
	}
	encrypt, err := booleanValue(set, CKA_ENCRYPT)
	if err != nil {
		return err
	}
	if unwrap && encrypt {
		return policyError(ErrUnwrapEncryptConflict,
			"CKA_UNWRAP and CKA_ENCRYPT are both true")
	}
	return nil
}

// ValidateAttributes checks the security-sensitive state of one key.
//
// The same key may not enable both wrapping and decryption, nor both unwrapping
// and encryption. Missing boolean attributes have the PKCS #11 default value
// false.
func ValidateAttributes(attrs []Attribute) error {
	set, err := collectAttributes(attrs)
	if err != nil {
		return err
	}
	return validateState(set)
}

// Validate is an alias for ValidateAttributes.
func Validate(attrs []Attribute) error { return ValidateAttributes(attrs) }

// ValidateKeyAttributes is an alias for ValidateAttributes.
func ValidateKeyAttributes(attrs []Attribute) error { return ValidateAttributes(attrs) }

// ValidateCreate checks a key's initial attribute set.
func ValidateCreate(attrs []Attribute) error { return ValidateAttributes(attrs) }

// CheckAttributes is an alias for ValidateAttributes.
func CheckAttributes(attrs []Attribute) error { return ValidateAttributes(attrs) }

// ValidateTransition checks a full replacement of oldAttrs with newAttrs.
//
// newAttrs is a complete attribute snapshot: a missing CKA_SENSITIVE or
// CKA_EXTRACTABLE is treated as its default value false. A nil oldAttrs means
// the key is being created, so previous-state restrictions do not apply.
func ValidateTransition(oldAttrs, newAttrs []Attribute) error {
	newSet, err := collectAttributes(newAttrs)
	if err != nil {
		return err
	}
	if oldAttrs == nil {
		return validateState(newSet)
	}
	oldSet, err := collectAttributes(oldAttrs)
	if err != nil {
		return err
	}

	oldSensitive, err := booleanValue(oldSet, CKA_SENSITIVE)
	if err != nil {
		return err
	}
	newSensitive, err := booleanValue(newSet, CKA_SENSITIVE)
	if err != nil {
		return err
	}
	if oldSensitive && !newSensitive {
		return policyError(ErrSensitiveIrreversible,
			"CKA_SENSITIVE was true and the new state sets it to false or omits it")
	}

	oldExtractable, err := booleanValue(oldSet, CKA_EXTRACTABLE)
	if err != nil {
		return err
	}
	newExtractable, err := booleanValue(newSet, CKA_EXTRACTABLE)
	if err != nil {
		return err
	}
	if !oldExtractable && newExtractable {
		return policyError(ErrExtractableIrreversible,
			"CKA_EXTRACTABLE was false and the new state sets it to true")
	}

	return validateState(newSet)
}

// ValidateUpdate checks an incremental attribute update.
//
// Only attributes present in changes are modified; omitted attributes retain
// their old values. A nil oldAttrs means the key is being created.
func ValidateUpdate(oldAttrs, changes []Attribute) error {
	changeSet, err := collectAttributes(changes)
	if err != nil {
		return err
	}
	if oldAttrs == nil {
		return validateState(changeSet)
	}
	oldSet, err := collectAttributes(oldAttrs)
	if err != nil {
		return err
	}

	if value, exists := changeSet[CKA_SENSITIVE]; exists {
		oldSensitive, err := booleanValue(oldSet, CKA_SENSITIVE)
		if err != nil {
			return err
		}
		newSensitive, err := decodeBool(CKA_SENSITIVE, value)
		if err != nil {
			return err
		}
		if oldSensitive && !newSensitive {
			return policyError(ErrSensitiveIrreversible,
				"CKA_SENSITIVE cannot be changed from true to false")
		}
	}

	if value, exists := changeSet[CKA_EXTRACTABLE]; exists {
		oldExtractable, err := booleanValue(oldSet, CKA_EXTRACTABLE)
		if err != nil {
			return err
		}
		newExtractable, err := decodeBool(CKA_EXTRACTABLE, value)
		if err != nil {
			return err
		}
		if !oldExtractable && newExtractable {
			return policyError(ErrExtractableIrreversible,
				"CKA_EXTRACTABLE cannot be changed from false to true")
		}
	}

	effective := make(attributeSet, len(oldSet)+len(changeSet))
	for attributeType, value := range oldSet {
		effective[attributeType] = value
	}
	for attributeType, value := range changeSet {
		effective[attributeType] = value
	}
	return validateState(effective)
}

// ValidateModification is an alias for ValidateUpdate.
func ValidateModification(oldAttrs, changes []Attribute) error {
	return ValidateUpdate(oldAttrs, changes)
}

// ValidateSetAttribute checks setting one attribute on an existing key.
func ValidateSetAttribute(oldAttrs []Attribute, attr Attribute) error {
	return ValidateUpdate(oldAttrs, []Attribute{attr})
}

// ValidateAttributeTransition checks a transition of one explicit attribute
// value. It cannot check cross-attribute conflicts because it receives only
// one attribute at a time; use ValidateUpdate or ValidateTransition for that.
func ValidateAttributeTransition(oldAttr, newAttr Attribute) error {
	if oldAttr.Type != newAttr.Type {
		return policyError(ErrAttributeTypeMismatch,
			"cannot change attribute 0x%x into attribute 0x%x", oldAttr.Type, newAttr.Type)
	}
	switch newAttr.Type {
	case CKA_SENSITIVE:
		oldValue, err := decodeBool(oldAttr.Type, oldAttr.Value)
		if err != nil {
			return err
		}
		newValue, err := decodeBool(newAttr.Type, newAttr.Value)
		if err != nil {
			return err
		}
		if oldValue && !newValue {
			return policyError(ErrSensitiveIrreversible,
				"CKA_SENSITIVE cannot be changed from true to false")
		}
	case CKA_EXTRACTABLE:
		oldValue, err := decodeBool(oldAttr.Type, oldAttr.Value)
		if err != nil {
			return err
		}
		newValue, err := decodeBool(newAttr.Type, newAttr.Value)
		if err != nil {
			return err
		}
		if !oldValue && newValue {
			return policyError(ErrExtractableIrreversible,
				"CKA_EXTRACTABLE cannot be changed from false to true")
		}
	default:
		for _, attributeType := range knownBooleanAttributes {
			if newAttr.Type == attributeType {
				if _, err := decodeBool(newAttr.Type, newAttr.Value); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// ValidateBooleanTransition checks one boolean attribute transition.
func ValidateBooleanTransition(attributeType AttributeType, oldValue, newValue bool) error {
	switch attributeType {
	case CKA_SENSITIVE:
		if oldValue && !newValue {
			return policyError(ErrSensitiveIrreversible,
				"CKA_SENSITIVE cannot be changed from true to false")
		}
	case CKA_EXTRACTABLE:
		if !oldValue && newValue {
			return policyError(ErrExtractableIrreversible,
				"CKA_EXTRACTABLE cannot be changed from false to true")
		}
	}
	return nil
}

// ValidateWrap checks that wrapping a key does not violate the wrapped key's
// CKA_WRAP_WITH_TRUSTED restriction.
//
// The usual argument order is wrappingKey, wrappedKey. Because PKCS #11
// attribute snapshots alone do not carry argument directions, the roles are
// inferred from CKA_WRAP when exactly one key has it enabled, and otherwise
// from CKA_WRAP_WITH_TRUSTED. Passing the arguments in the opposite order is
// therefore also safe.
func ValidateWrap(wrappingKey, wrappedKey []Attribute) error {
	wrappingSet, err := collectAttributes(wrappingKey)
	if err != nil {
		return err
	}
	wrappedSet, err := collectAttributes(wrappedKey)
	if err != nil {
		return err
	}

	wrappingWrap, err := booleanValue(wrappingSet, CKA_WRAP)
	if err != nil {
		return err
	}
	wrappedWrap, err := booleanValue(wrappedSet, CKA_WRAP)
	if err != nil {
		return err
	}
	if !wrappingWrap && wrappedWrap {
		wrappingSet, wrappedSet = wrappedSet, wrappingSet
	} else if !wrappingWrap && !wrappedWrap {
		firstRestricted, err := booleanValue(wrappingSet, CKA_WRAP_WITH_TRUSTED)
		if err != nil {
			return err
		}
		secondRestricted, err := booleanValue(wrappedSet, CKA_WRAP_WITH_TRUSTED)
		if err != nil {
			return err
		}
		if firstRestricted && !secondRestricted {
			wrappingSet, wrappedSet = wrappedSet, wrappingSet
		}
	}

	if err := validateState(wrappingSet); err != nil {
		return err
	}
	if err := validateState(wrappedSet); err != nil {
		return err
	}

	wrapWithTrusted, err := booleanValue(wrappedSet, CKA_WRAP_WITH_TRUSTED)
	if err != nil {
		return err
	}
	if !wrapWithTrusted {
		return nil
	}
	trusted, err := booleanValue(wrappingSet, CKA_TRUSTED)
	if err != nil {
		return err
	}
	if !trusted {
		return policyError(ErrUntrustedWrappingKey,
			"wrapped key requires CKA_TRUSTED on the wrapping key")
	}
	return nil
}

// ValidateWrapTrust is an alias for ValidateWrap. Its argument names follow
// the wrapped-key-first wording of the policy rule; role inference is the
// same, so either argument order is accepted.
func ValidateWrapTrust(wrappedKey, wrappingKey []Attribute) error {
	return ValidateWrap(wrappingKey, wrappedKey)
}

// ValidateWrappingKey is an alias for ValidateWrap.
func ValidateWrappingKey(wrappingKey, wrappedKey []Attribute) error {
	return ValidateWrap(wrappingKey, wrappedKey)
}

// CheckWrap is an alias for ValidateWrap.
func CheckWrap(wrappingKey, wrappedKey []Attribute) error {
	return ValidateWrap(wrappingKey, wrappedKey)
}

// Policy is a stateless policy object for callers that prefer methods.
type Policy struct{}

// DefaultPolicy is a ready-to-use stateless policy.
var DefaultPolicy Policy

// NewPolicy returns a stateless policy.
func NewPolicy() *Policy { return &Policy{} }

func (Policy) ValidateAttributes(attrs []Attribute) error { return ValidateAttributes(attrs) }
func (Policy) ValidateCreate(attrs []Attribute) error     { return ValidateCreate(attrs) }
func (Policy) ValidateTransition(oldAttrs, newAttrs []Attribute) error {
	return ValidateTransition(oldAttrs, newAttrs)
}
func (Policy) ValidateUpdate(oldAttrs, changes []Attribute) error {
	return ValidateUpdate(oldAttrs, changes)
}
func (Policy) ValidateWrap(wrappingKey, wrappedKey []Attribute) error {
	return ValidateWrap(wrappingKey, wrappedKey)
}
