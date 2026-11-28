package lfs

import (
	"testing"
	"time"
)

// TestSnapshotPinAndRelease verifies snapshot-pinned historical versions stay
// alive while the snapshot exists and become garbage once it is deleted.
func TestSnapshotPinAndRelease(t *testing.T) {
	const B = 2
	clock := &FakeClock{NowNs: 1000}
	fs := NewFS(Config{SegmentCount: 6, BlocksPerSeg: B, Clock: clock.Get})

	addrV1, err := fs.Write(NodeID(1), 0, []byte("v1"), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.CreateSnapshot("snap"); err != nil {
		t.Fatal(err)
	}

	// Overwrite the logical block; the NAT moves but the snapshot pins v1.
	addrV2, err := fs.Write(NodeID(1), 0, []byte("v2"), true)
	if err != nil {
		t.Fatal(err)
	}
	if addrV1 == addrV2 {
		t.Fatal("overwrite must append to a new physical block")
	}
	clock.Advance(time.Second)
	if !fs.IsLive(addrV1) {
		t.Fatal("pinned historical block must remain live while snapshot exists")
	}
	if !fs.IsLive(addrV2) {
		t.Fatal("current NAT block must be live")
	}

	pinned, ok, err := fs.SnapshotEntry("snap", NodeID(1), 0)
	if err != nil || !ok || pinned.Addr != addrV1 {
		t.Fatalf("snapshot must pin v1 at %+v, got %+v ok=%v err=%v", addrV1, pinned, ok, err)
	}

	// Fill enough segments that the pinned segment becomes a clean candidate.
	for i := 1; i < 4*B; i++ {
		if _, err := fs.Write(NodeID(2), BlockIndex(i), []byte("fill"), false); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Second)
	ranked := fs.RankCandidates()
	var pinnedSegCandidate bool
	for _, c := range ranked {
		if c.SegID == addrV1.Seg {
			pinnedSegCandidate = true
			if c.Live == 0 {
				t.Fatal("pinned segment must report live blocks while snapshot active")
			}
		}
	}
	if !pinnedSegCandidate {
		t.Fatal("pinned segment should be rankable as a non-active segment")
	}

	// Delete the snapshot: v1 loses its only reference and turns into garbage.
	if err := fs.DeleteSnapshot("snap"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if fs.IsLive(addrV1) {
		t.Fatal("block kept alive only by the snapshot must be dead after deletion")
	}
	if !fs.IsLive(addrV2) {
		t.Fatal("current NAT block must still be live")
	}

	// Cleaning must now be able to reclaim the formerly-pinned segment.
	reclaimed := false
	for attempt := 0; attempt < 8; attempt++ {
		stats, err := fs.Clean()
		if err == ErrNoCandidate {
			break
		}
		if err != nil {
			t.Fatalf("clean: %v", err)
		}
		if stats.Victim == addrV1.Seg || stats.ReclaimedEmpty > 0 {
			reclaimed = true
		}
	}
	if !reclaimed {
		t.Fatal("formerly pinned segment was not reclaimed after snapshot release")
	}

	// Current data survives and block conservation holds.
	data, _, err := fs.Read(NodeID(1), 0)
	if err != nil || string(data) != "v2" {
		t.Fatalf("current version lost: %q %v", data, err)
	}
	if !fs.Audit().Consistent {
		t.Fatal("audit inconsistent after snapshot release cleaning")
	}
}

// TestSnapshotLivenessOnly verifies the precise liveness rule for a block with
// no NAT mapping and no snapshot: it is dead, not live.
func TestSnapshotLivenessOnly(t *testing.T) {
	const B = 2
	fs := NewFS(Config{SegmentCount: 3, BlocksPerSeg: B, Clock: (&FakeClock{}).Get})
	addr, err := fs.Write(NodeID(1), 0, []byte("a"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.CreateSnapshot("s"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Write(NodeID(1), 0, []byte("b"), false); err != nil {
		t.Fatal(err)
	}
	if !fs.IsLive(addr) {
		t.Fatal("snapshot-only block must be live")
	}
	if err := fs.DeleteSnapshot("s"); err != nil {
		t.Fatal(err)
	}
	if fs.IsLive(addr) {
		t.Fatal("snapshot-only block must become garbage on delete")
	}
}
