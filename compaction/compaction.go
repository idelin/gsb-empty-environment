// Package compaction re-exports the scheduling portion of the root package.
package compaction

import root "prescreen.local/ps-b366051703"

type (
	Options        = root.CompactionOptions
	Task           = root.CompactionTask
	Scheduler      = root.CompactionScheduler
	LevelScore     = root.LevelScore
	FileMeta       = root.FileMeta
	KeyRange       = root.KeyRange
	Entry          = root.Entry
	RangeTombstone = root.RangeTombstone
	InternalKey    = root.InternalKey
	Version        = root.Version
	VersionSet     = root.VersionSet
	LockManager    = root.LockManager
	LockRequest    = root.LockRequest
)

const (
	KindPut         = root.KindPut
	KindDelete      = root.KindDelete
	KindRangeDelete = root.KindRangeDelete
)

func DefaultOptions() Options           { return root.DefaultCompactionOptions() }
func DefaultCompactionOptions() Options { return root.DefaultCompactionOptions() }

func NewScheduler(vs *VersionSet, opts Options) *Scheduler {
	return root.NewCompactionScheduler(vs, opts)
}
func NewCompactionScheduler(vs *VersionSet, opts Options) *Scheduler {
	return root.NewCompactionScheduler(vs, opts)
}
func NewTask(id uint64, level int, inputFiles, outputFiles []*FileMeta, bottommost bool) *Task {
	return root.NewCompactionTask(id, level, inputFiles, outputFiles, bottommost)
}
func NewCompactionTask(id uint64, level int, inputFiles, outputFiles []*FileMeta, bottommost bool) *Task {
	return root.NewCompactionTask(id, level, inputFiles, outputFiles, bottommost)
}
func ComputeLevelScore(level, fileCount int, size, compacting uint64, opts Options) float64 {
	return root.ComputeLevelScore(level, fileCount, size, compacting, opts)
}
func ComputeScores(v *Version, opts Options, compacting map[int]uint64) []LevelScore {
	return root.ComputeScores(v, opts, compacting)
}
func IsBottommost(v *Version, outputLevel int, r KeyRange) bool {
	return root.IsBottommost(v, outputLevel, r)
}

func NewVersion(numLevels int) *Version { return root.NewVersion(numLevels) }
func NewVersionFromFiles(levels [][]*FileMeta) *Version {
	return root.NewVersionFromFiles(levels)
}
func NewVersionSet(versions ...*Version) *VersionSet { return root.NewVersionSet(versions...) }
func NewLockManager() *LockManager                   { return root.NewLockManager() }
func NewFileMeta(id uint64, level int, entries []Entry, tombstones []RangeTombstone) *FileMeta {
	return root.NewFileMeta(id, level, entries, tombstones)
}
func NewSSTable(id uint64, level int, smallest, largest []byte, size uint64) *FileMeta {
	return root.NewSSTable(id, level, smallest, largest, size)
}
