package ledger

import (
	"math"
	"testing"
	"time"
)

const quarterDays = 90

// buildAcceptanceLedger reproduces the acceptance scenario:
//
//	v1 (recorded day 1, by carol): KPI A weight 0.6 valid on [1, 60),
//	    0.4 on [60, 91); KPI B weight 0.4 on [1, 60), 0.6 on [60, 91).
//	v2 (recorded day 45, by alice): retroactive full-quarter adjustment —
//	    KPI A weight 0.4 and KPI B weight 0.6, both valid on [1, 91).
func buildAcceptanceLedger(t *testing.T) *Ledger {
	t.Helper()
	l, err := New(quarterDays)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	_, err = l.Append("carol", "Q1 initial strategy", 1, base, []Assignment{
		{KPI: "A", Target: 100, Weight: 0.6, Valid: Interval{From: 1, To: 60}},
		{KPI: "A", Target: 100, Weight: 0.4, Valid: Interval{From: 60, To: 91}},
		{KPI: "B", Target: 50, Weight: 0.4, Valid: Interval{From: 1, To: 60}},
		{KPI: "B", Target: 50, Weight: 0.6, Valid: Interval{From: 60, To: 91}},
	})
	if err != nil {
		t.Fatalf("append v1: %v", err)
	}

	_, err = l.Append("alice", "retroactive reweight: A 0.4 / B 0.6 for whole quarter", 45,
		base.Add(44*24*time.Hour), []Assignment{
			{KPI: "A", Target: 100, Weight: 0.4, Valid: Interval{From: 1, To: 91}},
			{KPI: "B", Target: 50, Weight: 0.6, Valid: Interval{From: 1, To: 91}},
		})
	if err != nil {
		t.Fatalf("append v2: %v", err)
	}
	return l
}

func acceptanceActuals() Actuals {
	return Actuals{"A": 110, "B": 40} // ratios: A = 1.1, B = 0.8
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEffectiveWeightsAreDayAveraged(t *testing.T) {
	l := buildAcceptanceLedger(t)

	v1, err := l.Score(1, acceptanceActuals())
	if err != nil {
		t.Fatalf("score v1: %v", err)
	}
	// A: (0.6*59 + 0.4*31) / 90 = 47.8/90; B: (0.4*59 + 0.6*31) / 90 = 42.2/90.
	if want := 47.8 / 90; !almostEqual(v1.Weights["A"], want) {
		t.Errorf("v1 weight A = %v, want %v", v1.Weights["A"], want)
	}
	if want := 42.2 / 90; !almostEqual(v1.Weights["B"], want) {
		t.Errorf("v1 weight B = %v, want %v", v1.Weights["B"], want)
	}

	v2, err := l.Score(2, acceptanceActuals())
	if err != nil {
		t.Fatalf("score v2: %v", err)
	}
	if !almostEqual(v2.Weights["A"], 0.4) || !almostEqual(v2.Weights["B"], 0.6) {
		t.Errorf("v2 weights = %v, want A=0.4 B=0.6", v2.Weights)
	}
}

func TestRetroactiveAdjustmentCoversWholeQuarter(t *testing.T) {
	l := buildAcceptanceLedger(t)

	// Day 10 is before the day-45 recording, yet v2 retroactively governs it.
	active, err := l.ActiveAt(2, 10)
	if err != nil {
		t.Fatalf("ActiveAt: %v", err)
	}
	got := map[string]float64{}
	for _, a := range active {
		got[a.KPI] = a.Weight
	}
	if !almostEqual(got["A"], 0.4) || !almostEqual(got["B"], 0.6) {
		t.Errorf("v2 active weights on day 10 = %v, want A=0.4 B=0.6", got)
	}

	// The historical snapshot still shows the original weights for that day.
	activeV1, err := l.ActiveAt(1, 10)
	if err != nil {
		t.Fatalf("ActiveAt v1: %v", err)
	}
	gotV1 := map[string]float64{}
	for _, a := range activeV1 {
		gotV1[a.KPI] = a.Weight
	}
	if !almostEqual(gotV1["A"], 0.6) || !almostEqual(gotV1["B"], 0.4) {
		t.Errorf("v1 active weights on day 10 = %v, want A=0.6 B=0.4", gotV1)
	}
}

func TestScoreDiffBeforeAndAfterAdjustment(t *testing.T) {
	l := buildAcceptanceLedger(t)

	imp, err := l.Impact(1, 2, acceptanceActuals())
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}

	wantBefore := 47.8/90*1.1 + 42.2/90*0.8 // 0.959333...
	wantAfter := 0.4*1.1 + 0.6*0.8          // 0.92
	if !almostEqual(imp.ScoreBefore, wantBefore) {
		t.Errorf("ScoreBefore = %v, want %v", imp.ScoreBefore, wantBefore)
	}
	if !almostEqual(imp.ScoreAfter, wantAfter) {
		t.Errorf("ScoreAfter = %v, want %v", imp.ScoreAfter, wantAfter)
	}
	if !almostEqual(imp.Delta, wantAfter-wantBefore) {
		t.Errorf("Delta = %v, want %v", imp.Delta, wantAfter-wantBefore)
	}
	if imp.Delta >= 0 {
		t.Errorf("expected negative delta, got %v", imp.Delta)
	}
}

func TestVersionSignatureBindsResponsibleAuthor(t *testing.T) {
	l := buildAcceptanceLedger(t)

	imp, err := l.Impact(1, 2, acceptanceActuals())
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if imp.Author != "alice" {
		t.Errorf("impact author = %q, want %q", imp.Author, "alice")
	}
	if imp.RecordedDay != 45 {
		t.Errorf("impact recorded day = %d, want 45", imp.RecordedDay)
	}
	if imp.Signature == "" {
		t.Fatal("impact must carry the responsible version signature")
	}

	v1, _ := l.Version(1)
	v2, _ := l.Version(2)
	if v1.Signature() == v2.Signature() {
		t.Error("distinct versions must have distinct signatures")
	}
	if imp.Signature != v2.Signature() {
		t.Error("impact signature must equal the new version's signature")
	}

	// Signatures are deterministic for identical content.
	other, _ := New(quarterDays)
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	dup, err := other.Append("carol", "Q1 initial strategy", 1, base, []Assignment{
		{KPI: "A", Target: 100, Weight: 0.6, Valid: Interval{From: 1, To: 60}},
		{KPI: "A", Target: 100, Weight: 0.4, Valid: Interval{From: 60, To: 91}},
		{KPI: "B", Target: 50, Weight: 0.4, Valid: Interval{From: 1, To: 60}},
		{KPI: "B", Target: 50, Weight: 0.6, Valid: Interval{From: 60, To: 91}},
	})
	if err != nil {
		t.Fatalf("append dup: %v", err)
	}
	if dup.Signature() != v1.Signature() {
		t.Error("identical version content must produce identical signature")
	}
}

func TestHistoricalSnapshotReplay(t *testing.T) {
	l := buildAcceptanceLedger(t)

	// Replaying under the historical v1 snapshot must not be affected by v2.
	replay, err := l.Score(1, acceptanceActuals())
	if err != nil {
		t.Fatalf("replay v1: %v", err)
	}
	if want := 47.8/90*1.1 + 42.2/90*0.8; !almostEqual(replay.Total, want) {
		t.Errorf("historical replay total = %v, want %v", replay.Total, want)
	}
	if len(l.Versions()) != 2 {
		t.Errorf("versions = %d, want 2 (append-only)", len(l.Versions()))
	}
}

func TestValidation(t *testing.T) {
	l, err := New(quarterDays)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	now := time.Now()
	ok := []Assignment{{KPI: "A", Target: 100, Weight: 0.5, Valid: Interval{From: 1, To: 91}}}

	if _, err := l.Append("", "no author", 1, now, ok); err == nil {
		t.Error("expected error for empty author")
	}
	if _, err := l.Append("x", "bad day", 0, now, ok); err == nil {
		t.Error("expected error for recordedDay 0")
	}
	if _, err := l.Append("x", "bad target", 1, now, []Assignment{
		{KPI: "A", Target: 0, Weight: 0.5, Valid: Interval{From: 1, To: 91}},
	}); err == nil {
		t.Error("expected error for non-positive target")
	}
	if _, err := l.Append("x", "bad weight", 1, now, []Assignment{
		{KPI: "A", Target: 100, Weight: 1.5, Valid: Interval{From: 1, To: 91}},
	}); err == nil {
		t.Error("expected error for weight > 1")
	}
	if _, err := l.Append("x", "bad interval", 1, now, []Assignment{
		{KPI: "A", Target: 100, Weight: 0.5, Valid: Interval{From: 60, To: 60}},
	}); err == nil {
		t.Error("expected error for empty interval")
	}
	if _, err := l.Append("x", "overflow interval", 1, now, []Assignment{
		{KPI: "A", Target: 100, Weight: 0.5, Valid: Interval{From: 1, To: 92}},
	}); err == nil {
		t.Error("expected error for interval beyond period")
	}
	if _, err := l.Score(1, Actuals{}); err == nil {
		t.Error("expected error for missing version")
	}
	if _, err := l.Append("x", "ok", 1, now, ok); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := l.Score(1, Actuals{}); err == nil {
		t.Error("expected error for missing actual value")
	}
}
