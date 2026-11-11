package netsec

import (
	"math/big"
	"testing"
)

func TestDomain(t *testing.T) {
	domain, err := NewDomain([]string{"low", "mid", "high"})
	if err != nil {
		t.Fatalf("NewDomain error: %v", err)
	}
	if domain.Size() != 3 {
		t.Fatalf("size = %d, want 3", domain.Size())
	}
	if pos, ok := domain.Index("high"); !ok || pos != 2 {
		t.Fatalf("Index(high) = %d,%v, want 2,true", pos, ok)
	}
	if label, ok := domain.Label(1); !ok || label != "mid" {
		t.Fatalf("Label(1) = %q,%v, want mid,true", label, ok)
	}
	if _, ok := domain.Label(9); ok {
		t.Fatal("Label(9) must fail")
	}
	if _, err := NewDomain([]string{"a", "a"}); err == nil {
		t.Fatal("duplicate labels must fail")
	}
	if _, err := NewDomain(nil); err == nil {
		t.Fatal("empty domain must fail")
	}
}

func TestAddWithdrawKeepsGroupAndPopulation(t *testing.T) {
	domain, _ := NewDomain([]string{"a", "b", "c"})
	v := NewVerifier(domain)

	records := []struct {
		group string
		pos   int
	}{
		{"g1", 0}, {"g1", 0}, {"g1", 2},
		{"g2", 1}, {"g2", 2},
	}
	for _, r := range records {
		if err := v.AddRecordAt(r.group, r.pos); err != nil {
			t.Fatalf("AddRecordAt error: %v", err)
		}
	}

	if v.GrandTotal() != 5 {
		t.Fatalf("grand total = %d, want 5", v.GrandTotal())
	}
	if v.GroupSize("g1") != 3 || v.GroupSize("g2") != 2 {
		t.Fatalf("group sizes = %d,%d", v.GroupSize("g1"), v.GroupSize("g2"))
	}

	wantGrand := []int64{2, 1, 2}
	popDist := v.PopulationDistribution()
	for i, want := range wantGrand {
		if popDist[i].Cmp(big.NewRat(want, 5)) != 0 {
			t.Fatalf("population[%d] = %s, want %d/5", i, popDist[i].RatString(), want)
		}
	}

	// Withdraw one record: both group and population must move together.
	if err := v.WithdrawRecordAt("g1", 0); err != nil {
		t.Fatalf("WithdrawRecordAt error: %v", err)
	}
	if v.GrandTotal() != 4 || v.GroupSize("g1") != 2 {
		t.Fatalf("after withdraw totals = grand %d, g1 %d", v.GrandTotal(), v.GroupSize("g1"))
	}
	popDist = v.PopulationDistribution()
	wantGrandAfter := []int64{1, 1, 2}
	for i, want := range wantGrandAfter {
		if popDist[i].Cmp(big.NewRat(want, 4)) != 0 {
			t.Fatalf("population[%d] after withdraw = %s, want %d/4", i, popDist[i].RatString(), want)
		}
	}

	// Failed withdrawals must leave the state unchanged. g1/0 held two records
	// and one was withdrawn above, so one more succeeds and the next underflows.
	if err := v.WithdrawRecordAt("g1", 0); err != nil {
		t.Fatalf("second g1/0 withdraw should succeed, got %v", err)
	}
	if err := v.WithdrawRecordAt("g1", 0); err == nil {
		t.Fatal("expected underflow error")
	}
	if v.GrandTotal() != 3 {
		t.Fatalf("failed withdraw changed totals: %d", v.GrandTotal())
	}
	if err := v.WithdrawRecordAt("unknown", 0); err == nil {
		t.Fatal("withdraw from unknown group must fail")
	}
	if err := v.AddRecordAt("g1", 7); err == nil {
		t.Fatal("out-of-range position must fail")
	}
	if err := v.AddRecord("g1", "nope"); err == nil {
		t.Fatal("unknown label must fail")
	}
}

func TestWithdrawToZeroSampleAndBack(t *testing.T) {
	domain, _ := NewDomain([]string{"a", "b"})
	v := NewVerifier(domain)
	_ = v.AddRecordAt("g", 0)
	_ = v.AddRecordAt("g", 1)
	// Anchor keeps the population non-empty after group g is drained.
	_ = v.AddRecordAt("anchor", 0)
	_ = v.AddRecordAt("anchor", 0)

	threshold := big.NewRat(1, 10)
	reports, err := v.Audit(threshold)
	if err != nil {
		t.Fatalf("Audit error: %v", err)
	}
	for _, r := range reports {
		if r.Status == StatusZeroSample {
			t.Fatalf("group %s unexpectedly zero-sample", r.Group)
		}
	}

	if err := v.WithdrawRecordAt("g", 0); err != nil {
		t.Fatalf("withdraw error: %v", err)
	}
	if err := v.WithdrawRecordAt("g", 1); err != nil {
		t.Fatalf("withdraw error: %v", err)
	}
	if v.GroupSize("g") != 0 {
		t.Fatalf("drained group size = %d, want 0", v.GroupSize("g"))
	}

	reports, _ = v.Audit(threshold)
	var gReport *GroupReport
	for _, r := range reports {
		if r.Group == "g" {
			gReport = r
		}
	}
	if gReport == nil {
		t.Fatal("drained group missing from report")
	}
	if gReport.Status != StatusZeroSample || gReport.Distance != nil || gReport.Transport != nil {
		t.Fatalf("drained group report wrong: %+v", gReport)
	}

	// Re-adding a record restores an auditable distribution.
	if err := v.AddRecordAt("g", 1); err != nil {
		t.Fatalf("re-add error: %v", err)
	}
	reports, _ = v.Audit(threshold)
	for _, r := range reports {
		if r.Group == "g" && r.Status == StatusZeroSample {
			t.Fatal("re-added group must no longer be zero-sample")
		}
	}
}

// recomputeVerifier rebuilds a fresh verifier from the surviving records and
// compares every audit result against the incrementally maintained one.
func recomputeVerifier(t *testing.T, v *Verifier, records [][2]string, threshold *big.Rat) {
	t.Helper()
	fresh := NewVerifier(v.Domain())
	for _, r := range records {
		if err := fresh.AddRecord(r[0], r[1]); err != nil {
			t.Fatalf("recompute add error: %v", err)
		}
	}

	got, err := v.Audit(threshold)
	if err != nil {
		t.Fatalf("incremental Audit error: %v", err)
	}
	want, err := fresh.Audit(threshold)
	if err != nil {
		t.Fatalf("recomputed Audit error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("report count %d != recomputed %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Group != want[i].Group {
			t.Fatalf("report %d group %q != %q", i, got[i].Group, want[i].Group)
		}
		if got[i].Status != want[i].Status {
			t.Fatalf("group %q status %s != recomputed %s",
				got[i].Group, got[i].Status, want[i].Status)
		}
		if (got[i].Distance == nil) != (want[i].Distance == nil) {
			t.Fatalf("group %q distance presence mismatch", got[i].Group)
		}
		if got[i].Distance != nil && got[i].Distance.Cmp(want[i].Distance) != 0 {
			t.Fatalf("group %q distance %s != recomputed %s",
				got[i].Group, got[i].Distance.RatString(), want[i].Distance.RatString())
		}
		if got[i].GroupTotal != want[i].GroupTotal {
			t.Fatalf("group %q total %d != %d", got[i].Group, got[i].GroupTotal, want[i].GroupTotal)
		}
	}
	if v.GrandTotal() != fresh.GrandTotal() {
		t.Fatalf("grand total %d != recomputed %d", v.GrandTotal(), fresh.GrandTotal())
	}
}

func TestFullRecomputationAfterWithdrawals(t *testing.T) {
	labels := []string{"a", "b", "c", "d"}
	domain, _ := NewDomain(labels)
	v := NewVerifier(domain)

	records := [][2]string{
		{"g1", "a"}, {"g1", "a"}, {"g1", "c"}, {"g1", "d"},
		{"g2", "b"}, {"g2", "b"}, {"g2", "c"},
		{"g3", "a"}, {"g3", "d"}, {"g3", "d"}, {"g3", "d"},
	}
	for _, r := range records {
		if err := v.AddRecord(r[0], r[1]); err != nil {
			t.Fatalf("AddRecord error: %v", err)
		}
	}
	threshold := big.NewRat(1, 4)
	recomputeVerifier(t, v, records, threshold)

	// Withdraw a spread of records (including both g3/d copies) and re-verify
	// against a from-scratch rebuild of the surviving records.
	withdrawBudget := map[[2]string]int{
		{"g1", "a"}: 1,
		{"g2", "b"}: 1,
		{"g3", "d"}: 2,
	}
	surviving := make([][2]string, 0, len(records))
	for _, r := range records {
		if withdrawBudget[r] > 0 {
			withdrawBudget[r]--
			if err := v.WithdrawRecord(r[0], r[1]); err != nil {
				t.Fatalf("WithdrawRecord error: %v", err)
			}
			continue
		}
		surviving = append(surviving, r)
	}
	recomputeVerifier(t, v, surviving, threshold)
}
