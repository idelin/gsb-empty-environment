package changeaccept

import "testing"

func TestTrackerBindsGenerationTenantsHopAndLabelHistory(t *testing.T) {
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "x", Tenant: "tx", Egress: allowAll()},
			{ID: "b", Tenant: "ty", Ingress: allowAll()},
		},
		Edges: []Edge{{From: "x", To: "b"}},
	}
	snapshot := Snapshot{Generation: 4, Topology: topology}
	tracker := NewTracker()
	first := Connection{
		ID:     "c1",
		Pair:   pair("t1", "t2"),
		Path:   []EndpointID{"a", "x", "b"},
		Labels: []Label{"gold", "gold", "gold"},
	}
	second := Connection{
		ID:     "c1",
		Pair:   pair("t1", "t2"),
		Path:   []EndpointID{"z", "x", "b"},
		Labels: []Label{"silver", "gold", "gold"},
	}
	hop1, err := HopFor(first, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	hop2, err := HopFor(second, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if hop1.History == hop2.History {
		t.Fatal("different label histories must have different digests")
	}
	if got := tracker.Verify(hop1, snapshot.CheckHop); !got.Allowed || got.Status != CacheMiss {
		t.Fatalf("first verification = %+v", got)
	}
	if got := tracker.Verify(hop2, snapshot.CheckHop); !got.Allowed || got.Status != CacheMiss {
		t.Fatalf("another label history must miss, got %+v", got)
	}
	if got := tracker.Verify(hop1, snapshot.CheckHop); !got.Allowed || got.Status != CacheHit {
		t.Fatalf("exact concrete hop should hit, got %+v", got)
	}

	otherGeneration := hop1
	otherGeneration.Generation++
	if got := tracker.Verify(otherGeneration, snapshot.CheckHop); got.Status != CacheMiss {
		t.Fatalf("generation change must miss, got %+v", got)
	}
	otherTenant := hop1
	otherTenant.Pair.Source = "t9"
	if got := tracker.Verify(otherTenant, snapshot.CheckHop); got.Status != CacheMiss {
		t.Fatalf("source tenant change must miss, got %+v", got)
	}
	otherLabel := hop1
	otherLabel.ToLabel = "silver"
	if got := tracker.Verify(otherLabel, snapshot.CheckHop); got.Status != CacheMiss {
		t.Fatalf("concrete hop label change must miss, got %+v", got)
	}
	otherPolicy := hop1
	otherPolicy.SnapshotDigest = "different-policy"
	if got := tracker.Verify(otherPolicy, snapshot.CheckHop); got.Status != CacheMiss {
		t.Fatalf("snapshot digest change must miss, got %+v", got)
	}

	invalid := hop1
	invalid.History = [32]byte{}
	called := false
	if got := tracker.Verify(invalid, func(Hop) bool { called = true; return true }); got.Status != CacheRejected || called {
		t.Fatalf("malformed hop must fail closed without checker call: %+v, called=%v", got, called)
	}
	hits, misses := tracker.Stats()
	if hits != 1 || misses != 6 {
		t.Fatalf("stats = hits %d misses %d", hits, misses)
	}
}

func TestTrackerDoesNotCacheDenials(t *testing.T) {
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "a", Tenant: "t1", Egress: denyAll()},
			{ID: "b", Tenant: "t2", Ingress: allowAll()},
		},
		Edges: []Edge{{From: "a", To: "b"}},
	}
	snapshot := Snapshot{Generation: 1, Topology: topology}
	connection := Connection{
		ID:     "c",
		Pair:   pair("t1", "t2"),
		Path:   []EndpointID{"a", "b"},
		Labels: []Label{"gold", "gold"},
	}
	hop, err := HopFor(connection, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker()
	if got := tracker.Verify(hop, snapshot.CheckHop); got.Allowed || got.Status != CacheMiss {
		t.Fatalf("denial = %+v", got)
	}
	if got := tracker.Verify(hop, snapshot.CheckHop); got.Allowed || got.Status != CacheMiss {
		t.Fatalf("denial must not become a cache hit: %+v", got)
	}
}
