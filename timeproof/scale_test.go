package timeproof

import "testing"

func TestMaxScaleEnumeration(t *testing.T) {
	clock := &Clock{Segments: []DriftSeg{
		{From: RatInt(0), To: RatInt(100000), RateLo: NewRat(999, 1000), RateHi: NewRat(1001, 1000)},
	}}
	var receipts []*Receipt
	// 12 domains, up to 64 receipts total, several distinct monotonic instants.
	next := 0
	for d := 0; d < 12 && len(receipts) < 64; d++ {
		domain := string(rune('A' + d))
		for k := 0; k < 5 && len(receipts) < 64; k++ {
			send := int64(10 + next*3)
			next++
			receipts = append(receipts, &Receipt{
				Nonce: "n1", Domain: domain, ServerID: "srv-" + domain,
				SendMono: RatInt(send), RecvMono: RatInt(send + 2),
				ProcessLo: RatInt(0), ProcessHi: RatInt(1), OneWayDelay: RatInt(1),
				SignedLo: RatInt(1000 + send), SignedHi: RatInt(1000 + send + 5),
			})
		}
	}
	q := &Query{
		Nonce: "n1", Clock: clock, Receipts: receipts,
		QueryMono: RatInt(50), MaxFaulty: 3,
	}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	if sol.Truncated {
		t.Fatal("default cap should not truncate a 12-domain query")
	}
	if err := sol.Verify(q); err != nil {
		t.Fatalf("verify: %v", err)
	}
	t.Logf("domains=%d receipts=%d outcomes=%d conflicts=%d union=%d gaps=%d",
		len(sol.DomainName), len(receipts), len(sol.Outcomes), len(sol.Conflicts),
		len(sol.Union), len(sol.Gaps))
}
