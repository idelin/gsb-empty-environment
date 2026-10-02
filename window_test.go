package swq

import (
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"
)

var t0 = time.Unix(0, 0)

func at(sec float64) time.Time {
	return t0.Add(time.Duration(sec * float64(time.Second)))
}

func mustQuantile(t *testing.T, q *Quantiler, quantile float64) float64 {
	t.Helper()
	v, err := q.Quantile(quantile)
	if err != nil {
		t.Fatalf("Quantile(%v) error: %v", quantile, err)
	}
	return v
}

// Acceptance: with [1, 1, 9, 20] in the window, q=0.5 must be 1
// (rank floor(0.5*3)=1); after retracting one 1, q=0.5 must be 9
// (rank floor(0.5*2)=1 of [1, 9, 20]).
func TestAcceptanceScenario(t *testing.T) {
	q := New(10 * time.Second)
	q.Add(1, at(0))
	dup := q.Add(1, at(1))
	q.Add(9, at(2))
	q.Add(20, at(3))

	if got := mustQuantile(t, q, 0.5); got != 1 {
		t.Fatalf("q=0.5 of [1,1,9,20] = %v, want 1", got)
	}
	if !q.Remove(dup) {
		t.Fatal("Remove returned false for a live event")
	}
	if got := mustQuantile(t, q, 0.5); got != 9 {
		t.Fatalf("q=0.5 of [1,9,20] after retract = %v, want 9", got)
	}
	if q.Remove(dup) {
		t.Fatal("second Remove of the same ID must fail")
	}
}

// Duplicate (equal) values must be kept as distinct events and retracted
// one at a time.
func TestEqualValuesDeduplicateCorrectly(t *testing.T) {
	q := New(10 * time.Second)
	ids := make([]uint64, 0, 3)
	for i := 0; i < 3; i++ {
		ids = append(ids, q.Add(7, at(float64(i))))
	}
	if q.Len() != 3 {
		t.Fatalf("Len = %d, want 3", q.Len())
	}
	q.Remove(ids[0])
	if q.Len() != 2 {
		t.Fatalf("Len after one retract = %d, want 2", q.Len())
	}
	if got := mustQuantile(t, q, 0); got != 7 {
		t.Fatalf("min = %v, want 7", got)
	}
	if got := mustQuantile(t, q, 1); got != 7 {
		t.Fatalf("max = %v, want 7", got)
	}
	q.Remove(ids[1])
	q.Remove(ids[2])
	if _, err := q.Quantile(0.5); err != ErrEmpty {
		t.Fatalf("empty window error = %v, want ErrEmpty", err)
	}
}

// Events expire exactly when ts+window <= now; the boundary event
// (ts+window == now) is evicted.
func TestExpiryBoundary(t *testing.T) {
	q := New(10 * time.Second)
	q.Add(1, at(0))
	q.Add(2, at(5))

	q.Advance(at(9.999))
	if q.Len() != 2 {
		t.Fatalf("Len at 9.999s = %d, want 2", q.Len())
	}
	q.Advance(at(10)) // event at t=0 expires exactly here
	if q.Len() != 1 {
		t.Fatalf("Len at 10s = %d, want 1", q.Len())
	}
	if got := mustQuantile(t, q, 0.5); got != 2 {
		t.Fatalf("remaining value = %v, want 2", got)
	}
	q.Advance(at(15)) // event at t=5 expires
	if q.Len() != 0 {
		t.Fatalf("Len at 15s = %d, want 0", q.Len())
	}
}

// Out-of-order insertion must not corrupt ordering or expiry.
func TestOutOfOrderInsertion(t *testing.T) {
	q := New(10 * time.Second)
	q.Add(30, at(8))
	q.Add(10, at(2))
	q.Add(20, at(5))
	q.Add(40, at(1)) // late arrival, still inside the window
	q.Add(50, at(9))

	want := []float64{10, 20, 30, 40, 50}
	for i, w := range want {
		qq := float64(i) / float64(len(want)-1)
		if got := mustQuantile(t, q, qq); got != w {
			t.Fatalf("rank %d = %v, want %v", i, got, w)
		}
	}
}

// Rank rule: floor(q*(n-1)).
func TestRankRule(t *testing.T) {
	q := New(10 * time.Second)
	for i, v := range []float64{5, 1, 4, 2, 3} { // sorted: 1 2 3 4 5
		q.Add(v, at(float64(i)))
	}
	cases := []struct {
		q    float64
		want float64
	}{
		{0, 1}, {0.25, 2}, {0.5, 3}, {0.75, 4}, {1, 5},
		{0.4999, 2}, {0.5001, 3}, {0.9999, 4},
	}
	for _, c := range cases {
		if got := mustQuantile(t, q, c.q); got != c.want {
			t.Errorf("Quantile(%v) = %v, want %v", c.q, got, c.want)
		}
	}
	if _, err := q.Quantile(-0.1); err != ErrBadQuantile {
		t.Errorf("Quantile(-0.1) error = %v, want ErrBadQuantile", err)
	}
	if _, err := q.Quantile(1.1); err != ErrBadQuantile {
		t.Errorf("Quantile(1.1) error = %v, want ErrBadQuantile", err)
	}
}

// Randomized differential test against a brute-force sorted-slice model:
// random adds (some out of order), retracts and clock advances, checking
// length and several quantiles after every step.
func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	q := New(10 * time.Second)
	type modelEvent struct {
		id    uint64
		value float64
		ts    time.Time
	}
	var model []modelEvent
	now := t0
	qnow := t0 // clock as known to the Quantiler (only moves via Add/Advance)
	qs := []float64{0, 0.1, 0.25, 0.5, 0.9, 0.99, 1}

	check := func(step int) {
		t.Helper()
		// Expire model events exactly like the window does.
		kept := model[:0]
		for _, e := range model {
			if e.ts.Add(10 * time.Second).After(qnow) {
				kept = append(kept, e)
			}
		}
		model = kept
		if q.Len() != len(model) {
			t.Fatalf("step %d: Len = %d, want %d", step, q.Len(), len(model))
		}
		if len(model) == 0 {
			return
		}
		vs := make([]float64, len(model))
		for i, e := range model {
			vs[i] = e.value
		}
		sort.Float64s(vs)
		for _, qq := range qs {
			want := vs[int(math.Floor(qq*float64(len(vs)-1)))]
			if got := mustQuantile(t, q, qq); got != want {
				t.Fatalf("step %d: Quantile(%v) = %v, want %v (n=%d)",
					step, qq, got, want, len(vs))
			}
		}
	}

	for step := 0; step < 5000; step++ {
		switch rng.Intn(4) {
		case 0, 1: // add, sometimes out of order
			now = now.Add(time.Duration(rng.Float64() * 0.4 * float64(time.Second)))
			ts := now
			if rng.Intn(3) == 0 {
				ts = ts.Add(-time.Duration(rng.Float64() * 8 * float64(time.Second)))
			}
			v := float64(rng.Intn(20)) // small range -> many duplicates
			id := q.Add(v, ts)
			model = append(model, modelEvent{id: id, value: v, ts: ts})
			if ts.After(qnow) {
				qnow = ts
			}
		case 2: // retract a random live event
			if len(model) > 0 {
				i := rng.Intn(len(model))
				if !q.Remove(model[i].id) {
					t.Fatalf("step %d: Remove(%d) failed", step, model[i].id)
				}
				model = append(model[:i], model[i+1:]...)
			}
		case 3: // advance the clock
			now = now.Add(time.Duration(rng.Float64() * float64(time.Second)))
			q.Advance(now)
			qnow = now
		}
		check(step)
	}
}
