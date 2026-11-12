package netsec

import (
	"errors"
	"testing"
)

// brokenHSM returns no attribute values, mimicking a token that hides the
// policy attribute rather than returning it.
type brokenHSM struct {
	memoryHSM
}

func (b *brokenHSM) GetAttributeValue(session SessionHandle, object ObjectHandle, attrs []AttributeType) ([]Attribute, error) {
	return nil, nil
}

func TestWrapKey_FailsClosedWhenExtractableAttributeMissing(t *testing.T) {
	hsm := &brokenHSM{}
	guard := NewGuard(hsm)

	_, err := guard.WrapKey(1, wrappingKey, 42, wrapMech)
	if !errors.Is(err, ErrAttributeTypeInvalid) {
		t.Fatalf("missing CKA_EXTRACTABLE must fail closed, got %v", err)
	}
}

// malformedHSM answers boolean reads with an invalid CK_BBOOL encoding.
type malformedHSM struct {
	memoryHSM
}

func (m *malformedHSM) GetAttributeValue(session SessionHandle, object ObjectHandle, attrs []AttributeType) ([]Attribute, error) {
	result := make([]Attribute, 0, len(attrs))
	for _, attr := range attrs {
		result = append(result, Attribute{Type: attr, Value: []byte{9}})
	}
	return result, nil
}

func TestGetAttributeValue_FailsClosedOnMalformedSensitiveFlag(t *testing.T) {
	guard := NewGuard(&malformedHSM{})

	_, err := guard.GetAttributeValue(1, 42, []AttributeType{CKA_VALUE})
	if !errors.Is(err, ErrAttributeTypeInvalid) {
		t.Fatalf("malformed CKA_SENSITIVE must fail closed, got %v", err)
	}
}

func TestCopyObject_FailsClosedOnMalformedCurrentFlag(t *testing.T) {
	guard := NewGuard(&malformedHSM{})

	template := []Attribute{{Type: CKA_EXTRACTABLE, Value: ckBool(true)}}
	_, err := guard.CopyObject(1, 42, template)
	if !errors.Is(err, ErrAttributeTypeInvalid) {
		t.Fatalf("malformed current CKA_EXTRACTABLE must fail closed, got %v", err)
	}
}

func TestError_ComparesByCodeAndRenders(t *testing.T) {
	if !errors.Is(ErrKeyUnextractable, &Error{Code: CKR_KEY_UNEXTRACTABLE}) {
		t.Fatal("errors.Is must match Errors with the same CKR code")
	}
	if errors.Is(ErrKeyUnextractable, &Error{Code: CKR_ATTRIBUTE_SENSITIVE}) {
		t.Fatal("errors.Is must not match Errors with different CKR codes")
	}
	if errors.Is(ErrKeyUnextractable, errors.New("other")) {
		t.Fatal("errors.Is must not match unrelated error types")
	}

	plain := &Error{Code: CKR_FUNCTION_FAILED}
	if plain.Error() != "netsec: pkcs#11 error 0x6" {
		t.Fatalf("unexpected fallback rendering: %q", plain.Error())
	}
	if ErrAttributeSensitive.Error() == "" {
		t.Fatal("named errors should render their message")
	}
}

// mismatchedHSM answers with a different attribute type than requested.
type mismatchedHSM struct {
	memoryHSM
}

func (m *mismatchedHSM) GetAttributeValue(session SessionHandle, object ObjectHandle, attrs []AttributeType) ([]Attribute, error) {
	return []Attribute{{Type: CKA_LABEL, Value: ckBool(false)}}, nil
}

func TestWrapKey_FailsClosedOnMismatchedAttributeType(t *testing.T) {
	guard := NewGuard(&mismatchedHSM{})

	_, err := guard.WrapKey(1, wrappingKey, 42, wrapMech)
	if !errors.Is(err, ErrAttributeTypeInvalid) {
		t.Fatalf("mismatched attribute type must fail closed, got %v", err)
	}
}
