package netsec

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestCrossSessionSplice(t *testing.T) {
	// Build a second session and splice its confirmation into the first.
	other := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionTwo, []string{algWeak}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionTwo, []string{algWeak}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionTwo, algWeak), true, ""),
	}
	other = append(other, confirmEnvelope(t, other, peerA, sessionTwo, algWeak, true))

	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algStrong, algMedium}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algStrong, algMedium}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algStrong), true, ""),
		other[3], // validly authenticated, but from another session
	}
	envs = append(envs, confirmEnvelope(t, envs, peerB, sessionOne, algStrong, true))

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("spliced transcript reported secure")
	}
	if !report.HasError(FindingConfirmationHashMismatch) {
		t.Errorf("expected %s finding for spliced confirmation", FindingConfirmationHashMismatch)
	}
	if !report.HasError(FindingSessionMismatch) {
		t.Errorf("expected %s finding for foreign session id", FindingSessionMismatch)
	}
}

func TestSpliceSameSessionIDDifferentDigest(t *testing.T) {
	// Attacker reuses the session id so only the transcript binding can
	// expose the splice.
	otherPrefix := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algWeak}), true, ""),
	}
	fakeConfirm := confirmEnvelope(t, otherPrefix, peerA, sessionOne, algStrong, true)

	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algStrong}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algStrong}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algStrong), true, ""),
		fakeConfirm,
	}
	envs = append(envs, confirmEnvelope(t, envs, peerB, sessionOne, algStrong, true))

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("digest-forged transcript reported secure")
	}
	if !report.HasError(FindingConfirmationHashMismatch) {
		t.Errorf("expected %s finding", FindingConfirmationHashMismatch)
	}
	if report.HasError(FindingSessionMismatch) {
		t.Errorf("session ids match; mismatch finding is a false positive")
	}
}

func TestSelectionOutsideIntersection(t *testing.T) {
	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algStrong, algMedium}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algMedium, algWeak}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algStrong), true, ""),
	}
	envs = append(envs, confirmEnvelope(t, envs, peerA, sessionOne, algStrong, true))
	envs = append(envs, confirmEnvelope(t, envs, peerB, sessionOne, algStrong, true))

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("out-of-intersection selection reported secure")
	}
	if !report.HasError(FindingSelectionNotInCapabilities) {
		t.Errorf("expected %s finding", FindingSelectionNotInCapabilities)
	}
	if got := strings.Join(report.Intersection, ","); got != algMedium {
		t.Errorf("intersection = %q, want %q", got, algMedium)
	}
}

func TestPreferenceViolation(t *testing.T) {
	// Both peers authenticated strong support, but the log selects a weaker
	// algorithm: the downgrade against authenticated claims is detected.
	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algStrong, algMedium}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algStrong, algMedium}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algMedium), true, ""),
	}
	envs = append(envs, confirmEnvelope(t, envs, peerA, sessionOne, algMedium, true))
	envs = append(envs, confirmEnvelope(t, envs, peerB, sessionOne, algMedium, true))

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("preference violation reported secure")
	}
	if !report.HasError(FindingPreferenceViolation) {
		t.Errorf("expected %s finding", FindingPreferenceViolation)
	}
}

func TestDowngradeWeakOnlyClaimUnauthenticated(t *testing.T) {
	// The log claims both peers support only the weak algorithm, but every
	// capability entry is unauthenticated. The weak-only story is a clue at
	// best and must never be treated as proven.
	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algWeak}), false, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algWeak}), false, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algWeak), false, ""),
	}

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("fully unauthenticated transcript reported secure")
	}
	if !report.HasError(FindingMissingCapabilities) || !report.HasError(FindingMissingSelection) {
		t.Errorf("expected missing authenticated caps/selection findings, got %+v", report.Findings)
	}
	for _, peer := range []string{peerA, peerB} {
		clues := report.UnauthenticatedCapabilities[peer]
		if len(clues) != 1 || clues[0] != algWeak {
			t.Errorf("peer %s clues = %v, want [%s]", peer, clues, algWeak)
		}
		if _, ok := report.AuthenticatedCapabilities[peer]; ok {
			t.Errorf("peer %s must have no authenticated capabilities", peer)
		}
	}
}

func TestSignerMismatch(t *testing.T) {
	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algStrong}), true, peerB),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algStrong}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algStrong), true, ""),
	}
	envs = append(envs, confirmEnvelope(t, envs, peerA, sessionOne, algStrong, true))
	envs = append(envs, confirmEnvelope(t, envs, peerB, sessionOne, algStrong, true))

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("signer mismatch transcript reported secure")
	}
	if !report.HasError(FindingSignerMismatch) {
		t.Errorf("expected %s finding", FindingSignerMismatch)
	}
	if !report.HasError(FindingMissingCapabilities) {
		t.Errorf("peer A's forged message must not count as authenticated capabilities")
	}
}

func TestConfirmationAlgorithmMismatch(t *testing.T) {
	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algStrong, algMedium}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algStrong, algMedium}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algStrong), true, ""),
	}
	envs = append(envs, confirmEnvelope(t, envs, peerA, sessionOne, algMedium, true))
	envs = append(envs, confirmEnvelope(t, envs, peerB, sessionOne, algStrong, true))

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("algorithm-mismatched confirmation reported secure")
	}
	if !report.HasError(FindingConfirmationAlgorithmMismatch) {
		t.Errorf("expected %s finding", FindingConfirmationAlgorithmMismatch)
	}
}

func TestMissingConfirmation(t *testing.T) {
	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algStrong}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algStrong}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algStrong), true, ""),
		confirmEnvelope(t, nil, peerA, sessionOne, algStrong, true),
	}

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("one-sided confirmation reported secure")
	}
	if !report.HasError(FindingMissingConfirmation) {
		t.Errorf("expected %s finding", FindingMissingConfirmation)
	}
}

func TestMalformedMessage(t *testing.T) {
	envs := []Envelope{
		{Raw: []byte("{not json"), Auth: AuthMark{Authenticated: true}},
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algStrong}), true, ""),
	}

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("malformed transcript reported secure")
	}
	if !report.HasError(FindingMalformedMessage) {
		t.Errorf("expected %s finding", FindingMalformedMessage)
	}
}

func TestLateCapabilityMessageIsOrderViolation(t *testing.T) {
	// A capability announcement spliced in after the selection phase must be
	// flagged as an ordering violation even when unauthenticated, instead of
	// silently advancing the phase.
	envs := []Envelope{
		mustEnvelope(t, capsMessage(peerA, sessionOne, []string{algStrong}), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algStrong}), true, ""),
		mustEnvelope(t, selectionMessage(peerA, sessionOne, algStrong), true, ""),
		mustEnvelope(t, capsMessage(peerB, sessionOne, []string{algWeak}), false, ""),
	}
	envs = append(envs, confirmEnvelope(t, envs, peerA, sessionOne, algStrong, true))
	envs = append(envs, confirmEnvelope(t, envs, peerB, sessionOne, algStrong, true))

	report := checkReport(t, envs)
	if report.Secure {
		t.Fatalf("spliced late capability message reported secure")
	}
	if !report.HasError(FindingOrderViolation) {
		t.Errorf("expected %s finding", FindingOrderViolation)
	}
	if !report.HasError(FindingUnauthenticated) {
		t.Errorf("expected %s finding", FindingUnauthenticated)
	}
}

func TestUnknownFieldsAndBadShapes(t *testing.T) {
	cases := map[string][]byte{
		"unknown field":        []byte(`{"type":"selection","sender":"a","session_id":"s","selection":"x","evil":1}`),
		"unknown type":         []byte(`{"type":"handshake","sender":"a","session_id":"s"}`),
		"empty sender":         []byte(`{"type":"selection","sender":"","session_id":"s","selection":"x"}`),
		"empty session":        []byte(`{"type":"selection","sender":"a","session_id":"","selection":"x"}`),
		"empty caps":           []byte(`{"type":"capabilities","sender":"a","session_id":"s","capabilities":[]}`),
		"missing confirmation": []byte(`{"type":"confirmation","sender":"a","session_id":"s"}`),
		"empty raw":            nil,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeMessage(raw); err == nil {
				t.Fatalf("expected decode error")
			}
		})
	}
}

func TestDigestChangesWithRawBytes(t *testing.T) {
	base := validEnvelopes(t)
	d1 := hex.EncodeToString(TranscriptDigest(base))

	mutated := make([]Envelope, len(base))
	copy(mutated, base)
	msg, err := DecodeMessage(base[0].Raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	msg.Capabilities = append(msg.Capabilities, algWeak)
	mutated[0].Raw, err = EncodeMessage(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	d2 := hex.EncodeToString(TranscriptDigest(mutated))

	if d1 == d2 {
		t.Fatalf("digest must change when raw bytes change")
	}
	if got := hex.EncodeToString(PrefixDigest(base, len(base))); got != d1 {
		t.Errorf("PrefixDigest over all envelopes must equal TranscriptDigest")
	}
}

func TestInvalidTranscriptInputs(t *testing.T) {
	if _, err := Check(nil); err == nil {
		t.Errorf("nil transcript must error")
	}
	if _, err := Check(&Transcript{
		PreferenceOrder: []string{algStrong, algStrong},
	}); err == nil {
		t.Errorf("duplicate preference entries must error")
	}
	if _, err := Check(&Transcript{
		PreferenceOrder: []string{algStrong, ""},
	}); err == nil {
		t.Errorf("empty preference entry must error")
	}
}
