package netsec

import (
	"encoding/hex"
	"strings"
	"testing"
)

const (
	peerA      = "alice"
	peerB      = "bob"
	algStrong  = "AES256-GCM"
	algMedium  = "AES128-GCM"
	algWeak    = "3DES"
	sessionOne = "sess-001"
	sessionTwo = "sess-002"
)

var preferenceOrder = []string{algStrong, algMedium, algWeak}

func capsMessage(peer, session string, algs []string) *Message {
	return &Message{
		Type:         TypeCapabilities,
		Sender:       peer,
		SessionID:    session,
		Capabilities: algs,
	}
}

func selectionMessage(peer, session, alg string) *Message {
	return &Message{
		Type:      TypeSelection,
		Sender:    peer,
		SessionID: session,
		Selection: alg,
	}
}

func mustEnvelope(t *testing.T, msg *Message, authed bool, signer string) Envelope {
	t.Helper()
	raw, err := EncodeMessage(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	mark := AuthMark{Authenticated: authed}
	if authed {
		if signer == "" {
			signer = msg.Sender
		}
		mark.Signer = signer
	}
	return Envelope{Raw: raw, Auth: mark}
}

// confirmEnvelope builds a confirmation whose transcript hash binds to the
// exact raw bytes already present in prefix.
func confirmEnvelope(t *testing.T, prefix []Envelope, peer, session, alg string, authed bool) Envelope {
	t.Helper()
	msg := &Message{
		Type:      TypeConfirmation,
		Sender:    peer,
		SessionID: session,
		Confirmation: &Confirmation{
			Algorithm:      alg,
			TranscriptHash: hex.EncodeToString(PrefixDigest(prefix, len(prefix))),
		},
	}
	return mustEnvelope(t, msg, authed, "")
}

// validEnvelopes builds an intact, fully authenticated transcript selecting
// the strongest shared algorithm.
func validEnvelopes(t *testing.T) []Envelope {
	t.Helper()
	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algStrong, algMedium}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algStrong, algMedium, algWeak}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algStrong), true, ""),
	}
	envs = append(envs, confirmEnvelope(t, envs, peerA, sessionOne, algStrong, true))
	envs = append(envs, confirmEnvelope(t, envs, peerB, sessionOne, algStrong, true))
	return envs
}

func checkReport(t *testing.T, envs []Envelope) *Report {
	t.Helper()
	report, err := Check(&Transcript{Envelopes: envs, PreferenceOrder: preferenceOrder})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return report
}

func TestCheckValidTranscript(t *testing.T) {
	report := checkReport(t, validEnvelopes(t))

	if !report.Secure {
		t.Fatalf("expected secure transcript, got findings: %+v", report.Findings)
	}
	if report.SessionID != sessionOne {
		t.Errorf("session id = %q, want %q", report.SessionID, sessionOne)
	}
	if report.AuthenticatedSelection != algStrong {
		t.Errorf("selection = %q, want %q", report.AuthenticatedSelection, algStrong)
	}
	wantIntersection := []string{algStrong, algMedium}
	if strings.Join(report.Intersection, ",") != strings.Join(wantIntersection, ",") {
		t.Errorf("intersection = %v, want %v", report.Intersection, wantIntersection)
	}
	if len(report.UnauthenticatedCapabilities) != 0 {
		t.Errorf("expected no clue capabilities, got %v", report.UnauthenticatedCapabilities)
	}
}

func TestDeletionTamperedCapabilityMessage(t *testing.T) {
	// The log drops the strong algorithm from B's list. Because the raw bytes
	// no longer authenticate, the claim cannot prove B lacks strong support.
	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algStrong, algMedium}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algWeak}), false, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algWeak), true, ""),
	}
	envs = append(envs, confirmEnvelope(t, envs, peerA, sessionOne, algWeak, true))
	envs = append(envs, confirmEnvelope(t, envs, peerB, sessionOne, algWeak, true))

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("tampered transcript reported secure")
	}
	if !report.HasError(FindingUnauthenticated) {
		t.Errorf("expected %s finding", FindingUnauthenticated)
	}
	if !report.HasError(FindingMissingCapabilities) {
		t.Errorf("expected %s finding", FindingMissingCapabilities)
	}
	if !report.HasCode(FindingUnauthenticatedCapabilities) {
		t.Errorf("expected clue finding for unauthenticated capability claim")
	}
	if got := report.UnauthenticatedCapabilities[peerB]; len(got) != 1 || got[0] != algWeak {
		t.Errorf("clue capabilities = %v, want only [%s]", got, algWeak)
	}
}

func TestDeletionDroppedMessage(t *testing.T) {
	// A whole capability message is removed from the log while confirmations
	// still bind to the original transcript digest.
	original := validEnvelopes(t)
	envs := append([]Envelope{original[0]}, original[2:]...)

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("truncated transcript reported secure")
	}
	if !report.HasError(FindingMissingCapabilities) {
		t.Errorf("expected %s finding", FindingMissingCapabilities)
	}
	if !report.HasError(FindingConfirmationHashMismatch) {
		t.Errorf("expected %s finding after deletion", FindingConfirmationHashMismatch)
	}
}

func TestReorderMessages(t *testing.T) {
	envs := validEnvelopes(t)
	// Move the selection ahead of B's capability announcement.
	envs[1], envs[2] = envs[2], envs[1]

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("reordered transcript reported secure")
	}
	if !report.HasError(FindingOrderViolation) {
		t.Errorf("expected %s finding", FindingOrderViolation)
	}
	if !report.HasError(FindingConfirmationHashMismatch) {
		t.Errorf("expected %s finding after reorder", FindingConfirmationHashMismatch)
	}
}

func TestReorderWithinCapabilityList(t *testing.T) {
	// Reordering entries inside an authenticated capability list changes no
	// raw claim semantics: the authenticated set is order independent and the
	// same strongest algorithm must still be selected.
	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algMedium, algStrong}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algWeak, algStrong, algMedium}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algStrong), true, ""),
	}
	envs = append(envs, confirmEnvelope(t, envs, peerA, sessionOne, algStrong, true))
	envs = append(envs, confirmEnvelope(t, envs, peerB, sessionOne, algStrong, true))

	report := checkReport(t, envs)
	if !report.Secure {
		t.Fatalf("list-internal reorder flagged as attack: %+v", report.Findings)
	}
	if got := strings.Join(report.Intersection, ","); got != algStrong+","+algMedium {
		t.Errorf("intersection = %q", got)
	}
}
