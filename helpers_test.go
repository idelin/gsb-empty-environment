package netsec

import (
	"crypto/ed25519"
	"errors"
	"net/netip"
	"testing"
	"time"
)

const (
	asOrigin uint32 = 100
	asMid    uint32 = 200
	asEdge   uint32 = 300
	asHost   uint32 = 400
	asRogue  uint32 = 999
	gen1     uint64 = 1
	gen2     uint64 = 2
)

type asKeys struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// staticPolicy is a test RelationshipPolicy driven by an explicit allow set of
// (prefix -> permitted session peer) pairs. It is fully independent of the
// cryptographic chain checks.
type staticPolicy struct {
	allow map[netip.Prefix]map[uint32]bool
}

func (p staticPolicy) BusinessAccept(v *VerifiedPath, sessionPeerAS uint32) bool {
	peers, ok := p.allow[v.Prefix]
	if !ok {
		return false
	}
	return peers[sessionPeerAS]
}

type fixture struct {
	registry *KeyRegistry
	keys     map[uint32]asKeys
	host     *Host
	clock    time.Time
	prefix   netip.Prefix
	pathID   PathID
	origin   HopRecord
	mid      HopRecord
	edge     HopRecord
}

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("parse prefix: %v", err)
	}
	return p
}

func genKey(t *testing.T) asKeys {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return asKeys{pub: pub, priv: priv}
}

func registerKey(t *testing.T, reg *KeyRegistry, as uint32, gen uint64, k asKeys, notBefore, notAfter time.Time) {
	t.Helper()
	err := reg.Register(TrustedKey{
		AS:         as,
		Generation: gen,
		Public:     k.pub,
		NotBefore:  notBefore,
		NotAfter:   notAfter,
	})
	if err != nil {
		t.Fatalf("register key (as=%d gen=%d): %v", as, gen, err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	clock := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	registry := NewKeyRegistry()
	keys := map[uint32]asKeys{}
	for _, as := range []uint32{asOrigin, asMid, asEdge} {
		k := genKey(t)
		keys[as] = k
		registerKey(t, registry, as, gen1, k, time.Time{}, time.Time{})
	}

	prefix := mustPrefix(t, "203.0.113.0/24")
	pathID := PathID(4242)

	origin, err := SignOrigin(prefix, asOrigin, asMid, pathID, 1, gen1, keys[asOrigin].priv)
	if err != nil {
		t.Fatalf("SignOrigin: %v", err)
	}
	mid, err := SignHop(prefix, pathID, origin, asMid, asEdge, 2, gen1, keys[asMid].priv)
	if err != nil {
		t.Fatalf("SignHop mid: %v", err)
	}
	edge, err := SignHop(prefix, pathID, mid, asEdge, asHost, 1, gen1, keys[asEdge].priv)
	if err != nil {
		t.Fatalf("SignHop edge: %v", err)
	}

	host := NewHost(HostConfig{
		LocalAS:         asHost,
		Registry:        registry,
		MaxPCountPerHop: 4,
		MaxPathLength:   8,
		Now:             func() time.Time { return clock },
	})

	return &fixture{
		registry: registry,
		keys:     keys,
		host:     host,
		clock:    clock,
		prefix:   prefix,
		pathID:   pathID,
		origin:   origin,
		mid:      mid,
		edge:     edge,
	}
}

func (f *fixture) signedPath(records ...HopRecord) SignedPath {
	return SignedPath{
		Prefix:   f.prefix,
		OriginAS: asOrigin,
		PathID:   f.pathID,
		Records:  records,
	}
}

func cloneRecords(records []HopRecord) []HopRecord {
	out := make([]HopRecord, len(records))
	for i, r := range records {
		out[i] = r
		out[i].Signature = append([]byte(nil), r.Signature...)
	}
	return out
}

func assertErrIs(t *testing.T, err error, target error, label string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error %v, got nil", label, target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("%s: expected error %v, got %v", label, target, err)
	}
}
