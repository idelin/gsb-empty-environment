package pathsec

import "testing"

func TestStrictPolicyBlocksLeakWhileWeakPolicyFindsIt(t *testing.T) {
	g := leakGraph()
	roas := NewROATableFromRecords(ROA{
		Prefix: "203.0.113.0/24", MaxLength: 24, OriginASN: 65003,
	})
	base := SearchRequest{
		ObserverASN: 65000,
		Role:        RoleCustomer,
		Prefix:      "203.0.113.0/24",
		VictimASN:   65003,
		ROA:         roas,
	}

	strictReq := base
	strictReq.Policy = StrictAdmissionPolicy()
	strict, err := g.Search(strictReq)
	if err != nil {
		t.Fatal(err)
	}
	if len(strict.Chains) != 0 {
		t.Fatalf("strict policy should reject leak, got %+v", strict.Chains)
	}

	weakReq := base
	weakReq.Policy = WeakPolicy{ASPAMode: WeakASPAOneSidedEither}
	weak, err := g.Search(weakReq)
	if err != nil {
		t.Fatal(err)
	}
	if len(weak.RouteLeaks) == 0 {
		t.Fatal("one-sided weak policy should admit route leak")
	}
}
