package netsec

import (
	"errors"
	"testing"
)

func TestCopyObject_BlocksFlippingExtractableToTrue(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, false)
	guard := NewGuard(hsm)

	template := []Attribute{{Type: CKA_EXTRACTABLE, Value: ckBool(true)}}
	_, err := guard.CopyObject(session, key, template)
	if !errors.Is(err, ErrTemplateInconsistent) {
		t.Fatalf("expected ErrTemplateInconsistent, got %v", err)
	}
	if hsm.copyCalls != 0 {
		t.Fatalf("backend CopyObject must not be called, calls=%d", hsm.copyCalls)
	}
}

func TestCopyObject_AllowsStayingNonExtractable(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, false)
	guard := NewGuard(hsm)

	template := []Attribute{{Type: CKA_LABEL, Value: []byte("copy")}}
	copyHandle, err := guard.CopyObject(session, key, template)
	if err != nil {
		t.Fatalf("expected copy to succeed, got %v", err)
	}

	attrs, err := guard.GetAttributeValue(session, copyHandle, []AttributeType{CKA_EXTRACTABLE})
	if err != nil {
		t.Fatalf("reading copied object failed: %v", err)
	}
	if len(attrs) != 1 || attrs[0].Value[0] != 0 {
		t.Fatalf("copy must remain non-extractable, got %+v", attrs)
	}
}

func TestCopyObject_AllowsTrueWhenAlreadyExtractable(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, true)
	guard := NewGuard(hsm)

	template := []Attribute{{Type: CKA_EXTRACTABLE, Value: ckBool(true)}}
	if _, err := guard.CopyObject(session, key, template); err != nil {
		t.Fatalf("extractable object may stay extractable in a copy, got %v", err)
	}
	if hsm.copyCalls != 1 {
		t.Fatalf("expected one backend copy call, got %d", hsm.copyCalls)
	}
}

func TestSetAttributeValue_BlocksFlippingExtractableToTrue(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, false)
	guard := NewGuard(hsm)

	template := []Attribute{{Type: CKA_EXTRACTABLE, Value: ckBool(true)}}
	err := guard.SetAttributeValue(session, key, template)
	if !errors.Is(err, ErrTemplateInconsistent) {
		t.Fatalf("expected ErrTemplateInconsistent, got %v", err)
	}
	if hsm.setCalls != 0 {
		t.Fatalf("backend SetAttributeValue must not be called, calls=%d", hsm.setCalls)
	}

	attrs, readErr := hsm.GetAttributeValue(session, key, []AttributeType{CKA_EXTRACTABLE})
	if readErr != nil {
		t.Fatalf("reading object failed: %v", readErr)
	}
	if len(attrs) != 1 || attrs[0].Value[0] != 0 {
		t.Fatalf("object must remain non-extractable after blocked call, got %+v", attrs)
	}
}

func TestSetAttributeValue_AllowsOtherAttributes(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, false)
	guard := NewGuard(hsm)

	template := []Attribute{{Type: CKA_LABEL, Value: []byte("renamed")}}
	if err := guard.SetAttributeValue(session, key, template); err != nil {
		t.Fatalf("expected unrelated attribute update to succeed, got %v", err)
	}
	if hsm.setCalls != 1 {
		t.Fatalf("expected one backend set call, got %d", hsm.setCalls)
	}
}

func TestSessionObject_ProtectedForWholeSessionLifecycle(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, false)
	guard := NewGuard(hsm)

	if _, err := guard.WrapKey(session, wrappingKey, key, wrapMech); !errors.Is(err, ErrKeyUnextractable) {
		t.Fatalf("session key wrap must be blocked, got %v", err)
	}
	if _, err := guard.GetAttributeValue(session, key, []AttributeType{CKA_VALUE}); !errors.Is(err, ErrAttributeSensitive) {
		t.Fatalf("session key value read must be blocked, got %v", err)
	}

	hsm.CloseSession(session)

	if _, err := guard.WrapKey(session, wrappingKey, key, wrapMech); !errors.Is(err, ErrObjectHandleInvalid) {
		t.Fatalf("expected handle invalid after session close, got %v", err)
	}
}

func TestTokenObject_EnforcesSamePolicy(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	tokenKey := hsm.CreateObject(session, []Attribute{
		{Type: CKA_TOKEN, Value: ckBool(true)},
		{Type: CKA_SENSITIVE, Value: ckBool(true)},
		{Type: CKA_EXTRACTABLE, Value: ckBool(false)},
		{Type: CKA_VALUE, Value: secretValue},
	})
	guard := NewGuard(hsm)

	if _, err := guard.GetAttributeValue(session, tokenKey, []AttributeType{CKA_VALUE}); !errors.Is(err, ErrAttributeSensitive) {
		t.Fatalf("token key value read must be blocked, got %v", err)
	}
	if _, err := guard.WrapKey(session, wrappingKey, tokenKey, wrapMech); !errors.Is(err, ErrKeyUnextractable) {
		t.Fatalf("token key wrap must be blocked, got %v", err)
	}

	template := []Attribute{{Type: CKA_EXTRACTABLE, Value: ckBool(true)}}
	if err := guard.SetAttributeValue(session, tokenKey, template); !errors.Is(err, ErrTemplateInconsistent) {
		t.Fatalf("token key extractable flip must be blocked, got %v", err)
	}
}

func TestGuard_FailsClosedWhenBackendCannotReadPolicyAttributes(t *testing.T) {
	backendErr := &Error{Code: CKR_FUNCTION_FAILED, Message: "hsm unreachable"}
	guard := NewGuard(&errorBackend{err: backendErr})
	session := SessionHandle(1)
	key := ObjectHandle(7)

	if _, err := guard.WrapKey(session, wrappingKey, key, wrapMech); !errors.Is(err, backendErr) {
		t.Fatalf("wrap must propagate backend error instead of allowing export, got %v", err)
	}
	if _, err := guard.GetAttributeValue(session, key, []AttributeType{CKA_VALUE}); !errors.Is(err, backendErr) {
		t.Fatalf("sensitive read must propagate backend error, got %v", err)
	}
	template := []Attribute{{Type: CKA_EXTRACTABLE, Value: ckBool(true)}}
	if err := guard.SetAttributeValue(session, key, template); !errors.Is(err, backendErr) {
		t.Fatalf("set must propagate backend error instead of applying template, got %v", err)
	}
}

func TestGuard_RejectsMalformedBooleanTemplate(t *testing.T) {
	hsm := newMemoryHSM()
	session := hsm.OpenSession()
	key := newSessionKey(t, hsm, session, true, false)
	guard := NewGuard(hsm)

	template := []Attribute{{Type: CKA_EXTRACTABLE, Value: []byte{0, 1}}}
	if err := guard.SetAttributeValue(session, key, template); !errors.Is(err, ErrAttributeTypeInvalid) {
		t.Fatalf("malformed CK_BBOOL must be rejected, got %v", err)
	}
	if hsm.setCalls != 0 {
		t.Fatalf("backend must not receive malformed template, calls=%d", hsm.setCalls)
	}
}
