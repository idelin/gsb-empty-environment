// Package lsm implements a small RocksDB-style leveled LSM-tree building
// block: conflict-aware concurrent compaction scheduling, byte-threshold
// subcompaction splitting with range-tombstone clipping, atomic version
// installation, and a three-state WriteStall controller.
//
// The package is in-memory. FileMeta carries SST metadata together with the
// entries and range tombstones needed by the merge executor; an engine can
// replace that payload with table-reader handles without changing the
// scheduler or splitter contracts.
//
// The key invariants enforced here are:
//
//   - L0 may overlap. Every level >= 1 is sorted and pairwise non-overlapping.
//   - A candidate touching any file locked by another task is rejected; the
//     scheduler then tries the next candidate or lower-scored level.
//   - A compaction output range on the same target level may not intersect an
//     active task's output range.
//   - Subcompaction intervals are half-open, non-overlapping, and cover the
//     complete input range. A range tombstone is clipped into every interval
//     it intersects.
//   - Old versions and range tombstones are physically elided only when the
//     output is bottommost (no older overlapping keys) and no snapshot is
//     active.
package lsm
