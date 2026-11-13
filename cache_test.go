package netsec

import (
	"math/rand"
	"testing"
)

// naiveCache is deliberately implemented with ordered slices rather than the
// timestamped two-way array used by Cache.
type naiveCache struct {
	sets [NumSets][]int
}

type naiveAccess struct {
	set        int
	tag        int
	hit        bool
	evicted    bool
	evictedTag int
	beforeLRU  []int
	afterLRU   []int
}

func (n *naiveCache) access(addr int) naiveAccess {
	lineIndex := addr / LineSize
	setIndex := lineIndex % NumSets
	tag := lineIndex / NumSets
	entries := &n.sets[setIndex]

	result := naiveAccess{
		set:        setIndex,
		tag:        tag,
		evictedTag: -1,
		beforeLRU:  append([]int(nil), (*entries)...),
	}

	for i, existing := range *entries {
		if existing == tag {
			copy((*entries)[i:], (*entries)[i+1:])
			*entries = (*entries)[:len(*entries)-1]
			*entries = append(*entries, tag)
			result.hit = true
			result.afterLRU = append([]int(nil), (*entries)...)
			return result
		}
	}

	if len(*entries) == Ways {
		result.evicted = true
		result.evictedTag = (*entries)[0]
		*entries = (*entries)[1:]
	}
	*entries = append(*entries, tag)
	result.afterLRU = append([]int(nil), (*entries)...)
	return result
}

func (n *naiveCache) snapshotLRU() [NumSets][]int {
	var out [NumSets][]int
	for i := range n.sets {
		out[i] = append([]int(nil), n.sets[i]...)
	}
	return out
}

func equalInts(a, b []int) bool {
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

func compareCacheStep(t *testing.T, optimized *Cache, naive *naiveCache, addr int) AccessResult {
	t.Helper()
	before := naive.snapshotLRU()
	want := naive.access(addr)
	got, err := optimized.Access(addr)
	if err != nil {
		t.Fatalf("Access(%d): %v", addr, err)
	}
	if got.Set != want.set || got.Tag != want.tag || got.Hit != want.hit {
		t.Fatalf("Access(%d) = set %d tag %d hit %v, want set %d tag %d hit %v",
			addr, got.Set, got.Tag, got.Hit, want.set, want.tag, want.hit)
	}
	if got.Evicted != want.evicted || got.EvictedTag != want.evictedTag {
		t.Fatalf("Access(%d) eviction = (%v,%d), want (%v,%d)",
			addr, got.Evicted, got.EvictedTag, want.evicted, want.evictedTag)
	}
	if !equalInts(got.Before.LRU(), before[got.Set]) {
		t.Fatalf("Access(%d) before LRU = %v, want %v", addr, got.Before.LRU(), before[got.Set])
	}
	if !equalInts(got.After.LRU(), want.afterLRU) {
		t.Fatalf("Access(%d) after LRU = %v, want %v", addr, got.After.LRU(), want.afterLRU)
	}

	optimizedSnapshot := optimized.Snapshot()
	naiveSnapshot := naive.snapshotLRU()
	for setIndex := 0; setIndex < NumSets; setIndex++ {
		if !equalInts(optimizedSnapshot[setIndex].LRU(), naiveSnapshot[setIndex]) {
			t.Fatalf("after Access(%d), set %d LRU = %v, want %v",
				addr, setIndex, optimizedSnapshot[setIndex].LRU(), naiveSnapshot[setIndex])
		}
	}
	return got
}

func TestCacheMatchesNaiveReference(t *testing.T) {
	addresses := []int{
		0, 15, 16, 255, 256, 511, 512, 0, 256, 0, 4095, 3840,
		0x123, 0x223, 0x323, 0x023, 0x123, 0x423, 0x123,
	}
	rng := rand.New(rand.NewSource(64))
	for i := 0; i < 1000; i++ {
		addresses = append(addresses, rng.Intn(AddressSpace))
	}

	optimized := NewCache()
	naive := &naiveCache{}
	for _, addr := range addresses {
		compareCacheStep(t, optimized, naive, addr)
	}
}

func TestCacheConflictAndSameSetDifferentTag(t *testing.T) {
	if SetOf(0) != 0 || SetOf(256) != 0 || SetOf(512) != 0 {
		t.Fatalf("0, 256, and 512 must conflict in set 0")
	}
	if TagOf(0) == TagOf(256) || TagOf(256) == TagOf(512) {
		t.Fatalf("0, 256, and 512 must carry different tags")
	}

	cache := NewCache()
	first, _ := cache.Access(0)
	second, _ := cache.Access(256)
	if first.Hit || second.Hit {
		t.Fatalf("first two conflicting lines should miss")
	}
	if got := second.After.LRU(); !equalInts(got, []int{0, 1}) {
		t.Fatalf("after filling set 0 LRU = %v, want [0 1]", got)
	}

	third, _ := cache.Access(512)
	if !third.Evicted || third.EvictedTag != TagOf(0) {
		t.Fatalf("third conflicting access evicted (%v,%d), want tag %d",
			third.Evicted, third.EvictedTag, TagOf(0))
	}
	if got := third.After.LRU(); !equalInts(got, []int{TagOf(256), TagOf(512)}) {
		t.Fatalf("LRU after eviction = %v", got)
	}

	offset, _ := cache.Access(512 + LineSize - 1)
	if !offset.Hit {
		t.Fatalf("byte inside the same cache line should hit")
	}
	if SetOf(0) == SetOf(16) {
		t.Fatalf("address 16 should select a different set than address 0")
	}
}

func TestRepeatedPrimeUpdatesLRU(t *testing.T) {
	cache := NewCache()
	first, _ := cache.Access(256)
	second, _ := cache.Access(0)
	if first.Hit || second.Hit {
		t.Fatalf("initial accesses must miss")
	}
	repeated, _ := cache.Access(256)
	if !repeated.Hit {
		t.Fatalf("repeated prime address must hit")
	}
	if got := repeated.After.LRU(); !equalInts(got, []int{TagOf(0), TagOf(256)}) {
		t.Fatalf("repeated hit did not refresh LRU: %v", got)
	}

	third, _ := cache.Access(512)
	if !third.Evicted || third.EvictedTag != TagOf(0) {
		t.Fatalf("repeated prime should make the other line LRU; evicted (%v,%d)",
			third.Evicted, third.EvictedTag)
	}
}

func TestProbeOrderChangesObservation(t *testing.T) {
	victim := mustProgram(t, ConstExpr(0))
	prime := []int{0}

	first, err := Simulate(7, 3, prime, []int{0, 256, 512}, victim)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Simulate(7, 3, prime, []int{256, 0, 512}, victim)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProbeObservation != "HMM" {
		t.Fatalf("first probe observation = %q, want HMM", first.ProbeObservation)
	}
	if second.ProbeObservation != "MHM" {
		t.Fatalf("second probe observation = %q, want MHM", second.ProbeObservation)
	}
	if first.ProbeObservation == second.ProbeObservation {
		t.Fatalf("changing probe order should change the observation")
	}
}

func mustProgram(t *testing.T, loads ...*Expr) *Program {
	t.Helper()
	program, err := NewProgram(loads...)
	if err != nil {
		t.Fatal(err)
	}
	return program
}
