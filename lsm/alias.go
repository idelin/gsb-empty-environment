// Package lsm re-exports the root implementation for callers that prefer an
// explicit /lsm import path.
package lsm

import (
	"context"

	root "prescreen.local/ps-b366051703"
)

type (
	SeqNum                = root.SeqNum
	ValueKind             = root.ValueKind
	InternalKey           = root.InternalKey
	KeyRange              = root.KeyRange
	Entry                 = root.Entry
	RangeTombstone        = root.RangeTombstone
	FileMeta              = root.FileMeta
	SSTable               = root.SSTable
	FileMetadata          = root.FileMetadata
	Table                 = root.Table
	Tombstone             = root.Tombstone
	CompactionPlan        = root.CompactionPlan
	Version               = root.Version
	VersionSet            = root.VersionSet
	LockManager           = root.LockManager
	LockRequest           = root.LockRequest
	LevelScore            = root.LevelScore
	CompactionOptions     = root.CompactionOptions
	Options               = root.Options
	CompactionTask        = root.CompactionTask
	CompactionScheduler   = root.CompactionScheduler
	CompactionPicker      = root.CompactionPicker
	Scheduler             = root.Scheduler
	SubcompactionOptions  = root.SubcompactionOptions
	SubcompactionPlan     = root.SubcompactionPlan
	Subcompaction         = root.Subcompaction
	SubcompactionResult   = root.SubcompactionResult
	SubcompactionPlanner  = root.SubcompactionPlanner
	SubcompactionExecutor = root.SubcompactionExecutor
	MergeOptions          = root.MergeOptions
	MergeResult           = root.MergeResult
	WriteStallState       = root.WriteStallState
	WriteStallConfig      = root.WriteStallConfig
	WriteStallSnapshot    = root.WriteStallSnapshot
	StallTransition       = root.StallTransition
	WriteStallController  = root.WriteStallController
	WriteStall            = root.WriteStall
)

const (
	MaxSeqNum = root.MaxSeqNum

	KindPut           = root.KindPut
	KindDelete        = root.KindDelete
	KindRangeDelete   = root.KindRangeDelete
	TypeValue         = root.TypeValue
	TypeDeletion      = root.TypeDeletion
	TypeRangeDeletion = root.TypeRangeDeletion

	StallNormal            = root.StallNormal
	StallDelayed           = root.StallDelayed
	StallStopped           = root.StallStopped
	WriteStallNormal       = root.WriteStallNormal
	WriteStallDelayed      = root.WriteStallDelayed
	WriteStallStopped      = root.WriteStallStopped
	WriteStallStateNormal  = root.WriteStallStateNormal
	WriteStallStateDelayed = root.WriteStallStateDelayed
	WriteStallStateStopped = root.WriteStallStateStopped
)

var ErrWriteStallClosed = root.ErrWriteStallClosed

func MakeInternalKey(key []byte, seq SeqNum, kind ValueKind) InternalKey {
	return root.MakeInternalKey(key, seq, kind)
}

func CompareInternalKeys(a, b InternalKey) int { return root.CompareInternalKeys(a, b) }
func CompareUserKeys(a, b []byte) int          { return root.CompareUserKeys(a, b) }
func UpperBound(key []byte) []byte             { return root.UpperBound(key) }
func MakeKeyRange(start, end []byte) KeyRange  { return root.MakeKeyRange(start, end) }

func NewFileMeta(id uint64, level int, entries []Entry, tombstones []RangeTombstone) *FileMeta {
	return root.NewFileMeta(id, level, entries, tombstones)
}
func NewSSTable(id uint64, level int, smallest, largest []byte, size uint64) *FileMeta {
	return root.NewSSTable(id, level, smallest, largest, size)
}
func NewTable(id uint64, level int, smallest, largest []byte, size uint64) *FileMeta {
	return root.NewTable(id, level, smallest, largest, size)
}

func NewVersion(numLevels int) *Version { return root.NewVersion(numLevels) }
func NewVersionFromFiles(levels [][]*FileMeta) *Version {
	return root.NewVersionFromFiles(levels)
}
func NewVersionSet(versions ...*Version) *VersionSet { return root.NewVersionSet(versions...) }
func NewLockManager() *LockManager                   { return root.NewLockManager() }
func NewFileLockManager() *LockManager               { return root.NewFileLockManager() }

func NewCompactionScheduler(vs *VersionSet, opts CompactionOptions) *CompactionScheduler {
	return root.NewCompactionScheduler(vs, opts)
}
func NewCompactionPicker(vs *VersionSet, opts CompactionOptions) *CompactionPicker {
	return root.NewCompactionPicker(vs, opts)
}
func NewScheduler(vs *VersionSet, opts CompactionOptions) *CompactionScheduler {
	return root.NewScheduler(vs, opts)
}
func NewCompactionSchedulerWithLocks(vs *VersionSet, locks *LockManager, opts CompactionOptions) *CompactionScheduler {
	return root.NewCompactionSchedulerWithLocks(vs, locks, opts)
}
func NewCompactionTask(id uint64, level int, inputFiles, outputFiles []*FileMeta, bottommost bool) *CompactionTask {
	return root.NewCompactionTask(id, level, inputFiles, outputFiles, bottommost)
}
func DefaultCompactionOptions() CompactionOptions { return root.DefaultCompactionOptions() }
func ComputeLevelScore(level, fileCount int, size, compacting uint64, opts CompactionOptions) float64 {
	return root.ComputeLevelScore(level, fileCount, size, compacting, opts)
}
func ComputeScores(v *Version, opts CompactionOptions, compacting map[int]uint64) []LevelScore {
	return root.ComputeScores(v, opts, compacting)
}
func IsBottommost(v *Version, outputLevel int, r KeyRange) bool {
	return root.IsBottommost(v, outputLevel, r)
}
func NewSubcompactionPlanner(opts SubcompactionOptions) *SubcompactionPlanner {
	return root.NewSubcompactionPlanner(opts)
}
func NewSubcompactionExecutor(opts SubcompactionOptions) *SubcompactionExecutor {
	return root.NewSubcompactionExecutor(opts)
}

func PlanSubcompactions(task *CompactionTask, opts SubcompactionOptions) []*SubcompactionPlan {
	return root.PlanSubcompactions(task, opts)
}
func ExecuteSubcompactions(ctx context.Context, task *CompactionTask, opts SubcompactionOptions) ([]*FileMeta, error) {
	return root.ExecuteSubcompactions(ctx, task, opts)
}
func RunSubcompactions(ctx context.Context, task *CompactionTask, opts SubcompactionOptions) (*SubcompactionResult, error) {
	return root.RunSubcompactions(ctx, task, opts)
}
func MergeCompaction(entries []Entry, tombstones []RangeTombstone, opts MergeOptions) MergeResult {
	return root.MergeCompaction(entries, tombstones, opts)
}
func MergeEntries(entries []Entry, tombstones []RangeTombstone, opts MergeOptions) []Entry {
	return root.MergeEntries(entries, tombstones, opts)
}
func CanDropObsolete(opts MergeOptions) bool { return root.CanDropObsolete(opts) }

func Lookup(files []*FileMeta, key []byte, snapshot SeqNum) ([]byte, bool, bool) {
	return root.Lookup(files, key, snapshot)
}
func LookupVersion(v *Version, key []byte, snapshot SeqNum) ([]byte, bool, bool) {
	return root.LookupVersion(v, key, snapshot)
}

func DefaultWriteStallConfig() WriteStallConfig { return root.DefaultWriteStallConfig() }
func NewWriteStallController(configs ...WriteStallConfig) *WriteStallController {
	return root.NewWriteStallController(configs...)
}
func NewWriteStall(configs ...WriteStallConfig) *WriteStallController {
	return root.NewWriteStall(configs...)
}
