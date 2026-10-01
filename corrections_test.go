package main

import (
	"testing"
	"time"
)

func strptr(s string) *string      { return &s }
func tmptr(t time.Time) *time.Time { return &t }

// TestLateCorrectionRewritesHistory: a corrected alert entity/time turns a
// false positive into a detection, and the history records the match diff
// plus updated per-category stats.
func TestLateCorrectionRewritesHistory(t *testing.T) {
	cfg := testConfig()
	f1 := mkFault("f1", "h1", "cpu", base, base.Add(30*time.Minute))
	f2 := mkFault("f2", "h2", "cpu", base.Add(40*time.Minute), base.Add(60*time.Minute))
	fx := &Fixture{
		Config: cfg,
		Faults: []Fault{f1, f2},
		Alerts: []Alert{
			mkAlert("a1", "h1", "cpu", base.Add(2*time.Minute)),
			mkAlert("a2", "hX", "cpu", base.Add(45*time.Minute)), // wrong entity, later corrected
		},
		Corrections: []Correction{
			{Kind: "alert", ID: "a2", Entity: strptr("h2")},
		},
	}
	before := Match(fx.Faults, fx.Alerts, fx.Config)
	if before.MatchedCount != 1 || len(before.UnmatchedAlerts) != 1 || len(before.MissedFaults) != 1 {
		t.Fatalf("precondition failed: %+v", before)
	}
	hist, err := ApplyCorrections(fx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 {
		t.Fatalf("want 1 history entry, got %d", len(hist))
	}
	h := hist[0]
	if len(h.Added) != 1 || h.Added[0].FaultID != "f2" || h.Added[0].AlertID != "a2" {
		t.Fatalf("want added match f2<-a2, got %+v", h.Added)
	}
	if len(h.Removed) != 0 {
		t.Fatalf("want no removed matches, got %+v", h.Removed)
	}
	if h.Stats.Matched != 2 || h.Stats.FalsePositives != 0 || h.Stats.FalseNegatives != 0 {
		t.Fatalf("stats not updated after correction: %+v", h.Stats)
	}
	if h.Stats.Detections != 2 || h.Stats.MaxLatency != 5*time.Minute {
		t.Fatalf("latency stats wrong: %+v", h.Stats)
	}
}

// TestCorrectionCascadesAcrossSteps: two sequential corrections, each
// re-matching on top of the previous state.
func TestCorrectionCascadesAcrossSteps(t *testing.T) {
	cfg := testConfig()
	open := Fault{ID: "f1", Entity: "h", Symptom: "cpu", Start: base.Add(40 * time.Minute)}
	fx := &Fixture{
		Config: cfg,
		Faults: []Fault{open},
		Alerts: []Alert{mkAlert("a1", "h", "cpu", base.Add(50*time.Minute))},
		Corrections: []Correction{
			// first: fault gets an end before obs end but after the alert -> still matched
			{Kind: "fault", ID: "f1", End: tmptr(base.Add(55 * time.Minute))},
			// second: alert time corrected past end+lag -> match breaks, fault becomes missed
			{Kind: "alert", ID: "a1", Time: tmptr(base.Add(70 * time.Minute))},
		},
	}
	hist, err := ApplyCorrections(fx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("want 2 entries, got %d", len(hist))
	}
	if hist[0].Stats.Matched != 1 {
		t.Fatalf("step 1 should keep the match: %+v", hist[0].Stats)
	}
	if len(hist[1].Removed) != 1 || hist[1].Stats.FalseNegatives != 1 || hist[1].Stats.FalsePositives != 1 {
		t.Fatalf("step 2 should break the match into FP+FN: %+v", hist[1])
	}
}

// TestClockOffsetSweepShiftsMetrics: shifting the alert clock changes which
// alerts fall inside fault windows; metrics must stay consistent per offset.
func TestClockOffsetSweepShiftsMetrics(t *testing.T) {
	cfg := testConfig()
	faults := []Fault{mkFault("f1", "h", "cpu", base, base.Add(10*time.Minute))}
	alerts := []Alert{mkAlert("a1", "h", "cpu", base.Add(8*time.Minute))}

	matched := map[bool]int{}
	for _, off := range []time.Duration{0, 15 * time.Minute, 30 * time.Minute} {
		shifted := []Alert{{ID: "a1", Entity: "h", Symptom: "cpu", Time: alerts[0].Time.Add(off)}}
		s := computeStats(Match(faults, shifted, cfg))
		matched[s.Matched == 1]++
	}
	if matched[true] == 0 || matched[false] == 0 {
		t.Fatalf("offset sweep should flip the match outcome: %v", matched)
	}
	// at offset 0 the alert is inside the window with 8m latency
	s := computeStats(Match(faults, alerts, cfg))
	if s.Matched != 1 || s.MeanLatency != 8*time.Minute {
		t.Fatalf("bad baseline: %+v", s)
	}
}

// TestFixtureEndToEnd replays the shipped fixture and checks the headline
// numbers stay stable.
func TestFixtureEndToEnd(t *testing.T) {
	fx, err := loadFixture("testdata/fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	res := Match(fx.Faults, fx.Alerts, fx.Config)
	if res.MatchedCount != 2 {
		t.Fatalf("want 2 initial matches, got %d", res.MatchedCount)
	}
	s := computeStats(res)
	if s.FalsePositives != 2 || s.FalseNegatives != 1 || s.RightCensored != 1 {
		t.Fatalf("bad initial stats: %+v", s)
	}
	if s.Leads != 1 || s.MeanLead != 15*time.Minute {
		t.Fatalf("a3 should be a 15m lead: %+v", s)
	}
	hist, err := ApplyCorrections(fx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("want 2 corrections, got %d", len(hist))
	}
	// after correction 1 (a4 -> host-b @11:02): f4 detected, FP=1 FN=0 censored=1
	if h := hist[0].Stats; h.Matched != 3 || h.FalsePositives != 1 || h.FalseNegatives != 0 || h.RightCensored != 1 {
		t.Fatalf("bad stats after correction 1: %+v", h)
	}
	// after correction 2 (f3 ends 10:55, before obs end): f3 becomes missed
	if h := hist[1].Stats; h.RightCensored != 0 || h.FalseNegatives != 1 {
		t.Fatalf("bad stats after correction 2: %+v", h)
	}
	// enumeration must confirm optimality on the fixture too
	bestCount, bestCost := enumerateOptimal(fx.Faults, fx.Alerts, fx.Config)
	final := Match(fx.Faults, fx.Alerts, fx.Config)
	if final.MatchedCount != bestCount || final.TotalCost != bestCost {
		t.Fatalf("fixture matching not optimal: %d/%v vs %d/%v",
			final.MatchedCount, final.TotalCost, bestCount, bestCost)
	}
}
