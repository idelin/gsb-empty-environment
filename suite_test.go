package netsec

import (
	"errors"
	"testing"
)

func registeredRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(Suite{ID: 3, AuthAlg: 2, IntegrityAlg: 1, ConfAlg: 1}); err != nil {
		t.Fatalf("register suite 3: %v", err)
	}
	if err := reg.Register(Suite{ID: 17, AuthAlg: 3, IntegrityAlg: 2, ConfAlg: 2}); err != nil {
		t.Fatalf("register suite 17: %v", err)
	}
	return reg
}

func TestCipherSuiteZeroRejected(t *testing.T) {
	reg := NewRegistry()
	cipher0 := Suite{ID: 0, AuthAlg: 0, IntegrityAlg: 0, ConfAlg: 0}
	if err := reg.Register(cipher0); !errors.Is(err, ErrSuiteInsecure) {
		t.Fatalf("register cipher suite 0: got %v, want ErrSuiteInsecure", err)
	}
	if _, err := reg.Admit(0); !errors.Is(err, ErrSuiteNotRegistered) {
		t.Fatalf("admit cipher suite 0: got %v, want ErrSuiteNotRegistered", err)
	}
}

func TestNoneAlgorithmsRejected(t *testing.T) {
	reg := NewRegistry()
	cases := []Suite{
		{ID: 8, AuthAlg: 0, IntegrityAlg: 1, ConfAlg: 1}, // auth none
		{ID: 12, AuthAlg: 2, IntegrityAlg: 1, ConfAlg: 0}, // conf none
	}
	for _, s := range cases {
		if err := reg.Register(s); !errors.Is(err, ErrSuiteInsecure) {
			t.Errorf("register suite %d: got %v, want ErrSuiteInsecure", s.ID, err)
		}
	}
}

func TestUnregisteredSuiteRejected(t *testing.T) {
	reg := registeredRegistry(t)
	for _, id := range []uint8{3, 17} {
		if _, err := reg.Admit(id); err != nil {
			t.Errorf("admit registered suite %d: %v", id, err)
		}
	}
	if _, err := reg.Admit(5); !errors.Is(err, ErrSuiteNotRegistered) {
		t.Fatalf("admit unregistered suite 5: got %v, want ErrSuiteNotRegistered", err)
	}

	auth := newTestAuthenticator(reg)
	_, err := auth.HandleRAKP1(&RAKP1Request{
		SourceAddr: "10.0.0.9",
		SuiteID:    5,
		SessionID:  1,
		Username:   "root",
	})
	if !errors.Is(err, ErrSuiteNotRegistered) {
		t.Fatalf("RAKP1 with unregistered suite: got %v, want ErrSuiteNotRegistered", err)
	}
}
