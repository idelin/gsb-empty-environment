package netsec

import (
	"reflect"
	"testing"
)

var (
	strongAlgs = []string{"AES-GCM-256", "CHACHA20"}
	weakAlgs   = []string{"RC4", "3DES"}
)

func TestVerifyAcceptsAuthenticatedStrongNegotiation(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-happy")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		append([]string(nil), strongAlgs...),
		[]string{"CHACHA20", "AES-GCM-256"},
		"CHACHA20")

	report := verifier.Verify(records)
	assertSecure(t, report)

	if !reflect.DeepEqual(report.AuthenticatedCapabilitiesA, strongAlgs) {
		t.Fatalf("party A caps = %v, want %v", report.AuthenticatedCapabilitiesA, strongAlgs)
	}
	wantIntersection := []string{"AES-GCM-256", "CHACHA20"}
	if !reflect.DeepEqual(report.Intersection, wantIntersection) {
		t.Fatalf("intersection = %v, want %v", report.Intersection, wantIntersection)
	}
	if report.Selected != "CHACHA20" || !report.SelectedAuthenticated {
		t.Fatalf("selected = %q authenticated=%v", report.Selected, report.SelectedAuthenticated)
	}
	if len(report.Clues) != 0 {
		t.Fatalf("expected no unauthenticated clues, got %v", report.Clues)
	}
	for index, evidence := range report.Records {
		if !evidence.Authenticated {
			t.Fatalf("record %d not authenticated", index)
		}
		if len(evidence.Raw) == 0 {
			t.Fatalf("record %d has empty raw bytes", index)
		}
		if len(evidence.Marks) == 0 {
			t.Fatalf("record %d has no marks", index)
		}
	}
}

func TestVerifyWeakOnlyAuthenticatedIsAuthenticButNotSecure(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-weak")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		append([]string(nil), weakAlgs...),
		append([]string(nil), weakAlgs...),
		"RC4")

	report := verifier.Verify(records)
	if !report.Authentic {
		t.Fatalf("weak-only transcript is genuinely authenticated, findings: %v", report.Findings)
	}
	if report.Secure {
		t.Fatalf("weak-only transcript must not be Secure")
	}
	assertFinding(t, report, FindingWeakSelection)
	if report.Selected != "RC4" || !report.SelectedAuthenticated {
		t.Fatalf("selected = %q authenticated=%v, want authenticated RC4",
			report.Selected, report.SelectedAuthenticated)
	}
}

func TestVerifyDeletionFromCapabilityListFails(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-delete")
	verifier := newTestVerifier(t, keys, sessionID)

	// The real party A offer contains a strong algorithm; the log under test
	// claims party A only offered weak algorithms.
	records := buildTranscript(t, keys, sessionID,
		[]string{"AES-GCM-256", "RC4"},
		[]string{"AES-GCM-256", "RC4"},
		"AES-GCM-256")

	tampered := cloneRecords(records)
	tampered[0] = Record{
		Type:  TypeCapabilitiesA,
		Raw:   EncodeCapabilities(TypeCapabilitiesA, []string{"RC4"}),
		Marks: records[0].Marks,
	}

	report := verifier.Verify(tampered)
	assertNotAuthentic(t, report, FindingBadSignature)

	var sawRC4Clue bool
	for _, clue := range report.Clues {
		if clue.Algorithm == "RC4" && clue.Party == PartyA && !clue.Authenticated {
			sawRC4Clue = true
		}
	}
	if !sawRC4Clue {
		t.Fatalf("unauthenticated RC4 claim must remain a clue only, clues: %v", report.Clues)
	}
	for _, clue := range report.Clues {
		if clue.Authenticated {
			t.Fatalf("clues must never be marked authenticated: %v", clue)
		}
	}
}

func TestVerifyDeletingWholeRecordFails(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-drop")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		append([]string(nil), strongAlgs...),
		append([]string(nil), strongAlgs...),
		"AES-GCM-256")

	dropped := append([]Record(nil), records[1:]...)
	report := verifier.Verify(dropped)
	assertNotAuthentic(t, report, FindingMissingRecord)
	assertFinding(t, report, FindingBadSignature)
}

func TestVerifyUnauthenticatedCapabilityIsClueOnly(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-clue")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		append([]string(nil), weakAlgs...),
		append([]string(nil), weakAlgs...),
		"RC4")

	// Replace party A's signed offer with an unsigned strong-algorithm claim.
	modified := cloneRecords(records)
	modified[0] = Record{
		Type: TypeCapabilitiesA,
		Raw:  EncodeCapabilities(TypeCapabilitiesA, []string{"AES-GCM-256"}),
	}

	report := verifier.Verify(modified)
	if report.Authentic {
		t.Fatalf("transcript with unsigned offer must not be Authentic")
	}
	if report.Secure {
		t.Fatalf("transcript with unsigned offer must not be Secure")
	}
	assertFinding(t, report, FindingMissingSignature)

	var sawStrongClue bool
	for _, clue := range report.Clues {
		if clue.Algorithm == "AES-GCM-256" && !clue.Authenticated {
			sawStrongClue = true
		}
	}
	if !sawStrongClue {
		t.Fatalf("strong algorithm in unsigned record must surface as a clue, got %v", report.Clues)
	}
	if contains(report.AuthenticatedCapabilitiesA, "AES-GCM-256") {
		t.Fatalf("unauthenticated algorithm leaked into authenticated capabilities")
	}
	if contains(report.Intersection, "AES-GCM-256") {
		t.Fatalf("unauthenticated algorithm leaked into intersection")
	}
}
