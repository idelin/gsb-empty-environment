package slidewin

import (
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"
)

var t0 = time.Unix(1700000000, 0)

func at(sec float64) time.Time {
	return t0.Add(time.Duration(sec * float64(time.Second)))
}

func mustQuantile(t *testing.T, w *Window, q float64) float64 {
	t.Helper()
	v, ok := w.Quantile(q)
	if !ok {
		t.Fatalf("Quantile(%v) on empty window", q)
	}
	return v
}

// Acceptance: window holds [1, 1, 9, 20]; q=0.5 -> floor(0.5*3)=1 -> 1.
// Retracting one of the 1s leaves [1, 9, 20]; q=0.5 -> floor(0.5*2)=1 -> 9.
func TestAcceptanceScenario(t *testing.T) {
	w := NewWindow(10 * time.Second)
	ids := make(map[float64][]uint64)
	for _, v := range []float64{1, 1, 9, 20} {
		id, live := w.Add(at(0), v)
		if !live {
			t.Fatalf("event %v not live", v)
		}
		ids[v] = append(ids[v], id)
	}
	if got := mustQuantile(t, w, 0.5); got != 1 {
		t.Fatalf("q=0.5 of [1,1,9,20] = %v, want 1", got)
	}
	if !w.Retract(ids[1][0]) {
		t.Fatal("retract of a live 1 failed")
	}
	if got := mustQuantile(t, w, 0.5); got != 9 {
		t.Fatalf("q=0.5 after retracting one 1 = %v, want 9", got)
	}
}

// floor(q*(n-1)) rank positioning across the whole q range.
func TestFloorRankPositioning(t *testing.T) {
	w := NewWindow(10 * time.Second)
	vals := []float64{10, 30, 20, 40} // sorted: [10 20 30 40], n=4
	for _, v := range vals {
		w.Add(at(1), v)
	}
	cases := []struct {
		q    float64
		want float64
	}{
		{0.0, 10}, {0.01, 10}, {1.0 / 3.0, 20}, {0.5, 20},
		{0.66, 20}, {2.0 / 3.0, 30}, {0.99, 30}, {1.0, 40},
	}
	for _, c := range cases {
		if got := mustQuantile(t, w, c.q); got != c.want {
			t.Errorf("Quantile(%v) = %v, want %v", c.q, got, c.want)
		}
	}
}

// Events expire exactly when now-ts >= size; window is (now-size, now].
func TestExpiration(t *testing.T) {
	w := NewWindow(10 * time.Second)
	w.Add(at(0), 100)
	w.Add(at(5), 200)
	if w.Len() != 2 {
		t.Fatalf("Len = %d, want 2", w.Len())
	}
	w.Advance(at(9.999))
	if w.Len() != 2 {
		t.Fatalf("at 9.999s Len = %d, want 2", w.Len())
	}
	w.Advance(at(10)) // first event now exactly 10s old -> expired
	if w.Len() != 1 {
		t.Fatalf("at 10s Len = %d, want 1", w.Len())
	}
	if got := mustQuantile(t, w, 0.5); got != 200 {
		t.Fatalf("remaining value = %v, want 200", got)
	}
	w.Advance(at(15))
	if w.Len() != 0 {
		t.Fatalf("at 15s Len = %d, want 0", w.Len())
	}
	if _, ok := w.Quantile(0.5); ok {
		t.Fatal("quantile on drained window should report !ok")
	}
}

// Out-of-order inserts land in the same multiset as in-order inserts.
func TestOutOfOrderInsert(t *testing.T) {
	inOrder := NewWindow(10 * time.Second)
	shuffled := NewWindow(10 * time.Second)
	stamps := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	for i, s := range stamps {
		inOrder.Add(at(s), float64(i*7%13))
	}
	perm := rand.New(rand.NewSource(42)).Perm(len(stamps))
	for _, i := range perm {
		shuffled.Add(at(stamps[i]), float64(i*7%13))
	}
	for _, q := range []float64{0, 0.25, 0.5, 0.75, 1} {
		a := mustQuantile(t, inOrder, q)
		b := mustQuantile(t, shuffled, q)
		if a != b {
			t.Fatalf("q=%v: in-order %v != shuffled %v", q, a, b)
		}
	}
}

// A late event that is already expired never enters the window.
func TestBornExpired(t *testing.T) {
	w := NewWindow(10 * time.Second)
	w.Add(at(100), 1)
	if _, live := w.Add(at(50), 999); live {
		t.Fatal("event 50s behind window head must not go live")
	}
	if w.Len() != 1 {
		t.Fatalf("Len = %d, want 1", w.Len())
	}
}

// Equal values coexist and retract one occurrence at a time.
func TestDuplicateRetract(t *testing.T) {
	w := NewWindow(10 * time.Second)
	var ones []uint64
	for i := 0; i < 3; i++ {
		id, _ := w.Add(at(0), 1)
		ones = append(ones, id)
	}
	w.Add(at(0), 5)
	if w.Len() != 4 {
		t.Fatalf("Len = %d, want 4", w.Len())
	}
	for i, id := range ones {
		if !w.Retract(id) {
			t.Fatalf("retract #%d failed", i)
		}
		want := 4 - (i + 1)
		if w.Len() != want {
			t.Fatalf("after %d retracts Len = %d, want %d", i+1, w.Len(), want)
		}
	}
	if w.Retract(ones[0]) {
		t.Fatal("double retract of the same id must fail")
	}
	if got := mustQuantile(t, w, 0); got != 5 {
		t.Fatalf("only 5 should remain, got %v", got)
	}
}

// Retracting an already-expired id fails cleanly.
func TestRetractExpired(t *testing.T) {
	w := NewWindow(10 * time.Second)
	id, _ := w.Add(at(0), 7)
	w.Advance(at(20))
	if w.Retract(id) {
		t.Fatal("retract of expired event must fail")
	}
}

// Property test: random add/advance/retract stream vs naive sorted model.
func TestPropertyAgainstSortedModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 20; trial++ {
		w := NewWindow(10 * time.Second)
		type rec struct {
			ns    int64
			value float64
		}
		toNs := func(sec float64) int64 { return int64(sec * float64(time.Second)) }
		windowNs := int64(10 * time.Second)
		model := make(map[uint64]rec)
		now := 0.0
		head := math.Inf(-1) // max timestamp actually communicated to the window
		for step := 0; step < 400; step++ {
			switch rng.Intn(4) {
			case 0, 1: // add (sometimes out of order, often duplicated values)
				now += rng.Float64() * 2
				ts := now
				if rng.Intn(3) == 0 {
					ts -= rng.Float64() * 12 // may be born expired
				}
				v := float64(rng.Intn(6)) // small domain -> many duplicates
				id, live := w.Add(at(ts), v)
				if ts > head {
					head = ts
				}
				cutoff := toNs(head) - windowNs
				for mid, r := range model {
					if r.ns <= cutoff {
						delete(model, mid)
					}
				}
				wantLive := toNs(ts) > cutoff
				if live != wantLive {
					t.Fatalf("trial %d step %d: live=%v want %v", trial, step, live, wantLive)
				}
				if live {
					model[id] = rec{toNs(ts), v}
				}
			case 2: // advance
				now += rng.Float64() * 3
				w.Advance(at(now))
				if now > head {
					head = now
				}
				cutoff := toNs(head) - windowNs
				for mid, r := range model {
					if r.ns <= cutoff {
						delete(model, mid)
					}
				}
			case 3: // retract a random live id
				if len(model) == 0 {
					continue
				}
				ids := make([]uint64, 0, len(model))
				for id := range model {
					ids = append(ids, id)
				}
				victim := ids[rng.Intn(len(ids))]
				if !w.Retract(victim) {
					t.Fatalf("trial %d step %d: retract of live id failed", trial, step)
				}
				delete(model, victim)
			}

			if w.Len() != len(model) {
				t.Fatalf("trial %d step %d: Len=%d want %d", trial, step, w.Len(), len(model))
			}
			if len(model) == 0 {
				continue
			}
			sorted := make([]float64, 0, len(model))
			for _, r := range model {
				sorted = append(sorted, r.value)
			}
			sort.Float64s(sorted)
			for _, q := range []float64{0, 0.1, 0.5, 0.9, 1} {
				k := int(math.Floor(q * float64(len(sorted)-1)))
				if got := mustQuantile(t, w, q); got != sorted[k] {
					t.Fatalf("trial %d step %d: Quantile(%v)=%v want %v (n=%d)",
						trial, step, q, got, sorted[k], len(sorted))
				}
			}
		}
	}
}
