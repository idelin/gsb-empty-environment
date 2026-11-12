package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestIssueResumeRoundTrip(t *testing.T) {
	h := newTestHarness(t, 60, policyFor(testTenantA, AuthCertificate, 300, "h2", "http/1.1"))
	sessionData := []byte("session-blob-v1")

	ticket, err := h.manager.Issue(testTenantA, AuthCertificate, tls13Options("h2"), sessionData)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if len(ticket) == 0 || ticket[0] != envelopeVersion {
		t.Fatalf("ticket missing envelope version: % x", ticket)
	}

	session, err := h.manager.Resume(testTenantA, ticket)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if session.Tenant != testTenantA || session.AuthLevel != AuthCertificate {
		t.Fatalf("unexpected session: %+v", session)
	}
	if !bytes.Equal(session.SessionData, sessionData) {
		t.Fatalf("session data mismatch: got %q want %q", session.SessionData, sessionData)
	}
	if session.PolicyChanged {
		t.Fatal("PolicyChanged should be false under unchanged policy")
	}
	if h.factory.callCount() != 1 {
		t.Fatalf("factory calls = %d, want 1", h.factory.callCount())
	}
	if bytes.Contains(ticket, sessionData) {
		t.Fatal("plaintext session data leaked into ticket")
	}
}

func TestResumeCrossTenantRejected(t *testing.T) {
	h := newTestHarness(t,
		60,
		policyFor(testTenantA, AuthCertificate, 300, "h2"),
		policyFor(testTenantB, AuthCertificate, 300, "h2"),
	)

	ticket, err := h.manager.Issue(testTenantA, AuthCertificate, tls13Options("h2"), []byte("a-data"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	session, err := h.manager.Resume(testTenantB, ticket)
	if !errors.Is(err, ErrTicketAuth) && !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("cross-tenant err = %v, want ErrTicketAuth or ErrTenantMismatch", err)
	}
	if session != nil {
		t.Fatal("cross-tenant resume must not return a session")
	}

	session, err = h.manager.Resume("", ticket)
	if !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("empty tenant err = %v, want ErrEmptyTenant", err)
	}
	if session != nil {
		t.Fatal("empty-tenant resume must not return a session")
	}
	if h.factory.callCount() != 0 {
		t.Fatalf("factory must not run on cross-tenant use, calls = %d", h.factory.callCount())
	}

	if _, err := h.manager.Resume(testTenantA, ticket); err != nil {
		t.Fatalf("origin tenant resume: %v", err)
	}
}

func TestGracePeriodKeyRotation(t *testing.T) {
	const gracePeriod = uint64(120)
	h := newTestHarness(t, gracePeriod, policyFor(testTenantA, AuthCertificate, 600, "h2"))

	ticket, err := h.manager.Issue(testTenantA, AuthCertificate, tls13Options("h2"), []byte("rotate-me"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	oldKeyID := h.keyring.ActiveID()

	h.clock.advance(10)
	if err := h.keyring.Rotate(testKey(t), h.clock.get()); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if h.keyring.ActiveID() == oldKeyID {
		t.Fatal("key id must change after rotation")
	}

	newTicket, err := h.manager.Issue(testTenantA, AuthCertificate, tls13Options("h2"), []byte("fresh"))
	if err != nil {
		t.Fatalf("issue after rotation: %v", err)
	}
	if id, _ := envelopeKeyID(newTicket); id != h.keyring.ActiveID() {
		t.Fatal("new ticket must be sealed with the rotated key")
	}

	h.clock.advance(60)
	if _, err := h.manager.Resume(testTenantA, ticket); err != nil {
		t.Fatalf("old ticket within grace period: %v", err)
	}

	beforeFactoryCalls := h.factory.callCount()
	h.clock.advance(gracePeriod)
	session, err := h.manager.Resume(testTenantA, ticket)
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("expired grace err = %v, want ErrUnknownKey", err)
	}
	if session != nil {
		t.Fatal("ticket past grace period must not produce a session")
	}
	if h.factory.callCount() != beforeFactoryCalls {
		t.Fatal("factory must not run when key is past grace period")
	}

	if _, err := h.manager.Resume(testTenantA, newTicket); err != nil {
		t.Fatalf("new ticket after grace expiry: %v", err)
	}
}

func TestPolicyTighteningRejectsOldTicket(t *testing.T) {
	h := newTestHarness(t, 300, policyFor(testTenantA, AuthAnonymous, 600, "h2"))

	ticket, err := h.manager.Issue(testTenantA, AuthAnonymous, tls13Options("h2"), []byte("legacy"))
	if err != nil {
		t.Fatalf("issue under permissive policy: %v", err)
	}

	h.store.replace(policyFor(testTenantA, AuthCertificate, 600, "h2"))

	session, err := h.manager.Resume(testTenantA, ticket)
	if !errors.Is(err, ErrPolicyRejectsLevel) {
		t.Fatalf("tightened policy err = %v, want ErrPolicyRejectsLevel", err)
	}
	if session != nil {
		t.Fatal("old ticket must not bypass tightened client authentication")
	}
	if h.factory.callCount() != 0 {
		t.Fatalf("factory must not run when policy rejects level, calls = %d", h.factory.callCount())
	}

	compliantTicket, err := h.manager.Issue(testTenantA, AuthMutual, tls13Options("h2"), []byte("strict"))
	if err != nil {
		t.Fatalf("issue under tightened policy: %v", err)
	}
	restored, err := h.manager.Resume(testTenantA, compliantTicket)
	if err != nil {
		t.Fatalf("resume compliant ticket: %v", err)
	}
	if restored.AuthLevel != AuthMutual {
		t.Fatalf("restored level = %v, want mutual", restored.AuthLevel)
	}
}

func TestPolicyChangeMarkedButStillAllowed(t *testing.T) {
	h := newTestHarness(t, 300, policyFor(testTenantA, AuthCertificate, 600, "h2"))
	ticket, err := h.manager.Issue(testTenantA, AuthCertificate, tls13Options("h2"), []byte("data"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	h.store.replace(policyFor(testTenantA, AuthCertificate, 900, "h2"))
	session, err := h.manager.Resume(testTenantA, ticket)
	if err != nil {
		t.Fatalf("resume after allowed policy change: %v", err)
	}
	if !session.PolicyChanged {
		t.Fatal("session should be marked PolicyChanged when policy digest differs")
	}
	if h.factory.callCount() != 1 {
		t.Fatalf("factory calls = %d, want 1", h.factory.callCount())
	}
}

func TestProtocolOptionsEnforced(t *testing.T) {
	initial := Policy{
		Tenant:       testTenantA,
		MinAuthLevel: AuthCertificate,
		Options:      ProtocolOptions{MinVersion: 0x0303, MaxVersion: 0x0304, ALPN: []string{"h2", "http/1.1"}},
		TicketTTL:    600,
	}
	h := newTestHarness(t, 300, initial)

	legacyOptions := ProtocolOptions{MinVersion: 0x0303, MaxVersion: 0x0304, ALPN: []string{"http/1.1"}}
	ticket, err := h.manager.Issue(testTenantA, AuthCertificate, legacyOptions, []byte("proto"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	tightened := initial
	tightened.Options = tls13Options("h2")
	h.store.replace(tightened)

	if _, err := h.manager.Resume(testTenantA, ticket); !errors.Is(err, ErrPolicyRejectsLevel) {
		t.Fatalf("protocol tightening err = %v, want ErrPolicyRejectsLevel", err)
	}
	if h.factory.callCount() != 0 {
		t.Fatal("factory must not run when protocol options are rejected")
	}
}
