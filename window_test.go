package windowagg_test

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"sort"
	"testing"

	"windowagg"
)

// model is a naive reference implementation: a plain sorted slice.
type model struct {
	vals []int64 // always sorted
}

func (m *model) insert(v int64) {
	i := sort.Search(len(m.vals), func(i int) bool { return m.vals[i] >= v })
	m.vals = append(m.vals, 0)
	copy(m.vals[i+1:], m.vals[i:])
	m.vals[i] = v
}

func (m *model) revoke(v int64) bool {
	i := sort.Search(len(m.vals), func(i int) bool { return m.vals[i] >= v })
	if i >= len(m.vals) || m.vals[i] != v {
		return false
	}
	m.vals = append(m.vals[:i], m.vals[i+1:]...)
	return true
}

func (m *model) sum() *big.Int {
	s := new(big.Int)
	for _, v := range m.vals {
		s.Add(s, big.NewInt(v))
	}
	return s
}

func (m *model) median() (*big.Rat, bool) {
	n := len(m.vals)
	if n == 0 {
		return nil, false
	}
	if n%2 == 1 {
		return new(big.Rat).SetInt64(m.vals[n/2]), true
	}
	s := big.NewInt(m.vals[n/2-1])
	s.Add(s, big.NewInt(m.vals[n/2]))
	return new(big.Rat).SetFrac(s, big.NewInt(2)), true
}

func (m *model) trimmedMean(k int) (*big.Rat, bool) {
	n := len(m.vals)
	if k < 0 || n < 2*k+1 {
		return nil, false
	}
	s := new(big.Int)
	for _, v := range m.vals[k : n-k] {
		s.Add(s, big.NewInt(v))
	}
	return new(big.Rat).SetFrac(s, big.NewInt(int64(n-2*k))), true
}

func checkAgainstModel(t *testing.T, w *windowagg.Window, m *model, ctx string) {
	t.Helper()
	if w.Len() != len(m.vals) {
		t.Fatalf("%s: Len = %d, model = %d", ctx, w.Len(), len(m.vals))
	}
	if ws, ms := w.Sum(), m.sum(); ws.Cmp(ms) != 0 {
		t.Fatalf("%s: Sum = %s, model = %s", ctx, ws, ms)
	}
	wmed, wok := w.Median()
	mmed, mok := m.median()
	if wok != mok || (wok && wmed.Cmp(mmed) != 0) {
		t.Fatalf("%s: Median = %v,%v, model = %v,%v", ctx, wmed, wok, mmed, mok)
	}
	for k := 0; k <= 4; k++ {
		wtm, wok := w.TrimmedMean(k)
		mtm, mok := m.trimmedMean(k)
		if wok != mok || (wok && wtm.Cmp(mtm) != 0) {
			t.Fatalf("%s: TrimmedMean(%d) = %v,%v, model = %v,%v", ctx, k, wtm, wok, mtm, mok)
		}
	}
}

func TestEmptyWindow(t *testing.T) {
	var w windowagg.Window
	if w.Len() != 0 {
		t.Fatalf("Len = %d, want 0", w.Len())
	}
	if _, ok := w.Median(); ok {
		t.Fatal("Median on empty window should be missing")
	}
	if _, ok := w.TrimmedMean(0); ok {
		t.Fatal("TrimmedMean(0) on empty window should be missing")
	}
	if s := w.Sum(); s.Sign() != 0 {
		t.Fatalf("Sum = %s, want 0", s)
	}
	if w.Revoke(7) {
		t.Fatal("Revoke on empty window should report false")
	}
	checkAgainstModel(t, &w, &model{}, "empty")
}

func TestDuplicateBoundaryValues(t *testing.T) {
	var w windowagg.Window
	var m model
	for _, v := range []int64{1, 1, 2, 2, 2, 3} {
		w.Insert(v)
		m.insert(v)
	}
	checkAgainstModel(t, &w, &m, "insert 1,1,2,2,2,3")

	// k=1 trims one 1 and the 3, leaving 1,2,2,2 -> 7/4.
	if got, ok := w.TrimmedMean(1); !ok || got.RatString() != "7/4" {
		t.Fatalf("TrimmedMean(1) = %v,%v, want 7/4,true", got, ok)
	}
	// k=2 trims 1,1 and 2,3, leaving 2,2 -> 2.
	if got, ok := w.TrimmedMean(2); !ok || got.RatString() != "2" {
		t.Fatalf("TrimmedMean(2) = %v,%v, want 2,true", got, ok)
	}
	// Revoking one copy of a duplicated boundary value must keep the other.
	if !w.Revoke(1) || !w.Revoke(2) {
		t.Fatal("Revoke of present duplicates should report true")
	}
	m.revoke(1)
	m.revoke(2)
	checkAgainstModel(t, &w, &m, "after revoking one 1 and one 2")
	// Window is now {1,2,2,3}; trimming 1 and 3 leaves 2,2 -> 2.
	if got, ok := w.TrimmedMean(1); !ok || got.RatString() != "2" {
		t.Fatalf("TrimmedMean(1) = %v,%v, want 2,true", got, ok)
	}
	// Drain the window completely and confirm it behaves empty again.
	for _, v := range []int64{1, 2, 2, 3} {
		w.Revoke(v)
		m.revoke(v)
	}
	checkAgainstModel(t, &w, &m, "drained")
}

func TestTrimmedMeanThreshold(t *testing.T) {
	var w windowagg.Window
	for _, v := range []int64{10, 20, 30, 40, 50} {
		w.Insert(v)
	}
	if _, ok := w.TrimmedMean(-1); ok {
		t.Fatal("TrimmedMean(-1) should be missing")
	}
	if _, ok := w.TrimmedMean(2); !ok {
		t.Fatal("TrimmedMean(2) with 5 values should be present (2*2+1 = 5)")
	}
	if _, ok := w.TrimmedMean(3); ok {
		t.Fatal("TrimmedMean(3) with 5 values should be missing")
	}
	w.Revoke(30)
	if _, ok := w.TrimmedMean(2); ok {
		t.Fatal("TrimmedMean(2) with 4 values should be missing")
	}
	if got, ok := w.TrimmedMean(1); !ok || got.RatString() != "30" {
		t.Fatalf("TrimmedMean(1) = %v,%v, want 30,true", got, ok)
	}
}

func TestRevokeAbsentValue(t *testing.T) {
	var w windowagg.Window
	var m model
	w.Insert(5)
	m.insert(5)
	if w.Revoke(4) || w.Revoke(6) {
		t.Fatal("Revoke of absent values should report false")
	}
	checkAgainstModel(t, &w, &m, "after revoking absent values")
}

func TestLargeIntegerSums(t *testing.T) {
	var w windowagg.Window
	var m model
	vals := []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MinInt64, math.MinInt64}
	for _, v := range vals {
		w.Insert(v)
		m.insert(v)
	}
	// 3*(2^63-1) - 2*2^63 = 2^63 - 3, which overflows int64.
	want := big.NewInt(0).Sub(big.NewInt(0).Lsh(big.NewInt(1), 63), big.NewInt(3))
	if got := w.Sum(); got.Cmp(want) != 0 {
		t.Fatalf("Sum = %s, want %s", got, want)
	}
	checkAgainstModel(t, &w, &m, "extreme values")

	// Median of {MinInt64, MaxInt64} is -1/2, not representable in int64.
	var w2 windowagg.Window
	w2.Insert(math.MaxInt64)
	w2.Insert(math.MinInt64)
	if got, ok := w2.Median(); !ok || got.RatString() != "-1/2" {
		t.Fatalf("Median = %v,%v, want -1/2,true", got, ok)
	}
	// Mean of three copies of MaxInt64 must stay exact: MaxInt64/1.
	var w3 windowagg.Window
	for i := 0; i < 3; i++ {
		w3.Insert(math.MaxInt64)
	}
	if got, ok := w3.TrimmedMean(1); !ok || got.RatString() != fmt.Sprint(math.MaxInt64) {
		t.Fatalf("TrimmedMean(1) = %v,%v, want %d,true", got, ok, math.MaxInt64)
	}
}

func TestRandomizedAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	var w windowagg.Window
	var m model
	for op := 0; op < 3000; op++ {
		v := int64(rng.Intn(9) - 4) // small range forces many duplicates
		if rng.Intn(5) < 3 {
			w.Insert(v)
			m.insert(v)
		} else {
			if got, want := w.Revoke(v), m.revoke(v); got != want {
				t.Fatalf("op %d: Revoke(%d) = %v, model = %v", op, v, got, want)
			}
		}
		checkAgainstModel(t, &w, &m, fmt.Sprintf("op %d", op))
	}
}

func TestRandomizedExtremeValues(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	pool := []int64{math.MinInt64, math.MinInt64 + 1, -1, 0, 1, math.MaxInt64 - 1, math.MaxInt64}
	var w windowagg.Window
	var m model
	for op := 0; op < 2000; op++ {
		v := pool[rng.Intn(len(pool))]
		if rng.Intn(2) == 0 {
			w.Insert(v)
			m.insert(v)
		} else {
			if got, want := w.Revoke(v), m.revoke(v); got != want {
				t.Fatalf("op %d: Revoke(%d) = %v, model = %v", op, v, got, want)
			}
		}
		checkAgainstModel(t, &w, &m, fmt.Sprintf("op %d", op))
	}
}
