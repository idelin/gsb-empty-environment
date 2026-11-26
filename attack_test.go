package pathsec

import "testing"

func leakGraph() *AttackGraph {
	g := NewGraph()
	// Observer AS65000 is provider of leaking AS65001.
	g.AddCustomerProvider(65001, 65000)
	// Leak AS65001 also buys transit from AS65002.
	g.AddCustomerProvider(65001, 65002)
	// Victim/origin AS65003 is a customer of AS65002.
	g.AddCustomerProvider(65003, 65002)
	g.Corrupt(65001)
	return g
}

func TestAdversarialRouteLeakSearch(t *testing.T) {
	g := leakGraph()
	result, err := g.Search(SearchRequest{
		ObserverASN: 65000,
		Role:        RoleCustomer,
		Prefix:      "203.0.113.0/24",
		VictimASN:   65003,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RouteLeaks) == 0 {
		t.Fatalf("expected route leak chain, got %d chains: %+v", len(result.Chains), result.Chains)
	}
	leak := result.RouteLeaks[0]
	want := []uint32{65001, 65002, 65003}
	if !equalAS(leak.AdvertisedPath, want) {
		t.Fatalf("unexpected leak path %v, want %v", leak.AdvertisedPath, want)
	}
	if leak.ASPA.Status != StatusInvalid {
		t.Fatalf("strong role-aware ASPA should be Invalid, got %s", leak.ASPA.Status)
	}
	if leak.WeakASPA != StatusValid {
		t.Fatalf("weak one-sided ASPA should accept, got %s", leak.WeakASPA)
	}
	if leak.ROA.Status != StatusNotFound {
		t.Fatalf("missing ROA should be NotFound, got %s", leak.ROA.Status)
	}
}

func TestPathShorteningSearchAndCrossValidation(t *testing.T) {
	g := NewGraph()
	g.AddLink(100, 666)
	g.AddLink(666, 200)
	g.AddLink(200, 9)
	g.Corrupt(666)

	req := SearchRequest{
		ObserverASN: 100,
		Role:        RoleProvider,
		Prefix:      "198.51.100.0/24",
		VictimASN:   9,
	}
	report, err := g.CrossValidate(req)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Match {
		t.Fatalf("search/reference mismatch missing=%v extra=%v", report.MissingInSearch, report.ExtraInSearch)
	}
	if report.Search.Shortest == nil {
		t.Fatal("expected shortest attack")
	}
	foundShort := false
	foundPCount := false
	for _, chain := range report.Search.Chains {
		if chain.Type == AttackFalseOriginHijack && equalAS(chain.AdvertisedPath, []uint32{666}) {
			foundShort = true
		}
		if chain.Type == AttackPCountCompression {
			foundPCount = true
		}
	}
	if !foundShort {
		t.Fatalf("expected one-hop false-origin shortening, chains=%+v", report.Search.Chains)
	}
	if !foundPCount {
		t.Fatal("expected pCount=0 compression chain")
	}
}

func TestCrossValidationOnLeakGraph(t *testing.T) {
	g := leakGraph()
	report, err := g.CrossValidate(SearchRequest{
		ObserverASN: 65000,
		Role:        RoleCustomer,
		Prefix:      "203.0.113.0/24",
		VictimASN:   65003,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Match {
		t.Fatalf("mismatch missing=%v extra=%v", report.MissingInSearch, report.ExtraInSearch)
	}
	if len(report.Reference.RouteLeaks) == 0 {
		t.Fatal("reference enumeration found no route leaks")
	}
}

func TestGraphNodeBound(t *testing.T) {
	g := NewGraph()
	for i := uint32(1); i <= MaxAdversarialAS+1; i++ {
		g.AddAS(i)
	}
	if _, err := g.Search(SearchRequest{}); err == nil {
		t.Fatal("expected graph bound error")
	}
}

func equalAS(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
