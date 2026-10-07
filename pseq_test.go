package pseq

import (
	"math/rand"
	"testing"
)

// modelDigest recomputes the polynomial digest straight from an array,
// normalizing negatives first. It is the independent oracle for every
// digest the library reports.
func modelDigest(vals []int64, mod uint64) uint64 {
	m := int64(mod)
	var h uint64
	base := hashBase % mod
	for _, v := range vals {
		r := v % m
		if r < 0 {
			r += m
		}
		h = (h*base + uint64(r)) % mod
	}
	return h
}

func checkAgainstModel(t *testing.T, s *Seq, vals []int64, mod uint64) {
	t.Helper()
	if s.Len() != len(vals) {
		t.Fatalf("Len = %d, model %d", s.Len(), len(vals))
	}
	if got, want := s.Digest(), modelDigest(vals, mod); got != want {
		t.Fatalf("Digest = %d, model %d (len %d)", got, want, len(vals))
	}
	got := s.Values()
	for i, v := range vals {
		if got[i] != v {
			t.Fatalf("Values[%d] = %d, model %d", i, got[i], v)
		}
		if s.At(i) != v {
			t.Fatalf("At(%d) = %d, model %d", i, s.At(i), v)
		}
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// Different operation histories reaching the same sequence must yield the
// same digest and length.
func TestSameSequenceDifferentOpsSameDigest(t *testing.T) {
	f := NewFactory(DefaultMod)
	want := []int64{5, -3, 0, 7, 7, -1}

	a := f.Empty()
	for _, v := range want {
		a = a.PushBack(v)
	}

	b := f.Empty()
	for i := len(want) - 1; i >= 0; i-- {
		b = b.PushFront(want[i])
	}

	c := f.Of(want[:2]...).Concat(f.Of(want[:4]...).Slice(2, 4)).Concat(f.Of(want[4:]...))

	d := f.Of(append(append([]int64{}, want...), 42, 43)...)
	d, _, _ = d.PopBack()
	d, _, _ = d.PopBack()
	d = d.PushFront(-99)
	d, _, _ = d.PopFront()

	for name, s := range map[string]*Seq{"pushBack": a, "pushFront": b, "concat": c, "popTrim": d} {
		if s.Digest() != a.Digest() || s.Len() != a.Len() {
			t.Fatalf("%s: digest/len %d/%d, want %d/%d", name, s.Digest(), s.Len(), a.Digest(), a.Len())
		}
		if !s.Equal(a) {
			t.Fatalf("%s: content mismatch", name)
		}
		checkAgainstModel(t, s, want, DefaultMod)
	}
}

// Zero elements and deletions at both ends, down to empty and back.
func TestZeroElementsAndEndDeletes(t *testing.T) {
	f := NewFactory(DefaultMod)
	s := f.Empty()
	var model []int64

	for i := 0; i < 50; i++ {
		s = s.PushBack(0)
		model = append(model, 0)
	}
	s = s.PushFront(0)
	model = append([]int64{0}, model...)
	checkAgainstModel(t, s, model, DefaultMod)

	// Drain alternately from both ends.
	for len(model) > 0 {
		var v int64
		var ok bool
		if len(model)%2 == 0 {
			s, v, ok = s.PopFront()
			if !ok || v != model[0] {
				t.Fatalf("PopFront = %d,%v; model %d", v, ok, model[0])
			}
			model = model[1:]
		} else {
			s, v, ok = s.PopBack()
			if !ok || v != model[len(model)-1] {
				t.Fatalf("PopBack = %d,%v; model %d", v, ok, model[len(model)-1])
			}
			model = model[:len(model)-1]
		}
		checkAgainstModel(t, s, model, DefaultMod)
	}
	if _, _, ok := s.PopBack(); ok {
		t.Fatal("PopBack on empty reported ok")
	}
	if _, _, ok := s.PopFront(); ok {
		t.Fatal("PopFront on empty reported ok")
	}
	// Reuse after draining.
	s = s.PushBack(-7).PushFront(0)
	checkAgainstModel(t, s, []int64{0, -7}, DefaultMod)
}

// Negative elements are normalized before hashing; digest equality with
// different raw content must never be read as content equality.
func TestNegativeNormalizationAndCollision(t *testing.T) {
	f := NewFactory(DefaultMod)
	neg := f.Of(-1)
	pos := f.Of(int64(DefaultMod) - 1)
	if neg.Digest() != pos.Digest() || neg.Len() != pos.Len() {
		t.Fatal("normalized digest of -1 should equal digest of mod-1")
	}
	if neg.Equal(pos) {
		t.Fatal("Equal must compare raw content, not digests")
	}

	// Force abundant collisions with a tiny modulus.
	g := NewFactory(2)
	x := g.Of(1)
	y := g.Of(3)
	if x.Digest() != y.Digest() || x.Len() != y.Len() {
		t.Fatal("mod 2: [1] and [3] must collide in digest and length")
	}
	if x.Equal(y) {
		t.Fatal("mod 2: [1] and [3] are different content and must not be Equal")
	}
	// Concatenation still tracks real content under collisions.
	xy := x.Concat(y)
	yx := y.Concat(x)
	checkAgainstModel(t, xy, []int64{1, 3}, 2)
	checkAgainstModel(t, yx, []int64{3, 1}, 2)
	if xy.Equal(yx) {
		t.Fatal("mod 2: [1,3] and [3,1] must not be Equal")
	}
}

// Concatenating versions that share underlying nodes must not create any
// mutable aliasing: later operations on the result leave the inputs and
// every other version untouched.
func TestConcatSharedNodesNoMutableAlias(t *testing.T) {
	f := NewFactory(DefaultMod)
	base := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	v1 := f.Of(base...)
	v2 := v1.PushBack(9) // shares v1's spine and most nodes
	if v1.Len() != 8 {
		t.Fatal("v1 mutated by PushBack")
	}

	c := v1.Concat(v2)
	cModel := append(append([]int64{}, base...), append([]int64{}, base...)...)
	cModel = append(cModel, 9)
	checkAgainstModel(t, c, cModel, DefaultMod)

	// Structural sharing actually happened: c's left spine reuses v1 nodes.
	if !sharesAnyNode(v1, c) || !sharesAnyNode(v2, c) {
		t.Fatal("expected concat result to share immutable nodes with inputs")
	}

	// Derive new versions from c; v1, v2 and c must be bit-for-bit stable.
	d1, d2 := c.Digest(), c.Len()
	c2 := c.PushBack(100)
	c3, _, _ := c.PopFront()
	c4 := c.Slice(2, 9).Concat(v1)
	_ = c2
	_ = c3
	_ = c4
	checkAgainstModel(t, v1, base, DefaultMod)
	checkAgainstModel(t, v2, append(append([]int64{}, base...), 9), DefaultMod)
	if c.Digest() != d1 || c.Len() != d2 {
		t.Fatal("concat result changed after derived operations")
	}
	checkAgainstModel(t, c, cModel, DefaultMod)
}

func sharesAnyNode(a, b *Seq) bool {
	seen := make(map[*node]struct{})
	var walk func(n *node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		walk(n.left)
		walk(n.right)
	}
	walk(a.root)
	var found bool
	var probe func(n *node)
	probe = func(n *node) {
		if n == nil || found {
			return
		}
		if _, ok := seen[n]; ok {
			found = true
			return
		}
		probe(n.left)
		probe(n.right)
	}
	probe(b.root)
	return found
}

// Releasing a snapshot must make exactly its uniquely-held nodes
// unreachable; shared nodes stay live.
func TestReleaseReclaimsUnreachableNodes(t *testing.T) {
	f := NewFactory(DefaultMod)
	st := f.NewStore()

	v1 := f.Of(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	st.Commit("v1", v1)
	soloV1 := st.LiveNodes()
	if soloV1 != countNodes(v1.root) {
		t.Fatalf("LiveNodes = %d, want %d", soloV1, countNodes(v1.root))
	}

	v2 := v1.PushBack(11).PushBack(12)
	st.Commit("v2", v2)
	both := st.LiveNodes()
	v2Only := countNodes(v2.root)
	if both <= v2Only || both <= soloV1 {
		t.Fatalf("LiveNodes with two versions = %d (v1 %d, v2 %d)", both, soloV1, v2Only)
	}

	before := st.reachable()
	if !st.Release("v1") {
		t.Fatal("Release(v1) reported missing")
	}
	after := st.reachable()
	if len(after) != v2Only {
		t.Fatalf("after release LiveNodes = %d, want exactly v2's %d", len(after), v2Only)
	}
	for n := range after {
		if !reachableFrom(v2.root, n) {
			t.Fatal("node reachable after release is not part of v2")
		}
	}
	reclaimed := 0
	for n := range before {
		if _, ok := after[n]; !ok {
			reclaimed++
		}
	}
	if reclaimed != both-v2Only {
		t.Fatalf("reclaimed %d nodes, want %d", reclaimed, both-v2Only)
	}
	if st.Release("v1") {
		t.Fatal("double Release reported success")
	}
	// Releasing the last version empties the reachable set.
	st.Release("v2")
	if st.LiveNodes() != 0 {
		t.Fatalf("LiveNodes = %d after releasing all, want 0", st.LiveNodes())
	}
}

func countNodes(n *node) int {
	if n == nil {
		return 0
	}
	return 1 + countNodes(n.left) + countNodes(n.right)
}

func reachableFrom(root, target *node) bool {
	if root == nil {
		return false
	}
	if root == target {
		return true
	}
	return reachableFrom(root.left, target) || reachableFrom(root.right, target)
}

// Forked versions and repeated self-concatenation, checked against the
// array model.
func TestForkAndRepeatedConcat(t *testing.T) {
	f := NewFactory(DefaultMod)
	base := []int64{3, -1, 4}
	v := f.Of(base...)

	v1 := v.PushBack(10)
	v2 := v.PushFront(-10)
	checkAgainstModel(t, v1, []int64{3, -1, 4, 10}, DefaultMod)
	checkAgainstModel(t, v2, []int64{-10, 3, -1, 4}, DefaultMod)
	checkAgainstModel(t, v, base, DefaultMod) // fork source intact

	s := f.Of(1, -2)
	model := []int64{1, -2}
	for i := 0; i < 10; i++ {
		s = s.Concat(s)
		model = append(model, model...)
		checkAgainstModel(t, s, model, DefaultMod)
	}
	if s.Len() != 2048 {
		t.Fatalf("Len = %d after 10 doublings, want 2048", s.Len())
	}
}

// Interval evidence: digest and length of random slices match the array
// model recomputation.
func TestSliceEvidence(t *testing.T) {
	f := NewFactory(DefaultMod)
	rng := rand.New(rand.NewSource(7))
	var model []int64
	for i := 0; i < 300; i++ {
		model = append(model, int64(rng.Intn(2001)-1000))
	}
	s := f.Of(model...)
	for i := 0; i < 500; i++ {
		lo := rng.Intn(len(model) + 1)
		hi := lo + rng.Intn(len(model)-lo+1)
		digest, n := s.Evidence(lo, hi)
		if n != hi-lo {
			t.Fatalf("Evidence(%d,%d) length = %d", lo, hi, n)
		}
		if want := modelDigest(model[lo:hi], DefaultMod); digest != want {
			t.Fatalf("Evidence(%d,%d) digest = %d, model %d", lo, hi, digest, want)
		}
		sub := s.Slice(lo, hi)
		checkAgainstModel(t, sub, model[lo:hi], DefaultMod)
	}
}

// Randomized fuzz: mixed pushes, pops, snapshots, releases, forks,
// concats and slices, all checked against the array model.
func TestRandomOpsAgainstModel(t *testing.T) {
	const mod = 998244353
	f := NewFactory(mod)
	st := f.NewStore()
	rng := rand.New(rand.NewSource(42))

	var seqs []*Seq
	var models [][]int64
	add := func(s *Seq, m []int64) {
		seqs = append(seqs, s)
		models = append(models, m)
	}
	add(f.Empty(), nil)

	for step := 0; step < 4000; step++ {
		i := rng.Intn(len(seqs))
		s, m := seqs[i], models[i]
		switch rng.Intn(8) {
		case 0:
			v := int64(rng.Intn(41) - 20)
			if rng.Intn(10) == 0 {
				v = -v * int64(mod) // large negative, exercises normalization
			}
			add(s.PushBack(v), append(append([]int64{}, m...), v))
		case 1:
			v := int64(rng.Intn(41) - 20)
			add(s.PushFront(v), append([]int64{v}, m...))
		case 2:
			if len(m) > 0 {
				ns, v, ok := s.PopBack()
				if !ok || v != m[len(m)-1] {
					t.Fatalf("step %d: PopBack = %d,%v; model %d", step, v, ok, m[len(m)-1])
				}
				add(ns, append([]int64{}, m[:len(m)-1]...))
			}
		case 3:
			if len(m) > 0 {
				ns, v, ok := s.PopFront()
				if !ok || v != m[0] {
					t.Fatalf("step %d: PopFront = %d,%v; model %d", step, v, ok, m[0])
				}
				add(ns, append([]int64{}, m[1:]...))
			}
		case 4: // concat two live versions
			j := rng.Intn(len(seqs))
			nm := append(append([]int64{}, m...), models[j]...)
			add(s.Concat(seqs[j]), nm)
		case 5: // slice
			if len(m) > 0 {
				lo := rng.Intn(len(m) + 1)
				hi := lo + rng.Intn(len(m)-lo+1)
				add(s.Slice(lo, hi), append([]int64{}, m[lo:hi]...))
			}
		case 6: // snapshot, then sometimes release an older one
			st.Commit("snap", s)
			if rng.Intn(3) == 0 {
				st.Release("snap")
			} else {
				st.Commit("keep", s)
			}
		case 7: // fork: mutate a copy, original must stay put
			fork := s.PushBack(int64(step))
			checkAgainstModel(t, fork, append(append([]int64{}, m...), int64(step)), mod)
		}
		checkAgainstModel(t, s, m, mod)
		if len(seqs) > 64 { // keep the working set bounded
			seqs = append([]*Seq{seqs[len(seqs)-1]}, seqs[:len(seqs)/2]...)
			models = append([][]int64{models[len(models)-1]}, models[:len(models)/2]...)
		}
	}

	// Every snapshot still committed must match its model digest class:
	// recompute from its own Values(). The live-node count must equal the
	// exact number of nodes reachable from committed roots.
	wantLive := 0
	if kept, ok := st.Get("keep"); ok {
		checkAgainstModel(t, kept, kept.Values(), mod)
		wantLive = countNodes(kept.root)
	}
	if st.LiveNodes() != wantLive {
		t.Fatalf("LiveNodes = %d, want %d", st.LiveNodes(), wantLive)
	}
}
