package tokenzip

import (
	"math/big"
	"reflect"
	"testing"
)

func mustBuild(t *testing.T, entries [][]Item, budget *big.Int) *Dict {
	t.Helper()
	d, err := Build(entries, budget)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	return d
}

func TestNestedRepeats(t *testing.T) {
	entries := [][]Item{
		{Lit("a"), Lit("b")},        // 0: a b
		{Ref(0), Ref(0), Lit("c")},  // 1: a b a b c
		{Ref(1), Lit("d"), Ref(1)},  // 2: a b a b c d a b a b c
		{Ref(2), Ref(2)},            // 3: entry2 twice
	}
	d := mustBuild(t, entries, nil)

	wantLens := []int64{2, 5, 11, 22}
	for i, want := range wantLens {
		if got := d.Len(i); got.Cmp(big.NewInt(want)) != 0 {
			t.Errorf("Len(%d) = %s, want %d", i, got, want)
		}
	}

	exp2, err := d.Expand(2)
	if err != nil {
		t.Fatal(err)
	}
	want2 := []string{"a", "b", "a", "b", "c", "d", "a", "b", "a", "b", "c"}
	if !reflect.DeepEqual(exp2, want2) {
		t.Errorf("Expand(2) = %v, want %v", exp2, want2)
	}

	// Interval query on the nested entry maps back to original tokens.
	got, err := d.Query(3, big.NewInt(4), big.NewInt(9))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"c", "d", "a", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Query(3, 4, 9) = %v, want %v", got, want)
	}

	// Every single position matches the full expansion.
	full, _ := d.Expand(3)
	for i := range full {
		one, err := d.Query(3, big.NewInt(int64(i)), big.NewInt(int64(i+1)))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(one, full[i:i+1]) {
			t.Fatalf("position %d: got %v, want %v", i, one, full[i:i+1])
		}
	}
}

func TestEmptyPhraseRejected(t *testing.T) {
	_, err := Build([][]Item{{Lit("a")}, {}}, nil)
	if e, ok := err.(*EmptyPhraseError); !ok || e.Entry != 1 {
		t.Fatalf("got %v, want EmptyPhraseError for entry 1", err)
	}
}

func TestBadReference(t *testing.T) {
	_, err := Build([][]Item{{Ref(5)}}, nil)
	if e, ok := err.(*BadReferenceError); !ok || e.Entry != 0 || e.Ref != 5 {
		t.Fatalf("got %v, want BadReferenceError{0, 5}", err)
	}
}

func TestCycleDetected(t *testing.T) {
	// Mutual cycle.
	_, err := Build([][]Item{{Ref(1)}, {Ref(0)}}, nil)
	if _, ok := err.(*CycleError); !ok {
		t.Fatalf("mutual cycle: got %v, want CycleError", err)
	}
	// Self cycle.
	_, err = Build([][]Item{{Lit("x"), Ref(0)}}, nil)
	if _, ok := err.(*CycleError); !ok {
		t.Fatalf("self cycle: got %v, want CycleError", err)
	}
}

func TestBudgetExceeded(t *testing.T) {
	entries := [][]Item{
		{Lit("a"), Lit("b")},
		{Ref(0), Ref(0)},
	}
	_, err := Build(entries, big.NewInt(3))
	e, ok := err.(*BudgetExceededError)
	if !ok {
		t.Fatalf("got %v, want BudgetExceededError", err)
	}
	if e.Limit.Cmp(big.NewInt(3)) != 0 || e.Required.Cmp(big.NewInt(4)) != 0 {
		t.Fatalf("budget error details: limit=%s required=%s", e.Limit, e.Required)
	}
	// Exactly at budget is fine.
	if _, err := Build(entries, big.NewInt(4)); err != nil {
		t.Fatalf("exact budget should pass: %v", err)
	}
}

func TestHugeExpansionShortInterval(t *testing.T) {
	// e0 = a b; e_i = e_{i-1} e_{i-1}; e64 has length 2^65.
	n := 65
	entries := make([][]Item, n)
	entries[0] = []Item{Lit("a"), Lit("b")}
	for i := 1; i < n; i++ {
		entries[i] = []Item{Ref(i - 1), Ref(i - 1)}
	}
	d := mustBuild(t, entries, nil)

	total := new(big.Int).Lsh(big.NewInt(1), 65)
	if got := d.Len(n - 1); got.Cmp(total) != 0 {
		t.Fatalf("Len = %s, want %s", got, total)
	}

	// Interval straddling the midpoint: position 2^64-1 is the last token of
	// the first half ("b"), 2^64 is the first token of the second half ("a").
	half := new(big.Int).Lsh(big.NewInt(1), 64)
	lo := new(big.Int).Sub(half, big.NewInt(1))
	hi := new(big.Int).Add(half, big.NewInt(1))
	got, err := d.Query(n-1, lo, hi)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b", "a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("midpoint interval = %v, want %v", got, want)
	}

	// A short interval deep inside: positions 3*2^62 .. 3*2^62+3.
	base := new(big.Int).Mul(big.NewInt(3), new(big.Int).Lsh(big.NewInt(1), 62))
	got, err = d.Query(n-1, base, new(big.Int).Add(base, big.NewInt(3)))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deep interval = %v, want %v", got, want)
	}

	// Last token of the whole expansion.
	last := new(big.Int).Sub(total, big.NewInt(1))
	got, err = d.Query(n-1, last, total)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("last token = %v, want %v", got, want)
	}
}

func TestHugeExpansionBudget(t *testing.T) {
	entries := [][]Item{{Lit("x"), Lit("y")}}
	for i := 1; i < 40; i++ {
		entries = append(entries, []Item{Ref(i - 1), Ref(i - 1)})
	}
	_, err := Build(entries, new(big.Int).Lsh(big.NewInt(1), 30))
	if _, ok := err.(*BudgetExceededError); !ok {
		t.Fatalf("got %v, want BudgetExceededError", err)
	}
}

func TestQueryOutOfRange(t *testing.T) {
	d := mustBuild(t, [][]Item{{Lit("a"), Lit("b")}}, nil)
	if _, err := d.Query(0, big.NewInt(1), big.NewInt(3)); err == nil {
		t.Fatal("expected out-of-range error")
	}
	if _, err := d.Query(9, big.NewInt(0), big.NewInt(1)); err == nil {
		t.Fatal("expected bad entry error")
	}
}
