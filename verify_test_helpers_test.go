package netsec

import (
	"crypto/ed25519"
	"testing"
)

type testKeys struct {
	pubA  ed25519.PublicKey
	privA ed25519.PrivateKey
	pubB  ed25519.PublicKey
	privB ed25519.PrivateKey
}

func mustGenerateKeys(t *testing.T) testKeys {
	t.Helper()
	pubA, privA, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key A: %v", err)
	}
	pubB, privB, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key B: %v", err)
	}
	return testKeys{pubA: pubA, privA: privA, pubB: pubB, privB: privB}
}

func newTestVerifier(t *testing.T, keys testKeys, sessionID []byte) *Verifier {
	t.Helper()
	verifier, err := New(Policy{
		SessionID: sessionID,
		PartyAKey: keys.pubA,
		PartyBKey: keys.pubB,
		Acceptable: map[string]bool{
			"AES-GCM-256": true,
			"CHACHA20":    true,
		},
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return verifier
}

// buildTranscript constructs a fully signed transcript with an explicit
// selected algorithm, allowing tests to craft preference violations.
func buildTranscript(t *testing.T, keys testKeys, sessionID []byte, capsA, capsB []string, chosen string) []Record {
	t.Helper()
	recorder, err := NewRecorder(sessionID, keys.privA, keys.privB)
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := recorder.OfferA(capsA); err != nil {
		t.Fatalf("offer A: %v", err)
	}
	if err := recorder.OfferB(capsB); err != nil {
		t.Fatalf("offer B: %v", err)
	}
	if err := recorder.Select(chosen); err != nil {
		t.Fatalf("select: %v", err)
	}
	if err := recorder.Confirm(); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return recorder.Transcript()
}

func cloneRecords(records []Record) []Record {
	out := make([]Record, len(records))
	for index, rec := range records {
		copied := Record{Type: rec.Type, Raw: append([]byte(nil), rec.Raw...)}
		copied.Marks = make([]Mark, len(rec.Marks))
		for markIndex, mark := range rec.Marks {
			copied.Marks[markIndex] = Mark{
				Signer:    mark.Signer,
				Signature: append([]byte(nil), mark.Signature...),
			}
		}
		out[index] = copied
	}
	return out
}

func findCode(report *Report, code FindingCode) *Finding {
	for index := range report.Findings {
		if report.Findings[index].Code == code {
			return &report.Findings[index]
		}
	}
	return nil
}

func assertFinding(t *testing.T, report *Report, code FindingCode) {
	t.Helper()
	if findCode(report, code) == nil {
		t.Fatalf("expected finding %q, got findings: %v", code, report.Findings)
	}
}

func assertNoFinding(t *testing.T, report *Report, code FindingCode) {
	t.Helper()
	if finding := findCode(report, code); finding != nil {
		t.Fatalf("did not expect finding %q, got: %v", code, finding)
	}
}

func assertSecure(t *testing.T, report *Report) {
	t.Helper()
	if !report.Authentic {
		t.Fatalf("expected Authentic=true, findings: %v", report.Findings)
	}
	if !report.Secure {
		t.Fatalf("expected Secure=true, findings: %v", report.Findings)
	}
}

func assertNotAuthentic(t *testing.T, report *Report, code FindingCode) {
	t.Helper()
	if report.Authentic {
		t.Fatalf("expected Authentic=false, findings: %v", report.Findings)
	}
	if report.Secure {
		t.Fatalf("expected Secure=false for rejected transcript")
	}
	assertFinding(t, report, code)
}
