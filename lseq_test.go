package lseq

import (
	"math/rand"
	"testing"
)

// TestCompareOrder checks the total order over identifiers.
func TestCompareOrder(t *testing.T) {
	a := Identifier{{Digit: 5, Site: 1}}
	b := Identifier{{Digit: 5, Site: 1}, {Digit: 3, Site: 2}}
	c := Identifier{{Digit: 5, Site: 2}}
	d := Identifier{{Digit: 6, Site: 1}}
	if Compare(a, a) != 0 {
		t.Fatal("identifier must equal itself")
	}
	if Compare(a, b) >= 0 {
		t.Fatal("prefix must order before its extension")
	}
	if Compare(a, c) >= 0 {
		t.Fatal("same digit: lower site must order first")
	}
	if Compare(b, c) >= 0 || Compare(c, d) >= 0 {
		t.Fatal("expected (5#1)(3#2) < (5#2) < (6#1)")
	}
}

// TestAllocateBetween allocates between random neighbours and verifies
// strict betweenness plus uniqueness.
func TestAllocateBetween(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	a := NewAllocator(1, rng)
	ids := map[string]struct{}{}
	var prev Identifier
	for i := 0; i < 5000; i++ {
		id := a.Allocate(prev, nil)
		if prev != nil && Compare(prev, id) >= 0 {
			t.Fatalf("allocation %d not after prev: %v vs %v", i, prev, id)
		}
		key := id.String()
		if _, dup := ids[key]; dup {
			t.Fatalf("duplicate identifier %s", key)
		}
		ids[key] = struct{}{}
		prev = id
	}
}

// TestAlternatingDenseInsertDepth is the acceptance boundary: 1000 dense
// inserts alternating between the very beginning and the very end. Depth
// (identifier bit-width) must grow smoothly, not explode.
func TestAlternatingDenseInsertDepth(t *testing.T) {
	const n = 1000
	doc := NewDocument(1, rand.New(rand.NewSource(42)))
	expected := ""
	for i := 0; i < n; i++ {
		ch := rune('a' + i%26)
		if i%2 == 0 {
			doc.Insert(0, ch)
			expected = string(ch) + expected
		} else {
			doc.Insert(doc.Len(), ch)
			expected += string(ch)
		}
	}
	if got := doc.String(); got != expected {
		t.Fatalf("sequence mismatch after %d alternating inserts", n)
	}
	ids := doc.Identifiers()
	for i := 1; i < len(ids); i++ {
		if Compare(ids[i-1], ids[i]) >= 0 {
			t.Fatalf("identifiers not strictly ordered at %d", i)
		}
	}
	maxDepth := doc.MaxDepth()
	t.Logf("alternating %d inserts: maxDepth=%d histogram=%v", n, maxDepth, doc.DepthHistogram())
	if maxDepth > 16 {
		t.Fatalf("identifier depth exploded: maxDepth=%d, want <= 16", maxDepth)
	}
}

// TestSequentialAppendDepth simulates typing a long text left to right:
// depth must stay nearly constant.
func TestSequentialAppendDepth(t *testing.T) {
	doc := NewDocument(1, rand.New(rand.NewSource(1)))
	for i := 0; i < 2000; i++ {
		doc.Insert(doc.Len(), rune('a'+i%26))
	}
	maxDepth := doc.MaxDepth()
	t.Logf("2000 appends: maxDepth=%d histogram=%v", maxDepth, doc.DepthHistogram())
	if maxDepth > 8 {
		t.Fatalf("append depth grew too fast: maxDepth=%d, want <= 8", maxDepth)
	}
}

// TestSequentialPrependDepth simulates typing right to left at the head.
func TestSequentialPrependDepth(t *testing.T) {
	doc := NewDocument(1, rand.New(rand.NewSource(2)))
	for i := 0; i < 2000; i++ {
		doc.Insert(0, rune('a'+i%26))
	}
	maxDepth := doc.MaxDepth()
	t.Logf("2000 prepends: maxDepth=%d histogram=%v", maxDepth, doc.DepthHistogram())
	if maxDepth > 16 {
		t.Fatalf("prepend depth grew too fast: maxDepth=%d, want <= 16", maxDepth)
	}
}

// buildOps creates a causal op history across three sites:
//   - A types "hello" (left to right), then deletes 'e' (index 1);
//   - B merges A's inserts, then concurrently-with-C prepends "XY";
//   - C merges A's inserts, then concurrently-with-B appends "12",
//     then inserts 'm' between 'l' and 'o' (causally after A's text).
func buildOps() []Op {
	rng := rand.New(rand.NewSource(99))
	a := NewDocument(1, rand.New(rand.NewSource(11)))
	var ops []Op
	for _, ch := range "hello" {
		ops = append(ops, a.Insert(a.Len(), ch))
	}
	insertsOnly := ops[:5]

	b := NewDocument(2, rand.New(rand.NewSource(22)))
	for _, op := range insertsOnly {
		b.Merge(op)
	}
	for _, ch := range "XY" {
		ops = append(ops, b.Insert(0, ch))
	}

	c := NewDocument(3, rand.New(rand.NewSource(33)))
	for _, op := range insertsOnly {
		c.Merge(op)
	}
	for _, ch := range "12" {
		ops = append(ops, c.Insert(c.Len(), ch))
	}
	ops = append(ops, c.Insert(3, 'm')) // between second 'l' and 'o'

	ops = append(ops, a.Delete(1)) // delete 'e', causally last on A
	_ = rng
	return ops
}

// TestConvergenceRandomPermutation merges the same op set into fresh
// replicas under many random permutations; every replica must converge
// to the identical identifier sequence and text (strict causal order).
func TestConvergenceRandomPermutation(t *testing.T) {
	ops := buildOps()
	const want = "YXhlmlo12" // B prepends X then Y; 'e' deleted; 'm' between 'l' and 'o'

	var reference []Identifier
	rng := rand.New(rand.NewSource(2024))
	for trial := 0; trial < 100; trial++ {
		perm := rng.Perm(len(ops))
		replicas := make([]*Document, 3)
		for i := range replicas {
			replicas[i] = NewDocument(uint64(i+1), nil)
		}
		for _, idx := range perm {
			replicas[idx%len(replicas)].Merge(ops[idx])
		}
		// Cross-sync so every replica finally sees every op.
		for _, r := range replicas {
			for _, op := range ops {
				r.Merge(op)
			}
		}
		for i, r := range replicas {
			if got := r.String(); got != want {
				t.Fatalf("trial %d replica %d: got %q, want %q", trial, i, got, want)
			}
			ids := r.Identifiers()
			if reference == nil {
				reference = ids
				continue
			}
			if len(ids) != len(reference) {
				t.Fatalf("trial %d replica %d: identifier count diverged", trial, i)
			}
			for j := range ids {
				if Compare(ids[j], reference[j]) != 0 {
					t.Fatalf("trial %d replica %d: identifier %d diverged", trial, i, j)
				}
			}
		}
	}
}

// TestCausalOrderPreserved: B inserts 'C' between A's 'A' and 'B' only
// after observing them; any delivery order must yield "ACB".
func TestCausalOrderPreserved(t *testing.T) {
	a := NewDocument(1, rand.New(rand.NewSource(5)))
	opA := a.Insert(0, 'A')
	opB := a.Insert(1, 'B')

	b := NewDocument(2, rand.New(rand.NewSource(6)))
	b.Merge(opA)
	b.Merge(opB)
	opC := b.Insert(1, 'C') // causally between A and B

	ops := []Op{opA, opB, opC}
	rng := rand.New(rand.NewSource(8))
	for trial := 0; trial < 50; trial++ {
		doc := NewDocument(9, nil)
		for _, idx := range rng.Perm(len(ops)) {
			doc.Merge(ops[idx])
		}
		if got := doc.String(); got != "ACB" {
			t.Fatalf("trial %d: causal order violated, got %q", trial, got)
		}
	}
}

// TestDeleteCommutes checks deletes and inserts commute under shuffling.
func TestDeleteCommutes(t *testing.T) {
	a := NewDocument(1, rand.New(rand.NewSource(3)))
	var ops []Op
	for _, ch := range "abcd" {
		ops = append(ops, a.Insert(a.Len(), ch))
	}
	ops = append(ops, a.Delete(0)) // delete 'a'
	ops = append(ops, a.Delete(1)) // delete 'c' (visible index shifts)

	rng := rand.New(rand.NewSource(4))
	for trial := 0; trial < 50; trial++ {
		doc := NewDocument(2, nil)
		for _, idx := range rng.Perm(len(ops)) {
			doc.Merge(ops[idx])
		}
		if got := doc.String(); got != "bd" {
			t.Fatalf("trial %d: got %q, want %q", trial, got, "bd")
		}
	}
}
