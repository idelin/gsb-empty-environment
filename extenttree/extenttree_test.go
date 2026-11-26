package extenttree

import (
	"math/rand"
	"testing"
)

// blockState is the naive per-block ground truth.
type blockState struct {
	allocated bool
	pba       uint64
	unwritten bool
}

// pool is a shared bump allocator that records every allocation as a physical
// half-open interval, consumed in order by both the tree and the model.
type pool struct {
	next uint64
	log  []PhysRange
}

func newPool(start uint64) *pool { return &pool{next: start} }

func (p *pool) alloc(length uint64) (uint64, error) {
	start := p.next
	p.next += length
	p.log = append(p.log, PhysRange{Start: start, End: start + length})
	return start, nil
}

// drain returns and clears the recorded allocations in order.
func (p *pool) drain() []PhysRange {
	out := p.log
	p.log = nil
	return out
}

// naiveModel is the reference implementation: a flat array of logical blocks.
type naiveModel struct {
	blocks  map[uint64]blockState
	nextPBA uint64
}

func newNaiveModel() *naiveModel {
	return &naiveModel{blocks: map[uint64]blockState{}, nextPBA: 1000}
}

func (m *naiveModel) alloc(length uint64) (uint64, error) {
	pba := m.nextPBA
	m.nextPBA += length
	return pba, nil
}

func (m *naiveModel) isHole(lba, length uint64) bool {
	for x := lba; x < lba+length; x++ {
		if m.blocks[x].allocated {
			return false
		}
	}
	return true
}

func (m *naiveModel) preallocate(lba, length, pba uint64) {
	for i := uint64(0); i < length; i++ {
		m.blocks[lba+i] = blockState{allocated: true, pba: pba + i, unwritten: true}
	}
}

// writeInit mirrors the tree's flag semantics without allocating: allocated
// blocks flip to initialized; hole blocks take the explicit contiguous PBA
// the shared allocator handed to the tree for that maximal hole.
func (m *naiveModel) writeInit(lba, length uint64, holePBA func(holeStart, holeLen uint64) uint64) {
	end := lba + length
	for x := lba; x < end; {
		b := m.blocks[x]
		if b.allocated {
			b.unwritten = false
			m.blocks[x] = b
			x++
			continue
		}
		holeStart := x
		for x < end && !m.blocks[x].allocated {
			x++
		}
		holeLen := x - holeStart
		pba := holePBA(holeStart, holeLen)
		for i := uint64(0); i < holeLen; i++ {
			m.blocks[holeStart+i] = blockState{allocated: true, pba: pba + i}
		}
	}
}

// noHolePBA is used when a WriteInit range is known to be fully allocated, so
// no new physical allocation (and thus no hole PBA) is expected.
func noHolePBA(_, _ uint64) uint64 {
	panic("test: unexpected hole allocation")
}

func (m *naiveModel) punchHole(lba, length uint64) []PhysRange {
	var released []PhysRange
	for x := lba; x < lba+length; x++ {
		if b, ok := m.blocks[x]; ok && b.allocated {
			released = append(released, PhysRange{Start: b.pba, End: b.pba + 1})
		}
		delete(m.blocks, x)
	}
	return coalescePhys(released)
}

func (m *naiveModel) zeroRange(lba, length uint64) {
	for x := lba; x < lba+length; x++ {
		if b, ok := m.blocks[x]; ok && b.allocated {
			b.unwritten = true
			m.blocks[x] = b
		}
	}
}

// dumpExtents returns every extent in logical order via the leaf chain.
func dumpExtents(tr *Tree) []Extent {
	var out []Extent
	for n := tr.leftmostLeaf(); n != nil; n = n.next {
		out = append(out, n.extents...)
	}
	return out
}

// compareModel checks every block in [0, span) against the naive model.
func compareModel(t *testing.T, tr *Tree, m *naiveModel, span uint64) {
	t.Helper()
	segs, err := tr.Query(0, span)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var cursor uint64
	for _, s := range segs {
		if s.LBA != cursor {
			t.Fatalf("segment gap/overlap at %d, want cursor %d", s.LBA, cursor)
		}
		for i := uint64(0); i < s.Length; i++ {
			want, ok := m.blocks[cursor+i]
			if s.Allocated != ok {
				t.Fatalf("block %d allocated=%v, model %v", cursor+i, s.Allocated, ok)
			}
			if ok {
				gotPBA := s.PBA + i
				if gotPBA != want.pba || s.Unwritten != want.unwritten {
					t.Fatalf("block %d got (pba=%d,uw=%v), want (pba=%d,uw=%v)",
						cursor+i, gotPBA, s.Unwritten, want.pba, want.unwritten)
				}
			}
		}
		cursor += s.Length
	}
	if cursor != span {
		t.Fatalf("query covered %d blocks, want %d", cursor, span)
	}
}

// validateInvariants checks balance, occupancy, ordering and merge rules.
func validateInvariants(t *testing.T, tr *Tree) {
	t.Helper()
	root := tr.root

	var leafDepth int
	var depth func(n *node, d int)
	depth = func(n *node, d int) {
		if n.leaf {
			if leafDepth == 0 {
				leafDepth = d
			} else if d != leafDepth {
				t.Fatalf("unbalanced: leaf depths %d and %d", d, leafDepth)
			}
			return
		}
		if len(n.children) < 2 {
			t.Fatalf("internal node with %d children", len(n.children))
		}
		for _, c := range n.children {
			if c.parent != n {
				t.Fatalf("child parent pointer mismatch")
			}
			depth(c, d+1)
		}
	}
	depth(root, 1)

	var checkNode func(n *node, isRoot bool)
	checkNode = func(n *node, isRoot bool) {
		if n.leaf {
			if len(n.extents) > tr.m {
				t.Fatalf("leaf overflow: %d > %d", len(n.extents), tr.m)
			}
			if !isRoot && len(n.extents) < tr.minLeaf {
				t.Fatalf("leaf underflow: %d < %d", len(n.extents), tr.minLeaf)
			}
			for i, e := range n.extents {
				if e.Length == 0 {
					t.Fatalf("zero-length extent")
				}
				if i > 0 {
					prev := n.extents[i-1]
					if e.LBA < prev.End() {
						t.Fatalf("overlapping/out-of-order extents")
					}
					if canMerge(prev, e) {
						t.Fatalf("unmerged neighbors within leaf: %+v %+v", prev, e)
					}
				}
			}
			return
		}

		if len(n.keys) != len(n.children)-1 {
			t.Fatalf("internal keys %d != children-1 %d", len(n.keys), len(n.children)-1)
		}
		if len(n.children) > tr.m+1 {
			t.Fatalf("internal overflow: %d children", len(n.children))
		}
		if !isRoot && len(n.children) < tr.minLeaf+1 {
			t.Fatalf("internal underflow: %d < %d children", len(n.children), tr.minLeaf+1)
		}
		for i, k := range n.keys {
			want := subtreeMin(n.children[i+1])
			if k != want {
				t.Fatalf("separator key %d != subtree min %d", k, want)
			}
			if i > 0 && k <= n.keys[i-1] {
				t.Fatalf("non-ascending separator keys")
			}
		}
		for _, c := range n.children {
			checkNode(c, false)
		}
	}
	checkNode(root, true)

	// Leaf chain: order, prev links and cross-boundary merge rules.
	var chain []*node
	for n := tr.leftmostLeaf(); n != nil; n = n.next {
		chain = append(chain, n)
	}
	seen := map[*node]bool{}
	var prevLBA uint64
	havePrev := false
	for i, n := range chain {
		if seen[n] {
			t.Fatalf("leaf chain cycle")
		}
		seen[n] = true
		if i > 0 && n.prev != chain[i-1] {
			t.Fatalf("leaf prev link mismatch")
		}
		for _, e := range n.extents {
			if havePrev && e.LBA <= prevLBA {
				t.Fatalf("leaf chain not LBA-ordered")
			}
			prevLBA = e.LBA
			havePrev = true
		}
		if i > 0 && len(chain[i-1].extents) > 0 && len(n.extents) > 0 {
			last := chain[i-1].extents[len(chain[i-1].extents)-1]
			first := n.extents[0]
			if canMerge(last, first) {
				t.Fatalf("unmerged neighbors across leaf boundary")
			}
		}
	}
}

// extentsEqual compares two extent lists.
func extentsEqual(got, want []Extent) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

var _ = rand.New // keep math/rand import used across test files in build

// TestSplitIntoThree covers "write the middle of an unwritten extent": the
// unwritten extent must split precisely into unwritten/init/unwritten pieces.
func TestSplitIntoThree(t *testing.T) {
	tr, _ := New(4)
	m := newNaiveModel()

	const pba uint64 = 500
	if err := tr.Preallocate(10, 10, pba); err != nil {
		t.Fatalf("Preallocate: %v", err)
	}
	m.preallocate(10, 10, pba)
	validateInvariants(t, tr)
	compareModel(t, tr, m, 40)

	// Write the inner sub-range [13,17).
	if err := tr.WriteInit(13, 4, m.alloc); err != nil {
		t.Fatalf("WriteInit: %v", err)
	}
	m.writeInit(13, 4, noHolePBA)

	want := []Extent{
		{LBA: 10, Length: 3, PBA: 500, Unwritten: true},
		{LBA: 13, Length: 4, PBA: 503, Unwritten: false},
		{LBA: 17, Length: 3, PBA: 507, Unwritten: true},
	}
	if got := dumpExtents(tr); !extentsEqual(got, want) {
		t.Fatalf("split into three mismatch:\n got %+v\nwant %+v", got, want)
	}
	validateInvariants(t, tr)
	compareModel(t, tr, m, 40)
}

// TestComplementaryWritesMerge verifies two complementary writes collapse the
// three pieces back into a single initialized, physically contiguous extent.
func TestComplementaryWritesMerge(t *testing.T) {
	tr, _ := New(4)
	m := newNaiveModel()

	const pba uint64 = 500
	if err := tr.Preallocate(10, 10, pba); err != nil {
		t.Fatalf("Preallocate: %v", err)
	}
	m.preallocate(10, 10, pba)

	if err := tr.WriteInit(13, 4, m.alloc); err != nil {
		t.Fatalf("WriteInit middle: %v", err)
	}
	m.writeInit(13, 4, noHolePBA)
	if got := dumpExtents(tr); len(got) != 3 {
		t.Fatalf("expected 3 pieces, got %d", len(got))
	}

	// Write the two complementary side ranges.
	if err := tr.WriteInit(10, 3, m.alloc); err != nil {
		t.Fatalf("WriteInit left: %v", err)
	}
	m.writeInit(10, 3, noHolePBA)
	if err := tr.WriteInit(17, 3, m.alloc); err != nil {
		t.Fatalf("WriteInit right: %v", err)
	}
	m.writeInit(17, 3, noHolePBA)

	want := []Extent{{LBA: 10, Length: 10, PBA: 500, Unwritten: false}}
	if got := dumpExtents(tr); !extentsEqual(got, want) {
		t.Fatalf("expected single merged extent:\n got %+v\nwant %+v", got, want)
	}
	validateInvariants(t, tr)
	compareModel(t, tr, m, 40)
}

// TestPunchAcrossThree releases the middle of three distinct extents and
// checks the exact physical half-open intervals returned.
func TestPunchAcrossThree(t *testing.T) {
	tr, _ := New(4)
	m := newNaiveModel()

	// Three separate initialized extents, with holes between them so they can
	// never merge: [0,4), [10,14), [20,24).
	ranges := []struct{ lba, pba uint64 }{
		{0, 800}, {10, 900}, {20, 1000},
	}
	for _, r := range ranges {
		if err := tr.WriteInit(r.lba, 4, func(uint64) (uint64, error) {
			return r.pba, nil
		}); err != nil {
			t.Fatalf("WriteInit: %v", err)
		}
		m.blocks[r.lba] = blockState{allocated: true, pba: r.pba}
		for i := uint64(1); i < 4; i++ {
			m.blocks[r.lba+i] = blockState{allocated: true, pba: r.pba + i}
		}
	}
	validateInvariants(t, tr)

	// Punch the inner two blocks of each extent: [1,3), [11,13), [21,23).
	type hole struct{ lba, length uint64 }
	holes := []hole{{1, 2}, {11, 2}, {21, 2}}
	var gotReleased []PhysRange
	for _, h := range holes {
		rel, err := tr.PunchHole(h.lba, h.length)
		if err != nil {
			t.Fatalf("PunchHole: %v", err)
		}
		gotReleased = append(gotReleased, rel...)

		wantRel := m.punchHole(h.lba, h.length)
		// Each individual call must match the model exactly.
		if !physEqual(rel, wantRel) {
			t.Fatalf("PunchHole(%d,%d) released %+v, want %+v", h.lba, h.length, rel, wantRel)
		}
	}

	// The accumulated released list covers the precise middle physical blocks.
	wantReleased := []PhysRange{
		{Start: 801, End: 803},
		{Start: 901, End: 903},
		{Start: 1001, End: 1003},
	}
	if !physEqual(coalescePhys(gotReleased), wantReleased) {
		t.Fatalf("released %+v, want %+v", coalescePhys(gotReleased), wantReleased)
	}

	// Each original extent is now two boundary blocks with a hole between them.
	wantExtents := []Extent{
		{LBA: 0, Length: 1, PBA: 800},
		{LBA: 3, Length: 1, PBA: 803},
		{LBA: 10, Length: 1, PBA: 900},
		{LBA: 13, Length: 1, PBA: 903},
		{LBA: 20, Length: 1, PBA: 1000},
		{LBA: 23, Length: 1, PBA: 1003},
	}
	if got := dumpExtents(tr); !extentsEqual(got, wantExtents) {
		t.Fatalf("extents after punch:\n got %+v\nwant %+v", got, wantExtents)
	}
	validateInvariants(t, tr)
	compareModel(t, tr, m, 30)
}

// TestDeleteMergesAndLowersHeight forces leaf merges and height reduction by
// deleting large numbers of entries from a multi-level tree.
func TestDeleteMergesAndLowersHeight(t *testing.T) {
	const m = 3
	tr, _ := New(m)
	model := newNaiveModel()

	// Build many isolated extents (holes between) so none merge on insert.
	const n = 60
	for i := 0; i < n; i++ {
		lba := uint64(i * 3)
		pba := uint64(2000 + i*2)
		if err := tr.WriteInit(lba, 1, func(uint64) (uint64, error) {
			return pba, nil
		}); err != nil {
			t.Fatalf("seed WriteInit: %v", err)
		}
		model.blocks[lba] = blockState{allocated: true, pba: pba}
	}
	validateInvariants(t, tr)
	before := tr.Height()
	if before < 3 {
		t.Fatalf("expected multi-level tree, height=%d", before)
	}

	// Delete every extent, in an order that repeatedly leaves underflowing
	// leaves and forces borrows/merges up the tree.
	for i := 0; i < n; i++ {
		lba := uint64(i * 3)
		rel, err := tr.PunchHole(lba, 1)
		if err != nil {
			t.Fatalf("PunchHole %d: %v", lba, err)
		}
		wantRel := model.punchHole(lba, 1)
		if !physEqual(rel, wantRel) {
			t.Fatalf("delete %d released %+v want %+v", lba, rel, wantRel)
		}
		validateInvariants(t, tr)
	}

	if got := tr.Height(); got != 1 {
		t.Fatalf("height after full delete = %d, want 1", got)
	}
	if len(dumpExtents(tr)) != 0 {
		t.Fatalf("tree not empty after deleting all extents: %+v", dumpExtents(tr))
	}
	compareModel(t, tr, model, n*3)
}

// TestMergeRules confirms different flags or physical gaps never merge, while
// same-flag physically contiguous neighbors always do.
func TestMergeRules(t *testing.T) {
	tr, _ := New(8)

	// Same flag but physically discontiguous: must stay separate.
	if err := tr.Preallocate(0, 2, 100); err != nil {
		t.Fatal(err)
	}
	if err := tr.Preallocate(2, 2, 300); err != nil { // gap in PBA (102 vs 300)
		t.Fatal(err)
	}
	if got := dumpExtents(tr); len(got) != 2 {
		t.Fatalf("physical gap wrongly merged: %+v", got)
	}

	// Different flags, physically contiguous: must stay separate.
	if err := tr.WriteInit(4, 2, func(uint64) (uint64, error) {
		return 400, nil // initialized
	}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Preallocate(6, 2, 402); err != nil { // unwritten, contiguous
		t.Fatal(err)
	}
	if got := dumpExtents(tr); len(got) != 4 {
		t.Fatalf("flag boundary wrongly merged: %+v", got)
	}

	// Same flag, physically contiguous, logically adjacent: must merge.
	if err := tr.Preallocate(8, 2, 500); err != nil {
		t.Fatal(err)
	}
	if err := tr.Preallocate(10, 2, 502); err != nil {
		t.Fatal(err)
	}
	want := Extent{LBA: 8, Length: 4, PBA: 500, Unwritten: true}
	got := dumpExtents(tr)
	if len(got) != 5 || got[4] != want {
		t.Fatalf("contiguous same-flag extents not merged: %+v", got)
	}
	validateInvariants(t, tr)
}

func physEqual(a, b []PhysRange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRandomDifferential drives random operations through both the tree and
// the naive block array, comparing full state and structure after every step.
func TestRandomDifferential(t *testing.T) {
	const span = 256
	for _, m := range []int{3, 4, 5, 7} {
		for seed := int64(0); seed < 40; seed++ {
			rng := rand.New(rand.NewSource(seed*100 + int64(m)))
			tr, err := New(m)
			if err != nil {
				t.Fatalf("New(%d): %v", m, err)
			}
			model := newNaiveModel()
			pool := newPool(model.nextPBA)

			for step := 0; step < 400; step++ {
				length := uint64(1 + rng.Intn(12))
				lba := uint64(rng.Intn(span - int(length) + 1))

				switch rng.Intn(5) {
				case 0: // Preallocate, only on fully-free ranges
					if !model.isHole(lba, length) {
						continue
					}
					pba := pool.next
					pool.next += length
					if err := tr.Preallocate(lba, length, pba); err != nil {
						t.Fatalf("seed=%d m=%d Preallocate(%d,%d): %v", seed, m, lba, length, err)
					}
					model.preallocate(lba, length, pba)

				case 1: // WriteInit
					if err := tr.WriteInit(lba, length, pool.alloc); err != nil {
						t.Fatalf("seed=%d m=%d WriteInit: %v", seed, m, err)
					}
					allocs := pool.drain()
					ai := 0
					model.writeInit(lba, length, func(_, holeLen uint64) uint64 {
						r := allocs[ai]
						ai++
						if r.End-r.Start != holeLen {
							t.Fatalf("hole length %d != allocation %+v", holeLen, r)
						}
						return r.Start
					})

				case 2: // PunchHole
					got, err := tr.PunchHole(lba, length)
					if err != nil {
						t.Fatalf("seed=%d m=%d PunchHole: %v", seed, m, err)
					}
					want := model.punchHole(lba, length)
					if !physEqual(got, want) {
						t.Fatalf("seed=%d m=%d step=%d PunchHole(%d,%d) released %+v want %+v",
							seed, m, step, lba, length, got, want)
					}

				case 3: // ZeroRange
					if err := tr.ZeroRange(lba, length); err != nil {
						t.Fatalf("seed=%d m=%d ZeroRange: %v", seed, m, err)
					}
					model.zeroRange(lba, length)

				case 4: // Query-only step; still validated below
				}

				validateInvariants(t, tr)
				compareModel(t, tr, model, span)
			}
		}
	}
}

// TestZeroRangeKeepsPhysical confirms zeroing retains PBAs but flips the flag.
func TestZeroRangeKeepsPhysical(t *testing.T) {
	tr, _ := New(4)
	m := newNaiveModel()

	if err := tr.WriteInit(0, 8, func(uint64) (uint64, error) {
		return 700, nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < 8; i++ {
		m.blocks[i] = blockState{allocated: true, pba: 700 + i}
	}

	if err := tr.ZeroRange(2, 4); err != nil {
		t.Fatal(err)
	}
	m.zeroRange(2, 4)

	want := []Extent{
		{LBA: 0, Length: 2, PBA: 700},
		{LBA: 2, Length: 4, PBA: 702, Unwritten: true},
		{LBA: 6, Length: 2, PBA: 706},
	}
	if got := dumpExtents(tr); !extentsEqual(got, want) {
		t.Fatalf("ZeroRange split mismatch:\n got %+v\nwant %+v", got, want)
	}
	validateInvariants(t, tr)
	compareModel(t, tr, m, 16)

	// Writing the middle back initializes it; physically contiguous same-flag
	// neighbors then merge the initialized pieces back together.
	if err := tr.WriteInit(2, 4, m.alloc); err != nil {
		t.Fatal(err)
	}
	m.writeInit(2, 4, noHolePBA)
	want = []Extent{{LBA: 0, Length: 8, PBA: 700}}
	if got := dumpExtents(tr); !extentsEqual(got, want) {
		t.Fatalf("re-merge after write mismatch:\n got %+v\nwant %+v", got, want)
	}
	validateInvariants(t, tr)
	compareModel(t, tr, m, 16)
}

// TestPreallocateRejectsOverlap guards the precondition of Preallocate.
func TestPreallocateRejectsOverlap(t *testing.T) {
	tr, _ := New(4)
	if err := tr.Preallocate(0, 8, 100); err != nil {
		t.Fatal(err)
	}
	if err := tr.Preallocate(4, 8, 200); err == nil {
		t.Fatal("expected error on overlapping Preallocate")
	}
}

// TestRebuildInvariantsExhaustive rebuilds trees for every fan-out and a wide
// range of sizes, asserting the occupancy and balance invariants hold by
// construction even without any later mutation.
func TestRebuildInvariantsExhaustive(t *testing.T) {
	for _, m := range []int{3, 4, 5, 6, 8, 10} {
		tr, err := New(m)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n <= 250; n++ {
			var ex []Extent
			for i := 0; i < n; i++ {
				ex = append(ex, Extent{
					LBA:    uint64(i * 3),
					Length: 1,
					PBA:    uint64(5000 + i),
				})
			}
			tr.rebuild(ex)
			validateInvariants(t, tr)
			if got := dumpExtents(tr); len(got) != n {
				t.Fatalf("m=%d n=%d: rebuilt extent count %d", m, n, len(got))
			}
		}
	}
}

// TestQueryHoles verifies hole segments appear between sparse extents and at
// the boundaries of a query range.
func TestQueryHoles(t *testing.T) {
	tr, _ := New(4)
	if err := tr.Preallocate(4, 2, 100); err != nil {
		t.Fatal(err)
	}
	segs, err := tr.Query(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Segment{
		{LBA: 0, Length: 4},
		{LBA: 4, Length: 2, PBA: 100, Unwritten: true, Allocated: true},
		{LBA: 6, Length: 4},
	}
	if len(segs) != len(want) {
		t.Fatalf("segments %+v want %+v", segs, want)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Fatalf("segment %d = %+v want %+v", i, segs[i], want[i])
		}
	}
}

// TestConcurrentAccess exercises the mutex under mixed parallel operations.
func TestConcurrentAccess(t *testing.T) {
	tr, _ := New(4)
	alloc := func(uint64) (uint64, error) { return 1, nil }
	done := make(chan struct{})
	for w := 0; w < 4; w++ {
		go func(base int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				lba := uint64((base*7 + i) % 200)
				_ = tr.WriteInit(lba, 1, alloc)
				_, _ = tr.Query(0, 200)
				_ = tr.ZeroRange(lba, 1)
			}
		}(w)
	}
	for w := 0; w < 4; w++ {
		<-done
	}
	validateInvariants(t, tr)
}

// TestBadFanout rejects M values below 3.
func TestBadFanout(t *testing.T) {
	if _, err := New(2); err == nil {
		t.Fatal("expected error for M=2")
	}
	if _, err := New(0); err == nil {
		t.Fatal("expected error for M=0")
	}
}
