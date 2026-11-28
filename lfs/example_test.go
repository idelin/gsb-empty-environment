package lfs_test

import (
	"fmt"

	"prescreen.local/ps-6e4159f602/lfs"
)

// Example shows the typical lifecycle: writes, fsync, checkpoint, a torn CP
// slot, and roll-forward recovery that drops an unsynced tail write.
func Example() {
	clock := &lfs.FakeClock{NowNs: 1_000_000}
	fs := lfs.NewFS(lfs.Config{
		SegmentCount:       8,
		BlocksPerSeg:       4,
		EmergencyWatermark: 1,
		Clock:              clock.Get,
	})

	if _, err := fs.Write(1, 0, []byte("base"), true); err != nil {
		panic(err)
	}
	if _, err := fs.Fsync(1); err != nil {
		panic(err)
	}
	version, slot, err := fs.SaveCheckpoint()
	if err != nil {
		panic(err)
	}

	// A committed chain entry followed by an uncommitted tail write.
	if _, err := fs.Write(1, 1, []byte("synced"), true); err != nil {
		panic(err)
	}
	if _, err := fs.Fsync(1); err != nil {
		panic(err)
	}
	if _, err := fs.Write(1, 2, []byte("tail"), true); err != nil {
		panic(err)
	}

	// Simulate a half-written newest checkpoint slot; the older one is intact.
	if err := fs.Device().CorruptCPSlot(1 - slot); err != nil && err != lfs.ErrCorruptCheckpoint {
		panic(err)
	}

	recovered, report, err := lfs.Recover(fs.Device(), clock.Get)
	if err != nil {
		panic(err)
	}
	data, _, err := recovered.Read(1, 1)
	if err != nil {
		panic(err)
	}
	_, tailPresent := recovered.NATEntryOf(1, 2)

	fmt.Printf("cp=%d slot=%d reason=%s synced=%s tailResurrected=%v\n",
		version, slot, report.StopReason, string(data), tailPresent)
	// Output: cp=1 slot=0 reason=non-fsync-tail synced=synced tailResurrected=false
}
