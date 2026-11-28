// Package compaction (usage notes)
//
// A minimal end-to-end flow:
//
//	cfg := compaction.Config{
//	    NumLevels:         4,
//	    L0Trigger:         4,
//	    LevelTargetBytes:  []int64{0, 256 << 20, 256 << 30, 256 << 40},
//	    MaxSubcompactions: 4,
//	    SubcompactionBytes: 1 << 20,
//	    Stall:             compaction.DefaultStallThresholds(),
//	}
//	engine := compaction.NewEngine(cfg)
//
//	// Writes publish L0 SSTs and drive the write-stall state machine.
//	engine.Flush([]compaction.Entry{compaction.Entry{
//	    InternalKey: compaction.InternalKey{UserKey: "k", Seq: 1, Kind: compaction.KindPut},
//	    Value:       []byte("v"),
//	}}, nil)
//
//	// Background scheduling: workers pick conflict-free jobs and run
//	// parallel subcompactions, then atomically install new versions.
//	sched := compaction.NewScheduler(engine)
//	sched.MaxConcurrent = 2
//	sched.Start()
//	defer sched.Close()
//
//	// Snapshots anchor old versions; the write controller admits writes.
//	snap := engine.SnapshotTracker().Acquire(engine.LastSeq())
//	defer engine.SnapshotTracker().Release(snap)
//	if err := engine.WriteController().WaitWritable(ctx); err != nil { ... }
//
// Correctness guarantees exercised by the tests:
//   - Level scores: L0 uses file-count/trigger; L>=1 excludes bytes already
//     under compaction before dividing by the level target.
//   - Input expansion: transitive L0 overlap closure, all overlapping L+1
//     files and conflict-free same-level neighbor expansion.
//   - Locking: any locked input (or an intersecting L0->L1 range) makes the
//     scheduler skip to the next conflict-free candidate.
//   - Subcompactions tile the job range as disjoint half-open spans; range
//     tombstones are clipped into every span they intersect (zero
//     resurrection).
//   - Physical dropping happens only on the bottommost level with no active
//     snapshot; otherwise versions and tombstones are preserved.
//   - Installed L>=1 levels are strictly key-ordered and non-overlapping.
//   - WriteStall switches Normal <-> Delayed <-> Stopped from L0 file count
//     and pending compaction bytes, with hard stops taking precedence.
package compaction
