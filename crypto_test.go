package netsec_test

import (
	"bytes"
	"errors"
	"testing"

	netsec "example.org/netsec/q0497"
)

func TestWrapAccountKeyRoundTrip(t *testing.T) {
	recipient := testDevice(t, "recipient")
	accountKey := randomKey(t)
	binding := netsec.DeviceKeyBinding("acct-1", recipient.ID, "enr-1")

	envelope, err := netsec.WrapAccountKey(accountKey, recipient.Public().KeyAgreementPublicKey, binding)
	if err != nil {
		t.Fatalf("WrapAccountKey: %v", err)
	}
	if len(envelope) != 96 {
		t.Fatalf("envelope length = %d, want 96", len(envelope))
	}
	if bytes.Contains(envelope, accountKey) {
		t.Fatal("envelope contains the plaintext account key")
	}

	opened, err := netsec.UnwrapAccountKey(envelope, recipient.KeyAgreementPrivateKey.Bytes(), binding)
	if err != nil {
		t.Fatalf("UnwrapAccountKey: %v", err)
	}
	if !bytes.Equal(opened, accountKey) {
		t.Fatal("unwrapped key differs from original")
	}
}

func TestWrapAccountKeyRejectsWrongRecipientBindingAndTampering(t *testing.T) {
	recipient := testDevice(t, "recipient")
	other := testDevice(t, "other")
	accountKey := randomKey(t)
	binding := netsec.DeviceKeyBinding("acct-1", recipient.ID, "enr-1")

	envelope, err := netsec.WrapAccountKey(accountKey, recipient.Public().KeyAgreementPublicKey, binding)
	if err != nil {
		t.Fatalf("WrapAccountKey: %v", err)
	}

	if _, err := netsec.UnwrapAccountKey(envelope, other.KeyAgreementPrivateKey.Bytes(), binding); !errors.Is(err, netsec.ErrAccountKeyDecrypt) {
		t.Fatalf("wrong recipient error = %v, want ErrAccountKeyDecrypt", err)
	}
	wrongBinding := netsec.DeviceKeyBinding("acct-2", recipient.ID, "enr-1")
	if _, err := netsec.UnwrapAccountKey(envelope, recipient.KeyAgreementPrivateKey.Bytes(), wrongBinding); !errors.Is(err, netsec.ErrAccountKeyDecrypt) {
		t.Fatalf("wrong binding error = %v, want ErrAccountKeyDecrypt", err)
	}

	tampered := append([]byte(nil), envelope...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := netsec.UnwrapAccountKey(tampered, recipient.KeyAgreementPrivateKey.Bytes(), binding); !errors.Is(err, netsec.ErrAccountKeyDecrypt) {
		t.Fatalf("tampered envelope error = %v, want ErrAccountKeyDecrypt", err)
	}

	if _, err := netsec.UnwrapAccountKey(envelope[:len(envelope)-1], recipient.KeyAgreementPrivateKey.Bytes(), binding); !errors.Is(err, netsec.ErrInvalidWrappedKey) {
		t.Fatalf("truncated envelope error = %v, want ErrInvalidWrappedKey", err)
	}
	if _, err := netsec.WrapAccountKey(accountKey[:31], recipient.Public().KeyAgreementPublicKey, binding); !errors.Is(err, netsec.ErrInvalidArgument) {
		t.Fatalf("short account key error = %v", err)
	}
	if _, err := netsec.WrapAccountKey(accountKey, recipient.Public().KeyAgreementPublicKey, nil); !errors.Is(err, netsec.ErrInvalidArgument) {
		t.Fatalf("empty binding error = %v", err)
	}
}

func TestDeviceKeyBindingIsUnambiguous(t *testing.T) {
	a := netsec.DeviceKeyBinding("ab", "c", "d")
	b := netsec.DeviceKeyBinding("a", "bc", "d")
	if bytes.Equal(a, b) {
		t.Fatal("length-prefixed binding collided for different fields")
	}
	if !bytes.Equal(netsec.DeviceKeyBinding("a", "b", "c"), netsec.EnrollmentKeyBinding("a", "b", "c")) {
		t.Fatal("binding aliases differ")
	}
}

func TestEnrollmentEnvelopeCannotBeMovedToAnotherEnrollment(t *testing.T) {
	recipient := testDevice(t, "recipient")
	accountKey := randomKey(t)
	envelope, err := netsec.WrapAccountKey(accountKey, recipient.Public().KeyAgreementPublicKey, netsec.DeviceKeyBinding("acct", recipient.ID, "enr-1"))
	if err != nil {
		t.Fatalf("WrapAccountKey: %v", err)
	}
	movedBinding := netsec.DeviceKeyBinding("acct", recipient.ID, "enr-2")
	if _, err := netsec.UnwrapAccountKey(envelope, recipient.KeyAgreementPrivateKey.Bytes(), movedBinding); !errors.Is(err, netsec.ErrAccountKeyDecrypt) {
		t.Fatalf("moved enrollment envelope error = %v, want ErrAccountKeyDecrypt", err)
	}
}
