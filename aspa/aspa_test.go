package aspa

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

const (
	asCustomer = ASN(65010)
	asPeer     = ASN(65020)
	asTransit1 = ASN(64512)
	asTransit2 = ASN(64513)
)

var testNow = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

func seqPath(asns ...ASN) []Segment {
	return []Segment{{Type: SegSequence, ASNs: asns}}
}

// providerChainSnapshot authorizes customer->transit1->transit2.
func providerChainSnapshot(serial uint64) *Snapshot {
	return NewSnapshot(serial, []ASPA{
		{Customer: asCustomer, Providers: []ASN{asTransit1}},
		{Customer: asTransit1, Providers: []ASN{asTransit2}},
	}, testNow)
}

func TestValidProviderChain(t *testing.T) {
	v := NewVerifier(NewStore(providerChainSnapshot(1)))
	res := v.Verify(seqPath(asCustomer, asTransit1, asTransit2), RelationCustomer, AFIIPv4)
	if res.State != StateValid {
		t.Fatalf("got %v (%s), want Valid", res.State, res.Reason)
	}
	if res.Direction != Upstream || res.Serial != 1 {
		t.Fatalf("unexpected metadata: %+v", res)
	}
}

func TestCustomerLeaksPeerRoute(t *testing.T) {
	// The customer announces a route learned from a peer; the
	// customer's ASPA authorizes only its transit provider.
	snap := NewSnapshot(1, []ASPA{
		{Customer: asCustomer, Providers: []ASN{asTransit1}},
		{Customer: asPeer, Providers: []ASN{asTransit1}},
	}, testNow)
	v := NewVerifier(NewStore(snap))

	res := v.Verify(seqPath(asCustomer, asPeer), RelationCustomer, AFIIPv4)
	if res.State != StateInvalid {
		t.Fatalf("got %v, want Invalid", res.State)
	}
	if res.Conflict == nil {
		t.Fatal("missing conflict")
	}
	if res.Conflict.PairIndex != 0 || res.Conflict.Customer != asCustomer || res.Conflict.Provider != asPeer {
		t.Fatalf("wrong conflict: %+v", res.Conflict)
	}
	if res.Conflict.ASPA == nil || res.Conflict.ASPA.Customer != asCustomer {
		t.Fatalf("conflict must reference the customer ASPA used: %+v", res.Conflict.ASPA)
	}
}

func TestReverseTraversal(t *testing.T) {
	// Pairs must be checked customer->provider in propagation order
	// (neighbor first). The peer happens to list the customer as its
	// own provider, so an implementation that checks pairs in the
	// reverse direction would wrongly accept this leak.
	snap := NewSnapshot(1, []ASPA{
		{Customer: asCustomer, Providers: []ASN{asTransit1}},
		{Customer: asPeer, Providers: []ASN{asCustomer, asTransit1}},
	}, testNow)
	v := NewVerifier(NewStore(snap))

	leak := v.Verify(seqPath(asCustomer, asPeer, asTransit1), RelationCustomer, AFIIPv4)
	if leak.State != StateInvalid {
		t.Fatalf("leak: got %v, want Invalid", leak.State)
	}
	if leak.Conflict.PairIndex != 0 ||
		leak.Conflict.Customer != asCustomer ||
		leak.Conflict.Provider != asPeer {
		t.Fatalf("wrong conflict: %+v", leak.Conflict)
	}

	// The first conflict is reported from the neighbor side: pair 0
	// fails even though pair 1 is authorized.
	snap2 := NewSnapshot(2, []ASPA{
		{Customer: asCustomer, Providers: []ASN{asTransit1}},
		{Customer: asPeer, Providers: []ASN{asTransit1}},
	}, testNow)
	v2 := NewVerifier(NewStore(snap2))
	res := v2.Verify(seqPath(asCustomer, asPeer, asTransit1), RelationCustomer, AFIIPv4)
	if res.State != StateInvalid || res.Conflict.PairIndex != 0 {
		t.Fatalf("first conflict must be pair 0, got %+v", res.Conflict)
	}
}

func TestPrependDuplicateAS(t *testing.T) {
	v := NewVerifier(NewStore(providerChainSnapshot(1)))
	res := v.Verify(seqPath(asCustomer, asCustomer, asCustomer, asTransit1, asTransit2),
		RelationCustomer, AFIIPv4)
	if res.State != StateValid {
		t.Fatalf("got %v (%s), want Valid", res.State, res.Reason)
	}
}

func TestASSetYieldsUnknown(t *testing.T) {
	v := NewVerifier(NewStore(providerChainSnapshot(1)))
	path := []Segment{
		{Type: SegSequence, ASNs: []ASN{asCustomer}},
		{Type: SegSet, ASNs: []ASN{asTransit1, asTransit2}},
	}
	res := v.Verify(path, RelationCustomer, AFIIPv4)
	if res.State != StateUnknown {
		t.Fatalf("got %v, want Unknown", res.State)
	}
	if res.State == StateValid {
		t.Fatal("AS_SET must never verify as Valid")
	}
}

func TestConfederationSegmentsStripped(t *testing.T) {
	v := NewVerifier(NewStore(providerChainSnapshot(1)))
	path := []Segment{
		{Type: SegConfedSequence, ASNs: []ASN{ASN(65001), ASN(65002)}},
		{Type: SegSequence, ASNs: []ASN{asCustomer, asTransit1, asTransit2}},
		{Type: SegConfedSet, ASNs: []ASN{ASN(65003)}},
	}
	res := v.Verify(path, RelationCustomer, AFIIPv4)
	if res.State != StateValid {
		t.Fatalf("got %v (%s), want Valid", res.State, res.Reason)
	}
}

func TestMissingASPAYieldsUnknown(t *testing.T) {
	// transit1 has no ASPA record, so the pair (transit1, transit2)
	// cannot be verified: Unknown, not Invalid.
	snap := NewSnapshot(1, []ASPA{
		{Customer: asCustomer, Providers: []ASN{asTransit1}},
	}, testNow)
	v := NewVerifier(NewStore(snap))

	res := v.Verify(seqPath(asCustomer, asTransit1, asTransit2), RelationCustomer, AFIIPv4)
	if res.State != StateUnknown {
		t.Fatalf("got %v, want Unknown", res.State)
	}
	if res.Conflict == nil || res.Conflict.ASPA != nil {
		t.Fatalf("unknown conflict must reference the missing record: %+v", res.Conflict)
	}
	if res.Conflict.Customer != asTransit1 || res.Conflict.Provider != asTransit2 {
		t.Fatalf("wrong conflict: %+v", res.Conflict)
	}
}

func TestValleyDetection(t *testing.T) {
	// customer -> transit1 -> peer: the down-hop from transit1 to a
	// non-customer creates a valley after the up-ramp.
	snap := NewSnapshot(1, []ASPA{
		{Customer: asCustomer, Providers: []ASN{asTransit1}},
		{Customer: asTransit1, Providers: []ASN{asTransit2}},
		{Customer: asPeer, Providers: []ASN{asTransit2}},
	}, testNow)
	v := NewVerifier(NewStore(snap))

	res := v.Verify(seqPath(asCustomer, asTransit1, asPeer), RelationCustomer, AFIIPv4)
	if res.State != StateInvalid {
		t.Fatalf("got %v, want Invalid", res.State)
	}
	if res.Conflict.PairIndex != 1 ||
		res.Conflict.Customer != asTransit1 ||
		res.Conflict.Provider != asPeer {
		t.Fatalf("wrong conflict: %+v", res.Conflict)
	}
}

func TestDownstreamDirection(t *testing.T) {
	v := NewVerifier(NewStore(providerChainSnapshot(1)))
	res := v.Verify(seqPath(asTransit1, asCustomer), RelationProvider, AFIIPv6)
	if res.Direction != Downstream {
		t.Fatalf("provider session must verify downstream, got %v", res.Direction)
	}
	if res.State != StateValid {
		t.Fatalf("got %v (%s), want Valid", res.State, res.Reason)
	}
}

func TestSnapshotAtomicSwapAndSerialPinning(t *testing.T) {
	store := NewStore(providerChainSnapshot(1))
	v := NewVerifier(store)
	path := seqPath(asCustomer, asTransit1, asTransit2)

	before := v.Verify(path, RelationCustomer, AFIIPv4)
	if before.State != StateValid || before.Serial != 1 {
		t.Fatalf("before swap: got %v serial %d", before.State, before.Serial)
	}

	// Republish with transit1's ASPA revoked: the same path can no
	// longer be verified, and the old serial's cached result must
	// not leak into the new one.
	store.Publish(NewSnapshot(2, []ASPA{
		{Customer: asCustomer, Providers: []ASN{asTransit1}},
	}, testNow))

	after := v.Verify(path, RelationCustomer, AFIIPv4)
	if after.State != StateUnknown || after.Serial != 2 {
		t.Fatalf("after swap: got %v serial %d, want Unknown serial 2", after.State, after.Serial)
	}

	// The serial-1 cache entry is untouched and still reports the
	// old snapshot's verdict.
	cached := v.cache[cacheKey{dir: Upstream, afi: AFIIPv4, path: "65010-64512-64513", serial: 1}]
	if cached.State != StateValid {
		t.Fatalf("serial-1 cache entry mutated: %v", cached.State)
	}
}

func TestExpiredObjectsExcluded(t *testing.T) {
	expired := NewSnapshot(1, []ASPA{
		{Customer: asCustomer, Providers: []ASN{asTransit1}},
		{Customer: asTransit1, Providers: []ASN{asTransit2},
			NotAfter: testNow.Add(-time.Hour)},
	}, testNow)
	v := NewVerifier(NewStore(expired))

	res := v.Verify(seqPath(asCustomer, asTransit1, asTransit2), RelationCustomer, AFIIPv4)
	if res.State != StateUnknown {
		t.Fatalf("expired ASPA must not authorize: got %v", res.State)
	}
}

func TestCacheKeysSeparateDirectionAndAFI(t *testing.T) {
	v := NewVerifier(NewStore(providerChainSnapshot(1)))
	path := seqPath(asCustomer, asTransit1)

	v.VerifyDirection(path, Upstream, AFIIPv4)
	v.VerifyDirection(path, Upstream, AFIIPv6)
	v.VerifyDirection(path, Downstream, AFIIPv4)
	v.VerifyDirection(path, Upstream, AFIIPv4) // cached, no new entry

	if got := len(v.cache); got != 3 {
		t.Fatalf("cache entries = %d, want 3 (direction and AFI are key parts)", got)
	}
}

func TestConcurrentSwapDuringVerification(t *testing.T) {
	store := NewStore(providerChainSnapshot(1))
	v := NewVerifier(store)
	path := seqPath(asCustomer, asTransit1, asTransit2)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for serial := uint64(2); ; serial++ {
			select {
			case <-stop:
				return
			default:
				store.Publish(providerChainSnapshot(serial))
			}
		}
	}()

	for i := 0; i < 2000; i++ {
		res := v.Verify(path, RelationCustomer, AFIIPv4)
		if res.State != StateValid {
			t.Fatalf("got %v, want Valid", res.State)
		}
		if res.Serial == 0 {
			t.Fatal("result not pinned to a snapshot serial")
		}
	}
	close(stop)
	wg.Wait()
}

func TestNormalizeCanonicalForm(t *testing.T) {
	path := []Segment{
		{Type: SegConfedSequence, ASNs: []ASN{ASN(1)}},
		{Type: SegSequence, ASNs: []ASN{ASN(65010), ASN(65010), ASN(64512)}},
		{Type: SegSet, ASNs: []ASN{ASN(9), ASN(7)}},
	}
	seq, canonical, hasSet := normalize(path)
	if !hasSet {
		t.Fatal("expected hasSet")
	}
	if fmt.Sprint(seq) != "[65010 65010 64512]" {
		t.Fatalf("unexpected sequence: %v", seq)
	}
	if canonical != "65010-65010-64512-SET(7,9)" {
		t.Fatalf("unexpected canonical form: %q", canonical)
	}
}
