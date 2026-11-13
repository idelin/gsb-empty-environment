package netsec_test

import (
	"errors"
	"testing"

	"example.org/netsec/q0839"
)

func attrs(items ...netsec.Attribute) []netsec.Attribute {
	return items
}

func boolAttr(attributeType uint, value bool) netsec.Attribute {
	return netsec.Attribute{Type: attributeType, Value: value}
}

func TestValidateAttributesRejectsDangerousCombinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		attrs []netsec.Attribute
		want  error
	}{
		{
			name: "wrap and decrypt",
			attrs: attrs(
				boolAttr(netsec.CKA_WRAP, true),
				boolAttr(netsec.CKA_DECRYPT, true),
			),
			want: netsec.ErrWrapDecryptConflict,
		},
		{
			name: "unwrap and encrypt",
			attrs: attrs(
				boolAttr(netsec.CKA_UNWRAP, true),
				boolAttr(netsec.CKA_ENCRYPT, true),
			),
			want: netsec.ErrUnwrapEncryptConflict,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := netsec.ValidateAttributes(test.attrs)
			if !errors.Is(err, test.want) {
				t.Fatalf("ValidateAttributes() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateAttributesAllowsSafeCapabilities(t *testing.T) {
	t.Parallel()

	safe := attrs(
		boolAttr(netsec.CKA_WRAP, true),
		boolAttr(netsec.CKA_DECRYPT, false),
		boolAttr(netsec.CKA_UNWRAP, true),
		boolAttr(netsec.CKA_ENCRYPT, false),
		boolAttr(netsec.CKA_SENSITIVE, true),
		boolAttr(netsec.CKA_EXTRACTABLE, false),
	)
	if err := netsec.ValidateAttributes(safe); err != nil {
		t.Fatalf("ValidateAttributes() = %v, want nil", err)
	}
}

func TestSensitiveCannotBeDowngraded(t *testing.T) {
	t.Parallel()

	oldAttrs := attrs(boolAttr(netsec.CKA_SENSITIVE, true))
	for _, newValue := range []any{false, 0, []byte{0}} {
		err := netsec.ValidateUpdate(oldAttrs, attrs(netsec.Attribute{
			Type:  netsec.CKA_SENSITIVE,
			Value: newValue,
		}))
		if !errors.Is(err, netsec.ErrSensitiveIrreversible) {
			t.Errorf("ValidateUpdate(value %#v) error = %v, want %v",
				newValue, err, netsec.ErrSensitiveIrreversible)
		}
	}

	err := netsec.ValidateUpdate(oldAttrs, attrs(netsec.Attribute{
		Type:  netsec.CKA_SENSITIVE,
		Value: false,
	}))
	if !errors.Is(err, netsec.ErrSensitiveIrreversible) {
		t.Fatalf("ValidateUpdate() error = %v, want %v", err, netsec.ErrSensitiveIrreversible)
	}

	if err := netsec.ValidateTransition(oldAttrs, attrs(boolAttr(netsec.CKA_SENSITIVE, true))); err != nil {
		t.Fatalf("true to true transition rejected: %v", err)
	}
}

func TestSensitiveTransitionOmissionIsDowngrade(t *testing.T) {
	t.Parallel()

	err := netsec.ValidateTransition(
		attrs(boolAttr(netsec.CKA_SENSITIVE, true)),
		attrs(boolAttr(netsec.CKA_WRAP, true)),
	)
	if !errors.Is(err, netsec.ErrSensitiveIrreversible) {
		t.Fatalf("ValidateTransition() error = %v, want %v", err, netsec.ErrSensitiveIrreversible)
	}
}

func TestExtractableCannotBeUpgraded(t *testing.T) {
	t.Parallel()

	oldAttrs := attrs(boolAttr(netsec.CKA_EXTRACTABLE, false))
	err := netsec.ValidateUpdate(oldAttrs, attrs(boolAttr(netsec.CKA_EXTRACTABLE, true)))
	if !errors.Is(err, netsec.ErrExtractableIrreversible) {
		t.Fatalf("ValidateUpdate() error = %v, want %v", err, netsec.ErrExtractableIrreversible)
	}

	if err := netsec.ValidateUpdate(oldAttrs, attrs(boolAttr(netsec.CKA_EXTRACTABLE, false))); err != nil {
		t.Fatalf("false to false transition rejected: %v", err)
	}

	if err := netsec.ValidateUpdate(oldAttrs, nil); err != nil {
		t.Fatalf("nil update rejected: %v", err)
	}
}

func TestUpdateChecksConflictAgainstExistingAttributes(t *testing.T) {
	t.Parallel()

	oldAttrs := attrs(boolAttr(netsec.CKA_DECRYPT, true))
	err := netsec.ValidateUpdate(oldAttrs, attrs(boolAttr(netsec.CKA_WRAP, true)))
	if !errors.Is(err, netsec.ErrWrapDecryptConflict) {
		t.Fatalf("ValidateUpdate() error = %v, want %v", err, netsec.ErrWrapDecryptConflict)
	}

	if err := netsec.ValidateUpdate(oldAttrs, attrs(boolAttr(netsec.CKA_WRAP, false))); err != nil {
		t.Fatalf("non-conflicting update rejected: %v", err)
	}
}

func TestWrapWithTrustedRequiresTrustedWrappingKey(t *testing.T) {
	t.Parallel()

	wrappingKey := attrs(boolAttr(netsec.CKA_WRAP, true))
	wrappedKey := attrs(boolAttr(netsec.CKA_WRAP_WITH_TRUSTED, true))

	err := netsec.ValidateWrap(wrappingKey, wrappedKey)
	if !errors.Is(err, netsec.ErrUntrustedWrappingKey) {
		t.Fatalf("ValidateWrap() error = %v, want %v", err, netsec.ErrUntrustedWrappingKey)
	}

	trustedWrappingKey := append(wrappingKey, boolAttr(netsec.CKA_TRUSTED, true))
	if err := netsec.ValidateWrap(trustedWrappingKey, wrappedKey); err != nil {
		t.Fatalf("trusted wrapping key rejected: %v", err)
	}

	if err := netsec.ValidateWrap(wrappedKey, wrappingKey); !errors.Is(err, netsec.ErrUntrustedWrappingKey) {
		t.Fatalf("reversed argument order error = %v, want %v", err, netsec.ErrUntrustedWrappingKey)
	}
}

func TestWrapWithoutTrustedRestrictionNeedsNoTrustedKey(t *testing.T) {
	t.Parallel()

	wrappingKey := attrs(boolAttr(netsec.CKA_WRAP, true))
	wrappedKey := attrs(boolAttr(netsec.CKA_WRAP_WITH_TRUSTED, false))
	if err := netsec.ValidateWrap(wrappingKey, wrappedKey); err != nil {
		t.Fatalf("ValidateWrap() = %v, want nil", err)
	}
}

func TestInvalidBooleanIsRejected(t *testing.T) {
	t.Parallel()

	err := netsec.ValidateAttributes(attrs(netsec.Attribute{
		Type:  netsec.CKA_WRAP,
		Value: 2,
	}))
	if !errors.Is(err, netsec.ErrInvalidAttributeValue) {
		t.Fatalf("ValidateAttributes() error = %v, want %v", err, netsec.ErrInvalidAttributeValue)
	}
}

func TestConflictingDuplicateAttributeIsRejected(t *testing.T) {
	t.Parallel()

	err := netsec.ValidateAttributes(attrs(
		boolAttr(netsec.CKA_WRAP, true),
		boolAttr(netsec.CKA_WRAP, false),
	))
	if !errors.Is(err, netsec.ErrDuplicateAttribute) {
		t.Fatalf("ValidateAttributes() error = %v, want %v", err, netsec.ErrDuplicateAttribute)
	}
}
