package netsec

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func h(b byte) ContentHash { return ContentHash{b} }

func ingest(t *testing.T, l *Ledger, probe ProbeID, gen Gen, seq Seq, hash ContentHash) IngestResult {
	t.Helper()
	res, err := l.IngestRecord(Record{Probe: probe, Gen: gen, Seq: seq, Hash: hash})
	if err != nil {
		t.Fatalf("ingest gen=%d seq=%d: %v", gen, seq, err)
	}
	return res
}

func commit(t *testing.T, l *Ledger, probe ProbeID, gen Gen, high Seq) CommitResult {
	t.Helper()
	res, err := l.CommitWatermark(Watermark{Probe: probe, Gen: gen, High: high})
	if err != nil {
		t.Fatalf("commit gen=%d high=%d: %v", gen, high, err)
	}
	return res
}

func assertIntervals(t *testing.T, name string, got, want []Interval) {
	t.Helper()
	if !intervalsEqual(got, want) {
		t.Fatalf("%s: got %+v, want %+v", name, got, want)
	}
}

func mustMissing(t *testing.T, l *Ledger, gen Gen) []Interval {
	t.Helper()
	reports, err := l.QueryProbe("p")
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range reports {
		if report.Gen == gen {
			return report.Missing
		}
	}
	t.Fatalf("generation %d not found", gen)
	return nil
}

func TestGapOnlyConfirmedUnderWatermark(t *testing.T) {
	l := NewLedger()

	ingest(t, l, "p1", 1, 1, h(1))
	ingest(t, l, "p1", 1, 3, h(3))
	ingest(t, l, "p1", 1, 5, h(5))

	// No issuance yet: nothing is provably missing.
	report, err := l.QueryRange("p1", 1, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	assertIntervals(t, "missing before commit", report.Missing, nil)
	assertIntervals(t, "pending before commit", report.Pending, []Interval{{2, 2}, {4, 4}})

	res := commit(t, l, "p1", 1, 3)
	if res.Outcome != CommitAdvanced {
		t.Fatalf("outcome = %s", res.Outcome)
	}
	assertIntervals(t, "newly confirmed at 3", res.NewlyConfirmed, []Interval{{2, 2}})

	report, _ = l.QueryRange("p1", 1, 1, 5)
	assertIntervals(t, "missing at high=3", report.Missing, []Interval{{2, 2}})
	assertIntervals(t, "pending beyond high", report.Pending, []Interval{{4, 4}})

	res = commit(t, l, "p1", 1, 5)
	assertIntervals(t, "newly confirmed at 5", res.NewlyConfirmed, []Interval{{4, 4}})

	report, _ = l.QueryRange("p1", 1, 1, 5)
	assertIntervals(t, "missing at high=5", report.Missing, []Interval{{2, 2}, {4, 4}})
}

func TestRetransmissionNeverCountsAsMissing(t *testing.T) {
	l := NewLedger()
	ingest(t, l, "p", 1, 1, h(9))
	commit(t, l, "p", 1, 3)

	assertIntervals(t, "missing after commit", mustMissing(t, l, 1), []Interval{{2, 3}})

	res := ingest(t, l, "p", 1, 1, h(9))
	if res.Outcome != IngestDuplicate || res.FilledMissing {
		t.Fatalf("duplicate disposition = %+v", res)
	}
	assertIntervals(t, "missing unchanged after retransmission", mustMissing(t, l, 1), []Interval{{2, 3}})
}

func TestCorrectionOnRepair(t *testing.T) {
	l := NewLedger()
	ingest(t, l, "p", 1, 1, h(1))
	commit(t, l, "p", 1, 3)

	res := ingest(t, l, "p", 1, 2, h(2))
	if !res.FilledMissing || res.ConfirmedUnder != 3 || res.Correction == nil {
		t.Fatalf("repair disposition = %+v", res)
	}
	if res.Correction.Seq != 2 || res.Correction.Watermark != 3 {
		t.Fatalf("correction = %+v", res.Correction)
	}

	ingest(t, l, "p", 1, 3, h(3))
	assertIntervals(t, "all gaps repaired", mustMissing(t, l, 1), nil)

	corrections, err := l.Corrections("p", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(corrections) != 2 || corrections[0].Seq != 2 || corrections[1].Seq != 3 {
		t.Fatalf("corrections = %+v", corrections)
	}
	if corrections[1].Event <= corrections[0].Event {
		t.Fatal("correction events must be monotonic")
	}

	commitRes := commit(t, l, "p", 1, 5)
	assertIntervals(t, "new gaps after repair", commitRes.NewlyConfirmed, []Interval{{4, 5}})
}

func TestConflictRetainsOriginal(t *testing.T) {
	l := NewLedger()
	ingest(t, l, "p", 1, 1, h(0xaa))
	res := ingest(t, l, "p", 1, 1, h(0xbb))
	if res.Outcome != IngestConflict {
		t.Fatalf("outcome = %s", res.Outcome)
	}

	report, err := l.QueryRange("p", 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertIntervals(t, "conflicting seq still present", report.Present, []Interval{{1, 1}})

	epoch := l.epochs[EpochKey{"p", 1}]
	if !hashEqual(epoch.present[1], h(0xaa)) || epoch.conflictCount != 1 {
		t.Fatalf("original content overwritten or conflict not counted: %+v", epoch.present[1])
	}

	ingest(t, l, "p", 1, 1, h(0xbb))
	if epoch.conflictCount != 2 {
		t.Fatalf("conflictCount = %d", epoch.conflictCount)
	}
}

func TestGenerationsNeverMix(t *testing.T) {
	l := NewLedger()
	ingest(t, l, "p", 1, 1, h(1))
	commit(t, l, "p", 1, 2) // gen 1: seq 2 missing

	// Probe restarts: generation 2 starts its own sequence space at 1.
	ingest(t, l, "p", 2, 1, h(1))
	commit(t, l, "p", 2, 1)

	gens, err := l.Generations("p")
	if err != nil || !reflect.DeepEqual(gens, []Gen{1, 2}) {
		t.Fatalf("generations = %v, err %v", gens, err)
	}

	reports, err := l.QueryProbe("p")
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 || reports[0].Gen != 1 || reports[1].Gen != 2 {
		t.Fatalf("reports = %+v", reports)
	}
	assertIntervals(t, "gen1 missing", reports[0].Missing, []Interval{{2, 2}})
	assertIntervals(t, "gen2 present", reports[1].Present, []Interval{{1, 1}})
	assertIntervals(t, "gen2 missing", reports[1].Missing, nil)

	// A late gen-1 record fills only the gen-1 gap.
	res := ingest(t, l, "p", 1, 2, h(2))
	if !res.FilledMissing {
		t.Fatalf("late gen1 record = %+v", res)
	}
	reports, _ = l.QueryProbe("p")
	assertIntervals(t, "gen1 repaired", reports[0].Missing, nil)
	assertIntervals(t, "gen2 untouched", reports[1].Missing, nil)
}

func TestStaleAndRedundantWatermark(t *testing.T) {
	l := NewLedger()
	commit(t, l, "p", 1, 5)

	redundant := commit(t, l, "p", 1, 5)
	if redundant.Outcome != CommitRedundant {
		t.Fatalf("outcome = %s", redundant.Outcome)
	}

	stale, err := l.CommitWatermark(Watermark{Probe: "p", Gen: 1, High: 4})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Outcome != CommitStale || stale.High != 5 {
		t.Fatalf("stale disposition = %+v", stale)
	}

	high, _ := l.Watermark("p", 1)
	if high != 5 {
		t.Fatalf("watermark = %d", high)
	}

	// A gen-2 watermark of 3 is not stale: generations are independent.
	res := commit(t, l, "p", 2, 3)
	if res.Outcome != CommitAdvanced {
		t.Fatalf("gen2 commit outcome = %s", res.Outcome)
	}
}

func TestOutOfOrderDeliveryAndCoalescing(t *testing.T) {
	l := NewLedger()
	ingest(t, l, "p", 1, 3, h(3))
	ingest(t, l, "p", 1, 5, h(5))
	commit(t, l, "p", 1, 5)
	assertIntervals(t, "gaps after out-of-order", mustMissing(t, l, 1),
		[]Interval{{1, 2}, {4, 4}})

	ingest(t, l, "p", 1, 1, h(1))
	ingest(t, l, "p", 1, 2, h(2))
	ingest(t, l, "p", 1, 4, h(4))
	assertIntervals(t, "all repaired", mustMissing(t, l, 1), nil)

	commit(t, l, "p", 1, 10)
	ingest(t, l, "p", 1, 1, h(1)) // duplicate
	ingest(t, l, "p", 1, 5, h(5))
	ingest(t, l, "p", 1, 10, h(10))
	assertIntervals(t, "coalesced gaps", mustMissing(t, l, 1), []Interval{{6, 9}})
}

func TestHistoricalRangeQuery(t *testing.T) {
	l := NewLedger()
	for _, seq := range []Seq{1, 2, 4} {
		ingest(t, l, "p", 1, seq, h(byte(seq)))
	}
	commit(t, l, "p", 1, 6)
	ingest(t, l, "p", 1, 7, h(7))
	ingest(t, l, "p", 1, 8, h(8))

	report, err := l.QueryRange("p", 1, 2, 8)
	if err != nil {
		t.Fatal(err)
	}
	assertIntervals(t, "present", report.Present, []Interval{{2, 2}, {4, 4}, {7, 8}})
	assertIntervals(t, "missing", report.Missing, []Interval{{3, 3}, {5, 6}})
	assertIntervals(t, "pending", report.Pending, nil)

	report, _ = l.QueryRange("p", 1, 5, 8)
	assertIntervals(t, "subrange missing", report.Missing, []Interval{{5, 6}})
	assertIntervals(t, "subrange present above high", report.Present, []Interval{{7, 8}})
	assertIntervals(t, "subrange pending", report.Pending, nil)

	if _, err := l.QueryRange("p", 1, 9, 3); !errors.Is(err, ErrReversedRange) {
		t.Fatalf("reversed range err = %v", err)
	}
	if _, err := l.QueryRange("p", 1, 0, 3); !errors.Is(err, ErrZeroSeq) {
		t.Fatalf("zero from err = %v", err)
	}
	if _, err := l.QueryRange("p", 9, 1, 3); !errors.Is(err, ErrUnknownEpoch) {
		t.Fatalf("unknown epoch err = %v", err)
	}
}

func TestNewlyConfirmedExcludesPresent(t *testing.T) {
	l := NewLedger()
	ingest(t, l, "p", 1, 2, h(2))
	res := commit(t, l, "p", 1, 5)
	assertIntervals(t, "newly confirmed", res.NewlyConfirmed, []Interval{{1, 1}, {3, 5}})
}

func TestFullSetAuditClean(t *testing.T) {
	l, err := NewLedgerWithReference(5)
	if err != nil {
		t.Fatal(err)
	}
	for _, seq := range []Seq{5, 3, 1, 4, 2} {
		ingest(t, l, "p", 1, seq, h(byte(seq)))
	}
	commit(t, l, "p", 1, 5)

	report, err := l.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Issues) != 0 {
		t.Fatalf("unexpected issues: %+v", report.Issues)
	}

	view, complete := l.Reference().ReferenceComplete(EpochKey{"p", 1})
	if !complete || !view.Complete || len(view.MissingFullSet) != 0 {
		t.Fatalf("reference not complete: %+v", view)
	}
}

func TestFullSetAuditFindsDuplicateConflictAndBoundary(t *testing.T) {
	l, _ := NewLedgerWithReference(10)
	ingest(t, l, "p", 1, 1, h(1))
	ingest(t, l, "p", 1, 1, h(1)) // duplicate commitment
	ingest(t, l, "p", 1, 2, h(0xaa))
	ingest(t, l, "p", 1, 2, h(0xbb)) // content conflict
	commit(t, l, "p", 1, 10)

	// Redundant and stale issuances are boundary evidence.
	commit(t, l, "p", 1, 10)
	if _, err := l.CommitWatermark(Watermark{Probe: "p", Gen: 1, High: 8}); err != nil {
		t.Fatal(err)
	}

	report, err := l.Audit()
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[string]int{}
	for _, issue := range report.Issues {
		kinds[issue.Kind]++
	}
	// seq 1 was re-delivered identically (one duplicate); seq 2 was
	// re-delivered twice with divergent content, so it is both a duplicate
	// delivery and a content conflict.
	if kinds[AuditDuplicateDelivery] != 2 {
		t.Fatalf("duplicate count = %d, issues = %+v", kinds[AuditDuplicateDelivery], report.Issues)
	}
	if kinds[AuditContentConflict] != 1 ||
		kinds[AuditRedundantIssuance] != 1 ||
		kinds[AuditStaleIssuance] != 1 {
		t.Fatalf("issue counts wrong: %+v", report.Issues)
	}
	// Only seq 1 and 2 were delivered while the issuance covers [1,10]: the
	// full-set completeness check must flag the never-delivered small seqs.
	if kinds[AuditIncompleteFullSet] != 1 {
		t.Fatalf("incomplete-full-set count = %d, issues = %+v", kinds[AuditIncompleteFullSet], report.Issues)
	}

	// Missing set must still agree: gaps 3..10 are real and reference-derived.
	if kinds[AuditMissingMismatch] != 0 || kinds[AuditPresentMismatch] != 0 {
		t.Fatalf("unexpected mismatch issues: %+v", report.Issues)
	}
}

func TestFullSetAuditIncompleteFullSet(t *testing.T) {
	l, _ := NewLedgerWithReference(4)
	ingest(t, l, "p", 1, 1, h(1))
	ingest(t, l, "p", 1, 3, h(3))
	commit(t, l, "p", 1, 4) // universe [1,4] covered; 2 and 4 never delivered

	report, err := l.Audit()
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Kind == AuditIncompleteFullSet {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected incomplete-full-set issue, got %+v", report.Issues)
	}

	view, complete := l.Reference().ReferenceComplete(EpochKey{"p", 1})
	if complete {
		t.Fatal("universe must not be reported complete with missing small sequences")
	}
	assertIntervals(t, "missing full-set", view.MissingFullSet, []Interval{{2, 2}, {4, 4}})
}

func TestAuditDetectsLedgerReferenceDivergence(t *testing.T) {
	// Feed the reference independently, then tamper with ledger state to prove
	// the audit re-derives truth rather than trusting the ledger.
	ref, err := NewFullSetReference(5)
	if err != nil {
		t.Fatal(err)
	}
	if err := ref.ObserveRecord("p", 1, 1, h(1)); err != nil {
		t.Fatal(err)
	}
	if err := ref.ObserveRecord("p", 1, 2, h(2)); err != nil {
		t.Fatal(err)
	}
	if err := ref.ObserveCommit("p", 1, 5); err != nil {
		t.Fatal(err)
	}

	l := NewLedger()
	ingest(t, l, "p", 1, 1, h(1))
	// Ledger is missing seq 2 and never committed: every reference-derived
	// expectation should surface as an issue.
	l.ref = ref

	report := ref.AuditLedger(l)
	kinds := map[string]int{}
	for _, issue := range report.Issues {
		kinds[issue.Kind]++
	}
	if kinds[AuditPresentMismatch] != 1 || kinds[AuditMissingMismatch] != 1 {
		t.Fatalf("divergence not detected: %+v", report.Issues)
	}
	if kinds[AuditCommitNotMonotonic] == 0 {
		t.Fatalf("issuance boundary divergence not detected: %+v", report.Issues)
	}
}

func TestValidationErrors(t *testing.T) {
	l, _ := NewLedgerWithReference(3)

	if _, err := l.IngestRecord(Record{Probe: "", Gen: 1, Seq: 1, Hash: h(1)}); !errors.Is(err, ErrZeroProbeID) {
		t.Fatalf("probe err = %v", err)
	}
	if _, err := l.IngestRecord(Record{Probe: "p", Gen: 0, Seq: 1, Hash: h(1)}); !errors.Is(err, ErrZeroGen) {
		t.Fatalf("gen err = %v", err)
	}
	if _, err := l.IngestRecord(Record{Probe: "p", Gen: 1, Seq: 0, Hash: h(1)}); !errors.Is(err, ErrZeroSeq) {
		t.Fatalf("seq err = %v", err)
	}
	if _, err := l.IngestRecord(Record{Probe: "p", Gen: 1, Seq: 1, Hash: nil}); !errors.Is(err, ErrEmptyHash) {
		t.Fatalf("hash err = %v", err)
	}
	if _, err := l.CommitWatermark(Watermark{Probe: "p", Gen: 0}); !errors.Is(err, ErrZeroGen) {
		t.Fatalf("commit gen err = %v", err)
	}
	if _, err := NewFullSetReference(0); !errors.Is(err, ErrLimitRange) {
		t.Fatalf("limit err = %v", err)
	}
	if _, err := NewLedger().Audit(); err == nil {
		t.Fatal("audit without reference must error")
	}
}

func TestRecordsBeyondReferenceLimitAreAuditExcluded(t *testing.T) {
	l, _ := NewLedgerWithReference(3)
	for seq := Seq(1); seq <= 6; seq++ {
		ingest(t, l, "p", 1, seq, h(byte(seq)))
	}
	commit(t, l, "p", 1, 6)

	report, err := l.Audit()
	if err != nil {
		t.Fatal(err)
	}
	// Small universe is complete; large sequences are outside the reference.
	for _, issue := range report.Issues {
		if issue.Kind == AuditIncompleteFullSet || issue.Kind == AuditPresentMismatch {
			t.Fatalf("unexpected issue for out-of-limit seq: %+v", issue)
		}
	}
	if report.Limit != 3 {
		t.Fatalf("limit = %d", report.Limit)
	}

	view, complete := l.Reference().ReferenceComplete(EpochKey{"p", 1})
	if !complete || len(view.MissingFullSet) != 0 {
		t.Fatalf("small universe should be complete: %+v", view)
	}
}

func TestConcurrentAccess(t *testing.T) {
	l, _ := NewLedgerWithReference(8)
	var wg sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for round := 0; round < 50; round++ {
				gen := Gen(1 + round%2)
				seq := Seq((worker*50+round)%16 + 1)
				if _, err := l.IngestRecord(Record{
					Probe: "p", Gen: gen, Seq: seq, Hash: h(byte(seq)),
				}); err != nil {
					t.Errorf("ingest: %v", err)
					return
				}
				if _, err := l.CommitWatermark(Watermark{
					Probe: "p", Gen: gen, High: Seq(16),
				}); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
				if _, err := l.QueryRange("p", gen, 1, 16); err != nil {
					t.Errorf("query: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	report, err := l.Audit()
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range report.Issues {
		if issue.Kind == AuditPresentMismatch || issue.Kind == AuditMissingMismatch {
			t.Fatalf("concurrent divergence: %+v", issue)
		}
	}
}

func TestIntervalHelpers(t *testing.T) {
	assertIntervals(t, "coalesce", pointsToIntervals([]Seq{1, 2, 3, 5, 6, 8}),
		[]Interval{{1, 3}, {5, 6}, {8, 8}})
	assertIntervals(t, "invert", invertPoints(1, 6, []Seq{2, 4, 5}),
		[]Interval{{1, 1}, {3, 3}, {6, 6}})
	assertIntervals(t, "invert empty", invertPoints(1, 3, nil), []Interval{{1, 3}})
	if got := safeNext(^Seq(0)); got != ^Seq(0) {
		t.Fatalf("overflow clamp = %d", got)
	}
}
