package merge

import (
	"math/rand"
	"reflect"
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func cand(source string, prio int, seq int64, v interface{}) Candidate {
	return Candidate{Source: source, Priority: prio, Seq: seq, Value: v}
}

func TestPriorityWins(t *testing.T) {
	rec := Record{"f": {
		cand("low", 1, 100, "a"),
		cand("high", 2, 1, "b"),
	}}
	d := Merge(rec, base).Fields["f"]
	if d.Status != Resolved || d.Value != "b" {
		t.Fatalf("got %v %v", d.Status, d.Value)
	}
}

func TestSeqBreaksTie(t *testing.T) {
	rec := Record{"f": {
		cand("a", 1, 1, "old"),
		cand("b", 1, 2, "new"),
	}}
	d := Merge(rec, base).Fields["f"]
	if d.Status != Resolved || d.Value != "new" {
		t.Fatalf("got %v %v", d.Status, d.Value)
	}
}

func TestConflictOnSamePrioritySameSeqDifferentValue(t *testing.T) {
	rec := Record{"f": {
		cand("a", 1, 1, "x"),
		cand("b", 1, 1, "y"),
		cand("c", 0, 99, "z"),
	}}
	res := Merge(rec, base)
	d := res.Fields["f"]
	if d.Status != Conflict {
		t.Fatalf("want conflict, got %v", d.Status)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0] != "f" {
		t.Fatalf("conflicts: %v", res.Conflicts)
	}
	// 确定性兜底：Source 字典序最小者。
	if d.Value != "x" {
		t.Fatalf("fallback winner: %v", d.Value)
	}
}

func TestSameValueTieIsNotConflict(t *testing.T) {
	rec := Record{"f": {
		cand("a", 1, 1, "x"),
		cand("b", 1, 1, "x"),
	}}
	if d := Merge(rec, base).Fields["f"]; d.Status != Resolved {
		t.Fatalf("got %v", d.Status)
	}
}

func TestValidityWindowFilters(t *testing.T) {
	rec := Record{"f": {
		{Source: "expired", Priority: 9, Seq: 9, Value: "old",
			ValidFrom: base.Add(-2 * time.Hour), ValidTo: base.Add(-time.Hour)},
		{Source: "future", Priority: 9, Seq: 9, Value: "new",
			ValidFrom: base.Add(time.Hour)},
		{Source: "current", Priority: 1, Seq: 1, Value: "now"},
	}}
	d := Merge(rec, base).Fields["f"]
	if d.Status != Resolved || d.Value != "now" {
		t.Fatalf("got %v %v", d.Status, d.Value)
	}
	// 解释中应记录两条被时间过滤的候选。
	var filtered int
	for _, ex := range d.Candidates {
		if !ex.Effective {
			filtered++
		}
	}
	if filtered != 2 {
		t.Fatalf("filtered = %d", filtered)
	}
}

func TestBoundaryInclusiveFromExclusiveTo(t *testing.T) {
	rec := Record{"f": {
		{Source: "s", Priority: 1, Seq: 1, Value: "v",
			ValidFrom: base, ValidTo: base.Add(time.Hour)},
	}}
	if d := Merge(rec, base).Fields["f"]; d.Status != Resolved {
		t.Fatal("ValidFrom 应闭区间")
	}
	if d := Merge(rec, base.Add(time.Hour)).Fields["f"]; d.Status != Empty {
		t.Fatal("ValidTo 应开区间")
	}
}

func TestMissingDoesNotOverrideExplicitNull(t *testing.T) {
	// 高优先级来源缺失该字段（无候选），低优先级来源显式 null 应保留。
	rec := Record{"f": {
		{Source: "low", Priority: 1, Seq: 1, Null: true},
	}}
	d := Merge(rec, base).Fields["f"]
	if d.Status != Resolved || !d.Null || d.Value != nil {
		t.Fatalf("got %v null=%v value=%v", d.Status, d.Null, d.Value)
	}
	// 显式 null 也能在竞争中胜出。
	rec2 := Record{"f": {
		{Source: "a", Priority: 2, Seq: 1, Null: true},
		cand("b", 1, 9, "value"),
	}}
	if d := Merge(rec2, base).Fields["f"]; !d.Null {
		t.Fatalf("explicit null should win, got %v", d.Value)
	}
}

func TestNullVsValueConflict(t *testing.T) {
	rec := Record{"f": {
		{Source: "a", Priority: 1, Seq: 1, Null: true},
		cand("b", 1, 1, "v"),
	}}
	if d := Merge(rec, base).Fields["f"]; d.Status != Conflict {
		t.Fatalf("null vs value should conflict, got %v", d.Status)
	}
}

func TestExplanationCoversAllCandidates(t *testing.T) {
	rec := Record{"f": {
		cand("a", 1, 1, "x"),
		cand("b", 2, 1, "y"),
		cand("c", 2, 2, "z"),
	}}
	d := Merge(rec, base).Fields["f"]
	if len(d.Candidates) != 3 {
		t.Fatalf("explanations = %d", len(d.Candidates))
	}
	var winners int
	for _, ex := range d.Candidates {
		if !ex.Effective {
			t.Fatal("all should be effective")
		}
		if ex.Reason == "" {
			t.Fatal("reason must be filled")
		}
		if ex.Reason == "winner" {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d", winners)
	}
}

func TestEmptyRecordAndEmptyField(t *testing.T) {
	if res := Merge(Record{}, base); len(res.Decisions) != 0 {
		t.Fatal("empty record should have no decisions")
	}
	if d := Merge(Record{"f": nil}, base).Fields["f"]; d.Status != Empty {
		t.Fatalf("got %v", d.Status)
	}
}

// TestAgainstReferenceModel 用随机记录交叉核对 Merge 与直接排序筛选模型。
func TestAgainstReferenceModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	sources := []string{"s0", "s1", "s2"}
	values := []interface{}{"a", "b", 1, 2, true, nil}
	for trial := 0; trial < 5000; trial++ {
		rec := Record{}
		nFields := rng.Intn(4)
		for fi := 0; fi < nFields; fi++ {
			field := string(rune('a' + fi))
			n := rng.Intn(5)
			for ci := 0; ci < n; ci++ {
				c := Candidate{
					Source:   sources[rng.Intn(len(sources))],
					Priority: rng.Intn(3),
					Seq:      int64(rng.Intn(3)),
				}
				if rng.Intn(4) == 0 {
					c.Null = true
				} else {
					c.Value = values[rng.Intn(len(values))]
				}
				switch rng.Intn(4) {
				case 0: // 无界
				case 1:
					c.ValidFrom = base.Add(time.Duration(rng.Intn(3)-1) * time.Hour)
				case 2:
					c.ValidTo = base.Add(time.Duration(rng.Intn(3)-1) * time.Hour)
				case 3:
					c.ValidFrom = base.Add(-time.Hour)
					c.ValidTo = base.Add(time.Hour)
				}
				rec[field] = append(rec[field], c)
			}
		}
		qt := base.Add(time.Duration(rng.Intn(3)-1) * time.Hour)
		got := Merge(rec, qt)
		want := referenceMerge(rec, qt)
		for f, wd := range want.Fields {
			gd := got.Fields[f]
			if gd.Status != wd.Status {
				t.Fatalf("trial %d field %s: status %v vs %v\nrec=%v t=%v",
					trial, f, gd.Status, wd.Status, rec[f], qt)
			}
			if gd.Null != wd.Null || !reflect.DeepEqual(gd.Value, wd.Value) {
				t.Fatalf("trial %d field %s: (%v,%v) vs (%v,%v)",
					trial, f, gd.Value, gd.Null, wd.Value, wd.Null)
			}
			if (gd.Winner == nil) != (wd.Winner == nil) {
				t.Fatalf("trial %d field %s: winner mismatch", trial, f)
			}
			if gd.Winner != nil && gd.Winner.Source != wd.Winner.Source {
				t.Fatalf("trial %d field %s: winner source %s vs %s",
					trial, f, gd.Winner.Source, wd.Winner.Source)
			}
		}
		if !reflect.DeepEqual(got.Conflicts, want.Conflicts) {
			t.Fatalf("trial %d: conflicts %v vs %v", trial, got.Conflicts, want.Conflicts)
		}
	}
}
