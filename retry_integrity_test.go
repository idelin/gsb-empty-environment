package quicguard_test

import (
	"bytes"
	"errors"
	"testing"

	"formal.local/w27-86968a8181"
)

func TestRetryPacketIntegrityRoundTrip(t *testing.T) {
	g, _ := newTestGuard(t)

	origDCID := []byte{0xde, 0xad, 0xbe, 0xef}
	clientSCID := []byte{0xca, 0xfe}
	retrySCID := []byte{0x0a, 0x0b, 0x0c, 0x0d}
	token := bytes.Repeat([]byte{0x33}, 40)

	packet, err := g.BuildRetryPacket(origDCID, clientSCID, retrySCID, token)
	if err != nil {
		t.Fatal(err)
	}

	h, err := g.VerifyRetryPacket(packet, origDCID)
	if err != nil {
		t.Fatalf("valid Retry packet failed integrity: %v", err)
	}
	if h.Type != quicguard.TypeRetry || !bytes.Equal(h.SCID, retrySCID) ||
		!bytes.Equal(h.DCID, clientSCID) || !bytes.Equal(h.Token, token) {
		t.Fatalf("parsed Retry header mismatch: %+v", h)
	}

	// Any mutation of the tag or header must fail with constant-time mismatch.
	corrupt := append([]byte(nil), packet...)
	corrupt[len(corrupt)-1] ^= 0xff
	if _, err := g.VerifyRetryPacket(corrupt, origDCID); !errors.Is(err, quicguard.ErrRetryIntegrity) {
		t.Fatalf("tag tampering accepted: %v", err)
	}

	// A different original DCID changes the pseudo packet and must fail.
	otherDCID := []byte{0x01, 0x02, 0x03, 0x04}
	if _, err := g.VerifyRetryPacket(packet, otherDCID); !errors.Is(err, quicguard.ErrRetryIntegrity) {
		t.Fatalf("wrong original DCID accepted: %v", err)
	}

	// Header mutation invalidates the tag.
	corruptHeader := append([]byte(nil), packet...)
	corruptHeader[6] ^= 0x01
	if _, err := g.VerifyRetryPacket(corruptHeader, origDCID); !errors.Is(err, quicguard.ErrRetryIntegrity) {
		t.Fatalf("header tampering accepted: %v", err)
	}
}

func TestRetryTokenBindingMismatch(t *testing.T) {
	g, clock := newTestGuard(t)
	src := mustAddr(t, "198.51.100.33:443")

	token, err := g.MintRetryToken(&quicguard.RetryTokenClaims{
		ClientIP:     src.Addr(),
		OriginalDCID: clientDCID,
		RetrySCID:    serverSCID,
		IssuedAt:     clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The Initial DCID must equal the token's RetrySCID; using another DCID is
	// a binding failure and must not validate the source address.
	wrongDCID := []byte{0xff, 0xee, 0xdd, 0xcc}
	packet := initialPacket(wrongDCID, []byte{0xc0}, token, 1200)
	result, err := g.OnDatagram(src, packet)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(result.TokenErr, quicguard.ErrTokenBinding) || result.Validated {
		t.Fatalf("CID binding not enforced: tokenErr=%v validated=%v", result.TokenErr, result.Validated)
	}

	// The token nonce was consumed by the successful AEAD/IP/expiry checks even
	// though the CID binding failed; replay protection still applies.
	if _, err := g.VerifyRetryToken(token, src.Addr()); !errors.Is(err, quicguard.ErrTokenReplay) {
		t.Fatalf("replay after binding failure not blocked: %v", err)
	}

	// A correctly bound Initial validates and drains nothing (queue empty).
	token2, err := g.MintRetryToken(&quicguard.RetryTokenClaims{
		ClientIP:     src.Addr(),
		OriginalDCID: clientDCID,
		RetrySCID:    serverSCID,
		IssuedAt:     clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	good := initialPacket(serverSCID, []byte{0xc0}, token2, 1200)
	result, err = g.OnDatagram(src, good)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.TokenErr; err != nil || !result.Validated || result.ValidatedBy != "retry-token" {
		t.Fatalf("valid token did not validate: err=%v result=%+v", err, result)
	}
}
