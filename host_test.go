package netsec

import (
	"net/netip"
	"testing"
	"time"
)

func TestSessionPeerMustEqualFinalSigner(t *testing.T) {
	f := newFixture(t)
	sp := f.signedPath(f.origin, f.mid, f.edge)

	// The update arrives from a different AS than the final signer.
	_, err := f.host.VerifyCryptographic(sp, asRogue)
	assertErrIs(t, err, ErrSessionPeer, "wrong session peer")

	// Correct peer but the host AS is not the named final recipient.
	otherHost := NewHost(HostConfig{
		LocalAS: asRogue, Registry: f.registry,
		MaxPCountPerHop: 4, MaxPathLength: 8,
		Now: func() time.Time { return f.clock },
	})
	_, err = otherHost.VerifyCryptographic(sp, asEdge)
	assertErrIs(t, err, ErrFinalRecipient, "host not named as final recipient")
}

func TestASLoopRejected(t *testing.T) {
	f := newFixture(t)

	// Re-sign a chain in which the origin AS reappears later (a true loop).
	// origin -> mid -> origin, with origin again naming mid as next peer.
	o, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 1, gen1, f.keys[asOrigin].priv)
	if err != nil {
		t.Fatalf("SignOrigin: %v", err)
	}
	m, err := SignHop(f.prefix, f.pathID, o, asMid, asOrigin, 1, gen1, f.keys[asMid].priv)
	if err != nil {
		t.Fatalf("SignHop mid: %v", err)
	}
	loopRec, err := SignHop(f.prefix, f.pathID, m, asOrigin, asHost, 1, gen1, f.keys[asOrigin].priv)
	if err != nil {
		t.Fatalf("SignHop loop: %v", err)
	}
	_, err = f.host.VerifyCryptographic(f.signedPath(o, m, loopRec), asOrigin)
	assertErrIs(t, err, ErrASLoop, "repeated signer AS forms a loop")
}

func TestPrependingConsecutiveRepeatsAllowed(t *testing.T) {
	f := newFixture(t)
	// pCount 3 on one hop yields consecutive repeats (prepending), not a loop.
	o, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 1, gen1, f.keys[asOrigin].priv)
	if err != nil {
		t.Fatalf("SignOrigin: %v", err)
	}
	m, err := SignHop(f.prefix, f.pathID, o, asMid, asEdge, 3, gen1, f.keys[asMid].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	e, err := SignHop(f.prefix, f.pathID, m, asEdge, asHost, 1, gen1, f.keys[asEdge].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	verified, err := f.host.VerifyCryptographic(f.signedPath(o, m, e), asEdge)
	if err != nil {
		t.Fatalf("consecutive prepending should verify: %v", err)
	}
	want := []uint32{asEdge, asMid, asMid, asMid, asOrigin}
	if len(verified.LogicalPath) != len(want) {
		t.Fatalf("logical path = %v, want %v", verified.LogicalPath, want)
	}
	for i := range want {
		if verified.LogicalPath[i] != want[i] {
			t.Fatalf("logical path = %v, want %v", verified.LogicalPath, want)
		}
	}
}

func TestOriginRulesEnforced(t *testing.T) {
	f := newFixture(t)

	// signerAS != originAS even with a valid signature from that signer.
	badOrigin, err := SignOrigin(f.prefix, asMid, asEdge, f.pathID, 1, gen1, f.keys[asMid].priv)
	if err != nil {
		t.Fatalf("SignOrigin: %v", err)
	}
	sp := f.signedPath(badOrigin)
	sp.OriginAS = asOrigin
	_, err = f.host.VerifyCryptographic(sp, asEdge)
	assertErrIs(t, err, ErrBadOrigin, "origin signer must equal originAS")

	// Non-zero previousHash on the first record.
	tampered := cloneRecords([]HopRecord{f.origin, f.mid, f.edge})
	tampered[0].PreviousHash[0] = 0x01
	_, err = f.host.VerifyCryptographic(f.signedPath(tampered...), asEdge)
	if err == nil {
		t.Fatal("origin with non-zero previousHash must fail")
	}

	// Empty chain.
	_, err = f.host.VerifyCryptographic(f.signedPath(), asEdge)
	assertErrIs(t, err, ErrEmptyChain, "empty chain")
}

func TestZeroInstallOnAnyFailure(t *testing.T) {
	f := newFixture(t)
	f.host.policy = staticPolicy{allow: map[netip.Prefix]map[uint32]bool{
		f.prefix: {asEdge: true},
	}}

	// First, a good install populates the RIB.
	if _, err := f.host.Install(f.signedPath(f.origin, f.mid, f.edge), asEdge); err != nil {
		t.Fatalf("baseline install: %v", err)
	}

	// A subsequent invalid update for the same prefix must not change the RIB.
	tampered := cloneRecords([]HopRecord{f.origin, f.mid, f.edge})
	tampered[1].PCount = 4
	if _, err := f.host.Install(f.signedPath(tampered...), asEdge); err == nil {
		t.Fatal("tampered update must fail install")
	}
	route, ok := f.host.InstalledRoute(f.prefix)
	if !ok {
		t.Fatal("original route vanished after failed update")
	}
	if route.PathID != f.pathID {
		t.Fatal("RIB was mutated by a failed install")
	}
	if f.host.InstalledCount() != 1 {
		t.Fatalf("installed count = %d, want 1", f.host.InstalledCount())
	}
}

func TestCryptoValidButBusinessPolicyDenied(t *testing.T) {
	f := newFixture(t)

	// No policy configured: a cryptographically valid chain still does not
	// install, proving the two checks are distinct.
	if _, err := f.host.Install(f.signedPath(f.origin, f.mid, f.edge), asEdge); err == nil {
		t.Fatal("install without business policy must fail")
	} else {
		assertErrIs(t, err, ErrPolicyNotConfigured, "missing policy")
	}

	// Policy explicitly denies this peer even though signatures are perfect.
	f.host.policy = staticPolicy{allow: map[netip.Prefix]map[uint32]bool{
		f.prefix: {asRogue: true}, // asEdge is not permitted
	}}
	verified, err := f.host.VerifyCryptographic(f.signedPath(f.origin, f.mid, f.edge), asEdge)
	if err != nil {
		t.Fatalf("cryptographic verification should pass independently: %v", err)
	}
	if verified == nil {
		t.Fatal("expected verified path from cryptographic check")
	}
	_, err = f.host.Install(f.signedPath(f.origin, f.mid, f.edge), asEdge)
	assertErrIs(t, err, ErrBusinessPolicy, "business policy denial")
	if f.host.InstalledCount() != 0 {
		t.Fatal("policy-denied route must not install")
	}

	// When policy permits, the same chain installs cleanly.
	f.host.policy = staticPolicy{allow: map[netip.Prefix]map[uint32]bool{
		f.prefix: {asEdge: true},
	}}
	if _, err := f.host.Install(f.signedPath(f.origin, f.mid, f.edge), asEdge); err != nil {
		t.Fatalf("policy-allowed route should install: %v", err)
	}
}

func TestNonCanonicalPrefixRejected(t *testing.T) {
	f := newFixture(t)
	dirty := mustPrefix(t, "203.0.113.7/24") // host bits set
	if dirty == f.prefix {
		t.Fatal("test prefix unexpectedly canonical")
	}
	sp := f.signedPath(f.origin, f.mid, f.edge)
	sp.Prefix = dirty
	_, err := f.host.VerifyCryptographic(sp, asEdge)
	assertErrIs(t, err, ErrInvalidPrefix, "non-canonical prefix")
}
