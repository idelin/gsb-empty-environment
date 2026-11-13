package netsec

import (
	"crypto/ed25519"
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestIPv6ChainAndPathIDBinding(t *testing.T) {
	f := newFixture(t)
	v6 := mustPrefix(t, "2001:db8::/32")

	o, err := SignOrigin(v6, asOrigin, asMid, f.pathID, 1, gen1, f.keys[asOrigin].priv)
	if err != nil {
		t.Fatalf("SignOrigin v6: %v", err)
	}
	m, err := SignHop(v6, f.pathID, o, asMid, asEdge, 1, gen1, f.keys[asMid].priv)
	if err != nil {
		t.Fatalf("SignHop v6: %v", err)
	}
	e, err := SignHop(v6, f.pathID, m, asEdge, asHost, 1, gen1, f.keys[asEdge].priv)
	if err != nil {
		t.Fatalf("SignHop v6: %v", err)
	}
	sp := SignedPath{Prefix: v6, OriginAS: asOrigin, PathID: f.pathID, Records: []HopRecord{o, m, e}}
	verified, err := f.host.VerifyCryptographic(sp, asEdge)
	if err != nil {
		t.Fatalf("IPv6 chain should verify: %v", err)
	}
	if verified.Prefix != v6 {
		t.Fatalf("verified prefix = %v, want %v", verified.Prefix, v6)
	}
}

func TestRegistryGuardRails(t *testing.T) {
	reg := NewKeyRegistry()
	k := genKey(t)

	if err := reg.Register(TrustedKey{AS: asOrigin, Generation: gen1, Public: k.pub}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Duplicate (AS, generation) registration is rejected rather than overwritten.
	assertErrIs(t, reg.Register(TrustedKey{AS: asOrigin, Generation: gen1, Public: k.pub}),
		ErrKeyAlreadyRegistered, "duplicate registration")
	// Malformed public keys are rejected.
	assertErrIs(t, reg.Register(TrustedKey{AS: asMid, Generation: gen1, Public: ed25519.PublicKey{1, 2, 3}}),
		ErrInvalidPublicKey, "malformed public key")
	// Revoking an unknown generation fails.
	assertErrIs(t, reg.Revoke(asEdge, gen2), ErrUnknownKey, "revoke unknown key")

	if err := reg.Revoke(asOrigin, gen1); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := reg.Lookup(asOrigin, gen1, time.Now()); !errors.Is(err, ErrRevokedKey) {
		t.Fatalf("revoked lookup: got %v", err)
	}
}

func TestSigningGuardRails(t *testing.T) {
	f := newFixture(t)
	badPrefix := netip.Prefix{}

	if _, err := SignOrigin(badPrefix, asOrigin, asMid, f.pathID, 1, gen1, f.keys[asOrigin].priv); !errors.Is(err, ErrInvalidPrefix) {
		t.Fatalf("invalid prefix on origin: got %v", err)
	}
	if _, err := SignHop(badPrefix, f.pathID, f.origin, asMid, asEdge, 1, gen1, f.keys[asMid].priv); !errors.Is(err, ErrInvalidPrefix) {
		t.Fatalf("invalid prefix on hop: got %v", err)
	}

	// Zero pCount is never a valid hop.
	if _, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 0, gen1, f.keys[asOrigin].priv); !errors.Is(err, ErrPCountRange) {
		t.Fatalf("zero pCount: got %v", err)
	}
	// A malformed private key is rejected.
	if _, err := SignOrigin(f.prefix, asOrigin, asMid, f.pathID, 1, gen1, ed25519.PrivateKey{}); !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("bad private key: got %v", err)
	}
	// Zero-valued ASes are reserved.
	if _, err := SignOrigin(f.prefix, 0, asMid, f.pathID, 1, gen1, f.keys[asOrigin].priv); !errors.Is(err, ErrReservedAS) {
		t.Fatalf("zero originAS: got %v", err)
	}
}

func TestNonConsecutiveLoopDetection(t *testing.T) {
	if !hasASLoop([]uint32{asEdge, asMid, asOrigin, asMid}) {
		t.Fatal("non-consecutive repeated AS must be a loop")
	}
	if hasASLoop([]uint32{asEdge, asMid, asMid, asOrigin}) {
		t.Fatal("consecutive prepending repeats must not be a loop")
	}
	if hasASLoop([]uint32{asEdge, asMid, asOrigin}) {
		t.Fatal("clean path must not be flagged")
	}
}

func TestHostMisconfiguration(t *testing.T) {
	noReg := NewHost(HostConfig{LocalAS: asHost})
	_, err := noReg.VerifyCryptographic(SignedPath{}, asEdge)
	assertErrIs(t, err, ErrNoRegistry, "missing registry")

	noAS := NewHost(HostConfig{Registry: NewKeyRegistry()})
	_, err = noAS.VerifyCryptographic(SignedPath{Records: []HopRecord{{}}}, asEdge)
	assertErrIs(t, err, ErrNoLocalAS, "missing local AS")

	// Default limits are applied when the host config leaves them at zero.
	defaulted := NewHost(HostConfig{LocalAS: asHost, Registry: NewKeyRegistry()})
	if defaulted.maxPCount != DefaultMaxPCountPerHop || defaulted.maxPathLength != DefaultMaxPathLength {
		t.Fatal("default host limits not applied")
	}
}
