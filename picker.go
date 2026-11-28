package lsm

import (
	"bytes"
	"fmt"
	"sort"
	"sync"
)

// CompactionOptions controls level scoring, input selection, and
// subcompaction execution.
type CompactionOptions struct {
	// L0Trigger is the L0 file count at which score reaches 1.0.
	L0Trigger int
	// LevelTargetBytes optionally overrides the byte target for each level.
	// Index 0 is ignored.
	LevelTargetBytes []uint64
	// MaxBytesForLevelBase is the target for level 1 when LevelTargetBytes is
	// not supplied.
	MaxBytesForLevelBase uint64
	// LevelSizeMultiplier derives the target for L+1 from L.
	LevelSizeMultiplier int
	// MaxInputBytes limits optional adjacent-file expansion. Zero means no
	// additional limit.
	MaxInputBytes uint64
	// MaxSubcompactions is the upper bound on concurrent subcompactions.
	MaxSubcompactions int
	// SubcompactionThreshold is the input byte size above which a compaction
	// may be split. Zero disables automatic splitting.
	SubcompactionThreshold uint64
	// TargetFileSize is the desired size of an output SST.
	TargetFileSize uint64
	// DisableAdjacentExpansion turns off optional same-level expansion.
	DisableAdjacentExpansion bool
}

// Options is a short alias for CompactionOptions.
type Options = CompactionOptions

// DefaultCompactionOptions returns conservative RocksDB-like defaults.
func DefaultCompactionOptions() CompactionOptions {
	return CompactionOptions{
		L0Trigger:              4,
		MaxBytesForLevelBase:   256 << 20,
		LevelSizeMultiplier:    10,
		MaxSubcompactions:      4,
		SubcompactionThreshold: 64 << 20,
		TargetFileSize:         64 << 20,
	}
}

func (o CompactionOptions) withDefaults() CompactionOptions {
	d := DefaultCompactionOptions()
	if o.L0Trigger <= 0 {
		o.L0Trigger = d.L0Trigger
	}
	if o.MaxBytesForLevelBase == 0 {
		o.MaxBytesForLevelBase = d.MaxBytesForLevelBase
	}
	if o.LevelSizeMultiplier <= 0 {
		o.LevelSizeMultiplier = d.LevelSizeMultiplier
	}
	if o.MaxSubcompactions <= 0 {
		o.MaxSubcompactions = d.MaxSubcompactions
	}
	if o.TargetFileSize == 0 {
		o.TargetFileSize = d.TargetFileSize
	}
	return o
}

// TargetBytesForLevel returns the byte target used by level's score.
func (o CompactionOptions) TargetBytesForLevel(level int) uint64 {
	if level >= 1 && level < len(o.LevelTargetBytes) && o.LevelTargetBytes[level] > 0 {
		return o.LevelTargetBytes[level]
	}
	if level < 1 {
		return 0
	}
	base := o.MaxBytesForLevelBase
	if base == 0 {
		base = DefaultCompactionOptions().MaxBytesForLevelBase
	}
	mult := o.LevelSizeMultiplier
	if mult <= 0 {
		mult = DefaultCompactionOptions().LevelSizeMultiplier
	}
	target := base
	for i := 1; i < level; i++ {
		if target > ^uint64(0)/uint64(mult) {
			return ^uint64(0)
		}
		target *= uint64(mult)
	}
	return target
}

// LevelScore is the urgency of one level.
type LevelScore struct {
	Level           int
	Score           float64
	FileCount       int
	Size            uint64
	CompactingBytes uint64
	TargetBytes     uint64
}

// ComputeLevelScore implements the scoring rule: L0 uses file count divided
// by l0Trigger; L>=1 uses live bytes (minus bytes already being compacted)
// divided by the level byte target.
func ComputeLevelScore(level, fileCount int, size, compactingBytes uint64, opts CompactionOptions) float64 {
	opts = opts.withDefaults()
	if level == 0 {
		return float64(fileCount) / float64(opts.L0Trigger)
	}
	target := opts.TargetBytesForLevel(level)
	if target == 0 {
		return 0
	}
	if compactingBytes >= size {
		return 0
	}
	return float64(size-compactingBytes) / float64(target)
}

// ComputeScores returns scores for every level. compacting maps level to the
// bytes currently owned by active compaction inputs.
func ComputeScores(v *Version, opts CompactionOptions, compacting map[int]uint64) []LevelScore {
	if v == nil {
		return nil
	}
	out := make([]LevelScore, 0, len(v.Levels))
	for level, files := range v.Levels {
		size := v.LevelSize(level)
		cb := compacting[level]
		out = append(out, LevelScore{
			Level:           level,
			Score:           ComputeLevelScore(level, len(files), size, cb, opts),
			FileCount:       len(files),
			Size:            size,
			CompactingBytes: cb,
			TargetBytes:     opts.TargetBytesForLevel(level),
		})
	}
	return out
}

// IsBottommost reports whether outputLevel has no older overlapping keys for
// r. This is stronger and more precise than simply checking the last level.
func IsBottommost(v *Version, outputLevel int, r KeyRange) bool {
	if v == nil || outputLevel >= len(v.Levels)-1 {
		return v != nil && outputLevel >= len(v.Levels)-1
	}
	for level := outputLevel + 1; level < len(v.Levels); level++ {
		if v.LevelHasOverlap(level, r) {
			return false
		}
	}
	return true
}

// CompactionTask is a fully expanded and locked compaction. Inputs contains
// both input-level and output-level files; the split slices identify which
// level each file belongs to.
type CompactionTask struct {
	ID               uint64
	Level            int
	InputLevel       int
	OutputLevel      int
	Score            float64
	IsL0             bool
	Inputs           []*FileMeta
	InputLevelFiles  []*FileMeta
	OutputLevelFiles []*FileMeta
	Range            KeyRange
	Bottommost       bool
	L0ToL1           bool
}

// NewCompactionTask constructs a task from explicit level partitions. It is
// useful for tests and for engines that perform their own file selection.
func NewCompactionTask(id uint64, level int, inputLevelFiles, outputLevelFiles []*FileMeta, bottommost bool) *CompactionTask {
	t := &CompactionTask{
		ID:               id,
		Level:            level,
		InputLevel:       level,
		OutputLevel:      level + 1,
		IsL0:             level == 0,
		InputLevelFiles:  append([]*FileMeta(nil), inputLevelFiles...),
		OutputLevelFiles: append([]*FileMeta(nil), outputLevelFiles...),
		Bottommost:       bottommost,
		L0ToL1:           level == 0,
	}
	t.Inputs = append(t.Inputs, t.InputLevelFiles...)
	t.Inputs = append(t.Inputs, t.OutputLevelFiles...)
	t.Inputs = uniqueFiles(t.Inputs)
	t.Range = hullOfFiles(t.Inputs)
	return t
}

// InputBytes returns the sum of sizes of all inputs, counting a file once.
func (t *CompactionTask) InputBytes() uint64 {
	var n uint64
	for _, f := range uniqueFiles(t.allInputs()) {
		n += f.EffectiveSize()
	}
	return n
}

// AllInputFiles returns a copy of the combined input list.
func (t *CompactionTask) AllInputFiles() []*FileMeta {
	return append([]*FileMeta(nil), t.allInputs()...)
}

// AllInputs is an alias for AllInputFiles.
func (t *CompactionTask) AllInputs() []*FileMeta { return t.AllInputFiles() }

// TotalInputBytes is an alias for InputBytes.
func (t *CompactionTask) TotalInputBytes() uint64 { return t.InputBytes() }

// Files is an alias for AllInputFiles.
func (t *CompactionTask) Files() []*FileMeta { return t.AllInputFiles() }

// Bytes is an alias for InputBytes.
func (t *CompactionTask) Bytes() uint64 { return t.InputBytes() }

func (t *CompactionTask) allInputs() []*FileMeta {
	if t == nil {
		return nil
	}
	if len(t.InputLevelFiles) == 0 && len(t.OutputLevelFiles) == 0 {
		return t.Inputs
	}
	combined := make([]*FileMeta, 0, len(t.Inputs)+len(t.InputLevelFiles)+len(t.OutputLevelFiles))
	combined = append(combined, t.Inputs...)
	combined = append(combined, t.InputLevelFiles...)
	combined = append(combined, t.OutputLevelFiles...)
	return uniqueFiles(combined)
}

func (t *CompactionTask) outputLevel() int {
	if t == nil {
		return 0
	}
	level := t.Level
	if level == 0 && t.InputLevel != 0 {
		level = t.InputLevel
	}
	if t.OutputLevel > level {
		return t.OutputLevel
	}
	return level + 1
}

// CompactionScheduler picks conflict-free work from a VersionSet and owns the
// task locks until Finish or Abort is called.
type CompactionScheduler struct {
	mu         sync.Mutex
	versionSet *VersionSet
	locks      *LockManager
	opts       CompactionOptions
	nextTaskID uint64
	active     map[uint64]*CompactionTask
}

// CompactionPicker and Scheduler are compatibility aliases.
type CompactionPicker = CompactionScheduler

// Scheduler is an alias for CompactionScheduler.
type Scheduler = CompactionScheduler

// NewCompactionScheduler constructs a scheduler. A nil VersionSet yields a
// one-level empty set; a nil LockManager is created automatically.
func NewCompactionScheduler(vs *VersionSet, opts CompactionOptions) *CompactionScheduler {
	if vs == nil {
		vs = NewVersionSet(NewVersion(1))
	}
	return &CompactionScheduler{
		versionSet: vs,
		locks:      NewLockManager(),
		opts:       opts.withDefaults(),
		nextTaskID: 1,
		active:     make(map[uint64]*CompactionTask),
	}
}

// NewCompactionSchedulerWithLocks constructs a scheduler sharing an existing
// lock manager, which permits several schedulers to arbitrate one engine's
// compaction resources.
func NewCompactionSchedulerWithLocks(vs *VersionSet, locks *LockManager, opts CompactionOptions) *CompactionScheduler {
	s := NewCompactionScheduler(vs, opts)
	if locks != nil {
		s.locks = locks
	}
	return s
}

// NewScheduler is an alias for NewCompactionScheduler.
func NewScheduler(vs *VersionSet, opts CompactionOptions) *CompactionScheduler {
	return NewCompactionScheduler(vs, opts)
}

// NewCompactionPicker is an alias for NewCompactionScheduler.
func NewCompactionPicker(vs *VersionSet, opts CompactionOptions) *CompactionPicker {
	return NewCompactionScheduler(vs, opts)
}

// Options returns the effective options.
func (s *CompactionScheduler) Options() CompactionOptions { return s.opts }

// Locks returns the scheduler's lock manager.
func (s *CompactionScheduler) Locks() *LockManager { return s.locks }

// VersionSet returns the scheduler's version set.
func (s *CompactionScheduler) VersionSet() *VersionSet { return s.versionSet }

// Scores returns the current per-level urgency, excluding locked bytes.
func (s *CompactionScheduler) Scores() []LevelScore {
	return ComputeScores(s.versionSet.Current(), s.opts, s.locks.CompactingBytesByLevel())
}

// L0FileCount returns the current number of L0 files.
func (s *CompactionScheduler) L0FileCount() int {
	if s == nil {
		return 0
	}
	v := s.versionSet.Current()
	if v == nil || len(v.Levels) == 0 {
		return 0
	}
	return len(v.Levels[0])
}

// PendingCompactionBytes returns the total size of all locked compaction
// inputs across levels. It is the global pending-compaction watermark consumed
// by WriteStall.
func (s *CompactionScheduler) PendingCompactionBytes() uint64 {
	if s == nil {
		return 0
	}
	var total uint64
	for _, n := range s.locks.CompactingBytesByLevel() {
		total += n
	}
	return total
}

// Pick selects and locks the highest-score conflict-free compaction. It
// returns nil when no level reaches score 1.0 or every candidate conflicts
// with an active task.
func (s *CompactionScheduler) Pick() *CompactionTask {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	v := s.versionSet.Current()
	if v == nil || len(v.Levels) < 2 {
		return nil
	}
	scores := ComputeScores(v, s.opts, s.locks.CompactingBytesByLevel())
	order := make([]LevelScore, 0, len(scores))
	for _, score := range scores {
		if score.Level < len(v.Levels)-1 && score.Score >= 1.0 {
			order = append(order, score)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].Score == order[j].Score {
			return order[i].Level < order[j].Level
		}
		return order[i].Score > order[j].Score
	})
	for _, score := range order {
		var task *CompactionTask
		if score.Level == 0 {
			task = s.pickL0(v, score.Score)
		} else {
			task = s.pickLevel(v, score.Level, score.Score)
		}
		if task != nil {
			s.active[task.ID] = task
			return task
		}
	}
	return nil
}

// PickCompaction is an alias for Pick.
func (s *CompactionScheduler) PickCompaction() *CompactionTask { return s.Pick() }

// Schedule is an alias for Pick.
func (s *CompactionScheduler) Schedule() *CompactionTask { return s.Pick() }

// ActiveTasks returns a copy of tasks currently holding locks.
func (s *CompactionScheduler) ActiveTasks() []*CompactionTask {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*CompactionTask, 0, len(s.active))
	for _, t := range s.active {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Abort releases a task's locks without installing outputs.
func (s *CompactionScheduler) Abort(task *CompactionTask) {
	if s == nil || task == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.active[task.ID]; !ok {
		return
	}
	s.locks.Unlock(task.ID)
	delete(s.active, task.ID)
}

// Finish validates, installs, and unlocks a compaction. Outputs are sorted and
// assigned file IDs when zero; a validation or installation failure releases
// the task locks so a scheduler cannot leak compaction resources.
func (s *CompactionScheduler) Finish(task *CompactionTask, outputs []*FileMeta) error {
	if s == nil || task == nil {
		return fmt.Errorf("lsm: nil scheduler or task")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.active[task.ID]; !ok {
		return fmt.Errorf("lsm: task %d is not active", task.ID)
	}
	fail := func(err error) error {
		s.locks.Unlock(task.ID)
		delete(s.active, task.ID)
		return err
	}
	for i, f := range outputs {
		if f == nil {
			return fail(fmt.Errorf("lsm: nil output %d for task %d", i, task.ID))
		}
		f.Level = task.outputLevel()
		if f.FileID == 0 {
			f.FileID = s.versionSet.NextFileID()
		}
		f.Normalize()
		if err := f.Validate(); err != nil {
			return fail(err)
		}
	}
	sortLevel(outputs)
	if err := s.versionSet.InstallCompaction(task.allInputs(), outputs, task.outputLevel()); err != nil {
		return fail(err)
	}
	s.locks.Unlock(task.ID)
	delete(s.active, task.ID)
	return nil
}

// Release is an alias for Abort.
func (s *CompactionScheduler) Release(task *CompactionTask) { s.Abort(task) }

func (s *CompactionScheduler) pickL0(v *Version, score float64) *CompactionTask {
	files := append([]*FileMeta(nil), v.Levels[0]...)
	sort.SliceStable(files, func(i, j int) bool { return fileLess(files[i], files[j]) })
	covered := make(map[*FileMeta]bool)
	for _, seed := range files {
		if seed == nil || covered[seed] {
			continue
		}
		inputs := expandL0Component(files, seed)
		for _, f := range inputs {
			covered[f] = true
		}
		hull := hullOfFiles(inputs)
		outputs := overlappingLevelFiles(v.Levels[1], hull)
		all := uniqueFiles(append(append([]*FileMeta(nil), inputs...), outputs...))
		if hasLockedFile(s.locks, all) {
			continue
		}
		task := &CompactionTask{
			ID:               s.allocateTaskIDLocked(),
			Level:            0,
			InputLevel:       0,
			OutputLevel:      1,
			Score:            score,
			IsL0:             true,
			Inputs:           all,
			InputLevelFiles:  inputs,
			OutputLevelFiles: outputs,
			Range:            hullOfFiles(all),
			Bottommost:       IsBottommost(v, 1, hullOfFiles(all)),
			L0ToL1:           true,
		}
		if s.tryLockTaskLocked(task) {
			return task
		}
	}
	return nil
}

func (s *CompactionScheduler) pickLevel(v *Version, level int, score float64) *CompactionTask {
	if level < 1 || level >= len(v.Levels)-1 {
		return nil
	}
	files := append([]*FileMeta(nil), v.Levels[level]...)
	sortLevel(files)
	for i, seed := range files {
		if seed == nil || s.locks.IsFileLocked(seed.FileID) {
			continue
		}
		baseInput := []*FileMeta{seed}
		baseHull := hullOfFiles(baseInput)
		baseOutputs := overlappingLevelFiles(v.Levels[level+1], baseHull)
		if hasLockedFile(s.locks, baseOutputs) {
			continue
		}
		baseAll := uniqueFiles(append(append([]*FileMeta(nil), baseInput...), baseOutputs...))
		baseTask := s.makeTaskLocked(level, score, baseInput, baseOutputs, baseAll, v)
		if !s.locksRangeFree(baseTask) {
			continue
		}

		if !s.opts.DisableAdjacentExpansion {
			expanded := s.expandAdjacent(v, level, i, baseInput, baseOutputs)
			if expanded != nil {
				expandedTask := s.makeTaskLocked(level, score, expanded.inputs, expanded.outputs, expanded.all, v)
				if s.locksRangeFree(expandedTask) && s.tryLockTaskLocked(expandedTask) {
					return expandedTask
				}
			}
		}
		if s.tryLockTaskLocked(baseTask) {
			return baseTask
		}
	}
	return nil
}

type expandedCandidate struct {
	inputs  []*FileMeta
	outputs []*FileMeta
	all     []*FileMeta
}

func (s *CompactionScheduler) expandAdjacent(v *Version, level, seedIndex int, inputs, outputs []*FileMeta) *expandedCandidate {
	curInputs := append([]*FileMeta(nil), inputs...)
	curOutputs := append([]*FileMeta(nil), outputs...)
	if s.opts.MaxInputBytes > 0 && sumFileBytes(curInputs)+sumFileBytes(curOutputs) > s.opts.MaxInputBytes {
		return nil
	}
	changed := false
	for _, step := range []int{-1, 1} {
		idx := seedIndex + step
		for idx >= 0 && idx < len(v.Levels[level]) {
			next := v.Levels[level][idx]
			if next == nil || s.locks.IsFileLocked(next.FileID) {
				break
			}
			tentativeInputs := append(append([]*FileMeta(nil), curInputs...), next)
			tentativeInputs = uniqueFiles(tentativeInputs)
			tentativeHull := hullOfFiles(tentativeInputs)
			tentativeOutputs := overlappingLevelFiles(v.Levels[level+1], tentativeHull)
			if hasLockedFile(s.locks, tentativeOutputs) {
				break
			}
			tentativeAll := uniqueFiles(append(tentativeInputs, tentativeOutputs...))
			if s.opts.MaxInputBytes > 0 && sumFileBytes(tentativeAll) > s.opts.MaxInputBytes {
				break
			}
			curInputs = tentativeInputs
			curOutputs = tentativeOutputs
			changed = true
			idx += step
		}
	}
	if !changed {
		return nil
	}
	sortLevel(curInputs)
	sortLevel(curOutputs)
	return &expandedCandidate{
		inputs:  curInputs,
		outputs: curOutputs,
		all:     uniqueFiles(append(curInputs, curOutputs...)),
	}
}

func (s *CompactionScheduler) makeTaskLocked(level int, score float64, inputs, outputs, all []*FileMeta, v *Version) *CompactionTask {
	hull := hullOfFiles(all)
	return &CompactionTask{
		ID:               s.allocateTaskIDLocked(),
		Level:            level,
		InputLevel:       level,
		OutputLevel:      level + 1,
		Score:            score,
		IsL0:             level == 0,
		Inputs:           all,
		InputLevelFiles:  inputs,
		OutputLevelFiles: outputs,
		Range:            hull,
		Bottommost:       IsBottommost(v, level+1, hull),
		L0ToL1:           level == 0,
	}
}

func (s *CompactionScheduler) tryLockTaskLocked(task *CompactionTask) bool {
	return s.locks.TryLock(LockRequest{
		TaskID:      task.ID,
		InputLevel:  task.Level,
		OutputLevel: task.outputLevel(),
		InputFiles:  task.Inputs,
		OutputRange: task.Range,
		L0ToL1:      task.L0ToL1,
	})
}

func (s *CompactionScheduler) locksRangeFree(task *CompactionTask) bool {
	// TryLock performs the authoritative atomic check. This cheap method only
	// avoids allocating an ID for obviously file-conflicting candidates.
	return !hasLockedFile(s.locks, task.Inputs)
}

func (s *CompactionScheduler) allocateTaskIDLocked() uint64 {
	for {
		id := s.nextTaskID
		s.nextTaskID++
		if !s.locks.HasTask(id) {
			return id
		}
	}
}

func expandL0Component(files []*FileMeta, seed *FileMeta) []*FileMeta {
	selected := []*FileMeta{seed}
	selectedSet := map[*FileMeta]bool{seed: true}
	hull := seed.Bounds()
	for {
		added := false
		for _, f := range files {
			if f == nil || selectedSet[f] {
				continue
			}
			if f.OverlapsRange(hull) {
				selected = append(selected, f)
				selectedSet[f] = true
				hull = unionRanges(hull, f.Bounds())
				added = true
			}
		}
		if !added {
			break
		}
	}
	sort.SliceStable(selected, func(i, j int) bool { return fileLess(selected[i], selected[j]) })
	return selected
}

func overlappingLevelFiles(files []*FileMeta, r KeyRange) []*FileMeta {
	var out []*FileMeta
	for _, f := range files {
		if f != nil && f.OverlapsRange(r) {
			out = append(out, f)
		}
	}
	return out
}

func unionRanges(a, b KeyRange) KeyRange {
	out := KeyRange{Start: a.Start, End: a.End}
	if b.Start != nil && (out.Start == nil || bytes.Compare(b.Start, out.Start) < 0) {
		out.Start = b.Start
	}
	if b.End == nil {
		out.End = nil
	} else if out.End != nil && bytes.Compare(b.End, out.End) > 0 {
		out.End = b.End
	}
	return out.Clone()
}

func hullOfFiles(files []*FileMeta) KeyRange {
	var hull KeyRange
	set := false
	for _, f := range uniqueFiles(files) {
		if f == nil || f.empty() {
			continue
		}
		b := f.Bounds()
		if !set {
			hull = b
			set = true
		} else {
			hull = unionRanges(hull, b)
		}
	}
	return hull
}

func uniqueFiles(files []*FileMeta) []*FileMeta {
	out := make([]*FileMeta, 0, len(files))
	ids := make(map[uint64]bool, len(files))
	ptrs := make(map[*FileMeta]bool, len(files))
	for _, f := range files {
		if f == nil {
			continue
		}
		if f.FileID != 0 {
			if ids[f.FileID] {
				continue
			}
			ids[f.FileID] = true
		} else {
			if ptrs[f] {
				continue
			}
			ptrs[f] = true
		}
		out = append(out, f)
	}
	return out
}

func hasLockedFile(m *LockManager, files []*FileMeta) bool {
	for _, f := range files {
		if f != nil && f.FileID != 0 && m.IsFileLocked(f.FileID) {
			return true
		}
	}
	return false
}

func sumFileBytes(files []*FileMeta) uint64 {
	var n uint64
	for _, f := range files {
		if f != nil {
			n += f.EffectiveSize()
		}
	}
	return n
}
