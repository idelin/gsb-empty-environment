package lfs

import (
	"testing"
	"time"
)

// TestCleanRepointsSnapshotPins verifies that when a snapshot-pinned block is
// migrated by the cleaner, every snapshot pointer follows it atomically and
// still resolves to the original (pinned) version.
func TestCleanRepointsSnapshotPins(t *testing.T) {
	clock := &FakeClock{NowNs: 1000}
	const B = 2
	fs := NewFS(Config{SegmentCount: 8, BlocksPerSeg: B, Clock: clock.Get, EmergencyWatermark: 1})

	// block0 v1 lands first; the snapshot created below will pin only it.
	pinnedAddr, err := fs.Write(NodeID(1), 0, []byte("v1"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.CreateSnapshot("snap"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	// Fill the remainder of the pinned segment with a block that is NOT in the
	// snapshot, then overwrite that block so its local copy becomes garbage.
	// The segment is now partially live: only snapshot-pinned block0 survives.
	otherAddr, err := fs.Write(NodeID(1), 1, []byte("keep"), false)
	if err != nil {
		t.Fatal(err)
	}
	if otherAddr.Seg != pinnedAddr.Seg {
		t.Fatalf("test setup expects block1 in pinned segment %d, got %d",
			pinnedAddr.Seg, otherAddr.Seg)
	}
	if _, err := fs.Write(NodeID(1), 1, []byte("keep2"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Write(NodeID(1), 0, []byte("v2"), false); err != nil {
		t.Fatal(err)
	}
	// Add overwrite churn on other inodes to fill and age further segments.
	for round := 0; round < 4; round++ {
		clock.Advance(time.Second)
		for i := BlockIndex(0); i < BlockIndex(3*B); i++ {
			if _, err := fs.Write(NodeID(2), i, []byte{byte(round)}, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	clock.Advance(10 * time.Second)

	migrated := false
	var sawPinnedSegment bool
	for attempt := 0; attempt < 12; attempt++ {
		stats, err := fs.Clean()
		if err == ErrNoCandidate {
			break
		}
		if err != nil {
			t.Fatalf("clean: %v", err)
		}
		if stats.Migrated > 0 {
			migrated = true
		}
		if stats.Victim == pinnedAddr.Seg {
			sawPinnedSegment = true
		}
	}
	if !migrated {
		t.Fatal("expected at least one live-block migration")
	}
	if !sawPinnedSegment {
		t.Fatal("the partially-live pinned segment should have been cleaned")
	}

	pinned, ok, err := fs.SnapshotEntry("snap", NodeID(1), 0)
	if err != nil || !ok {
		t.Fatalf("snapshot entry lost: ok=%v err=%v", ok, err)
	}
	node, err := fs.dev.ReadNode(pinned.Addr)
	if err != nil {
		t.Fatalf("pinned block not readable after migration: %v", err)
	}
	if string(node.Data) != "v1" {
		t.Fatalf("snapshot must preserve v1, got %q", node.Data)
	}
	if pinned.Addr == pinnedAddr {
		t.Log("pinned block happened not to migrate; pointer still valid")
	}
	if !fs.IsLive(pinned.Addr) {
		t.Fatal("repointed snapshot block must remain live")
	}

	// Current NAT still independently points at v2.
	current, ok := fs.NATEntryOf(NodeID(1), 0)
	if !ok {
		t.Fatal("current NAT entry missing")
	}
	curNode, err := fs.dev.ReadNode(current.Addr)
	if err != nil || string(curNode.Data) != "v2" {
		t.Fatalf("current version should be v2: %v %q", err, curNode.Data)
	}
	if !fs.Audit().Consistent {
		t.Fatal("audit inconsistent after snapshot repointing clean")
	}
}
