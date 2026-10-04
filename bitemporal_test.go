package bitemporal

import (
	"testing"
	"time"
)

func d(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func iv(from, to string) Interval {
	t := Infinity
	if to != "" {
		t = d(to)
	}
	return Interval{From: d(from), To: t}
}

func mustPut(t *testing.T, s *Store, key, val string, valid Interval, sys string) {
	t.Helper()
	if _, err := s.Put(key, val, valid, d(sys)); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func mustCorrect(t *testing.T, s *Store, key, sys string, segs ...Segment) *Transaction {
	t.Helper()
	tx, err := s.Correct(key, d(sys), segs, "test")
	if err != nil {
		t.Fatalf("Correct: %v", err)
	}
	return tx
}

func asOf(t *testing.T, s *Store, key, valid, sys string) (string, bool) {
	t.Helper()
	return s.AsOf(key, d(valid), d(sys))
}

// 双时间查询：同一业务时刻，不同系统时刻看到不同版本。
func TestBitemporalQuery(t *testing.T) {
	s := NewStore()
	mustPut(t, s, "salary", "100", iv("2024-01-01", ""), "2024-01-01")
	mustCorrect(t, s, "salary", "2024-06-01",
		Segment{Valid: iv("2024-01-01", ""), Value: "120"})

	if got, ok := asOf(t, s, "salary", "2024-03-01", "2024-05-01"); !ok || got != "100" {
		t.Fatalf("old system slice: got %q ok=%v, want 100", got, ok)
	}
	if got, ok := asOf(t, s, "salary", "2024-03-01", "2024-06-01"); !ok || got != "120" {
		t.Fatalf("new system slice: got %q ok=%v, want 120", got, ok)
	}
	if _, ok := asOf(t, s, "salary", "2023-12-31", "2024-06-01"); ok {
		t.Fatal("before valid-from should miss")
	}
}

// 更正只关闭旧系统区间并追加版本，历史完整保留。
func TestCorrectKeepsHistory(t *testing.T) {
	s := NewStore()
	mustPut(t, s, "k", "A", iv("2024-01-01", ""), "2024-01-01")
	before := len(s.History("k"))

	tx := mustCorrect(t, s, "k", "2024-02-01",
		Segment{Valid: iv("2024-01-01", ""), Value: "B"})

	if got := len(s.History("k")); got != before+1 {
		t.Fatalf("history len = %d, want %d", got, before+1)
	}
	old := tx.Closed[0]
	if !old.Sys.To.Equal(d("2024-02-01")) {
		t.Fatalf("old version Sys.To = %v, want 2024-02-01", old.Sys.To)
	}
	if old.Value != "A" || tx.Added[0].Value != "B" {
		t.Fatalf("closed=%q added=%q", old.Value, tx.Added[0].Value)
	}
}

// 业务区间拆分：一段历史被拆成相邻两段，边界相接且完整覆盖原区间。
func TestSplitValidInterval(t *testing.T) {
	s := NewStore()
	mustPut(t, s, "salary", "100", iv("2024-01-01", ""), "2024-01-01")

	segs := []Segment{
		{Valid: iv("2024-03-01", ""), Value: "120"},
		{Valid: iv("2024-01-01", "2024-03-01"), Value: "100"},
	}
	if err := CheckTiling(segs, d("2024-01-01"), Infinity); err != nil {
		t.Fatalf("tiling: %v", err)
	}
	mustCorrect(t, s, "salary", "2024-06-01", segs...)

	if got, _ := asOf(t, s, "salary", "2024-02-15", "2024-06-01"); got != "100" {
		t.Fatalf("first piece: got %q, want 100", got)
	}
	if got, _ := asOf(t, s, "salary", "2024-03-01", "2024-06-01"); got != "120" {
		t.Fatalf("second piece: got %q, want 120", got)
	}
	slice := s.SliceAt("salary", d("2024-06-01"))
	if len(slice) != 2 || !slice[0].Valid.To.Equal(slice[1].Valid.From) {
		t.Fatalf("slice not adjacent: %v", slice)
	}
}

// 边界不相接（有缝或重叠）必须被拒绝。
func TestCheckTilingRejectsGapAndOverlap(t *testing.T) {
	gap := []Segment{
		{Valid: iv("2024-01-01", "2024-03-01"), Value: "a"},
		{Valid: iv("2024-04-01", ""), Value: "b"},
	}
	if err := CheckTiling(gap, d("2024-01-01"), Infinity); err == nil {
		t.Fatal("gap should fail tiling")
	}
	overlap := []Segment{
		{Valid: iv("2024-01-01", "2024-03-01"), Value: "a"},
		{Valid: iv("2024-02-01", ""), Value: "b"},
	}
	if err := CheckTiling(overlap, d("2024-01-01"), Infinity); err == nil {
		t.Fatal("overlap should fail tiling")
	}
}

// 同一键在同一系统切面内禁止业务区间重叠。
func TestOverlapRejected(t *testing.T) {
	s := NewStore()
	mustPut(t, s, "k", "A", iv("2024-01-01", "2024-06-01"), "2024-01-01")

	if _, err := s.Put("k", "B", iv("2024-03-01", "2024-09-01"), d("2024-01-02")); err == nil {
		t.Fatal("overlapping Put should fail")
	}
	if _, err := s.Correct("k", d("2024-02-01"), []Segment{
		{Valid: iv("2024-01-01", "2024-04-01"), Value: "X"},
		{Valid: iv("2024-03-01", ""), Value: "Y"},
	}, "overlap"); err == nil {
		t.Fatal("mutually overlapping segments should fail")
	}
	// 失败的更正不得留下副作用：旧版本仍然打开。
	if got, _ := asOf(t, s, "k", "2024-02-01", "2024-02-01"); got != "A" {
		t.Fatalf("failed correction left side effects, got %q", got)
	}
	// 不同键互不影响。
	mustPut(t, s, "other", "Z", iv("2024-01-01", "2024-06-01"), "2024-01-02")
}

// 撤销更正：恢复原值，且撤销本身也记入历史。
func TestUndoCorrection(t *testing.T) {
	s := NewStore()
	mustPut(t, s, "k", "A", iv("2024-01-01", ""), "2024-01-01")
	tx := mustCorrect(t, s, "k", "2024-02-01",
		Segment{Valid: iv("2024-01-01", ""), Value: "B"})

	undo, err := s.Undo(tx.ID, d("2024-03-01"))
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if got, _ := asOf(t, s, "k", "2024-01-15", "2024-03-01"); got != "A" {
		t.Fatalf("after undo: got %q, want A", got)
	}
	// 撤销前的系统切面仍能看到 B（历史不可篡改）。
	if got, _ := asOf(t, s, "k", "2024-01-15", "2024-02-15"); got != "B" {
		t.Fatalf("pre-undo slice: got %q, want B", got)
	}
	if len(undo.Closed) != 1 || len(undo.Added) != 1 {
		t.Fatalf("undo closed=%d added=%d", len(undo.Closed), len(undo.Added))
	}
	if _, err := s.Undo(tx.ID, d("2024-03-02")); err == nil {
		t.Fatal("double undo should fail")
	}
}

// 后续更正覆盖了同一区间时，撤销旧更正必须失败。
func TestUndoConflict(t *testing.T) {
	s := NewStore()
	mustPut(t, s, "k", "A", iv("2024-01-01", ""), "2024-01-01")
	tx1 := mustCorrect(t, s, "k", "2024-02-01",
		Segment{Valid: iv("2024-01-01", ""), Value: "B"})
	mustCorrect(t, s, "k", "2024-03-01",
		Segment{Valid: iv("2024-01-01", ""), Value: "C"})

	if _, err := s.Undo(tx1.ID, d("2024-04-01")); err == nil {
		t.Fatal("undo of superseded correction should fail")
	}
	if got, _ := asOf(t, s, "k", "2024-01-15", "2024-04-01"); got != "C" {
		t.Fatalf("failed undo left side effects, got %q", got)
	}
}
