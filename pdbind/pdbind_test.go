package pdbind

import (
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"
)

var (
	duidA = []byte{0x00, 0x01, 0x00, 0x01, 0xaa}
	duidB = []byte{0x00, 0x01, 0x00, 0x01, 0xbb}

	testPool = PrefixPool{
		CIDR:         netip.MustParsePrefix("2001:db8:1::/48"),
		DelegatedLen: 50,
	}
	pd0 = netip.MustParsePrefix("2001:db8:1::/50")
	pd1 = netip.MustParsePrefix("2001:db8:1:4000::/50")
	pd2 = netip.MustParsePrefix("2001:db8:1:8000::/50")
	pd3 = netip.MustParsePrefix("2001:db8:1:c000::/50")

	linkA     = netip.MustParseAddr("2001:db8:ffff::1")
	linkBNet  = netip.MustParsePrefix("2001:db8:ffff::/48")
	peerRelay = netip.MustParseAddr("fe80::1")
	peerCli   = netip.MustParseAddr("fe80::abcd")
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestManager(clock *fakeClock) (*Manager, *MemoryRouteTable) {
	rt := NewMemoryRouteTable()
	mgr := NewManager(Config{
		Pool:          testPool,
		AllowedLinks:  []netip.Prefix{linkBNet},
		MaxRelayDepth: 32,
		PreferredLft:  100,
		ValidLft:      200,
		Now:           clock.now,
	}, rt)
	return mgr, rt
}

func leaseOf(prefix netip.Prefix) *PrefixLease {
	return &PrefixLease{Prefix: prefix, PreferredLft: 100, ValidLft: 200}
}

func relayChain(depth int, link netip.Addr) []RelayHop {
	hops := make([]RelayHop, depth)
	for i := range hops {
		hops[i] = RelayHop{HopCount: uint8(i), LinkAddr: link, PeerAddr: peerRelay}
	}
	return hops
}

func mustProcess(t *testing.T, mgr *Manager, msg ClientMessage, relay []RelayHop) *Decision {
	t.Helper()
	d, err := mgr.Process(msg, relay)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	return d
}

func commit(t *testing.T, d *Decision) {
	t.Helper()
	if err := d.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}

// TestHintConflict verifies that a hint for a prefix already delegated to
// somebody else is ignored and that a free in-pool hint is honoured. The
// routing table is touched only after Commit.
func TestHintConflict(t *testing.T) {
	clock := newFakeClock()
	mgr, rt := newTestManager(clock)
	relay := relayChain(1, linkA)

	dA := mustProcess(t, mgr, ClientMessage{
		Type:  MsgSolicit,
		DUID:  duidA,
		IAPDs: []IAPD{{IAID: 7, Hint: leaseOf(pd0)}},
	}, relay)
	if dA.Status != StatusSuccess || dA.IAPDs[0].Hint.Prefix != pd0 {
		t.Fatalf("client A expected pd0, got status=%d lease=%+v", dA.Status, dA.IAPDs[0].Hint)
	}
	if _, ok := rt.Lookup(pd0); ok {
		t.Fatal("route installed before Commit")
	}
	commit(t, dA)
	if route, ok := rt.Lookup(pd0); !ok || route.NextHop != peerRelay || route.Link != linkA {
		t.Fatalf("route for pd0 not installed correctly: %+v ok=%v", route, ok)
	}

	// Attacker requests the victim's prefix as a hint: ignored, next free
	// pool prefix is allocated instead.
	dB := mustProcess(t, mgr, ClientMessage{
		Type:  MsgRequest,
		DUID:  duidB,
		IAPDs: []IAPD{{IAID: 7, Hint: leaseOf(pd0)}},
	}, relay)
	if dB.Status != StatusSuccess {
		t.Fatalf("expected Success with alternative prefix, got %d", dB.Status)
	}
	if got := dB.IAPDs[0].Hint.Prefix; got == pd0 {
		t.Fatal("attacker was delegated the victim's prefix via a hint")
	} else if got != pd1 {
		t.Fatalf("expected next free prefix pd1, got %s", got)
	}
	commit(t, dB)

	// A hint outside the pool is ignored and a free prefix is chosen.
	outside := netip.MustParsePrefix("2001:dead::/50")
	dC := mustProcess(t, mgr, ClientMessage{
		Type:  MsgRequest,
		DUID:  []byte{0x0c},
		IAPDs: []IAPD{{IAID: 1, Hint: leaseOf(outside)}},
	}, relay)
	if dC.Status != StatusSuccess || dC.IAPDs[0].Hint.Prefix != pd2 {
		t.Fatalf("expected free pd2 for out-of-pool hint, got %d %+v", dC.Status, dC.IAPDs)
	}
	commit(t, dC)

	// An aligned, free, in-pool hint is honoured.
	dD := mustProcess(t, mgr, ClientMessage{
		Type:  MsgRequest,
		DUID:  []byte{0x0d},
		IAPDs: []IAPD{{IAID: 1, Hint: leaseOf(pd3)}},
	}, relay)
	if dD.IAPDs[0].Hint.Prefix != pd3 {
		t.Fatalf("free in-pool hint should be honoured, got %s", dD.IAPDs[0].Hint.Prefix)
	}
	commit(t, dD)

	// Pool exhausted -> NoPrefixAvail and no route change.
	dE := mustProcess(t, mgr, ClientMessage{
		Type:  MsgRequest,
		DUID:  []byte{0x0e},
		IAPDs: []IAPD{{IAID: 1, Hint: leaseOf(pd0)}},
	}, relay)
	if dE.Status != StatusNoPrefixAvail {
		t.Fatalf("expected NoPrefixAvail, got %d", dE.Status)
	}
	if err := dE.Commit(); err != nil {
		t.Fatal(err)
	}
	if rt.Count() != 4 {
		t.Fatalf("expected 4 routes, got %d", rt.Count())
	}

	// Owner re-requesting with a foreign hint keeps its own prefix.
	dA2 := mustProcess(t, mgr, ClientMessage{
		Type:  MsgRequest,
		DUID:  duidA,
		IAPDs: []IAPD{{IAID: 7, Hint: leaseOf(pd1)}},
	}, relay)
	if dA2.IAPDs[0].Hint.Prefix != pd0 {
		t.Fatalf("owner must keep its own prefix, got %s", dA2.IAPDs[0].Hint.Prefix)
	}

	// Abort leaves the routing table untouched.
	pending := mustProcess(t, mgr, ClientMessage{
		Type:  MsgRequest,
		DUID:  []byte{0x0f},
		IAPDs: []IAPD{{IAID: 1}},
	}, relay)
	pending.Abort()
	if err := pending.Commit(); err != nil {
		t.Fatal(err)
	}
	if rt.Count() != 4 {
		t.Fatalf("aborted decision must not change routes, got %d", rt.Count())
	}
}

// TestForeignRelease ensures only the owner can release/renew a prefix;
// forged messages from another DUID get NoBinding without touching the route.
func TestForeignRelease(t *testing.T) {
	clock := newFakeClock()
	mgr, rt := newTestManager(clock)
	relay := relayChain(1, linkA)

	dA := mustProcess(t, mgr, ClientMessage{
		Type: MsgSolicit,
		DUID: duidA,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
		},
	}, relay)
	commit(t, dA)

	forged := mustProcess(t, mgr, ClientMessage{
		Type: MsgRelease,
		DUID: duidB,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
			{IAID: 99, Hint: leaseOf(pd0)},
		},
	}, relay)
	if forged.Status != StatusNoBinding {
		t.Fatalf("foreign Release must be NoBinding, got %d", forged.Status)
	}
	commit(t, forged)
	if _, ok := rt.Lookup(pd0); !ok {
		t.Fatal("foreign Release removed the victim's route")
	}
	if _, ok := mgr.Lookup(MakeKey(duidA, 7)); !ok {
		t.Fatal("foreign Release removed the victim's binding")
	}

	fakeRenew := mustProcess(t, mgr, ClientMessage{
		Type: MsgRenew,
		DUID: duidB,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
		},
	}, relay)
	if fakeRenew.Status != StatusNoBinding {
		t.Fatalf("foreign Renew must be NoBinding, got %d", fakeRenew.Status)
	}
	commit(t, fakeRenew)

	mismatch := mustProcess(t, mgr, ClientMessage{
		Type: MsgRenew,
		DUID: duidA,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd1)},
		},
	}, relay)
	if mismatch.Status != StatusNoBinding {
		t.Fatalf("mismatched Renew must be NoBinding, got %d", mismatch.Status)
	}

	clock.advance(50 * time.Second)
	renew := mustProcess(t, mgr, ClientMessage{
		Type: MsgRenew,
		DUID: duidA,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
		},
	}, relay)
	if renew.Status != StatusSuccess {
		t.Fatalf("owner Renew must succeed, got %d", renew.Status)
	}
	commit(t, renew)

	evilRebind := mustProcess(t, mgr, ClientMessage{
		Type: MsgRebind,
		DUID: duidB,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
		},
	}, relay)
	if evilRebind.Status != StatusNoBinding {
		t.Fatalf("foreign Rebind must be NoBinding, got %d", evilRebind.Status)
	}
	goodRebind := mustProcess(t, mgr, ClientMessage{
		Type: MsgRebind,
		DUID: duidA,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
		},
	}, relay)
	if goodRebind.Status != StatusSuccess {
		t.Fatalf("owner Rebind must succeed, got %d", goodRebind.Status)
	}

	release := mustProcess(t, mgr, ClientMessage{
		Type: MsgRelease,
		DUID: duidA,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
		},
	}, relay)
	if release.Status != StatusSuccess {
		t.Fatalf("owner Release must succeed, got %d", release.Status)
	}
	if _, ok := rt.Lookup(pd0); !ok {
		t.Fatal("route withdrawn before Commit")
	}
	commit(t, release)
	if _, ok := rt.Lookup(pd0); ok {
		t.Fatal("route not withdrawn after Release commit")
	}
	if _, ok := mgr.Lookup(MakeKey(duidA, 7)); ok {
		t.Fatal("binding not removed after Release commit")
	}

	realloc := mustProcess(t, mgr, ClientMessage{
		Type: MsgRequest,
		DUID: duidB,
		IAPDs: []IAPD{
			{IAID: 3, Hint: leaseOf(pd0)},
		},
	}, relay)
	if realloc.IAPDs[0].Hint.Prefix != pd0 {
		t.Fatalf("released prefix should be reusable, got %s", realloc.IAPDs[0].Hint.Prefix)
	}
}

// TestRelayDepth enforces the relay-forward chain depth limit and link
// address validation.
func TestRelayDepth(t *testing.T) {
	clock := newFakeClock()
	mgr, rt := newTestManager(clock)

	msg := ClientMessage{
		Type:  MsgSolicit,
		DUID:  duidA,
		IAPDs: []IAPD{{IAID: 1, Hint: leaseOf(pd0)}},
	}

	d32 := mustProcess(t, mgr, msg, relayChain(32, linkA))
	commit(t, d32)
	if _, ok := rt.Lookup(pd0); !ok {
		t.Fatal("delegation at depth 32 should install a route")
	}

	if _, err := mgr.Process(msg, relayChain(33, linkA)); !errors.Is(err, ErrRelayDepth) {
		t.Fatalf("expected ErrRelayDepth, got %v", err)
	}

	foreignLink := netip.MustParseAddr("2001:db8:beef::1")
	if _, err := mgr.Process(msg, relayChain(1, foreignLink)); !errors.Is(err, ErrLinkNotAllowed) {
		t.Fatalf("expected ErrLinkNotAllowed, got %v", err)
	}

	badPeer := []RelayHop{{
		LinkAddr: linkA,
		PeerAddr: netip.IPv6Unspecified(),
	}}
	if _, err := mgr.Process(msg, badPeer); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("expected ErrInvalidLink for unspecified peer, got %v", err)
	}

	zeroLink := []RelayHop{{
		LinkAddr: netip.IPv6Unspecified(),
		PeerAddr: peerRelay,
	}}
	if _, err := mgr.Process(msg, zeroLink); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("expected ErrInvalidLink for unspecified link, got %v", err)
	}

	// Depth limit is configurable independently of the default.
	strict := NewManager(Config{
		Pool:          testPool,
		MaxRelayDepth: 1,
		PreferredLft:  100,
		ValidLft:      200,
		Now:           clock.now,
	}, NewMemoryRouteTable())
	if _, err := strict.Process(msg, relayChain(2, linkA)); !errors.Is(err, ErrRelayDepth) {
		t.Fatalf("expected ErrRelayDepth with custom limit, got %v", err)
	}
}

// TestLeaseExpiry verifies that an expired delegation stops matching Renew
// and Release, its route is withdrawn by the sweeper, and the prefix becomes
// allocatable again.
func TestLeaseExpiry(t *testing.T) {
	clock := newFakeClock()
	mgr, rt := newTestManager(clock)
	relay := relayChain(1, linkA)

	dA := mustProcess(t, mgr, ClientMessage{
		Type: MsgSolicit,
		DUID: duidA,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
		},
	}, relay)
	commit(t, dA)

	// Move beyond the 200s valid lifetime.
	clock.advance(201 * time.Second)

	// Sweeper withdraws the route and frees the prefix.
	reclaimed := mgr.SweepExpired()
	if len(reclaimed) != 1 || reclaimed[0] != pd0 {
		t.Fatalf("expected pd0 reclaimed, got %v", reclaimed)
	}
	if _, ok := rt.Lookup(pd0); ok {
		t.Fatal("route not withdrawn after expiry sweep")
	}
	if _, ok := mgr.Lookup(MakeKey(duidA, 7)); ok {
		t.Fatal("expired binding should not be returned by Lookup")
	}

	lateRenew := mustProcess(t, mgr, ClientMessage{
		Type: MsgRenew,
		DUID: duidA,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
		},
	}, relay)
	if lateRenew.Status != StatusNoBinding {
		t.Fatalf("Renew after expiry must be NoBinding, got %d", lateRenew.Status)
	}

	lateRelease := mustProcess(t, mgr, ClientMessage{
		Type: MsgRelease,
		DUID: duidA,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
		},
	}, relay)
	if lateRelease.Status != StatusNoBinding {
		t.Fatalf("Release after expiry must be NoBinding, got %d", lateRelease.Status)
	}

	again := mustProcess(t, mgr, ClientMessage{
		Type: MsgRequest,
		DUID: duidB,
		IAPDs: []IAPD{
			{IAID: 7, Hint: leaseOf(pd0)},
		},
	}, relay)
	if again.Status != StatusSuccess || again.IAPDs[0].Hint.Prefix != pd0 {
		t.Fatalf("expired prefix should be re-delegatable, got %d %+v", again.Status, again.IAPDs)
	}
	commit(t, again)
	if route, ok := rt.Lookup(pd0); !ok || route.NextHop != peerRelay {
		t.Fatalf("re-delegated prefix route missing or wrong: %+v", route)
	}
}

// TestDirectClient covers messages without a relay: the client address is the
// route next hop and DUID+IAID still scopes the binding.
func TestDirectClient(t *testing.T) {
	clock := newFakeClock()
	mgr, rt := newTestManager(clock)

	d := mustProcess(t, mgr, ClientMessage{
		Type:     MsgRequest,
		DUID:     duidA,
		PeerAddr: peerCli,
		IAPDs:    []IAPD{{IAID: 42, Hint: leaseOf(pd0)}},
	}, nil)
	commit(t, d)
	route, ok := rt.Lookup(pd0)
	if !ok || route.NextHop != peerCli {
		t.Fatalf("direct client route must use client address as next hop: %+v", route)
	}

	if _, err := mgr.Process(ClientMessage{Type: MsgRequest, IAPDs: []IAPD{{IAID: 1}}}, nil); !errors.Is(err, ErrNoDUID) {
		t.Fatalf("expected ErrNoDUID, got %v", err)
	}
}
