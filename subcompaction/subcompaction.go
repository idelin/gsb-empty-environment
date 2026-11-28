// Package subcompaction re-exports planning and execution of child
// compactions.
package subcompaction

import (
	"context"

	root "prescreen.local/ps-b366051703"
)

type (
	Options        = root.SubcompactionOptions
	Plan           = root.SubcompactionPlan
	Result         = root.SubcompactionResult
	Task           = root.CompactionTask
	FileMeta       = root.FileMeta
	Entry          = root.Entry
	RangeTombstone = root.RangeTombstone
	KeyRange       = root.KeyRange
	ValueKind      = root.ValueKind
	InternalKey    = root.InternalKey
	MergeOptions   = root.MergeOptions
	MergeResult    = root.MergeResult
	SeqNum         = root.SeqNum
)

const (
	KindPut         = root.KindPut
	KindDelete      = root.KindDelete
	KindRangeDelete = root.KindRangeDelete
	MaxSeqNum       = root.MaxSeqNum
)

func NewFileMeta(id uint64, level int, entries []Entry, tombstones []RangeTombstone) *FileMeta {
	return root.NewFileMeta(id, level, entries, tombstones)
}

func PlanSubcompactions(task *Task, opts Options) []*Plan {
	return root.PlanSubcompactions(task, opts)
}
func Split(task *Task, opts Options) []*Plan { return root.PlanSubcompactions(task, opts) }

func Execute(ctx context.Context, task *Task, opts Options) ([]*FileMeta, error) {
	return root.ExecuteSubcompactions(ctx, task, opts)
}
func ExecuteSubcompactions(ctx context.Context, task *Task, opts Options) ([]*FileMeta, error) {
	return root.ExecuteSubcompactions(ctx, task, opts)
}
func Run(ctx context.Context, task *Task, opts Options) (*Result, error) {
	return root.RunSubcompactions(ctx, task, opts)
}
func RunSubcompactions(ctx context.Context, task *Task, opts Options) (*Result, error) {
	return root.RunSubcompactions(ctx, task, opts)
}
func MergeCompaction(entries []Entry, tombstones []RangeTombstone, opts MergeOptions) MergeResult {
	return root.MergeCompaction(entries, tombstones, opts)
}
func MergeEntries(entries []Entry, tombstones []RangeTombstone, opts MergeOptions) []Entry {
	return root.MergeEntries(entries, tombstones, opts)
}
func CanDropObsolete(opts MergeOptions) bool { return root.CanDropObsolete(opts) }
func NewCompactionTask(id uint64, level int, inputFiles, outputFiles []*FileMeta, bottommost bool) *Task {
	return root.NewCompactionTask(id, level, inputFiles, outputFiles, bottommost)
}
