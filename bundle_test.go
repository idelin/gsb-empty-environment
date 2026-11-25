package workloadid

import (
	"crypto/x509"
	"errors"
	"net/url"
	"testing"
	"time"
)

// TestGenerationAtomicSwap ensures a bundle activation is observed as one whole
// snapshot and older generations cannot overwrite newer ones.
func TestGenerationAtomicSwap(t *testing.T) {
	f := newFixture(t)
	snap, err := f.manager.Current()
	if err != nil {
		t.Fatal(err)
	}

	newRoot2, err := newRoot("rotated-root")
	if err != nil {
		t.Fatal(err)
	}
	newInter2, err := newIntermediate(newRoot2, "rotated-inter", leafOptions{serial: 9, ttl: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}

	// Stale generation must be rejected.
	if _, err := f.manager.Activate(snap.Generation-1, snap.Config, RootBundle{
		Roots: []*x509.Certificate{newRoot2.cert},
	}); !errors.Is(err, ErrGenerationStale) {
		t.Fatalf("expected ErrGenerationStale, got %v", err)
	}

	// Credential survives a bundle rotation (carried over atomically).
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)

	if _, err := f.manager.Activate(snap.Generation+1, snap.Config, RootBundle{
		Roots:         []*x509.Certificate{newRoot2.cert},
		Intermediates: []*x509.Certificate{newInter2.cert},
	}); err != nil {
		t.Fatal(err)
	}
	active := f.manager.MustCurrent()
	if active.Generation != snap.Generation+1 {
		t.Fatalf("generation = %d", active.Generation)
	}
	if active.Credential == nil || active.Credential.Leaf.SerialNumber != local.cert.SerialNumber {
		t.Fatal("credential must be carried across bundle generation")
	}
	// A verification sees the new roots consistently, never a half-merged pool.
	if subjects := active.Roots.Subjects(); len(subjects) != 1 {
		t.Fatalf("expected exactly one root in new generation, got %d", len(subjects))
	}
}

// TestShortLivedRotationEnforced rejects credentials beyond the TTL ceiling and
// accepts normal short-lived rotation.
func TestShortLivedRotationEnforced(t *testing.T) {
	f := newFixture(t)

	// Too long lived (> MaxLeafTTL of 24h).
	longLived, err := newLeaf(f.inter, leafOptions{
		serial: 50, ttl: 48 * time.Hour, uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.RotateCredential(2, WorkloadCredential{
		Leaf: longLived.cert, PrivateKey: longLived.key,
	}); !errors.Is(err, ErrShortLivedTTL) {
		t.Fatalf("expected ErrShortLivedTTL, got %v", err)
	}

	// A short-lived credential rotates cleanly.
	short, err := newLeaf(f.inter, leafOptions{
		serial: 51, ttl: 30 * time.Minute, uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := f.manager.RotateCredential(2, WorkloadCredential{
		Leaf: short.cert, PrivateKey: short.key,
	})
	if err != nil {
		t.Fatalf("short-lived rotation should succeed: %v", err)
	}
	if snap.Credential.Leaf.SerialNumber.Int64() != 51 {
		t.Fatal("rotated credential not active")
	}

	// An older rotation generation cannot roll the credential back.
	if _, err := f.manager.RotateCredential(1, WorkloadCredential{
		Leaf: short.cert, PrivateKey: short.key,
	}); !errors.Is(err, ErrGenerationStale) {
		t.Fatalf("expected ErrGenerationStale on rollback, got %v", err)
	}
}

// TestManagerClockRollbackIntercepted ensures a backwards wall clock blocks
// generation activation.
func TestManagerClockRollbackIntercepted(t *testing.T) {
	f := newFixture(t)
	snap := f.manager.MustCurrent()
	f.clock.Advance(-time.Minute)
	if _, err := f.manager.Activate(snap.Generation+1, snap.Config, RootBundle{
		Roots: []*x509.Certificate{f.root.cert},
	}); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("expected ErrClockRollback, got %v", err)
	}
}

// TestChainDepthEnforced rejects chains longer than the configured depth.
func TestChainDepthEnforced(t *testing.T) {
	f := newFixture(t)
	// Build an extra intermediate to lengthen the chain.
	second, err := newIntermediate(f.inter, "second-inter", leafOptions{serial: 60, ttl: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := newLeaf(second, leafOptions{
		serial: 61, ttl: time.Hour, uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Tighten the depth limit and add the extra intermediate.
	snap := f.manager.MustCurrent()
	cfg := snap.Config
	cfg.MaxChainDepth = 2
	pool := x509.NewCertPool()
	pool.AddCert(f.inter.cert)
	pool.AddCert(second.cert)
	if _, err := f.manager.Activate(snap.Generation+1, cfg, RootBundle{
		Roots:         []*x509.Certificate{f.root.cert},
		Intermediates: []*x509.Certificate{f.inter.cert, second.cert},
		Revocation:    snap.Revocation,
	}); err != nil {
		t.Fatal(err)
	}
	// Chain here is leaf -> second -> inter -> root (4 certs).
	_, _, err = f.verifier.Authenticate(derChain(leaf.cert, second.cert, f.inter.cert))
	if !errors.Is(err, ErrChainDepth) {
		t.Fatalf("expected ErrChainDepth, got %v", err)
	}
}

// TestNoSnapshotFailsClosed ensures verification fails before any bundle exists.
func TestNoSnapshotFailsClosed(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC))
	manager := NewManager(clock)
	verifier := NewVerifier(manager, WithClock(clock))
	if _, _, err := verifier.Authenticate([][]byte{{1, 2, 3}}); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("expected ErrNoSnapshot, got %v", err)
	}
}
