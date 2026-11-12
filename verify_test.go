package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"errors"
	"testing"
)

func validRequest(t *testing.T) AttestationRequest {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), testReader())
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	events := fixtureEvents()
	log := buildEventLog(fixtureBanks(), events)
	replay, err := ReplayEventLog(log)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	selection := []PCRBank{
		{Hash: AlgSHA1, PCRs: []int{0, 1}},
		{Hash: AlgSHA256, PCRs: []int{0, 1}},
	}
	nonce := []byte("freshness-nonce-1234")
	quote := buildQuote(t, nonce, selection, replay, crypto.SHA256)
	sig, err := signQuote(priv, AlgECDSA, AlgSHA256, quote)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	return AttestationRequest{
		Quote:            quote,
		Signature:        sig,
		AKPublic:         encodeECCAKPublic(priv),
		Nonce:            nonce,
		RequestedPCRs:    selection,
		EventLog:         log,
		AllowedSequences: []AllowedSequence{allowedFromReplay(replay, "device-legal")},
	}
}

func TestVerifyValidMixedBanks(t *testing.T) {
	result, err := Verify(validRequest(t))
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if result.DeviceID != "device-legal" {
		t.Fatalf("device id = %q, want device-legal", result.DeviceID)
	}
	if len(result.Replay.Algorithms) != 2 ||
		result.Replay.Algorithms[0] != AlgSHA1 ||
		result.Replay.Algorithms[1] != AlgSHA256 {
		t.Fatalf("algorithms = %v, want SHA-1 then SHA-256", result.Replay.Algorithms)
	}
}

func TestVerifyCrossPCRReorderCaughtBySequence(t *testing.T) {
	req := validRequest(t)
	events := fixtureEvents()
	// Swapping the first events of PCR0 and PCR1 leaves each individual PCR
	// value unchanged, so the Quote digest still matches; the policy layer
	// must reject the altered boot sequence.
	events[0], events[1] = events[1], events[0]
	req.EventLog = buildEventLog(fixtureBanks(), events)

	_, err := Verify(req)
	if !errors.Is(err, ErrNoMatchingIdentity) {
		t.Fatalf("err = %v, want ErrNoMatchingIdentity", err)
	}
}

func TestVerifySamePCRReorderCaughtByDigest(t *testing.T) {
	req := validRequest(t)
	// Reorder the two PCR0 extends. Extend is order-sensitive within a PCR,
	// so the recomputed Quote digest must diverge.
	events := fixtureEvents()
	events[0], events[2] = events[2], events[0]
	req.EventLog = buildEventLog(fixtureBanks(), events)

	_, err := Verify(req)
	if !errors.Is(err, ErrPCRDigestMismatch) {
		t.Fatalf("err = %v, want ErrPCRDigestMismatch", err)
	}
}

func TestVerifyDeletedEventRejected(t *testing.T) {
	req := validRequest(t)
	events := fixtureEvents()
	req.EventLog = buildEventLog(fixtureBanks(), events[:2])

	_, err := Verify(req)
	if !errors.Is(err, ErrPCRDigestMismatch) {
		t.Fatalf("err = %v, want ErrPCRDigestMismatch", err)
	}
}

func TestVerifyForgedLogContentRejected(t *testing.T) {
	req := validRequest(t)
	events := fixtureEvents()
	events[1].data = []byte("tampered-boot-config")
	req.EventLog = buildEventLog(fixtureBanks(), events)

	_, err := Verify(req)
	if !errors.Is(err, ErrPCRDigestMismatch) {
		t.Fatalf("err = %v, want ErrPCRDigestMismatch", err)
	}
}

func TestVerifyBadSignatureRejected(t *testing.T) {
	req := validRequest(t)
	req.Signature[len(req.Signature)-1] ^= 0xFF

	_, err := Verify(req)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("err = %v, want ErrSignature", err)
	}
}

func TestVerifyNonceMismatch(t *testing.T) {
	req := validRequest(t)
	req.Nonce = []byte("stale-nonce")

	_, err := Verify(req)
	if !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("err = %v, want ErrNonceMismatch", err)
	}
}

func TestVerifyPCRSelectionMismatch(t *testing.T) {
	req := validRequest(t)
	req.RequestedPCRs = []PCRBank{
		{Hash: AlgSHA1, PCRs: []int{0}},
		{Hash: AlgSHA256, PCRs: []int{0, 1}},
	}

	_, err := Verify(req)
	if !errors.Is(err, ErrPCRSelectionMismatch) {
		t.Fatalf("err = %v, want ErrPCRSelectionMismatch", err)
	}
}

func TestVerifyNoMatchingIdentity(t *testing.T) {
	req := validRequest(t)
	req.AllowedSequences[0].Measurements[0].Digests[0].Digest[0] ^= 0xFF

	_, err := Verify(req)
	if !errors.Is(err, ErrNoMatchingIdentity) {
		t.Fatalf("err = %v, want ErrNoMatchingIdentity", err)
	}
}

func TestVerifyRSAAK(t *testing.T) {
	cases := []struct {
		name   string
		scheme AlgorithmID
	}{
		{name: "RSAPSS", scheme: AlgRSAPSS},
		{name: "RSASSA", scheme: AlgRSASSA},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runRSAAK(t, tc.scheme)
		})
	}
}

func runRSAAK(t *testing.T, scheme AlgorithmID) {
	req := validRequest(t)
	replay, err := ReplayEventLog(req.EventLog)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	priv, err := rsa.GenerateKey(testReader(), 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	quote := buildQuote(t, req.Nonce, req.RequestedPCRs, replay, crypto.SHA256)
	sig, err := signQuote(priv, scheme, AlgSHA256, quote)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	req.Quote = quote
	req.Signature = sig
	req.AKPublic = encodeRSAAKPublic(priv)

	result, err := Verify(req)
	if err != nil {
		t.Fatalf("Verify with RSA AK (%v) failed: %v", scheme, err)
	}
	if result.DeviceID != "device-legal" {
		t.Fatalf("device id = %q", result.DeviceID)
	}
}
