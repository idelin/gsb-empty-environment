package netsec

import (
	"errors"
	"testing"
)

var (
	secretValue = []byte("hsm-secret-key-material")
	wrapMech    = Mechanism{Mechanism: 0x0001}
	wrappingKey = ObjectHandle(999)
)

// newSessionKey creates a session object carrying the policy attributes
// under test plus a CKA_VALUE that must never cross the guard.
func newSessionKey(t *testing.T, hsm *memoryHSM, session SessionHandle, sensitive, extractable bool) ObjectHandle {
	t.Helper()
	return hsm.CreateObject(session, []Attribute{
		{Type: CKA_CLASS, Value: []byte{0, 0, 0, 0}},
		{Type: CKA_TOKEN, Value: ckBool(false)},
		{Type: CKA_SENSITIVE, Value: ckBool(sensitive)},
		{Type: CKA_EXTRACTABLE, Value: ckBool(extractable)},
		{Type: CKA_VALUE, Value: secretValue},
	})
}

func TestGetAttributeValue_BlocksSensitiveValue(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, false)
	guard := NewGuard(hsm)

	_, err := guard.GetAttributeValue(session, key, []AttributeType{CKA_VALUE})
	if !errors.Is(err, ErrAttributeSensitive) {
		t.Fatalf("expected ErrAttributeSensitive, got %v", err)
	}
}

func TestGetAttributeValue_BlocksValueInMixedTemplate(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, false)
	guard := NewGuard(hsm)

	_, err := guard.GetAttributeValue(session, key, []AttributeType{CKA_LABEL, CKA_VALUE, CKA_MODULUS})
	if !errors.Is(err, ErrAttributeSensitive) {
		t.Fatalf("expected ErrAttributeSensitive for mixed template, got %v", err)
	}
}

func TestGetAttributeValue_AllowsNonSensitiveAttributes(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, false)
	guard := NewGuard(hsm)

	attrs, err := guard.GetAttributeValue(session, key, []AttributeType{CKA_EXTRACTABLE, CKA_SENSITIVE})
	if err != nil {
		t.Fatalf("expected non-secret read to succeed, got %v", err)
	}
	if len(attrs) != 2 {
		t.Fatalf("expected 2 attributes, got %d", len(attrs))
	}
	for _, attr := range attrs {
		if attr.Type == CKA_VALUE {
			t.Fatal("CKA_VALUE must never be returned for a sensitive object")
		}
	}
}

func TestGetAttributeValue_NonSensitiveObjectMayReadValue(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, false, true)
	guard := NewGuard(hsm)

	attrs, err := guard.GetAttributeValue(session, key, []AttributeType{CKA_VALUE})
	if err != nil {
		t.Fatalf("expected read on non-sensitive object to succeed, got %v", err)
	}
	if len(attrs) != 1 || string(attrs[0].Value) != string(secretValue) {
		t.Fatalf("expected raw value for non-sensitive object, got %+v", attrs)
	}
}

func TestWrapKey_BlocksUnextractableSessionKey(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, false)
	guard := NewGuard(hsm)

	_, err := guard.WrapKey(session, wrappingKey, key, wrapMech)
	if !errors.Is(err, ErrKeyUnextractable) {
		t.Fatalf("expected ErrKeyUnextractable, got %v", err)
	}
	if hsm.wrapCalls != 0 {
		t.Fatalf("backend WrapKey must not be called, calls=%d", hsm.wrapCalls)
	}
}

func TestWrapKey_AllowsExtractableKey(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, true)
	guard := NewGuard(hsm)

	wrapped, err := guard.WrapKey(session, wrappingKey, key, wrapMech)
	if err != nil {
		t.Fatalf("expected wrap to succeed, got %v", err)
	}
	if len(wrapped) == 0 {
		t.Fatal("expected wrapped key bytes from backend")
	}
	if hsm.wrapCalls != 1 {
		t.Fatalf("expected one backend wrap call, got %d", hsm.wrapCalls)
	}
}
