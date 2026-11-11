package netsec

import "testing"

func TestVerifyReorderedRecordsFail(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-reorder")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		append([]string(nil), strongAlgs...),
		append([]string(nil), strongAlgs...),
		"AES-GCM-256")

	reordered := cloneRecords(records)
	reordered[0], reordered[1] = reordered[1], reordered[0]

	report := verifier.Verify(reordered)
	assertNotAuthentic(t, report, FindingReordered)
	assertFinding(t, report, FindingBadSignature)
}

func TestVerifyCrossSessionSplicingFails(t *testing.T) {
	keys := mustGenerateKeys(t)
	verifier := newTestVerifier(t, keys, sessionIDFor("target"))

	target := buildTranscript(t, keys, []byte("session-target"),
		append([]string(nil), weakAlgs...),
		append([]string(nil), weakAlgs...),
		"RC4")

	// A foreign session's authenticated strong offers and matching selection
	// are spliced in front of the target session's confirmation.
	foreign := buildTranscript(t, keys, []byte("session-foreign"),
		append([]string(nil), strongAlgs...),
		append([]string(nil), strongAlgs...),
		"AES-GCM-256")

	spliced := []Record{
		foreign[0],
		foreign[1],
		foreign[2],
		target[3],
	}

	report := verifier.Verify(spliced)
	if report.Authentic {
		t.Fatalf("cross-session splice must not be Authentic")
	}
	assertFinding(t, report, FindingBadSignature)
	assertFinding(t, report, FindingChainMismatch)
	if report.Secure {
		t.Fatalf("spliced transcript must never be Secure")
	}
}

func TestVerifyForeignKeySignatureRejected(t *testing.T) {
	keys := mustGenerateKeys(t)
	attacker := mustGenerateKeys(t)
	sessionID := []byte("session-foreign-key")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		append([]string(nil), weakAlgs...),
		append([]string(nil), weakAlgs...),
		"RC4")

	// Attacker forges a strong capability list and signs with an untrusted key.
	forgeRaw := EncodeCapabilities(TypeCapabilitiesA, []string{"AES-GCM-256"})
	forged := cloneRecords(records)
	forged[0] = Record{
		Type: TypeCapabilitiesA,
		Raw:  forgeRaw,
		Marks: []Mark{
			signRaw(attacker.privA, sessionID, 0, forgeRaw, TypeCapabilitiesA, PartyA),
		},
	}

	report := verifier.Verify(forged)
	assertNotAuthentic(t, report, FindingBadSignature)
	if contains(report.AuthenticatedCapabilitiesA, "AES-GCM-256") {
		t.Fatalf("foreign-key signature must not authenticate a capability")
	}
}

func TestVerifyPreferenceViolationRejected(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-pref")
	verifier := newTestVerifier(t, keys, sessionID)

	// B's authenticated preference lists CHACHA20 first, but the selection
	// records a lower-preference mutually supported algorithm.
	records := buildTranscript(t, keys, sessionID,
		[]string{"AES-GCM-256", "CHACHA20"},
		[]string{"CHACHA20", "AES-GCM-256"},
		"AES-GCM-256")

	report := verifier.Verify(records)
	assertNotAuthentic(t, report, FindingPreferenceViolation)
}

func TestVerifyHostPreferenceOrderApplied(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-host-pref")
	verifier, err := New(Policy{
		SessionID:       sessionID,
		PartyAKey:       keys.pubA,
		PartyBKey:       keys.pubB,
		PreferenceOrder: []string{"AES-GCM-256", "CHACHA20"},
		Acceptable:      map[string]bool{"AES-GCM-256": true, "CHACHA20": true},
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	records := buildTranscript(t, keys, sessionID,
		[]string{"AES-GCM-256", "CHACHA20"},
		[]string{"CHACHA20", "AES-GCM-256"},
		"AES-GCM-256")

	report := verifier.Verify(records)
	assertSecure(t, report)
	assertNoFinding(t, report, FindingPreferenceViolation)
}

func TestVerifySelectionNotInIntersectionRejected(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-not-intersection")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		[]string{"AES-GCM-256"},
		[]string{"CHACHA20"},
		"AES-GCM-256")

	report := verifier.Verify(records)
	assertNotAuthentic(t, report, FindingSelectionNotInIntersection)
}

func TestVerifyTamperedSelectionRawFails(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-tamper-selection")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		append([]string(nil), strongAlgs...),
		append([]string(nil), strongAlgs...),
		"AES-GCM-256")

	tampered := cloneRecords(records)
	tampered[2] = Record{
		Type: TypeSelection,
		Raw:  EncodeSelection("CHACHA20", records[0].Raw, records[1].Raw),
		// Reuse the old mark, which now covers different raw bytes.
		Marks: records[2].Marks,
	}

	report := verifier.Verify(tampered)
	assertNotAuthentic(t, report, FindingBadSignature)
	assertFinding(t, report, FindingChainMismatch)
}

func TestVerifyMalformedRawRejected(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-malformed")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		append([]string(nil), strongAlgs...),
		append([]string(nil), strongAlgs...),
		"AES-GCM-256")

	cases := []struct {
		name string
		slot int
		raw  []byte
	}{
		{"garbage header", 0, []byte("garbage")},
		{"truncated caps", 1, append(encodeHeader(TypeCapabilitiesB), 0x00, 0x02, 0x00, 0x04)},
		{"short selection", 2, append(encodeHeader(TypeSelection), 0x00)},
		{"short confirmation", 3, append(encodeHeader(TypeConfirmation), 0x01, 0x02)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modified := cloneRecords(records)
			modified[tc.slot].Raw = tc.raw
			report := verifier.Verify(modified)
			assertNotAuthentic(t, report, FindingMalformed)
		})
	}
}

func TestVerifyUnexpectedExtraRecordRejected(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-extra")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		append([]string(nil), strongAlgs...),
		append([]string(nil), strongAlgs...),
		"AES-GCM-256")

	extra := append(cloneRecords(records), records[0])
	report := verifier.Verify(extra)
	assertNotAuthentic(t, report, FindingUnexpectedRecord)
}

func TestVerifyMissingConfirmationSignatureRejected(t *testing.T) {
	keys := mustGenerateKeys(t)
	sessionID := []byte("session-missing-confirm-sig")
	verifier := newTestVerifier(t, keys, sessionID)

	records := buildTranscript(t, keys, sessionID,
		append([]string(nil), strongAlgs...),
		append([]string(nil), strongAlgs...),
		"AES-GCM-256")

	modified := cloneRecords(records)
	modified[3].Marks = []Mark{records[3].Marks[0]}
	report := verifier.Verify(modified)
	assertNotAuthentic(t, report, FindingMissingSignature)
}

func sessionIDFor(name string) []byte {
	return []byte("session-" + name)
}
