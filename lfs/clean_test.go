package lfs

import (
	"math"
	"testing"
	"time"
)

func TestBenefitAgeScore(t *testing.T) {
	age := int64(1000)
	if got := benefitAgeScore(1.0, age); got != 0 {
		t.Fatalf("fully live segment must score 0, got %v", got)
	}
	empty := benefitAgeScore(0.0, age)
	if want := float64(age); empty != want {
		t.Fatalf("empty score = %v, want %v", empty, want)
	}
	half := benefitAgeScore(0.5, age)
	want := (0.5 * float64(age)) / 1.5
	if math.Abs(half-want) > 1e-9 {
		t.Fatalf("half-live score = %v, want %v", half, want)
	}
}

// TestBenefitAgeSelection builds a hot/cold bimodal workload and verifies the
// cleaner picks the old, low-utilization cold segment instead of a freshly
// overwritten hot one, and migrates blocks into the correct temperature target.
func TestBenefitAgeSelection(t *testing.T) {
	clock := &FakeClock{NowNs: 0}
	const B = 4
	fs := NewFS(Config{SegmentCount: 10, BlocksPerSeg: B, Clock: clock.Get, EmergencyWatermark: 1})

	// Cold stream: two cold segments. The first becomes mostly garbage once its
	// logical blocks are overwritten, but stays old (large age).
	for i := 0; i < 2*B; i++ {
		if _, err := fs.Write(NodeID(1), BlockIndex(i), []byte("cold"), false); err != nil {
			t.Fatalf("cold write %d: %v", i, err)
		}
	}
	clock.Advance(10 * time.Second)

	// Hot stream: repeatedly overwrite two logical blocks, producing hot
	// segments that are young (small age) even where utilization is low.
	for v := 0; v < 3*B; v++ {
		blk := BlockIndex(v % 2)
		if _, err := fs.Write(NodeID(2), blk, []byte("hot"), true); err != nil {
			t.Fatalf("hot overwrite %d: %v", v, err)
		}
	}
	clock.Advance(time.Second)

	// Make the first cold segment low utilization by overwriting most of its
	// logical blocks into the hot stream, without changing its old age.
	oldColdSeg := -1
	for i := 0; i < B; i++ {
		if entry, ok := fs.NATEntryOf(NodeID(1), BlockIndex(i)); ok {
			if oldColdSeg < 0 {
				oldColdSeg = entry.Addr.Seg
			}
		}
		if i < B-1 {
			if _, err := fs.Write(NodeID(1), BlockIndex(i), []byte("rewrite"), true); err != nil {
				t.Fatalf("cold overwrite %d: %v", i, err)
			}
		}
	}

	ranked := fs.RankCandidates()
	if len(ranked) == 0 {
		t.Fatal("expected cleanable candidate segments")
	}
	for i := 1; i < len(ranked); i++ {
		if ranked[i-1].Score < ranked[i].Score {
			t.Fatalf("candidates not ranked by score: %+v", ranked)
		}
		if fs.segmentOf(ranked[i].SegID).Active {
			t.Fatal("active segment must never be a cleaning candidate")
		}
	}

	best := ranked[0]
	if best.SegID != oldColdSeg {
		t.Fatalf("expected oldest low-u cold segment %d to win, got %d (%+v)",
			oldColdSeg, best.SegID, best)
	}
	if best.Utilization >= 1.0 || best.AgeNs <= 0 {
		t.Fatalf("winning candidate must be partially live and aged: %+v", best)
	}

	before := fs.Audit()
	if !before.Consistent {
		t.Fatalf("pre-clean audit inconsistent: %+v", before)
	}
	stats, err := fs.Clean()
	if err != nil {
		t.Fatalf("clean failed: %v", err)
	}
	if stats.Victim != best.SegID {
		t.Fatalf("cleaned victim %d, want ranked best %d", stats.Victim, best.SegID)
	}
	if stats.MigratedCold != 1 || stats.MigratedHot != 0 {
		t.Fatalf("one surviving cold block must migrate to a cold target: %+v", stats)
	}
	if !stats.FreedSegment {
		t.Fatal("victim segment should be returned to the free pool")
	}

	for i := 0; i < 2*B; i++ {
		if _, _, err := fs.Read(NodeID(1), BlockIndex(i)); err != nil {
			t.Fatalf("cold block %d lost after clean: %v", i, err)
		}
	}
	if _, _, err := fs.Read(NodeID(2), 0); err != nil {
		t.Fatalf("hot block lost after clean: %v", err)
	}
	after := fs.Audit()
	if !after.Consistent {
		t.Fatalf("post-clean audit inconsistent: %+v", after)
	}
	if after.OccupiedBlocks > before.OccupiedBlocks {
		t.Fatalf("cleaning must not inflate occupied blocks: before=%d after=%d",
			before.OccupiedBlocks, after.OccupiedBlocks)
	}
}

// TestEmergencyWatermark exhausts the free pool under overwrite load and
// verifies the on-demand cleaner still makes progress instead of deadlocking.
func TestEmergencyWatermark(t *testing.T) {
	clock := &FakeClock{NowNs: 0}
	const B = 2
	fs := NewFS(Config{SegmentCount: 5, BlocksPerSeg: B, Clock: clock.Get, EmergencyWatermark: 1})

	// Far more logical-block churn than the device can hold; each overwrite
	// makes the prior physical version garbage that cleaning can reclaim.
	for round := 0; round < 12; round++ {
		clock.Advance(time.Second)
		for i := 0; i < B; i++ {
			if _, err := fs.Write(NodeID(7), BlockIndex(i), []byte{byte(round)}, round%2 == 0); err != nil {
				t.Fatalf("round %d write %d under pressure failed: %v", round, i, err)
			}
		}
	}
	if !fs.Audit().Consistent {
		t.Fatal("audit inconsistent after emergency cleaning")
	}
	for i := 0; i < B; i++ {
		data, _, err := fs.Read(NodeID(7), BlockIndex(i))
		if err != nil || data[0] != 11 {
			t.Fatalf("latest version lost at block %d: %v %v", i, data, err)
		}
	}
}

// TestEmptySegmentImmediateReclaim checks u==0 segments are recycled without
// any migration.
func TestEmptySegmentImmediateReclaim(t *testing.T) {
	clock := &FakeClock{NowNs: 0}
	const B = 2
	fs := NewFS(Config{SegmentCount: 5, BlocksPerSeg: B, Clock: clock.Get})
	// Fill and seal a hot segment, then invalidate every block in it.
	for i := 0; i < B; i++ {
		if _, err := fs.Write(NodeID(1), BlockIndex(i), []byte("a"), true); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Second)
	for i := 0; i < B; i++ {
		if _, err := fs.Write(NodeID(1), BlockIndex(i), []byte("b"), false); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Second)
	stats, err := fs.Clean()
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if stats.ReclaimedEmpty == 0 {
		t.Fatalf("expected immediate zero-migration reclaim, got %+v", stats)
	}
	if stats.Migrated != 0 {
		t.Fatalf("dead segment must not migrate any block: %+v", stats)
	}
	if !fs.Audit().Consistent {
		t.Fatal("audit inconsistent after empty reclaim")
	}
}
