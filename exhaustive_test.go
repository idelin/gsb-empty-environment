package netsec

import (
	"math/big"
	"sync"
	"testing"
)

func sumCounts(counts []int64) int64 {
	var total int64
	for _, c := range counts {
		total += c
	}
	return total
}

// TestExhaustiveVerifierAgainstFullRecompute enumerates every small count table
// (two groups, three ordered positions, totals up to perGroupMax), drives the
// incremental add path, then checks each audit against the count-level EMD,
// the evidence reconstruction and a fully rebuilt verifier. Zero vectors
// exercise the zero-sample path; all-at-one-position vectors exercise
// endpoints.
func TestExhaustiveVerifierAgainstFullRecompute(t *testing.T) {
	domain, err := NewDomain([]string{"x", "y", "z"})
	if err != nil {
		t.Fatalf("NewDomain error: %v", err)
	}
	const perGroupMax = 3

	var vectors [][]int64
	for total := int64(0); total <= perGroupMax; total++ {
		compositions(3, total, &vectors)
	}

	threshold := big.NewRat(1, 3)
	for _, g1Counts := range vectors {
		for _, g2Counts := range vectors {
			v := NewVerifier(domain)
			type entry struct {
				group string
				pos   int
			}
			var records []entry
			addVector := func(group string, counts []int64) {
				for pos, c := range counts {
					for i := int64(0); i < c; i++ {
						if err := v.AddRecordAt(group, pos); err != nil {
							t.Fatalf("add error: %v", err)
						}
						records = append(records, entry{group, pos})
					}
				}
			}
			addVector("g1", g1Counts)
			addVector("g2", g2Counts)

			reports, err := v.Audit(threshold)
			if err != nil {
				t.Fatalf("Audit error: %v", err)
			}

			grand := []int64{
				g1Counts[0] + g2Counts[0],
				g1Counts[1] + g2Counts[1],
				g1Counts[2] + g2Counts[2],
			}
			grandTotal := sumCounts(grand)

			for _, report := range reports {
				var ownCounts []int64
				if report.Group == "g1" {
					ownCounts = g1Counts
				} else {
					ownCounts = g2Counts
				}
				ownTotal := sumCounts(ownCounts)

				if ownTotal == 0 {
					if report.Status != StatusZeroSample {
						t.Fatalf("g1=%v g2=%v: zero-sample group reported as %s",
							g1Counts, g2Counts, report.Status)
					}
					if report.Distance != nil {
						t.Fatal("zero-sample report must carry no distance")
					}
					continue
				}

				wantDistance, err := CountWasserstein1(ownCounts, ownTotal, grand, grandTotal)
				if err != nil {
					t.Fatalf("reference EMD error: %v", err)
				}
				if report.Distance.Cmp(wantDistance) != 0 {
					t.Fatalf("g1=%v g2=%v group=%s distance %s != reference %s",
						g1Counts, g2Counts, report.Group,
						report.Distance.RatString(), wantDistance.RatString())
				}
				wantStatus := StatusWithin
				if wantDistance.Cmp(threshold) > 0 {
					wantStatus = StatusExceeding
				}
				if report.Status != wantStatus {
					t.Fatalf("g1=%v g2=%v group=%s status %s, want %s",
						g1Counts, g2Counts, report.Group, report.Status, wantStatus)
				}

				evidenceCost := new(big.Rat)
				for _, f := range report.Transport {
					evidenceCost.Add(evidenceCost, f.Cost())
				}
				if evidenceCost.Cmp(report.Distance) != 0 {
					t.Fatalf("evidence cost %s != distance %s",
						evidenceCost.RatString(), report.Distance.RatString())
				}
			}

			// Full recomputation from the surviving records must be identical.
			fresh := NewVerifier(domain)
			for _, r := range records {
				if err := fresh.AddRecordAt(r.group, r.pos); err != nil {
					t.Fatalf("rebuild add error: %v", err)
				}
			}
			freshReports, _ := fresh.Audit(threshold)
			if len(freshReports) != len(reports) {
				t.Fatalf("recomputed report count %d != %d", len(freshReports), len(reports))
			}
			for i := range reports {
				if freshReports[i].Group != reports[i].Group ||
					freshReports[i].Status != reports[i].Status {
					t.Fatalf("recomputed report %d differs: %+v vs %+v",
						i, freshReports[i], reports[i])
				}
				if freshReports[i].Distance == nil || reports[i].Distance == nil {
					continue
				}
				if freshReports[i].Distance.Cmp(reports[i].Distance) != 0 {
					t.Fatalf("recomputed distance %s != %s",
						freshReports[i].Distance.RatString(), reports[i].Distance.RatString())
				}
			}
		}
	}
}

func TestExhaustiveWithdrawalsThenRecompute(t *testing.T) {
	// Same enumeration, but withdraw every record of g1 and half of g2 one by
	// one; the maintained verifier must match a verifier fed only survivors.
	domain, _ := NewDomain([]string{"x", "y", "z"})
	const perGroupMax = 3

	var vectors [][]int64
	for total := int64(1); total <= perGroupMax; total++ {
		compositions(3, total, &vectors)
	}

	threshold := big.NewRat(1, 2)
	for _, g1Counts := range vectors {
		for _, g2Counts := range vectors {
			v := NewVerifier(domain)
			fresh := NewVerifier(domain)
			// Register g1 in the rebuild as well: a fully withdrawn group stays
			// known to the maintained verifier and is reported zero-sample.
			_ = fresh.AddRecordAt("g1", 0)
			if err := fresh.WithdrawRecordAt("g1", 0); err != nil {
				t.Fatalf("fresh g1 setup error: %v", err)
			}

			for pos, c := range g1Counts {
				for i := int64(0); i < c; i++ {
					_ = v.AddRecordAt("g1", pos)
				}
			}
			g2Total := sumCounts(g2Counts)
			withdrawTarget := g2Total / 2
			withdrawnFromG2 := int64(0)
			for pos, c := range g2Counts {
				for i := int64(0); i < c; i++ {
					_ = v.AddRecordAt("g2", pos)
					if withdrawnFromG2 < withdrawTarget {
						withdrawnFromG2++
						if err := v.WithdrawRecordAt("g2", pos); err != nil {
							t.Fatalf("withdraw error: %v", err)
						}
						continue
					}
					_ = fresh.AddRecordAt("g2", pos)
				}
			}
			for pos, c := range g1Counts {
				for i := int64(0); i < c; i++ {
					if err := v.WithdrawRecordAt("g1", pos); err != nil {
						t.Fatalf("g1 withdraw error: %v", err)
					}
				}
			}

			got, _ := v.Audit(threshold)
			want, _ := fresh.Audit(threshold)
			if len(got) != len(want) {
				t.Fatalf("g1=%v g2=%v: report count %d != recomputed %d",
					g1Counts, g2Counts, len(got), len(want))
			}
			for i := range want {
				if got[i].Group != want[i].Group || got[i].Status != want[i].Status {
					t.Fatalf("g1=%v g2=%v: report mismatch %+v vs %+v",
						g1Counts, g2Counts, got[i], want[i])
				}
			}
			if v.GroupSize("g1") != 0 {
				t.Fatalf("g1 should be drained, size=%d", v.GroupSize("g1"))
			}
			if v.GrandTotal() != fresh.GrandTotal() {
				t.Fatalf("grand total %d != recomputed %d", v.GrandTotal(), fresh.GrandTotal())
			}
		}
	}
}

func TestThresholdBoundaryIsStrict(t *testing.T) {
	domain, _ := NewDomain([]string{"a", "b", "c"})
	v := NewVerifier(domain)
	// Group sits at the endpoint. The population including that record is
	// uniform (4/4/4), so the exact distance is 1: CDF gaps 2/3 then 1/3.
	_ = v.AddRecordAt("pop", 0)
	_ = v.AddRecordAt("pop", 0)
	_ = v.AddRecordAt("pop", 0)
	for i := 0; i < 4; i++ {
		_ = v.AddRecordAt("pop", 1)
		_ = v.AddRecordAt("pop", 2)
	}
	_ = v.AddRecordAt("edge", 0)

	report, ok, err := v.AuditGroup("edge", big.NewRat(1, 1))
	if err != nil || !ok {
		t.Fatalf("AuditGroup ok=%v err=%v", ok, err)
	}
	if report.Distance.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("distance = %s, want 1", report.Distance.RatString())
	}
	if report.Status != StatusWithin {
		t.Fatalf("distance equal to threshold must be within, got %s", report.Status)
	}

	report, _, _ = v.AuditGroup("edge", big.NewRat(999, 1000))
	if report.Status != StatusExceeding {
		t.Fatalf("distance above threshold must exceed, got %s", report.Status)
	}
	if len(Exceeding([]*GroupReport{report})) != 1 {
		t.Fatal("Exceeding must return the violating report")
	}

	if _, ok, err := v.AuditGroup("missing", big.NewRat(1, 1)); err != nil || ok {
		t.Fatalf("unknown group: ok=%v err=%v", ok, err)
	}
	if _, err := v.Audit(nil); err == nil {
		t.Fatal("nil threshold must fail")
	}
	if _, err := v.Audit(big.NewRat(-1, 1)); err == nil {
		t.Fatal("negative threshold must fail")
	}
}

func TestEmptyVerifierAudit(t *testing.T) {
	domain, _ := NewDomain([]string{"a", "b"})
	v := NewVerifier(domain)
	reports, err := v.Audit(big.NewRat(1, 10))
	if err != nil {
		t.Fatalf("Audit error: %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("empty verifier must have no reports, got %d", len(reports))
	}
	pop := v.PopulationDistribution()
	if len(pop) != 2 || pop[0] != nil {
		t.Fatalf("empty population distribution wrong: %+v", pop)
	}
}

func TestConcurrentAccess(t *testing.T) {
	domain, _ := NewDomain([]string{"a", "b", "c"})
	v := NewVerifier(domain)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			name := string(rune('g' + g))
			for i := 0; i < 100; i++ {
				_ = v.AddRecordAt(name, i%3)
				_, _ = v.Audit(big.NewRat(1, 2))
				if i%2 == 0 {
					_ = v.WithdrawRecordAt(name, i%3)
				}
			}
		}(g)
	}
	wg.Wait()

	// Every surviving record is accounted for exactly once in group + population.
	snap := v.snapshot()
	var groupSum int64
	for _, g := range snap.groups {
		groupSum += g.total
		var bucketSum int64
		for _, c := range g.counts {
			bucketSum += c
		}
		if bucketSum != g.total {
			t.Fatalf("group buckets %d != total %d", bucketSum, g.total)
		}
	}
	if groupSum != snap.total {
		t.Fatalf("sum of group totals %d != grand total %d", groupSum, snap.total)
	}
}
