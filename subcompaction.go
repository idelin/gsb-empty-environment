package lsm

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"
)

// SubcompactionOptions controls splitting and execution.
type SubcompactionOptions struct {
	// MaxSubcompactions is the hard upper bound on child compactions.
	MaxSubcompactions int
	// ThresholdBytes enables splitting when the task's input exceeds it.
	ThresholdBytes uint64
	// Snapshots anchors old versions. When nil and execution is driven by a
	// scheduler, the scheduler supplies the VersionSet's active snapshots.
	Snapshots []SeqNum
	// TargetFileSize is accepted for output partitioning. This implementation
	// emits one output table per interval.
	TargetFileSize uint64
	// NextFileID optionally allocates output file identifiers.
	NextFileID func() uint64
	// UseVersionSetSnapshots asks the scheduler integration to replace
	// Snapshots with the VersionSet's current snapshot list.
	UseVersionSetSnapshots bool
}

func (o SubcompactionOptions) effectiveMax() int {
	if o.MaxSubcompactions <= 0 {
		return DefaultCompactionOptions().MaxSubcompactions
	}
	return o.MaxSubcompactions
}

func (o SubcompactionOptions) effectiveThreshold() uint64 {
	return o.ThresholdBytes
}

// SubcompactionPlan is one closed-open slice of a compaction. RangeTombstones
// are already clipped to Range, and every plan intersecting a tombstone
// receives its own clipped copy.
type SubcompactionPlan struct {
	Index           int
	Start           []byte
	End             []byte
	Range           KeyRange
	Inputs          []*FileMeta
	Entries         []Entry
	RangeTombstones []RangeTombstone
	Bottommost      bool
	DropTombstones  bool
	Snapshots       []SeqNum
}

// SubcompactionPlanner is a stateful convenience wrapper around
// PlanSubcompactions.
type SubcompactionPlanner struct {
	Options SubcompactionOptions
}

// NewSubcompactionPlanner returns a planner using opts.
func NewSubcompactionPlanner(opts SubcompactionOptions) *SubcompactionPlanner {
	return &SubcompactionPlanner{Options: opts}
}

// Split plans child compactions for task.
func (p *SubcompactionPlanner) Split(task *CompactionTask) []*SubcompactionPlan {
	if p == nil {
		return PlanSubcompactions(task, SubcompactionOptions{})
	}
	return PlanSubcompactions(task, p.Options)
}

// Plan is an alias for Split.
func (p *SubcompactionPlanner) Plan(task *CompactionTask) []*SubcompactionPlan {
	return p.Split(task)
}

// SubcompactionExecutor is a stateful convenience wrapper around
// RunSubcompactions.
type SubcompactionExecutor struct {
	Options SubcompactionOptions
}

// NewSubcompactionExecutor returns an executor using opts.
func NewSubcompactionExecutor(opts SubcompactionOptions) *SubcompactionExecutor {
	return &SubcompactionExecutor{Options: opts}
}

// Execute runs task's subcompactions.
func (e *SubcompactionExecutor) Execute(ctx context.Context, task *CompactionTask) ([]*FileMeta, error) {
	if e == nil {
		return ExecuteSubcompactions(ctx, task, SubcompactionOptions{})
	}
	return ExecuteSubcompactions(ctx, task, e.Options)
}

// Subcompaction is an alias for SubcompactionPlan.
type Subcompaction = SubcompactionPlan

// StartKey returns the inclusive lower boundary (nil means negative
// infinity).
func (p *SubcompactionPlan) StartKey() []byte {
	if p == nil {
		return nil
	}
	return p.Start
}

// EndKey returns the exclusive upper boundary (nil means positive infinity).
func (p *SubcompactionPlan) EndKey() []byte {
	if p == nil {
		return nil
	}
	return p.End
}

// Covers reports whether key belongs to the plan's half-open interval.
func (p *SubcompactionPlan) Covers(key []byte) bool {
	if p == nil {
		return false
	}
	return p.Range.Contains(key)
}

// PlanSubcompactions splits a task at input/output SST boundary keys. The
// result contains at most MaxSubcompactions non-overlapping [start,end)
// intervals, and a range tombstone spanning several intervals is clipped and
// emitted into every interval it intersects.
func PlanSubcompactions(task *CompactionTask, opts SubcompactionOptions) []*SubcompactionPlan {
	if task == nil || len(task.allInputs()) == 0 {
		return nil
	}
	maxSub := opts.effectiveMax()
	if maxSub < 1 {
		maxSub = 1
	}
	snapshots := append([]SeqNum(nil), opts.Snapshots...)
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i] < snapshots[j] })
	canDrop := task.Bottommost && len(snapshots) == 0

	full := hullOfFiles(task.allInputs())
	if full.Empty() {
		full = task.Range
	}
	if full.Empty() && len(task.allInputs()) == 0 {
		return nil
	}

	desired := 1
	threshold := opts.effectiveThreshold()
	if threshold > 0 && task.InputBytes() > threshold {
		chunks := task.InputBytes() / threshold
		if task.InputBytes()%threshold != 0 {
			chunks++
		}
		if chunks > uint64(maxSub) {
			chunks = uint64(maxSub)
		}
		desired = int(chunks)
		if desired < 2 {
			desired = 2
		}
		if desired > maxSub {
			desired = maxSub
		}
	}

	bounds := subcompactionBoundaries(task, full)
	if desired <= 1 || len(bounds) < 2 {
		return []*SubcompactionPlan{buildSubcompactionPlan(task, 0, full, snapshots, canDrop)}
	}

	// bounds always contains full.Start at index 0 (unless it is unbounded)
	// and full.End at the end (unless it is unbounded). Interior candidates
	// are real SST/tombstone boundary keys.
	interior := make([][]byte, 0, len(bounds))
	for _, b := range bounds {
		if full.Start != nil && bytes.Compare(b, full.Start) <= 0 {
			continue
		}
		if full.End != nil && bytes.Compare(b, full.End) >= 0 {
			continue
		}
		interior = append(interior, b)
	}
	want := desired - 1
	if want > len(interior) {
		want = len(interior)
	}
	splits := chooseBoundaries(interior, want)

	plans := make([]*SubcompactionPlan, 0, len(splits)+1)
	start := cloneBytes(full.Start)
	for i, split := range splits {
		current := KeyRange{Start: start, End: split}
		plans = append(plans, buildSubcompactionPlan(task, i, current, snapshots, canDrop))
		start = split
	}
	plans = append(plans, buildSubcompactionPlan(task, len(splits), KeyRange{Start: start, End: cloneBytes(full.End)}, snapshots, canDrop))
	return plans
}

// Split is a convenience method on a task.
func (t *CompactionTask) Split(opts SubcompactionOptions) []*SubcompactionPlan {
	return PlanSubcompactions(t, opts)
}

func subcompactionBoundaries(task *CompactionTask, full KeyRange) [][]byte {
	seen := make(map[string][]byte)
	add := func(k []byte) {
		if k == nil {
			return
		}
		key := string(k)
		if _, ok := seen[key]; !ok {
			seen[key] = cloneBytes(k)
		}
	}
	add(full.Start)
	add(full.End)
	for _, f := range task.allInputs() {
		if f == nil {
			continue
		}
		add(f.Smallest.UserKey)
		add(f.Largest.UserKey)
		for _, t := range f.RangeTombstones {
			add(t.Start)
			add(t.End)
		}
	}
	out := make([][]byte, 0, len(seen))
	for _, k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}

func chooseBoundaries(candidates [][]byte, n int) [][]byte {
	if n <= 0 || len(candidates) == 0 {
		return nil
	}
	if n >= len(candidates) {
		out := make([][]byte, len(candidates))
		for i := range candidates {
			out[i] = cloneBytes(candidates[i])
		}
		return out
	}
	out := make([][]byte, 0, n)
	for i := 1; i <= n; i++ {
		idx := i * len(candidates) / (n + 1)
		if idx >= len(candidates) {
			idx = len(candidates) - 1
		}
		// Move left if the index was already used; uniqueness matters because
		// interval boundaries must be strictly increasing.
		for idx > 0 && len(out) > 0 && bytes.Compare(candidates[idx], out[len(out)-1]) <= 0 {
			idx--
		}
		if len(out) > 0 && bytes.Compare(candidates[idx], out[len(out)-1]) <= 0 {
			continue
		}
		out = append(out, cloneBytes(candidates[idx]))
	}
	return out
}

func buildSubcompactionPlan(task *CompactionTask, index int, r KeyRange, snapshots []SeqNum, canDrop bool) *SubcompactionPlan {
	plan := &SubcompactionPlan{
		Index:          index,
		Start:          cloneBytes(r.Start),
		End:            cloneBytes(r.End),
		Range:          r.Clone(),
		Bottommost:     task.Bottommost,
		DropTombstones: canDrop,
		Snapshots:      append([]SeqNum(nil), snapshots...),
	}
	seenFiles := make(map[*FileMeta]bool)
	for _, f := range task.allInputs() {
		if f == nil || !f.OverlapsRange(r) {
			continue
		}
		if !seenFiles[f] {
			plan.Inputs = append(plan.Inputs, f)
			seenFiles[f] = true
		}
		for _, e := range f.Entries {
			if r.Contains(e.Key) {
				plan.Entries = append(plan.Entries, e.Clone())
			}
		}
		for _, t := range f.RangeTombstones {
			if clipped, ok := t.Clip(r); ok {
				plan.RangeTombstones = append(plan.RangeTombstones, clipped)
			}
		}
	}
	sort.SliceStable(plan.Entries, func(i, j int) bool {
		return CompareInternalKeys(plan.Entries[i].InternalKey(), plan.Entries[j].InternalKey()) < 0
	})
	plan.RangeTombstones = uniqueTombstones(plan.RangeTombstones)
	sort.SliceStable(plan.RangeTombstones, func(i, j int) bool {
		if c := bytes.Compare(plan.RangeTombstones[i].Start, plan.RangeTombstones[j].Start); c != 0 {
			return c < 0
		}
		if c := bytes.Compare(plan.RangeTombstones[i].End, plan.RangeTombstones[j].End); c != 0 {
			return c < 0
		}
		return plan.RangeTombstones[i].Seq > plan.RangeTombstones[j].Seq
	})
	return plan
}

// SubcompactionResult reports the plans and output files of an execution.
type SubcompactionResult struct {
	Plans   []*SubcompactionPlan
	Outputs []*FileMeta
}

// ExecuteSubcompactions splits and executes all child merges concurrently,
// preserving plan order in the returned output list.
func ExecuteSubcompactions(ctx context.Context, task *CompactionTask, opts SubcompactionOptions) ([]*FileMeta, error) {
	result, err := RunSubcompactions(ctx, task, opts)
	if err != nil {
		return nil, err
	}
	return result.Outputs, nil
}

// RunSubcompactions is the detailed form of ExecuteSubcompactions.
func RunSubcompactions(ctx context.Context, task *CompactionTask, opts SubcompactionOptions) (*SubcompactionResult, error) {
	if task == nil {
		return nil, fmt.Errorf("lsm: nil compaction task")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plans := PlanSubcompactions(task, opts)
	if len(plans) == 0 {
		return &SubcompactionResult{}, nil
	}
	type outcome struct {
		file *FileMeta
		err  error
	}
	outcomes := make([]outcome, len(plans))
	sem := make(chan struct{}, opts.effectiveMax())
	var wg sync.WaitGroup
	for i, plan := range plans {
		i, plan := i, plan
		if err := ctx.Err(); err != nil {
			outcomes[i] = outcome{err: err}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			select {
			case <-ctx.Done():
				outcomes[i] = outcome{err: ctx.Err()}
				return
			default:
			}
			file, err := executeOneSubcompaction(task, plan)
			outcomes[i] = outcome{file: file, err: err}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &SubcompactionResult{Plans: plans}
	for i := range outcomes {
		if outcomes[i].err != nil {
			return nil, fmt.Errorf("lsm: subcompaction %d: %w", i, outcomes[i].err)
		}
		if outcomes[i].file != nil {
			result.Outputs = append(result.Outputs, outcomes[i].file)
		}
	}
	if opts.NextFileID != nil {
		for _, f := range result.Outputs {
			if f.FileID == 0 {
				f.FileID = opts.NextFileID()
			}
		}
	}
	return result, nil
}

func executeOneSubcompaction(task *CompactionTask, plan *SubcompactionPlan) (*FileMeta, error) {
	merged := MergeCompaction(plan.Entries, plan.RangeTombstones, MergeOptions{
		Snapshots:  plan.Snapshots,
		Bottommost: task.Bottommost,
	})
	if len(merged.Entries) == 0 && len(merged.RangeTombstones) == 0 {
		return nil, nil
	}
	file := &FileMeta{
		Level:           task.outputLevel(),
		Entries:         merged.Entries,
		RangeTombstones: merged.RangeTombstones,
	}
	if len(merged.Entries) > 0 {
		file.Smallest = merged.Entries[0].InternalKey().Clone()
		file.Largest = merged.Entries[len(merged.Entries)-1].InternalKey().Clone()
	}
	minK, maxK, ok := planExtent(merged.Entries, merged.RangeTombstones)
	if ok {
		if file.Smallest.UserKey == nil {
			file.Smallest = MakeInternalKey(minK, MaxSeqNum, KindRangeDelete)
		}
		if file.Largest.UserKey == nil {
			file.Largest = MakeInternalKey(maxK, 0, KindRangeDelete)
		}
	}
	file.Size = file.EffectiveSize()
	if err := file.Validate(); err != nil {
		return nil, err
	}
	return file, nil
}

func planExtent(entries []Entry, tombstones []RangeTombstone) (minK, maxK []byte, ok bool) {
	take := func(k []byte) {
		if !ok {
			minK = cloneBytes(k)
			maxK = cloneBytes(k)
			ok = true
			return
		}
		if bytes.Compare(k, minK) < 0 {
			minK = cloneBytes(k)
		}
		if bytes.Compare(k, maxK) > 0 {
			maxK = cloneBytes(k)
		}
	}
	for _, e := range entries {
		take(e.Key)
	}
	for _, t := range tombstones {
		if t.Start != nil {
			take(t.Start)
		}
		if t.End != nil {
			take(t.End)
		}
	}
	return minK, maxK, ok
}

// ExecuteSubcompactionsAndInstall executes a locked task and installs the
// resulting outputs. When opts.UseVersionSetSnapshots is set the current
// snapshot anchors are captured before planning.
func (s *CompactionScheduler) ExecuteSubcompactionsAndInstall(ctx context.Context, task *CompactionTask, opts SubcompactionOptions) ([]*FileMeta, error) {
	if s == nil || task == nil {
		return nil, fmt.Errorf("lsm: nil scheduler or task")
	}
	if opts.MaxSubcompactions == 0 {
		opts.MaxSubcompactions = s.opts.MaxSubcompactions
	}
	if opts.ThresholdBytes == 0 {
		opts.ThresholdBytes = s.opts.SubcompactionThreshold
	}
	if opts.UseVersionSetSnapshots || opts.Snapshots == nil {
		opts.Snapshots = s.versionSet.Snapshots()
	}
	opts.NextFileID = s.versionSet.NextFileID
	outputs, err := ExecuteSubcompactions(ctx, task, opts)
	if err != nil {
		s.Abort(task)
		return nil, err
	}
	if err := s.Finish(task, outputs); err != nil {
		return nil, err
	}
	return outputs, nil
}
