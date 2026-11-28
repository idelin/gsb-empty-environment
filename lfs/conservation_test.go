package lfs

import (
	"testing"
	"time"
)

// TestBlockConservation runs a mixed hot/cold workload with snapshots and many
// cleaning passes, asserting block conservation at every step and after
// checkpoint/recovery.
func TestBlockConservation(t *testing.T) {
	clock := &FakeClock{NowNs: 0}
	const B = 3
	fs := NewFS(Config{SegmentCount: 12, BlocksPerSeg: B, Clock: clock.Get, EmergencyWatermark: 1})

	writeRound := func(round int) {
		for ino := NodeID(1); ino <= 4; ino++ {
			for blk := BlockIndex(0); blk < 2; blk++ {
				hot := (ino+NodeID(round))%2 == 0
				if _, err := fs.Write(ino, blk, []byte{byte(round), byte(ino), byte(blk)}, hot); err != nil {
					t.Fatalf("write round=%d ino=%d blk=%d: %v", round, ino, blk, err)
				}
			}
		}
	}

	writeRound(0)
	if err := fs.CreateSnapshot("s0"); err != nil {
		t.Fatal(err)
	}

	for round := 1; round <= 12; round++ {
		clock.Advance(time.Second)
		writeRound(round)
		if round%3 == 0 {
			if _, err := fs.Fsync(NodeID(1)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := fs.CleanUntilStable(); err != nil {
			t.Fatalf("clean round %d: %v", round, err)
		}
		report := fs.Audit()
		if !report.Consistent {
			t.Fatalf("conservation broken at round %d: %+v", round, report)
		}
		if report.OccupiedBlocks != report.LiveBlocks+report.DeadBlocks {
			t.Fatalf("occupied != live+dead at round %d: %+v", round, report)
		}
	}

	// Every current logical block still reads the newest version.
	for ino := NodeID(1); ino <= 4; ino++ {
		for blk := BlockIndex(0); blk < 2; blk++ {
			data, _, err := fs.Read(ino, blk)
			if err != nil {
				t.Fatalf("read ino=%d blk=%d: %v", ino, blk, err)
			}
			if data[1] != byte(ino) || data[2] != byte(blk) {
				t.Fatalf("stale data for ino=%d blk=%d: %v", ino, blk, data)
			}
		}
	}

	// Release the snapshot, clean everything, then checkpoint and recover.
	if err := fs.DeleteSnapshot("s0"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.CleanUntilStable(); err != nil {
		t.Fatalf("final clean: %v", err)
	}
	if _, _, err := fs.SaveCheckpoint(); err != nil {
		t.Fatal(err)
	}
	recovered, _, err := Recover(fs.Device(), clock.Get)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	post := recovered.Audit()
	if !post.Consistent {
		t.Fatalf("post-recovery conservation broken: %+v", post)
	}
	for ino := NodeID(1); ino <= 4; ino++ {
		for blk := BlockIndex(0); blk < 2; blk++ {
			if _, _, err := recovered.Read(ino, blk); err != nil {
				t.Fatalf("recovered read ino=%d blk=%d: %v", ino, blk, err)
			}
		}
	}
}

// TestCleanThenRecoverPreservesData ensures migrated NAT pointers survive a
// checkpoint and recovery cycle.
func TestCleanThenRecoverPreservesData(t *testing.T) {
	clock := &FakeClock{NowNs: 100}
	const B = 2
	fs := NewFS(Config{SegmentCount: 5, BlocksPerSeg: B, Clock: clock.Get, EmergencyWatermark: 1})

	addrs := map[BlockIndex]PhysAddr{}
	for i := BlockIndex(0); i < 4; i++ {
		addr, err := fs.Write(NodeID(9), i, []byte{byte(i)}, i%2 == 0)
		if err != nil {
			t.Fatal(err)
		}
		addrs[i] = addr
	}
	// Overwrite the first block to create garbage and make cleaning productive.
	newAddr, err := fs.Write(NodeID(9), 0, []byte{99}, true)
	if err != nil {
		t.Fatal(err)
	}
	if newAddr == addrs[0] {
		t.Fatal("overwrite must relocate")
	}
	if _, err := fs.Fsync(NodeID(9)); err != nil {
		t.Fatal(err)
	}

	if _, err := fs.CleanUntilStable(); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if _, _, err := fs.SaveCheckpoint(); err != nil {
		t.Fatal(err)
	}
	recovered, _, err := Recover(fs.Device(), clock.Get)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	for i := BlockIndex(0); i < 4; i++ {
		data, _, err := recovered.Read(NodeID(9), i)
		if err != nil {
			t.Fatalf("recovered block %d: %v", i, err)
		}
		want := byte(i)
		if i == 0 {
			want = 99
		}
		if data[0] != want {
			t.Fatalf("recovered block %d = %d, want %d", i, data[0], want)
		}
	}
	if !recovered.Audit().Consistent {
		t.Fatal("audit inconsistent after clean+recover")
	}
}
