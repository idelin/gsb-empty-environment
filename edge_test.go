package netsec

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func TestConstructorValidation(t *testing.T) {
	if _, err := NewManager(nil); !errors.Is(err, ErrNoManager) {
		t.Fatalf("NewManager(nil) = %v, want ErrNoManager", err)
	}
	if _, err := NewManagerWithClock(nil, time.Now); !errors.Is(err, ErrNoManager) {
		t.Fatalf("empty secret = %v, want ErrNoManager", err)
	}
	if _, err := NewManagerWithClock([]byte("secret"), nil); !errors.Is(err, ErrNoManager) {
		t.Fatalf("nil clock = %v, want ErrNoManager", err)
	}

	manager, err := NewManager([]byte("wall-clock-secret"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := manager.Provision("acct", Channel{ID: "a@example.test", Kind: "email"}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if ch, err := manager.ActiveChannel("acct"); err != nil || ch.ID != "a@example.test" {
		t.Fatalf("wall-clock manager unusable: ch=%v err=%v", ch, err)
	}
}

func TestProvisionAndLookupErrors(t *testing.T) {
	manager, _ := newScenarioManager(t)

	if err := manager.Provision("", Channel{ID: "a@example.test", Kind: "email"}); !errors.Is(err, ErrInvalidAccount) {
		t.Fatalf("empty account Provision = %v", err)
	}
	if err := manager.Provision("acct", Channel{}); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("empty channel Provision = %v", err)
	}
	if _, err := manager.ActiveChannel("missing"); !errors.Is(err, ErrNoActiveChannel) {
		t.Fatalf("unknown account = %v, want ErrNoActiveChannel", err)
	}

	old := Channel{ID: "old@example.test", Kind: "email"}
	next := Channel{ID: "new@example.test", Kind: "email"}
	mustProvision(t, manager, "acct", old)

	// Re-provisioning while a transaction is in flight must be refused.
	mustBegin(t, manager, "acct", next, time.Hour)
	if err := manager.Provision("acct", Channel{ID: "x@example.test", Kind: "email"}); err == nil {
		t.Fatalf("re-provision during pending change unexpectedly accepted")
	}

	// Once pending state is gone, re-provisioning works.
	view, _ := manager.Pending("acct")
	if err := manager.Revoke("acct", view.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	replacement := Channel{ID: "reset@example.test", Kind: "email"}
	if err := manager.Provision("acct", replacement); err != nil {
		t.Fatalf("re-provision after revoke: %v", err)
	}
	active, _ := manager.ActiveChannel("acct")
	if !active.equal(replacement) {
		t.Fatalf("active = %v, want %v", active, replacement)
	}
}

func TestChallengeAndRoleValidation(t *testing.T) {
	manager, _ := newScenarioManager(t)
	accountID := "acct-role"
	old := Channel{ID: "old@example.test", Kind: "email"}
	next := Channel{ID: "new@example.test", Kind: "email"}
	mustProvision(t, manager, accountID, old)
	view := mustBegin(t, manager, accountID, next, time.Hour)

	if _, _, err := manager.SendChallenge(accountID, view.ID, Role(99)); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("invalid role challenge = %v, want ErrInvalidRole", err)
	}
	if _, err := manager.Confirm(accountID, view.ID, Role(99), "anything"); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("invalid role confirm = %v, want ErrInvalidRole", err)
	}

	if _, err := EncodeProof(nil, view.Digest, RoleOld, view.Deadline); !errors.Is(err, ErrNoManager) {
		t.Fatalf("EncodeProof empty secret = %v", err)
	}
	if _, _, _, err := DecodeProof(nil, "token"); !errors.Is(err, ErrNoManager) {
		t.Fatalf("DecodeProof empty secret = %v", err)
	}
	if _, err := EncodeProof([]byte("s"), view.Digest, Role(99), view.Deadline); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("EncodeProof bad role = %v", err)
	}

	// Token with an unknown role byte inside the signed envelope.
	token, err := EncodeProof(manager.secret, view.Digest, RoleOld, view.Deadline)
	if err != nil {
		t.Fatalf("EncodeProof: %v", err)
	}
	raw, err := tokenEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode test token: %v", err)
	}
	raw[sha256.Size] = '?'
	// Re-sign so signature validation passes and the role check is reached.
	digest := Digest(append([]byte(nil), raw[:sha256.Size]...))
	deadline := view.Deadline
	payload := proofPayload(digest, RoleOld, deadline)
	copy(raw[len(raw)-sha256.Size:], signPayload(manager.secret, payload))
	if _, _, _, err := DecodeProof(manager.secret, tokenEncoding.EncodeToString(raw)); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("unknown role byte = %v, want ErrInvalidRole", err)
	}
}

func TestStringersAndDigestRendering(t *testing.T) {
	if RoleOld.String() != "old" || RoleNew.String() != "new" || Role(7).String() == "" {
		t.Fatalf("role stringer drift")
	}
	for status, want := range map[Status]string{
		StatusPending:    "pending",
		StatusCommitted:  "committed",
		StatusRevoked:    "revoked",
		StatusSuperseded: "superseded",
	} {
		if status.String() != want {
			t.Fatalf("status %d string = %q, want %q", status, status.String(), want)
		}
	}
	if Status(42).String() == "" {
		t.Fatalf("unknown status must still render")
	}
	digest := computeDigest("acct", "change", Channel{ID: "o", Kind: "email"}, Channel{ID: "n", Kind: "email"})
	if digest.String() == "" || len(digest.String()) < sha256.Size {
		t.Fatalf("digest rendering suspicious: %q", digest.String())
	}
	if digest.Equal(nil) || digest.Equal(Digest{1, 2, 3}) {
		t.Fatalf("digest equality gave false positive")
	}
}

func TestRevokeWithoutPending(t *testing.T) {
	manager, _ := newScenarioManager(t)
	mustProvision(t, manager, "acct", Channel{ID: "old@example.test", Kind: "email"})
	if err := manager.Revoke("acct", "anything"); !errors.Is(err, ErrUnknownChange) {
		t.Fatalf("Revoke without pending = %v, want ErrUnknownChange", err)
	}
	if _, err := manager.History("missing"); !errors.Is(err, ErrNoActiveChannel) {
		t.Fatalf("History unknown account = %v", err)
	}
}
