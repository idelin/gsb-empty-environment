package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestTamperedTicketRejected(t *testing.T) {
	h := newTestHarness(t, 300, policyFor(testTenantA, AuthCertificate, 600, "h2"))
	ticket, err := h.manager.Issue(testTenantA, AuthCertificate, tls13Options("h2"), []byte("secret"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	tampered := bytes.Clone(ticket)
	tampered[len(tampered)-1] ^= 0xFF
	if _, err := h.manager.Resume(testTenantA, tampered); err == nil {
		t.Fatal("tampered ticket must fail AEAD authentication")
	}

	truncated := bytes.Clone(ticket[:1+keyIDSize])
	if _, err := h.manager.Resume(testTenantA, truncated); !errors.Is(err, ErrMalformedTicket) {
		t.Fatalf("truncated ticket err = %v, want ErrMalformedTicket", err)
	}

	foreign := bytes.Clone(ticket)
	foreign[0] = envelopeVersion + 1
	if _, err := h.manager.Resume(testTenantA, foreign); !errors.Is(err, ErrMalformedTicket) {
		t.Fatalf("bad version err = %v, want ErrMalformedTicket", err)
	}

	if h.factory.callCount() != 0 {
		t.Fatalf("factory must not run on invalid tickets, calls = %d", h.factory.callCount())
	}
	if _, err := h.manager.Resume(testTenantA, ticket); err != nil {
		t.Fatalf("original ticket must still validate: %v", err)
	}
}

func TestExpiredTicketRejected(t *testing.T) {
	const ttl = uint64(300)
	h := newTestHarness(t, 60, policyFor(testTenantA, AuthCertificate, ttl, "h2"))
	ticket, err := h.manager.Issue(testTenantA, AuthCertificate, tls13Options("h2"), []byte("ephemeral"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	h.clock.advance(ttl)
	session, err := h.manager.Resume(testTenantA, ticket)
	if !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired err = %v, want ErrTicketExpired", err)
	}
	if session != nil || h.factory.callCount() != 0 {
		t.Fatal("expired ticket must not create a session")
	}
}

func TestPolicyDigestDeterministicAndSensitive(t *testing.T) {
	base := policyFor(testTenantA, AuthCertificate, 600, "h2", "http/1.1")
	reordered := base
	reordered.Options.ALPN = []string{"http/1.1", "h2"}

	if base.Digest() != reordered.Digest() {
		t.Fatal("digest must be independent of ALPN ordering")
	}

	variants := []Policy{
		base,
	}
	changed := base
	changed.Tenant = testTenantB
	variants = append(variants, changed)

	changed = base
	changed.MinAuthLevel = AuthMutual
	variants = append(variants, changed)

	changed = base
	changed.TicketTTL = 900
	variants = append(variants, changed)

	changed = base
	changed.Options = tls13Options("h2")
	variants = append(variants, changed)

	seen := make(map[[32]byte]int)
	for i, policy := range variants {
		digest := policy.Digest()
		if prior, ok := seen[digest]; ok {
			t.Fatalf("digest collision between variant %d and %d", prior, i)
		}
		seen[digest] = i
	}
}

func TestKeyringValidation(t *testing.T) {
	if _, err := NewKeyring([]byte("short"), baseTime, 60); err == nil {
		t.Fatal("invalid key length must be rejected")
	}

	keyring, err := NewKeyring(testKey(t), baseTime, 60)
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	if err := keyring.Rotate([]byte("short"), baseTime); err == nil {
		t.Fatal("rotation with invalid key must be rejected")
	}

	plaintext := []byte("payload")
	sealed, err := keyring.Seal(plaintext, []byte("aad"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	opened, err := keyring.Open(sealed, []byte("aad"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened = %q, want %q", opened, plaintext)
	}
	if _, err := keyring.Open(sealed, []byte("other-aad")); err == nil {
		t.Fatal("AAD mismatch must fail authentication")
	}

	if _, err := NewManager(nil, PolicyProviderFunc(func(string) (Policy, bool) {
		return Policy{}, false
	}), nil); err == nil {
		t.Fatal("nil keyring must be rejected")
	}
}

func TestIssueRejectsUnauthorizedLevel(t *testing.T) {
	h := newTestHarness(t, 60, policyFor(testTenantA, AuthMutual, 600, "h2"))
	if _, err := h.manager.Issue(testTenantA, AuthAnonymous, tls13Options("h2"), nil); !errors.Is(err, ErrPolicyRejectsLevel) {
		t.Fatalf("issue below min level err = %v, want ErrPolicyRejectsLevel", err)
	}
	if _, err := h.manager.Issue("", AuthMutual, tls13Options("h2"), nil); !errors.Is(err, ErrEmptyTenant) {
		t.Fatal("empty tenant must be rejected at issue time")
	}

	h.store.policies = map[string]Policy{}
	if _, err := h.manager.Issue(testTenantA, AuthMutual, tls13Options("h2"), nil); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("missing policy err = %v, want ErrPolicyUnavailable", err)
	}
}

func TestConcurrentIssueResume(t *testing.T) {
	h := newTestHarness(t, 300, policyFor(testTenantA, AuthCertificate, 600, "h2"))
	var wg sync.WaitGroup

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("session-%d", i))
			ticket, err := h.manager.Issue(testTenantA, AuthCertificate, tls13Options("h2"), data)
			if err != nil {
				t.Errorf("issue %d: %v", i, err)
				return
			}
			session, err := h.manager.Resume(testTenantA, ticket)
			if err != nil {
				t.Errorf("resume %d: %v", i, err)
				return
			}
			if !bytes.Equal(session.SessionData, data) {
				t.Errorf("session %d data mismatch", i)
			}
		}(i)
	}
	wg.Wait()
}
