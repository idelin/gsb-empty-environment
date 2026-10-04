package incjoin

import (
	"math/rand"
	"reflect"
	"testing"
)

func checkAgainstFullJoin(t *testing.T, e *Engine) {
	t.Helper()
	want := FullJoin(e.a, e.b)
	got := e.Result()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incremental result mismatch:\n got: %v\nwant: %v", got, want)
	}
}

func applyDelta(base, d Delta) map[string]int {
	out := make(map[string]int, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range d {
		out[k] += v
		if out[k] == 0 {
			delete(out, k)
		}
	}
	return out
}

func TestDuplicateKeys(t *testing.T) {
	e := New()
	// 3 copies of "x" in A, 2 copies in B -> 6 result rows.
	d, err := e.Apply(Batch{
		{Side: A, Key: "x", N: 3},
		{Side: B, Key: "x", N: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d["x"] != 6 {
		t.Fatalf("delta = %v, want x:6", d)
	}
	// One more copy on each side: 4*3 - 3*2 = 6 new rows.
	d, err = e.Apply(Batch{
		{Side: A, Key: "x", N: 1},
		{Side: B, Key: "x", N: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d["x"] != 6 {
		t.Fatalf("delta = %v, want x:6", d)
	}
	checkAgainstFullJoin(t, e)
}

func TestDeleteToZero(t *testing.T) {
	e := New()
	if _, err := e.Apply(Batch{
		{Side: A, Key: "k", N: 2},
		{Side: B, Key: "k", N: 5},
	}); err != nil {
		t.Fatal(err)
	}
	// Delete all of A's copies: result for "k" drops from 10 to 0.
	d, err := e.Apply(Batch{{Side: A, Key: "k", N: 2, Delete: true}})
	if err != nil {
		t.Fatal(err)
	}
	if d["k"] != -10 {
		t.Fatalf("delta = %v, want k:-10", d)
	}
	if len(e.Result()) != 0 {
		t.Fatalf("result should be empty, got %v", e.Result())
	}
	if e.Multiplicity(A, "k") != 0 || e.Multiplicity(B, "k") != 5 {
		t.Fatalf("bad multiplicities: A=%d B=%d",
			e.Multiplicity(A, "k"), e.Multiplicity(B, "k"))
	}
	checkAgainstFullJoin(t, e)
}

func TestSameBatchDeleteThenInsert(t *testing.T) {
	e := New()
	if _, err := e.Apply(Batch{
		{Side: A, Key: "k", N: 4},
		{Side: B, Key: "k", N: 3},
	}); err != nil {
		t.Fatal(err)
	}
	// Same batch, same key: delete 3 then insert 5 -> net +2 on A.
	// Result: 6*3 - 4*3 = +6.
	d, err := e.Apply(Batch{
		{Side: A, Key: "k", N: 3, Delete: true},
		{Side: A, Key: "k", N: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d["k"] != 6 {
		t.Fatalf("delta = %v, want k:6", d)
	}
	if e.Multiplicity(A, "k") != 6 {
		t.Fatalf("A multiplicity = %d, want 6", e.Multiplicity(A, "k"))
	}
	checkAgainstFullJoin(t, e)
}

func TestCrossDeltaWithinBatch(t *testing.T) {
	e := New()
	if _, err := e.Apply(Batch{
		{Side: A, Key: "k", N: 2},
		{Side: B, Key: "k", N: 3},
	}); err != nil {
		t.Fatal(err)
	}
	// Both tables change in one batch: dA=+1, dB=+1.
	// dR = dA*B_old + A_old*dB + dA*dB = 1*3 + 2*1 + 1*1 = 6.
	d, err := e.Apply(Batch{
		{Side: A, Key: "k", N: 1},
		{Side: B, Key: "k", N: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d["k"] != 6 {
		t.Fatalf("delta = %v, want k:6 (cross term dA*dB=1 must be counted)", d)
	}
	// Brand-new key inserted on both sides in the same batch:
	// only the cross term contributes. dR = 2*2 = 4.
	d, err = e.Apply(Batch{
		{Side: A, Key: "new", N: 2},
		{Side: B, Key: "new", N: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d["new"] != 4 {
		t.Fatalf("delta = %v, want new:4", d)
	}
	checkAgainstFullJoin(t, e)
}

func TestRejectNonPositiveCount(t *testing.T) {
	e := New()
	for _, n := range []int{0, -1, -100} {
		if _, err := e.Apply(Batch{{Side: A, Key: "x", N: n}}); err != ErrNonPositiveCount {
			t.Fatalf("N=%d: err = %v, want ErrNonPositiveCount", n, err)
		}
		if _, err := e.Apply(Batch{{Side: B, Key: "x", N: n, Delete: true}}); err != ErrNonPositiveCount {
			t.Fatalf("delete N=%d: err = %v, want ErrNonPositiveCount", n, err)
		}
	}
}

func TestRejectNegativeMultiplicity(t *testing.T) {
	e := New()
	if _, err := e.Apply(Batch{{Side: A, Key: "x", N: 2}}); err != nil {
		t.Fatal(err)
	}
	// Deleting more than present is rejected.
	if _, err := e.Apply(Batch{{Side: A, Key: "x", N: 3, Delete: true}}); err != ErrNegativeMultiplicity {
		t.Fatalf("err = %v, want ErrNegativeMultiplicity", err)
	}
	// Deleting a key that does not exist is rejected.
	if _, err := e.Apply(Batch{{Side: B, Key: "ghost", N: 1, Delete: true}}); err != ErrNegativeMultiplicity {
		t.Fatalf("err = %v, want ErrNegativeMultiplicity", err)
	}
	// Failed batches are atomic: state unchanged.
	if e.Multiplicity(A, "x") != 2 {
		t.Fatalf("state mutated by rejected batch: A[x]=%d", e.Multiplicity(A, "x"))
	}
	// Within one batch a later insert does not rescue an earlier
	// over-delete (ops apply in order).
	if _, err := e.Apply(Batch{
		{Side: A, Key: "x", N: 3, Delete: true},
		{Side: A, Key: "x", N: 3},
	}); err != ErrNegativeMultiplicity {
		t.Fatalf("err = %v, want ErrNegativeMultiplicity", err)
	}
	checkAgainstFullJoin(t, e)
}

func TestDeltaAppliesToPreviousResult(t *testing.T) {
	e := New()
	maintained := map[string]int{}
	batches := []Batch{
		{{Side: A, Key: "a", N: 2}, {Side: B, Key: "a", N: 3}},
		{{Side: A, Key: "a", N: 1, Delete: true}, {Side: B, Key: "b", N: 4}},
		{{Side: A, Key: "b", N: 5}, {Side: B, Key: "a", N: 3, Delete: true}},
	}
	for i, batch := range batches {
		d, err := e.Apply(batch)
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		maintained = applyDelta(maintained, d)
		if !reflect.DeepEqual(maintained, e.Result()) {
			t.Fatalf("batch %d: applying delta to previous result = %v, engine = %v",
				i, maintained, e.Result())
		}
	}
	checkAgainstFullJoin(t, e)
}

func TestRandomizedAgainstFullJoin(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	e := New()
	keys := []string{"p", "q", "r", "s", "t"}
	for step := 0; step < 500; step++ {
		var batch Batch
		working := map[Side]map[string]int{A: {}, B: {}}
		for _, s := range []Side{A, B} {
			for _, k := range keys {
				working[s][k] = e.Multiplicity(s, k)
			}
		}
		for j := 0; j < rng.Intn(5); j++ {
			side := Side(rng.Intn(2))
			key := keys[rng.Intn(len(keys))]
			if rng.Intn(2) == 0 {
				n := 1 + rng.Intn(3)
				batch = append(batch, Op{Side: side, Key: key, N: n})
				working[side][key] += n
			} else if cur := working[side][key]; cur > 0 {
				n := 1 + rng.Intn(cur)
				batch = append(batch, Op{Side: side, Key: key, N: n, Delete: true})
				working[side][key] -= n
			}
		}
		if _, err := e.Apply(batch); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		checkAgainstFullJoin(t, e)
	}
}
