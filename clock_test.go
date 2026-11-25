package workloadid

import (
	"errors"
	"net/url"
	"testing"
	"time"
)

// TestVerifierClockRollbackAcrossConnections ensures a clock that moves
// backwards between two handshakes blocks the second verification.
func TestVerifierClockRollbackAcrossConnections(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)
	leaf := f.prodLeaf(leafOptions{serial: 80, uris: []*url.URL{mustURI(spiffeAPI)}})

	if _, err := f.verifier.Authorize(AuthRequest{
		RawCerts:     derChain(leaf.cert, f.inter.cert),
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attestation:  attestationClaim(),
	}); err != nil {
		t.Fatalf("first connection should succeed: %v", err)
	}

	// Roll the wall clock backwards. The manager-level guard also trips, so move
	// the clock back through a fresh verifier that only exercises the
	// cross-connection high-water mark.
	f.clock.Advance(-time.Minute)
	verifier := NewVerifier(f.manager, WithClock(f.clock))
	verifier.wallHighWater.store(f.clock.Now().Add(time.Minute))
	_, err := verifier.Authorize(AuthRequest{
		RawCerts:     derChain(leaf.cert, f.inter.cert),
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attestation:  attestationClaim(),
	})
	if !errors.Is(err, ErrClockRollback) {
		t.Fatalf("expected ErrClockRollback across connections, got %v", err)
	}
}
