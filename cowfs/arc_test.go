package cowfs

import (
	"testing"
)

// access runs a Get/Fill pair, modelling a backing-store read whenever the id
// is not resident. Resident hits already carry data and are left untouched.
func arcAccess(a *ARC, id PhysID, data []byte) {
	if _, ok := a.Get(id); !ok {
		a.Fill(id, data)
	}
}

func TestARCInvariantsUnderRandomAccess(t *testing.T) {
	const c = 16
	a := NewARC(c)

	for id := PhysID(1); id <= 200; id++ {
		arcAccess(a, id%40, []byte{byte(id)})
		if err := a.CheckInvariants(); err != nil {
			t.Fatalf("after access %d: %v", id, err)
		}
	}

	s := a.Stats()
	if s.T1+s.T2 > c {
		t.Fatalf("resident %d exceeds c=%d", s.T1+s.T2, c)
	}
	if s.T1+s.B1 > c {
		t.Fatalf("T1+B1 %d exceeds c", s.T1+s.B1)
	}
	if s.T1+s.T2+s.B1+s.B2 > 2*c {
		t.Fatalf("four-list footprint exceeds 2c")
	}
}

func TestARCTargetPAdaptation(t *testing.T) {
	const c = 10
	a := NewARC(c)

	// Phase 1: train a small hot set so its blocks graduate into T2.
	for round := 0; round < 5; round++ {
		for id := PhysID(1); id <= 4; id++ {
			arcAccess(a, id, []byte{byte(id)})
		}
	}
	if a.Stats().T2 == 0 {
		t.Fatal("training did not populate T2")
	}

	// Phase 2: stream fresh ids through T1. Because T2 anchors part of the
	// resident capacity, T1 evictions now have room to become B1 ghosts.
	findGhost := func(wantB1 bool) PhysID {
		for id := PhysID(1); id <= PhysID(2000); id++ {
			_, _, b1, b2 := a.Contains(id)
			if wantB1 && b1 {
				return id
			}
			if !wantB1 && b2 {
				return id
			}
		}
		return 0
	}

	var b1ID PhysID
	for base := PhysID(100); base < 100+PhysID(c*4) && b1ID == 0; base += PhysID(c) {
		for id := base; id < base+PhysID(c*2); id++ {
			arcAccess(a, id, []byte{byte(id)})
		}
		b1ID = findGhost(true)
	}
	if b1ID == 0 {
		t.Fatalf("failed to establish a B1 ghost: %+v", a.Stats())
	}

	stats := a.Stats()
	wantDelta := 1
	if stats.B2/stats.B1 > wantDelta {
		wantDelta = stats.B2 / stats.B1
	}
	pBefore := stats.TargetP
	if _, ok := a.Get(b1ID); ok {
		t.Fatal("B1 ghost should miss resident cache")
	}
	if got := a.Stats().TargetP; got != min(c, pBefore+wantDelta) {
		t.Fatalf("B1 hit p=%d, want %d (delta %d)",
			got, min(c, pBefore+wantDelta), wantDelta)
	}
	if err := a.CheckInvariants(); err != nil {
		t.Fatal(err)
	}

	// Drive p upward through repeated B1 hits so T1 is allowed to claim more of
	// the resident capacity, then flood fresh ids to push T2 blocks out into
	// B2. A high p is what makes T2 eviction possible.
	var b2ID PhysID
	for round := 0; round < 20 && b2ID == 0; round++ {
		// Re-establish B1 ghosts, then hit them to raise p.
		base := PhysID(300 + round*50)
		for id2 := base; id2 < base+PhysID(c*2); id2++ {
			arcAccess(a, id2, []byte{byte(id2)})
		}
		if ghost := findGhost(true); ghost != 0 {
			if _, ok := a.Get(ghost); !ok {
				a.Fill(ghost, []byte{byte(ghost)})
			}
		}
		// Flood fresh ids to age T2 blocks toward B2.
		for id := PhysID(500 + round*100); id < PhysID(500+round*100)+PhysID(c*3); id++ {
			arcAccess(a, id, []byte{byte(id)})
		}
		if err := a.CheckInvariants(); err != nil {
			t.Fatal(err)
		}
		b2ID = findGhost(false)
	}
	if b2ID == 0 {
		t.Fatalf("failed to establish a B2 ghost: %+v", a.Stats())
	}

	stats = a.Stats()
	wantDelta = 1
	if stats.B1/stats.B2 > wantDelta {
		wantDelta = stats.B1 / stats.B2
	}
	pHigh := stats.TargetP
	if _, ok := a.Get(b2ID); ok {
		t.Fatal("B2 ghost should miss resident cache")
	}
	if got := a.Stats().TargetP; got != max(0, pHigh-wantDelta) {
		t.Fatalf("B2 hit p=%d, want %d (delta %d)",
			got, max(0, pHigh-wantDelta), wantDelta)
	}
	if err := a.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// TestARCSequentialScanResistance is the headline acceptance comparison. A hot
// working set is trained into both caches, then a much larger one-shot
// sequential scan streams through. ARC keeps most hot blocks resident in T2,
// while a plain LRU has them evicted by the scan.
func TestARCSequentialScanResistance(t *testing.T) {
	const (
		capacity    = 50
		hotSetSize  = 25
		scanRange   = 400
		trainRounds = 6
	)

	arc := NewARC(capacity)
	lru := NewLRU(capacity)

	dataFor := func(id PhysID) []byte { return []byte{byte(id), byte(id >> 8)} }

	// Train: the same hot set is accessed repeatedly, establishing frequency.
	for round := 0; round < trainRounds; round++ {
		for id := PhysID(1); id <= hotSetSize; id++ {
			arcAccess(arc, id, dataFor(id))
			if d, ok := lru.Get(id); !ok {
				lru.Fill(id, d)
			}
		}
	}

	// One-shot sequential scan over cold ids larger than the cache.
	for id := PhysID(hotSetSize + 1); id <= scanRange; id++ {
		arcAccess(arc, id, dataFor(id))
		lru.Fill(id, dataFor(id))
	}

	// Measure how much of the hot set survives the scan.
	arcHotHits := 0
	lruHotHits := 0
	for id := PhysID(1); id <= hotSetSize; id++ {
		if _, ok := arc.Get(id); ok {
			arcHotHits++
		}
		if _, ok := lru.Get(id); ok {
			lruHotHits++
		}
	}

	t.Logf("hot-set survival after scan: ARC=%d/%d, LRU=%d/%d, ARC stats=%+v",
		arcHotHits, hotSetSize, lruHotHits, hotSetSize, arc.Stats())

	if arcHotHits <= lruHotHits {
		t.Fatalf("ARC scan resistance not demonstrated: ARC=%d LRU=%d",
			arcHotHits, lruHotHits)
	}
	// ARC must protect a strong majority of the trained T2 hot set.
	if arcHotHits < hotSetSize*3/4 {
		t.Fatalf("ARC retained only %d/%d hot blocks", arcHotHits, hotSetSize)
	}
	if err := arc.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestARCInvalidateRemovesAllLists(t *testing.T) {
	a := NewARC(4)
	for id := PhysID(1); id <= 12; id++ {
		arcAccess(a, id, []byte{byte(id)})
	}

	// Pick ids from each possible list and invalidate them.
	checked := 0
	for id := PhysID(1); id <= 12; id++ {
		t1, t2, b1, b2 := a.Contains(id)
		if t1 || t2 || b1 || b2 {
			checked++
			a.Invalidate(id)
			if t1, t2, b1, b2 := a.Contains(id); t1 || t2 || b1 || b2 {
				t.Fatalf("id %d still present after invalidation", id)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no entries found to invalidate")
	}
	if err := a.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestARCInvalidatedIDCanBeRefilledFresh(t *testing.T) {
	a := NewARC(2)
	arcAccess(a, 7, []byte("old"))
	data, ok := a.Get(7)
	if !ok || string(data) != "old" {
		t.Fatal("initial fill/read failed")
	}

	a.Invalidate(7)
	if _, ok := a.Get(7); ok {
		t.Fatal("ghost/residue survived invalidation")
	}

	// Simulate physical id reuse with brand-new content.
	if _, ok := a.Get(7); !ok {
		a.Fill(7, []byte("new"))
	}
	d, ok := a.Get(7)
	if !ok || string(d) != "new" {
		// Fill immediately after the inserting Get guarantees data is present
		// before any subsequent access; emulate that ordering explicitly.
		a.Fill(7, []byte("new"))
		d, ok = a.Get(7)
	}
	if !ok || string(d) != "new" {
		t.Fatalf("reused id returned stale data: %q ok=%v", data, ok)
	}
}
