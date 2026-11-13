package netsec

import (
	"crypto/ed25519"
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestValidChainVerifiesAndInstalls(t *testing.T) {
	f := newFixture(t)
	f.host.policy = staticPolicy{allow: map[netip.Prefix]map[uint32]bool{
		f.prefix: {asEdge: true},
	}}

	sp := f.signedPath(f.origin, f.mid, f.edge)
	verified, err := f.host.Install(sp, asEdge)
	if err != nil {
		t.Fatalf("install valid chain: %v", err)
	}

	// BGP receiver-to-origin order: edge(1) + mid prepend(2) + origin(1).
	wantLogical := []uint32{asEdge, asMid, asMid, asOrigin}
	if len(verified.LogicalPath) != len(wantLogical) {
		t.Fatalf("logical path length = %d, want %d", len(verified.LogicalPath), len(wantLogical))
	}
	for i := range wantLogical {
		if verified.LogicalPath[i] != wantLogical[i] {
			t.Fatalf("logical path = %v, want %v", verified.LogicalPath, wantLogical)
		}
	}
	wantChain := []uint32{asOrigin, asMid, asEdge}
	for i := range wantChain {
		if verified.ChainOrder[i] != wantChain[i] {
			t.Fatalf("chain order = %v, want %v", verified.ChainOrder, wantChain)
		}
	}
	if len(verified.Evidence) != 3 {
		t.Fatalf("evidence count = %d, want 3", len(verified.Evidence))
	}
	for i, ev := range verified.Evidence {
		want := RecordDigest(f.prefix, f.pathID, sp.Records[i])
		if ev.Digest != want {
			t.Fatalf("evidence[%d] digest mismatch", i)
		}
		if len(ev.Record.Signature) != ed25519.SignatureSize {
			t.Fatalf("evidence[%d] missing real signature", i)
		}
	}
	if got := f.host.InstalledCount(); got != 1 {
		t.Fatalf("installed count = %d, want 1", got)
	}
	if _, ok := f.host.InstalledRoute(f.prefix); !ok {
		t.Fatal("route not present in RIB")
	}
}

func TestDeleteHopBreaksLinkage(t *testing.T) {
	f := newFixture(t)
	// Drop the middle hop: the edge record still binds the removed digest.
	sp := f.signedPath(f.origin, f.edge)
	_, err := f.host.VerifyCryptographic(sp, asEdge)
	assertErrIs(t, err, ErrPreviousHash, "delete middle hop")
	if f.host.InstalledCount() != 0 {
		t.Fatal("chain must not install after failed verification")
	}
}

func TestSwapNextPeerNotAuthorized(t *testing.T) {
	f := newFixture(t)

	// Variant 1: tamper the final nextPeerAS without producing a valid sig.
	tampered := cloneRecords([]HopRecord{f.origin, f.mid, f.edge})
	tampered[2].NextPeerAS = asRogue
	_, err := f.host.VerifyCryptographic(f.signedPath(tampered...), asEdge)
	assertErrIs(t, err, ErrBadSignature, "tampered nextPeer without re-sign")

	// Variant 2: a genuinely valid chain addressed to a different neighbor must
	// still be rejected at our AS (the next-hop binding blocks the redirect).
	retOrigin, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 1, gen1, f.keys[asOrigin].priv)
	if err != nil {
		t.Fatalf("SignOrigin: %v", err)
	}
	retMid, err := SignHop(f.prefix, f.pathID, retOrigin, asMid, asEdge, 1, gen1, f.keys[asMid].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	retEdge, err := SignHop(f.prefix, f.pathID, retMid, asEdge, asRogue, 1, gen1, f.keys[asEdge].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	_, err = f.host.VerifyCryptographic(f.signedPath(retOrigin, retMid, retEdge), asEdge)
	assertErrIs(t, err, ErrFinalRecipient, "valid chain addressed to another neighbor")

	// Variant 3: the edge AS cannot even re-sign a hop that retargets the
	// receiver, because SignHop requires signer == previous nextPeer.
	if _, err := SignHop(f.prefix, f.pathID, f.mid, asRogue, asHost, 1, gen1, f.keys[asEdge].priv); !errors.Is(err, ErrNextPeerMismatch) {
		t.Fatalf("unauthorized signer: expected ErrNextPeerMismatch, got %v", err)
	}
}

func TestReorderRecordsRejected(t *testing.T) {
	f := newFixture(t)
	_, err := f.host.VerifyCryptographic(f.signedPath(f.mid, f.origin, f.edge), asEdge)
	if err == nil {
		t.Fatal("reordered chain must not verify")
	}
	if !errors.Is(err, ErrBadOrigin) && !errors.Is(err, ErrPreviousHash) {
		t.Fatalf("reordered chain: expected origin/hash error, got %v", err)
	}
}

func TestCrossPrefixCarryRejected(t *testing.T) {
	f := newFixture(t)
	other := mustPrefix(t, "198.51.100.0/24")

	// A fully valid chain for a different prefix, replayed under our prefix.
	o, err := SignOrigin(other, asOrigin, asMid, f.pathID, 1, gen1, f.keys[asOrigin].priv)
	if err != nil {
		t.Fatalf("SignOrigin: %v", err)
	}
	m, err := SignHop(other, f.pathID, o, asMid, asEdge, 1, gen1, f.keys[asMid].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	e, err := SignHop(other, f.pathID, m, asEdge, asHost, 1, gen1, f.keys[asEdge].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	_, err = f.host.VerifyCryptographic(f.signedPath(o, m, e), asEdge)
	assertErrIs(t, err, ErrBadSignature, "whole chain carried across prefixes")

	// Splice one foreign-prefix record into our chain.
	_, err = f.host.VerifyCryptographic(f.signedPath(f.origin, m, f.edge), asEdge)
	if err == nil {
		t.Fatal("spliced foreign-prefix record must not verify")
	}
	if !errors.Is(err, ErrBadSignature) && !errors.Is(err, ErrPreviousHash) {
		t.Fatalf("spliced record: expected signature/hash error, got %v", err)
	}

	// Replaying the same chain under a different pathID also breaks every sig.
	swapped := f.signedPath(f.origin, f.mid, f.edge)
	swapped.PathID = PathID(9999)
	_, err = f.host.VerifyCryptographic(swapped, asEdge)
	assertErrIs(t, err, ErrBadSignature, "chain replayed under different pathID")
}

func TestPCountInflationRejected(t *testing.T) {
	f := newFixture(t)

	// Tamper pCount to another in-range value without a fresh signature: the
	// signed transcript changes, so the Ed25519 signature no longer matches.
	tampered := cloneRecords([]HopRecord{f.origin, f.mid, f.edge})
	tampered[1].PCount = 3
	_, err := f.host.VerifyCryptographic(f.signedPath(tampered...), asEdge)
	assertErrIs(t, err, ErrBadSignature, "inflated pCount breaks signature")

	// Pushing pCount beyond the host maximum (even with a copied signature) is
	// rejected by the explicit range bound.
	overCap := cloneRecords([]HopRecord{f.origin, f.mid, f.edge})
	overCap[1].PCount = 5
	_, err = f.host.VerifyCryptographic(f.signedPath(overCap...), asEdge)
	assertErrIs(t, err, ErrPCountRange, "pCount beyond host cap")

	// A correctly-signed pCount above the host per-hop maximum is rejected.
	bigOrigin, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 8, gen1, f.keys[asOrigin].priv)
	if err != nil {
		t.Fatalf("SignOrigin: %v", err)
	}
	_, err = f.host.VerifyCryptographic(f.signedPath(bigOrigin), asEdge)
	assertErrIs(t, err, ErrPCountRange, "pCount above host maximum")

	// A validly signed chain whose expanded path exceeds the length cap.
	lo, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 4, gen1, f.keys[asOrigin].priv)
	if err != nil {
		t.Fatalf("SignOrigin: %v", err)
	}
	lm, err := SignHop(f.prefix, f.pathID, lo, asMid, asEdge, 4, gen1, f.keys[asMid].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	le, err := SignHop(f.prefix, f.pathID, lm, asEdge, asHost, 4, gen1, f.keys[asEdge].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	_, err = f.host.VerifyCryptographic(f.signedPath(lo, lm, le), asEdge)
	assertErrIs(t, err, ErrPathTooLong, "expanded path over length cap")
	if f.host.InstalledCount() != 0 {
		t.Fatal("inflated paths must not install")
	}
}

func TestKeyRotationRevokedExpiredAndUnknown(t *testing.T) {
	f := newFixture(t)

	// Rotate the origin AS to generation 2 and sign a chain with the new key.
	newKey := genKey(t)
	registerKey(t, f.registry, asOrigin, gen2, newKey, time.Time{}, time.Time{})
	o2, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 1, gen2, newKey.priv)
	if err != nil {
		t.Fatalf("SignOrigin gen2: %v", err)
	}
	m2, err := SignHop(f.prefix, f.pathID, o2, asMid, asEdge, 1, gen1, f.keys[asMid].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	e2, err := SignHop(f.prefix, f.pathID, m2, asEdge, asHost, 1, gen1, f.keys[asEdge].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	if _, err := f.host.VerifyCryptographic(f.signedPath(o2, m2, e2), asEdge); err != nil {
		t.Fatalf("rotated generation should verify before revocation: %v", err)
	}

	// Revoking generation 2 must immediately invalidate new and old updates.
	if err := f.registry.Revoke(asOrigin, gen2); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err = f.host.VerifyCryptographic(f.signedPath(o2, m2, e2), asEdge)
	assertErrIs(t, err, ErrRevokedKey, "revoked generation rejected")

	// Generation 1 remains trusted and still verifies after the rotation.
	if _, err := f.host.VerifyCryptographic(f.signedPath(f.origin, f.mid, f.edge), asEdge); err != nil {
		t.Fatalf("unrevoked generation 1 should still verify: %v", err)
	}

	// An unregistered (unknown) generation cannot resolve a key.
	o3, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 1, 77, newKey.priv)
	if err != nil {
		t.Fatalf("SignOrigin unknown gen: %v", err)
	}
	_, err = f.host.VerifyCryptographic(f.signedPath(o3), asEdge)
	assertErrIs(t, err, ErrUnknownKey, "unknown generation rejected")

	// An expired key is rejected based on the host clock.
	expReg := NewKeyRegistry()
	expKey := genKey(t)
	expiredAt := f.clock.Add(-time.Minute)
	registerKey(t, expReg, asOrigin, gen1, expKey, time.Time{}, expiredAt)
	registerKey(t, expReg, asMid, gen1, f.keys[asMid], time.Time{}, time.Time{})
	registerKey(t, expReg, asEdge, gen1, f.keys[asEdge], time.Time{}, time.Time{})
	expHost := NewHost(HostConfig{
		LocalAS: asHost, Registry: expReg,
		MaxPCountPerHop: 4, MaxPathLength: 8,
		Now: func() time.Time { return f.clock },
	})
	xo, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 1, gen1, expKey.priv)
	if err != nil {
		t.Fatalf("SignOrigin expired: %v", err)
	}
	xm, err := SignHop(f.prefix, f.pathID, xo, asMid, asEdge, 1, gen1, f.keys[asMid].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	xe, err := SignHop(f.prefix, f.pathID, xm, asEdge, asHost, 1, gen1, f.keys[asEdge].priv)
	if err != nil {
		t.Fatalf("SignHop: %v", err)
	}
	_, err = expHost.VerifyCryptographic(f.signedPath(xo, xm, xe), asEdge)
	assertErrIs(t, err, ErrExpiredKey, "expired key rejected")

	// A not-yet-valid key is rejected as well.
	futureReg := NewKeyRegistry()
	futureKey := genKey(t)
	registerKey(t, futureReg, asOrigin, gen1, futureKey, f.clock.Add(time.Hour), time.Time{})
	futureHost := NewHost(HostConfig{
		LocalAS: asHost, Registry: futureReg,
		Now: func() time.Time { return f.clock },
	})
	fo, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 1, gen1, futureKey.priv)
	if err != nil {
		t.Fatalf("SignOrigin future: %v", err)
	}
	_, err = futureHost.VerifyCryptographic(f.signedPath(fo), asEdge)
	assertErrIs(t, err, ErrKeyNotYetValid, "not-yet-valid key rejected")
}
