package pdbind

import (
	"errors"
	"net/netip"
	"testing"
)

// TestMultipleIAPDNoDuplicate ensures several IA_PDs in one message never
// receive the same prefix, even when all carry the same occupied hint.
func TestMultipleIAPDNoDuplicate(t *testing.T) {
	clock := newFakeClock()
	mgr, rt := newTestManager(clock)
	relay := relayChain(1, linkA)

	d := mustProcess(t, mgr, ClientMessage{
		Type: MsgRequest,
		DUID: duidA,
		IAPDs: []IAPD{
			{IAID: 1, Hint: leaseOf(pd0)},
			{IAID: 2, Hint: leaseOf(pd0)},
			{IAID: 3, Hint: leaseOf(pd0)},
		},
	}, relay)
	if d.Status != StatusSuccess {
		t.Fatalf("expected Success, got %d", d.Status)
	}
	seen := map[netip.Prefix]bool{}
	for _, ia := range d.IAPDs {
		if seen[ia.Hint.Prefix] {
			t.Fatalf("prefix %s delegated twice in one message", ia.Hint.Prefix)
		}
		seen[ia.Hint.Prefix] = true
	}
	if err := d.Commit(); err != nil {
		t.Fatal(err)
	}
	if rt.Count() != 3 {
		t.Fatalf("expected 3 routes, got %d", rt.Count())
	}

	// Distinct keys: same IAID under a different DUID is an independent
	// binding and must not collide in the map.
	other := mustProcess(t, mgr, ClientMessage{
		Type: MsgRequest,
		DUID: duidB,
		IAPDs: []IAPD{
			{IAID: 1, Hint: leaseOf(pd0)},
		},
	}, relay)
	if other.IAPDs[0].Hint.Prefix == pd0 {
		t.Fatal("DUID+IAID key isolation failed")
	}
}

// TestConfirmBehaviour covers IA_PD carried in Confirm.
func TestConfirmBehaviour(t *testing.T) {
	clock := newFakeClock()
	mgr, _ := newTestManager(clock)
	relay := relayChain(1, linkA)

	commit(t, mustProcess(t, mgr, ClientMessage{
		Type:  MsgSolicit,
		DUID:  duidA,
		IAPDs: []IAPD{{IAID: 7, Hint: leaseOf(pd0)}},
	}, relay))

	good := mustProcess(t, mgr, ClientMessage{
		Type:  MsgConfirm,
		DUID:  duidA,
		IAPDs: []IAPD{{IAID: 7, Hint: leaseOf(pd0)}},
	}, relay)
	if good.Status != StatusSuccess {
		t.Fatalf("matching Confirm must succeed, got %d", good.Status)
	}

	bad := mustProcess(t, mgr, ClientMessage{
		Type:  MsgConfirm,
		DUID:  duidA,
		IAPDs: []IAPD{{IAID: 7, Hint: leaseOf(pd1)}},
	}, relay)
	if bad.Status != StatusNoBinding {
		t.Fatalf("non-matching Confirm must be NoBinding, got %d", bad.Status)
	}
}

// TestRelayedEndToEnd drives parsed wire data straight into the manager.
func TestRelayedEndToEnd(t *testing.T) {
	clock := newFakeClock()
	mgr, rt := newTestManager(clock)

	inner := clientPacket(MsgRequest, duidA, iaPDOption(5, iaPrefixOption(pd0, 100, 200)))
	packet := relayForw(linkA, peerRelay, inner)

	msg, hops, err := ParseMessage(packet, netip.MustParseAddr("fe80::10"), 32)
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	d, err := mgr.Process(msg, hops)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if d.Status != StatusSuccess || d.IAPDs[0].Hint.Prefix != pd0 {
		t.Fatalf("unexpected decision: %+v", d)
	}
	if err := d.Commit(); err != nil {
		t.Fatal(err)
	}
	route, ok := rt.Lookup(pd0)
	if !ok || route.NextHop != peerRelay || route.Link != linkA {
		t.Fatalf("relayed route wrong: %+v", route)
	}

	// Wire-level depth rejection happens before the manager is involved.
	deep := inner
	for i := 0; i < 33; i++ {
		deep = relayForw(linkA, peerRelay, deep)
	}
	if _, _, err := ParseMessage(deep, netip.MustParseAddr("fe80::10"), 32); !errors.Is(err, ErrRelayDepth) {
		t.Fatalf("expected ErrRelayDepth from parser, got %v", err)
	}
}

// TestBindingKey covers key construction and rendering.
func TestBindingKey(t *testing.T) {
	key := MakeKey(duidA, 42)
	if key.DUIDHex == "" || key.IAID != 42 {
		t.Fatalf("bad key: %+v", key)
	}
	if key.String() != key.DUIDHex+"/42" {
		t.Fatalf("unexpected key rendering: %s", key.String())
	}
	if MakeKey(duidA, 1) == MakeKey(duidB, 1) {
		t.Fatal("keys for different DUIDs must differ")
	}
}

// TestPoolValidation covers malformed pool configuration.
func TestPoolValidation(t *testing.T) {
	v4 := PrefixPool{CIDR: netip.MustParsePrefix("10.0.0.0/8"), DelegatedLen: 16}
	if err := v4.Validate(); err != ErrMalformedMessage {
		t.Fatalf("expected malformed IPv4 pool, got %v", err)
	}
	badLen := PrefixPool{CIDR: netip.MustParsePrefix("2001:db8::/48"), DelegatedLen: 40}
	if err := badLen.Validate(); err != ErrMalformedMessage {
		t.Fatalf("expected malformed delegated length, got %v", err)
	}
	good := PrefixPool{CIDR: netip.MustParsePrefix("2001:db8::/48"), DelegatedLen: 56}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid pool rejected: %v", err)
	}
}
