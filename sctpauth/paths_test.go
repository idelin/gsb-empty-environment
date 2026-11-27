package sctpauth

import (
	"errors"
	"testing"
)

func TestMultihomingGateBlocksReflectionUntilConfirmed(t *testing.T) {
	keys := testKeyRing()
	manager := NewPathManager(keys, []string{"192.0.2.10", "203.0.113.77"}, false)

	primary := "192.0.2.10"
	victim := "203.0.113.77"

	if state, err := manager.State(victim); err != nil || state != PathUnconfirmed {
		t.Fatalf("secondary path state = %v (%v), want UNCONFIRMED", state, err)
	}
	if err := manager.RequireConfirmed(victim); !errors.Is(err, ErrPathUnconfirmed) {
		t.Fatalf("pre-validation send error = %v, want ErrPathUnconfirmed", err)
	}

	challenge, err := manager.NewChallenge(victim)
	if err != nil {
		t.Fatalf("new challenge: %v", err)
	}

	wrong := challenge
	wrong[0] ^= 0xFF
	if err := manager.HandleAck(victim, wrong); !errors.Is(err, ErrPathUnconfirmed) {
		t.Fatalf("forged ack error = %v, want ErrPathUnconfirmed", err)
	}
	if manager.IsConfirmed(victim) {
		t.Fatal("path must not confirm on a wrong nonce")
	}

	attackerNonce := keys.Nonce()
	if err := manager.HandleAck(primary, attackerNonce); !errors.Is(err, ErrPathUnconfirmed) {
		t.Fatalf("ack from another source error = %v, want ErrPathUnconfirmed", err)
	}

	if err := manager.HandleAck(victim, challenge); err != nil {
		t.Fatalf("valid ack: %v", err)
	}
	if err := manager.RequireConfirmed(victim); err != nil {
		t.Fatalf("confirmed path should allow traffic: %v", err)
	}

	replay, err := manager.NewChallenge(victim)
	if err != nil {
		t.Fatalf("re-challenge: %v", err)
	}
	if replay == challenge {
		t.Fatal("each path challenge must use an independent nonce")
	}
}

func TestASCONFAddedAddressStartsUnconfirmed(t *testing.T) {
	keys := testKeyRing()
	manager := NewPathManager(keys, []string{"192.0.2.10"}, true)

	newAddr := "198.51.100.9"
	if _, err := manager.Add(newAddr); err != nil {
		t.Fatalf("asconf add: %v", err)
	}
	if err := manager.RequireConfirmed(newAddr); !errors.Is(err, ErrPathUnconfirmed) {
		t.Fatalf("asconf address error = %v, want ErrPathUnconfirmed", err)
	}

	challenge, err := manager.NewChallenge(newAddr)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	if err := manager.HandleAck(newAddr, challenge); err != nil {
		t.Fatalf("confirm asconf address: %v", err)
	}
	if err := manager.RequireConfirmed(newAddr); err != nil {
		t.Fatalf("post-validation send: %v", err)
	}

	if _, err := manager.Add(newAddr); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if err := manager.RequireConfirmed(newAddr); !errors.Is(err, ErrPathUnconfirmed) {
		t.Fatalf("re-added path must be unconfirmed again: %v", err)
	}
}

func TestUnknownAddressRejected(t *testing.T) {
	manager := NewPathManager(testKeyRing(), []string{"192.0.2.10"}, true)
	if _, err := manager.NewChallenge("198.51.100.250"); !errors.Is(err, ErrUnknownPath) {
		t.Fatalf("unknown challenge error = %v, want ErrUnknownPath", err)
	}
	if err := manager.RequireConfirmed("198.51.100.250"); !errors.Is(err, ErrUnknownPath) {
		t.Fatalf("unknown send error = %v, want ErrUnknownPath", err)
	}
}

func TestAddressCanonicalization(t *testing.T) {
	manager := NewPathManager(testKeyRing(), []string{"2001:DB8::1", "2001:db8:0:0:0:0:0:1"}, false)
	if len(manager.Snapshot()) != 1 {
		t.Fatalf("equivalent IPv6 forms should dedupe, got %d paths", len(manager.Snapshot()))
	}
	if _, err := manager.State("2001:db8::1"); err != nil {
		t.Fatalf("canonical IPv6 lookup: %v", err)
	}
}
