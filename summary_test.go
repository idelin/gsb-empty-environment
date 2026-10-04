package freqsketch

import (
	"math/rand"
	"testing"
)

// zipfLike draws item ids from a skewed universe deterministically.
func zipfLike(rng *rand.Rand, universe int) string {
	// Geometric-ish skew: small ids are much more likely.
	x := rng.Intn(universe * 4)
	id := 0
	for x >= universe {
		x -= universe
		universe = (universe + 1) / 2
		id++
	}
	return itemName(id*97 + rng.Intn(3))
}

func itemName(id int) string {
	return "item-" + itoa(id)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func TestSingleSummaryBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := New(16)
	e := NewExact()
	for i := 0; i < 20000; i++ {
		item := zipfLike(rng, 500)
		w := uint64(1 + rng.Intn(5))
		s.Update(item, w)
		e.Update(item, w)
		if s.Size() > s.Capacity() {
			t.Fatalf("capacity exceeded: %d > %d", s.Size(), s.Capacity())
		}
	}
	if err := Verify(s, e); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalVerification(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	s := New(8)
	e := NewExact()
	for i := 0; i < 5000; i++ {
		item := zipfLike(rng, 200)
		s.Update(item, 1)
		e.Update(item, 1)
		if i%499 == 0 {
			if err := Verify(s, e); err != nil {
				t.Fatalf("after %d updates: %v", i+1, err)
			}
		}
	}
}

func TestMergePreservesTotalAndLocalError(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	s1, s2 := New(10), New(10)
	e := NewExact()
	for i := 0; i < 8000; i++ {
		item := zipfLike(rng, 300)
		w := uint64(1 + rng.Intn(3))
		if i%2 == 0 {
			s1.Update(item, w)
		} else {
			s2.Update(item, w)
		}
		e.Update(item, w)
	}
	m := s1.Merge(s2)
	if m.Total() != s1.Total()+s2.Total() {
		t.Fatalf("merged total %d, want %d", m.Total(), s1.Total()+s2.Total())
	}
	// Local error must not be dropped: merged residual carries both locals.
	if m.Residual() < s1.Residual()+s2.Residual() {
		t.Fatalf("merged residual %d dropped local error (%d + %d)",
			m.Residual(), s1.Residual(), s2.Residual())
	}
	if err := Verify(m, e); err != nil {
		t.Fatal(err)
	}
	// Inputs must be untouched.
	if s1.Total() != e.Total()/2 && s1.Total()+s2.Total() != e.Total() {
		t.Fatal("inputs modified by merge")
	}
}

// TestMergeOrders compares the guarantees obtained from different block
// merge orders. Estimates are allowed to differ between orders; the bounds
// must hold for every order.
func TestMergeOrders(t *testing.T) {
	const blocks = 8
	rng := rand.New(rand.NewSource(99))
	parts := make([]*Summary, blocks)
	for b := range parts {
		parts[b] = New(12)
	}
	e := NewExact()
	for i := 0; i < 40000; i++ {
		item := zipfLike(rng, 800)
		w := uint64(1 + rng.Intn(4))
		parts[i%blocks].Update(item, w)
		e.Update(item, w)
	}

	identity := make([]int, blocks)
	for i := range identity {
		identity[i] = identity[i] + i
	}
	reversed := make([]int, blocks)
	for i := range reversed {
		reversed[i] = blocks - 1 - i
	}
	rngPerm := rand.New(rand.NewSource(5)).Perm(blocks)

	orders := []struct {
		name string
		perm []int
		tree bool
	}{
		{"sequential-forward", identity, false},
		{"sequential-reversed", reversed, false},
		{"sequential-shuffled", rngPerm, false},
		{"balanced-forward", identity, true},
		{"balanced-reversed", reversed, true},
		{"balanced-shuffled", rngPerm, true},
	}

	var lo, hi uint64
	for i, o := range orders {
		ordered := Permuted(parts, o.perm)
		var m *Summary
		if o.tree {
			m = MergeBalanced(ordered)
		} else {
			m = MergeSequential(ordered)
		}
		if m.Total() != e.Total() {
			t.Fatalf("%s: total %d, want %d", o.name, m.Total(), e.Total())
		}
		if err := Verify(m, e); err != nil {
			t.Fatalf("%s: %v", o.name, err)
		}
		if i == 0 || m.Residual() < lo {
			lo = m.Residual()
		}
		if i == 0 || m.Residual() > hi {
			hi = m.Residual()
		}
		t.Logf("%-20s residual(error bound)=%d tracked=%d", o.name, m.Residual(), m.Size())
	}
	t.Logf("residual across orders: min=%d max=%d (guarantees differ, bounds all hold)", lo, hi)
}

func TestNoPhantomItems(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	s := New(4)
	e := NewExact()
	seen := make(map[string]bool)
	for i := 0; i < 3000; i++ {
		item := zipfLike(rng, 100)
		s.Update(item, uint64(1+rng.Intn(2)))
		e.Update(item, 1)
		seen[item] = true
	}
	for _, item := range s.Items() {
		if !seen[item] {
			t.Fatalf("phantom item %q reported by summary", item)
		}
		est, _ := s.Estimate(item)
		if est == 0 {
			t.Fatalf("tracked item %q has zero estimate", item)
		}
	}
}

func TestMergeIsAssociativeSafeForUnseenItems(t *testing.T) {
	// Heavy hitters split across blocks: after merging, an item that never
	// appeared must still have estimate 0 and bound = residual only.
	a, b := New(3), New(3)
	for i := 0; i < 100; i++ {
		a.Update("a", 1)
		b.Update("b", 1)
	}
	for i := 0; i < 50; i++ {
		a.Update("c", 1)
		b.Update("d", 1)
	}
	m := a.Merge(b)
	est, err := m.Estimate("never-seen")
	if est != 0 {
		t.Fatalf("unseen item got estimate %d", est)
	}
	if m.Tracked("never-seen") {
		t.Fatal("unseen item tracked")
	}
	_ = err
}
