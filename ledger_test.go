package kpi

import (
	"math"
	"testing"
)

const quarterDays = 90

// 验收边界场景：
// v1（第1天登记）：目标A 权重0.6 在 [1,60) 有效，目标B 权重0.4 在 [1,60) 有效；
// v2（第45天登记，追溯全季度）：目标A 权重0.4 [1,91)，目标B 权重0.6 [1,91)。
func buildAcceptanceLedger() *Ledger {
	l := NewLedger(quarterDays)
	l.AddVersion("张三", "年初战略规划", 1, []Entry{
		{KPI: "A", Weight: 0.6, StartDay: 1, EndDay: 60},
		{KPI: "B", Weight: 0.4, StartDay: 1, EndDay: 60},
	})
	l.AddVersion("李四", "战略重心由A转向B，追溯全季度", 45, []Entry{
		{KPI: "A", Weight: 0.4, StartDay: 1, EndDay: 91},
		{KPI: "B", Weight: 0.6, StartDay: 1, EndDay: 91},
	})
	return l
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEffectiveWeightsV1(t *testing.T) {
	l := buildAcceptanceLedger()
	w, err := l.EffectiveWeights(1)
	if err != nil {
		t.Fatal(err)
	}
	// 0.6 * 59/90, 0.4 * 59/90
	if !almostEqual(w["A"], 0.6*59.0/90.0) {
		t.Errorf("A effective weight = %v, want %v", w["A"], 0.6*59.0/90.0)
	}
	if !almostEqual(w["B"], 0.4*59.0/90.0) {
		t.Errorf("B effective weight = %v, want %v", w["B"], 0.4*59.0/90.0)
	}
}

func TestEffectiveWeightsV2Retroactive(t *testing.T) {
	l := buildAcceptanceLedger()
	w, err := l.EffectiveWeights(2)
	if err != nil {
		t.Fatal(err)
	}
	if !almostEqual(w["A"], 0.4) || !almostEqual(w["B"], 0.6) {
		t.Errorf("v2 weights = %v, want A=0.4 B=0.6", w)
	}
}

func TestScoreDiffBeforeAfter(t *testing.T) {
	l := buildAcceptanceLedger()
	actuals := map[string]float64{"A": 80, "B": 90}

	before, err := l.Score(1, actuals)
	if err != nil {
		t.Fatal(err)
	}
	after, err := l.Score(2, actuals)
	if err != nil {
		t.Fatal(err)
	}

	wantBefore := (0.6*59.0/90.0)*80 + (0.4*59.0/90.0)*90 // 55.0666...
	wantAfter := 0.4*80 + 0.6*90                          // 86
	if !almostEqual(before, wantBefore) {
		t.Errorf("before = %v, want %v", before, wantBefore)
	}
	if !almostEqual(after, wantAfter) {
		t.Errorf("after = %v, want %v", after, wantAfter)
	}

	imp, err := l.Impact(1, 2, actuals)
	if err != nil {
		t.Fatal(err)
	}
	if !almostEqual(imp.ScoreDelta, wantAfter-wantBefore) {
		t.Errorf("delta = %v, want %v", imp.ScoreDelta, wantAfter-wantBefore)
	}
	if len(imp.WeightChanges) != 2 {
		t.Fatalf("weight changes = %v, want 2 entries", imp.WeightChanges)
	}
}

func TestHistoricalSnapshotPreserved(t *testing.T) {
	l := buildAcceptanceLedger()
	// 历史快照不受后续版本影响
	v1, ok := l.Version(1)
	if !ok {
		t.Fatal("version 1 missing")
	}
	if v1.Entries[0].Weight != 0.6 || v1.Entries[0].StartDay != 1 || v1.Entries[0].EndDay != 60 {
		t.Errorf("v1 snapshot mutated: %+v", v1.Entries[0])
	}
	if len(l.Versions()) != 2 {
		t.Errorf("versions = %d, want 2", len(l.Versions()))
	}
}

func TestVersionSignature(t *testing.T) {
	l := buildAcceptanceLedger()
	v1, _ := l.Version(1)
	v2, _ := l.Version(2)
	if v1.Signature == "" || v2.Signature == "" {
		t.Fatal("signature must be recorded")
	}
	if v1.Signature == v2.Signature {
		t.Error("different versions must have different signatures")
	}
	if v2.Author != "李四" {
		t.Errorf("v2 author = %q, want 李四", v2.Author)
	}
	// 签名为内容哈希，可重算校验
	if sign(v2) != v2.Signature {
		t.Error("signature does not match recomputed content hash")
	}
}

func TestErrors(t *testing.T) {
	l := buildAcceptanceLedger()
	if _, err := l.Score(99, nil); err == nil {
		t.Error("expected error for unknown version")
	}
	bad := NewLedger(90)
	bad.AddVersion("王五", "坏区间", 1, []Entry{{KPI: "A", Weight: 1, StartDay: 10, EndDay: 10}})
	if _, err := bad.EffectiveWeights(1); err == nil {
		t.Error("expected error for empty interval")
	}
}
