package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"testing"
)

// TestQuoteWithForgedDigestRejected signs a structurally valid Quote whose
// embedded PCR digest no longer matches the replayed log. This proves signature
// verification alone cannot authenticate the boot measurements.
func TestQuoteWithForgedDigestRejected(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), testReader())
	if err != nil {
		t.Fatalf("generate key: %v", err)
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
	// Flip the final byte of the embedded pcrDigest (last byte of the blob),
	// then re-sign so the signature itself stays valid.
	quote[len(quote)-1] ^= 0xFF
	sig, err := signQuote(priv, AlgECDSA, AlgSHA256, quote)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	req := AttestationRequest{
		Quote:            quote,
		Signature:        sig,
		AKPublic:         encodeECCAKPublic(priv),
		Nonce:            nonce,
		RequestedPCRs:    selection,
		EventLog:         log,
		AllowedSequences: []AllowedSequence{allowedFromReplay(replay, "device-legal")},
	}

	_, err = Verify(req)
	if !errors.Is(err, ErrPCRDigestMismatch) {
		t.Fatalf("err = %v, want ErrPCRDigestMismatch", err)
	}
}

// TestWrongAKRejected ensures a Quote signed by another key is refused.
func TestWrongAKRejected(t *testing.T) {
	req := validRequest(t)
	other, err := ecdsa.GenerateKey(elliptic.P256(), testReader())
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	req.AKPublic = encodeECCAKPublic(other)

	_, err = Verify(req)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("err = %v, want ErrSignature", err)
	}
}
