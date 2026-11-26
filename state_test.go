package quicguard_test

import (
	"errors"
	"testing"
	"time"

	"formal.local/w27-86968a8181"
)

func TestReplayEntryExpiresAfterTTL(t *testing.T) {
	g, clock := newTestGuard(t)
	src := mustAddr(t, "198.51.100.77:443")

	token, err := g.MintRetryToken(&quicguard.RetryTokenClaims{
		ClientIP:     src.Addr(),
		OriginalDCID: clientDCID,
		RetrySCID:    serverSCID,
		IssuedAt:     clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.VerifyRetryToken(token, src.Addr()); err != nil {
		t.Fatalf("first use failed: %v", err)
	}
	if _, err := g.VerifyRetryToken(token, src.Addr()); !errors.Is(err, quicguard.ErrTokenReplay) {
		t.Fatalf("immediate reuse not treated as replay: %v", err)
	}

	// After the TTL the nonce is swept on the next verify, so a token whose
	// own timestamp is also expired fails with ErrTokenExpired rather than
	// replay. This proves expired entries cannot pin the replay table forever.
	clock.Advance(11 * time.Second)
	_, err = g.VerifyRetryToken(token, src.Addr())
	if !errors.Is(err, quicguard.ErrTokenExpired) {
		t.Fatalf("expired token error = %v, want ErrTokenExpired", err)
	}
}

func TestIndependentPerSourceBudgets(t *testing.T) {
	g, _ := newTestGuard(t)
	a := mustAddr(t, "198.51.100.1:1000")
	b := mustAddr(t, "198.51.100.2:1000") // distinct IP => distinct budget

	if _, err := g.OnDatagram(a, zeroRTTPacket(clientDCID, serverSCID, 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.OnDatagram(b, zeroRTTPacket(clientDCID, serverSCID, 40)); err != nil {
		t.Fatal(err)
	}

	// Path A may spend up to 300; path B only up to 120.
	if _, err := g.Send(a, make([]byte, 300)); err != nil {
		t.Fatalf("path A budget wrong: %v", err)
	}
	if _, err := g.Send(b, make([]byte, 121)); !errors.Is(err, quicguard.ErrAmplificationLimited) {
		t.Fatalf("path B shared path A budget: %v", err)
	}
	if snap := g.Snapshot(a); snap.TXBytes != 300 || snap.Validated {
		t.Fatalf("path A state wrong: %+v", snap)
	}
}

func TestNewGuardRejectsWeakKeys(t *testing.T) {
	clock := newFakeClock()
	if _, err := quicguard.NewGuard(quicguard.Config{
		TokenAEADKey: make([]byte, 31),
		ResetHMACKey: make([]byte, 32),
		Now:          clock.Now,
	}); !errors.Is(err, quicguard.ErrMalformed) {
		t.Fatalf("short AEAD key accepted: %v", err)
	}
	if _, err := quicguard.NewGuard(quicguard.Config{
		TokenAEADKey: make([]byte, 32),
		ResetHMACKey: make([]byte, 31),
		Now:          clock.Now,
	}); !errors.Is(err, quicguard.ErrMalformed) {
		t.Fatalf("short HMAC key accepted: %v", err)
	}
}

func TestDeterministicRandomness(t *testing.T) {
	clock := newFakeClock()
	cfg := quicguard.Config{
		TokenAEADKey: make([]byte, 32),
		ResetHMACKey: make([]byte, 32),
		TokenTTL:     time.Minute,
		Now:          clock.Now,
	}
	cfg.Random = &sequenceReader{}
	g1, err := quicguard.NewGuard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Random = &sequenceReader{}
	g2, err := quicguard.NewGuard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	claims := &quicguard.RetryTokenClaims{
		ClientIP:     mustAddr(t, "198.51.100.9:443").Addr(),
		OriginalDCID: clientDCID,
		RetrySCID:    serverSCID,
		IssuedAt:     clock.Now(),
	}
	t1, err := g1.MintRetryToken(claims)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := g2.MintRetryToken(claims)
	if err != nil {
		t.Fatal(err)
	}
	if string(t1) != string(t2) {
		t.Fatalf("deterministic RNG produced different nonces:\n%x\n%x", t1, t2)
	}
}
