package pathsec

import "testing"

func TestCrossValidationDenseSmallGraph(t *testing.T) {
	g := NewGraph()
	const n = 6
	for i := uint32(1); i <= n; i++ {
		g.AddAS(i)
	}
	// Deterministic dense policy: lower ASN is a customer of higher ASN.
	for i := uint32(1); i <= n; i++ {
		for j := i + 1; j <= n; j++ {
			if (i+j)%3 == 0 {
				g.AddPeer(i, j)
			} else {
				g.AddCustomerProvider(i, j)
			}
		}
	}
	g.Corrupt(2, 5)

	report, err := g.CrossValidate(SearchRequest{
		ObserverASN: 1,
		Role:        RoleCustomer,
		Prefix:      "198.18.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Match {
		t.Fatalf("search/reference mismatch missing=%d extra=%d firstMissing=%v",
			len(report.MissingInSearch), len(report.ExtraInSearch), firstKeys(report.MissingInSearch, 5))
	}
	if report.ReferenceCount == 0 {
		t.Fatal("expected dense graph to contain attack candidates")
	}
}

func firstKeys(keys []string, n int) []string {
	if len(keys) < n {
		return keys
	}
	return keys[:n]
}
