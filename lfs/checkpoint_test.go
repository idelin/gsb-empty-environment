package lfs

import (
	"errors"
	"testing"
)

func newTestFS(t *testing.T, segs, B int) (*FS, *FakeClock) {
	t.Helper()
	clock := &FakeClock{NowNs: 1_000_000}
	fs := NewFS(Config{SegmentCount: segs, BlocksPerSeg: B, Clock: clock.Get, EmergencyWatermark: 1})
	return fs, clock
}

// TestCheckpointTornSlotFallback writes alternating checkpoints, tears the
// newest slot and verifies recovery falls back to the older valid slot.
func TestCheckpointTornSlotFallback(t *testing.T) {
	fs, _ := newTestFS(t, 6, 4)
	for i := 0; i < 4; i++ {
		if _, err := fs.Write(NodeID(1), BlockIndex(i), []byte{byte(i)}, true); err != nil {
			t.Fatal(err)
		}
	}
	v1, slot1, err := fs.SaveCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := fs.Write(NodeID(2), BlockIndex(i), []byte("x"), false); err != nil {
			t.Fatal(err)
		}
	}
	v2, slot2, err := fs.SaveCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if slot1 == slot2 || v2 != v1+1 {
		t.Fatalf("checkpoints must alternate slots with increasing versions: s1=%d s2=%d v1=%d v2=%d",
			slot1, slot2, v1, v2)
	}

	// Tear the newest CP slot; recovery must transparently use the old one.
	if err := fs.Device().CorruptCPSlot(slot2); err != nil {
		t.Fatal(err)
	}
	recovered, report, err := Recover(fs.Device(), fs.clock)
	if err != nil {
		t.Fatalf("recovery should fall back to the old slot, got %v", err)
	}
	if report.CPVersion != v1 || report.CPSlot != slot1 {
		t.Fatalf("recovered wrong checkpoint: version=%d slot=%d, want v=%d slot=%d",
			report.CPVersion, report.CPSlot, v1, slot1)
	}
	// The v2-only inode 2 writes were not in the v1 checkpoint and must not
	// resurrect, while v1 data remains intact.
	for i := 0; i < 4; i++ {
		data, _, rerr := recovered.Read(NodeID(1), BlockIndex(i))
		if rerr != nil || data[0] != byte(i) {
			t.Fatalf("checkpoint v1 data lost at %d: %q %v", i, data, rerr)
		}
	}
	if _, ok := recovered.NATEntryOf(NodeID(2), 0); ok {
		t.Fatal("post-old-checkpoint blocks must not resurrect without roll-forward fsync")
	}

	// Tearing both slots leaves no usable checkpoint.
	if err := fs.Device().CorruptCPSlot(slot1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Recover(fs.Device(), fs.clock); !errors.Is(err, ErrNoCheckpoint) {
		t.Fatalf("expected ErrNoCheckpoint with both slots torn, got %v", err)
	}
}

// TestRollForwardDiscardsUnsyncedTail replays an fsync chain and ensures the
// non-fsync tail write is discarded while committed data survives.
func TestRollForwardDiscardsUnsyncedTail(t *testing.T) {
	fs, _ := newTestFS(t, 8, 4)
	if _, err := fs.Write(NodeID(1), 0, []byte("base"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Fsync(NodeID(1)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fs.SaveCheckpoint(); err != nil {
		t.Fatal(err)
	}

	// Committed chain after the checkpoint.
	syncedAddr, err := fs.Write(NodeID(1), 1, []byte("synced"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Fsync(NodeID(1)); err != nil {
		t.Fatal(err)
	}

	// Tail writes never closed by an fsync: they must be dropped on recovery.
	tailAddr, err := fs.Write(NodeID(1), 2, []byte("tail"), true)
	if err != nil {
		t.Fatal(err)
	}
	tailAddr2, err := fs.Write(NodeID(1), 3, []byte("tail2"), false)
	if err != nil {
		t.Fatal(err)
	}

	recovered, report, err := Recover(fs.Device(), fs.clock)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.StopReason != StopNonFsyncTail {
		t.Fatalf("expected non-fsync-tail stop, got %s", report.StopReason)
	}
	if report.ReplayedFsyncs != 1 || report.DroppedTail != 2 {
		t.Fatalf("unexpected replay counters: %+v", report)
	}

	data, addr, err := recovered.Read(NodeID(1), 1)
	if err != nil || string(data) != "synced" || addr != syncedAddr {
		t.Fatalf("fsynced block must roll forward: %q %+v %v", data, addr, err)
	}
	if _, ok := recovered.NATEntryOf(NodeID(1), 2); ok {
		t.Fatal("unsynced tail block must not resurrect")
	}
	if _, ok := recovered.NATEntryOf(NodeID(1), 3); ok {
		t.Fatal("second unsynced tail block must not resurrect")
	}
	if recovered.IsLive(tailAddr) || recovered.IsLive(tailAddr2) {
		t.Fatal("unsynced orphan blocks must be dead after recovery")
	}
	if !recovered.Audit().Consistent {
		t.Fatal("audit inconsistent after roll-forward")
	}
}

// TestRollForwardChecksumStop corrupts the next node in the chain and verifies
// replay stops precisely at the checksum fault.
func TestRollForwardChecksumStop(t *testing.T) {
	fs, _ := newTestFS(t, 8, 4)
	if _, err := fs.Write(NodeID(3), 0, []byte("base"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Fsync(NodeID(3)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fs.SaveCheckpoint(); err != nil {
		t.Fatal(err)
	}

	goodAddr, err := fs.Write(NodeID(3), 1, []byte("good"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Fsync(NodeID(3)); err != nil {
		t.Fatal(err)
	}
	badAddr, err := fs.Write(NodeID(3), 2, []byte("bad"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Fsync(NodeID(3)); err != nil {
		t.Fatal(err)
	}
	fs.Device().CorruptBlock(badAddr)

	recovered, report, err := Recover(fs.Device(), fs.clock)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.StopReason != StopChecksum {
		t.Fatalf("expected checksum stop, got %s", report.StopReason)
	}
	if _, _, rerr := recovered.Read(NodeID(3), 1); rerr != nil {
		t.Fatalf("chain before the fault must replay: %v", rerr)
	}
	if _, ok := recovered.NATEntryOf(NodeID(3), 2); ok {
		t.Fatal("block at the checksum fault must not resurrect")
	}
	if !recovered.IsLive(goodAddr) {
		t.Fatal("replayed good block must be live")
	}
}

// TestRollForwardSequenceGap wipes a mid-chain block and verifies the version
// sequence fault stops replay at the gap.
func TestRollForwardSequenceGap(t *testing.T) {
	fs, _ := newTestFS(t, 8, 4)
	if _, err := fs.Write(NodeID(4), 0, []byte("base"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Fsync(NodeID(4)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fs.SaveCheckpoint(); err != nil {
		t.Fatal(err)
	}

	gapAddr, err := fs.Write(NodeID(4), 1, []byte("gap"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Fsync(NodeID(4)); err != nil {
		t.Fatal(err)
	}
	laterAddr, err := fs.Write(NodeID(4), 2, []byte("later"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Fsync(NodeID(4)); err != nil {
		t.Fatal(err)
	}
	fs.Device().WipeBlock(gapAddr)

	recovered, report, err := Recover(fs.Device(), fs.clock)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.StopReason != StopSequenceGap {
		t.Fatalf("expected sequence-gap stop, got %s", report.StopReason)
	}
	if _, ok := recovered.NATEntryOf(NodeID(4), 1); ok {
		t.Fatal("block missing from the chain must not resurrect")
	}
	if _, ok := recovered.NATEntryOf(NodeID(4), 2); ok {
		t.Fatal("block after the gap must not replay either")
	}
	if recovered.IsLive(laterAddr) {
		t.Fatal("post-gap block must stay dead")
	}
}
