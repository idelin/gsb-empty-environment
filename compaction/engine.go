package compaction

import (
	"context"
	"sort"
	"sync"
)

// Config tunes the level layout, compaction picking and subcompaction
// splitting. LevelTargetBytes[i] is the size budget for level i (i >= 1).
type Config struct {
	NumLevels          int
	L0Trigger          int
	LevelTargetBytes   []int64
	MaxSubcompactions  int
	SubcompactionBytes int64
	// MaxCompactionBytes optionally caps same-level adjacent expansion. Zero
	// means unlimited expansion while neighbors stay conflict-free.
	MaxCompactionBytes int64
	Stall              StallThresholds
}

// Engine is the in-memory leveled LSM store. It owns the current Version,
// the active compaction jobs, snapshot tracking and the write controller.
type Engine struct {
	cfg Config

	mu sync.Mutex
	// version is replaced atomically by install. Readers and the scheduler
	// take a pointer under mu and then work against the immutable snapshot.
	version *Version

	nextFileNumber uint64
	lastSeq        uint64

	jobs map[uint64]*Job

	// lockedFiles maps file number to the id of the job that owns it.
	lockedFiles map[uint64]uint64

	// l0Ranges tracks every active L0->L1 compaction footprint so a second
	// one may only start when its range is disjoint.
	l0Ranges []activeL0

	// beingCompactedBytes sums sizes of files currently locked; it is removed
	// from level size when computing level scores.
	beingCompactedBytes map[int]int64

	snapshots *SnapshotTracker
	stall     *WriteController

	closed bool
}

type activeL0 struct {
	jobID uint64
	r     KeyRange
}

// NewEngine constructs an engine with sane defaults filled in for any zero
// configuration field.
func NewEngine(cfg Config) *Engine {
	if cfg.NumLevels <= 1 {
		cfg.NumLevels = 7
	}
	if cfg.L0Trigger <= 0 {
		cfg.L0Trigger = 4
	}
	if cfg.MaxSubcompactions <= 0 {
		cfg.MaxSubcompactions = 1
	}
	if cfg.SubcompactionBytes <= 0 {
		cfg.SubcompactionBytes = 1 << 20
	}
	if len(cfg.LevelTargetBytes) == 0 {
		cfg.LevelTargetBytes = make([]int64, cfg.NumLevels)
		base := int64(256 << 20)
		for i := 1; i < cfg.NumLevels; i++ {
			cfg.LevelTargetBytes[i] = base
			base *= 10
		}
	}

	e := &Engine{
		cfg:                 cfg,
		version:             &Version{Levels: make([][]*SSTable, cfg.NumLevels)},
		jobs:                make(map[uint64]*Job),
		lockedFiles:         make(map[uint64]uint64),
		beingCompactedBytes: make(map[int]int64),
		snapshots:           NewSnapshotTracker(),
		stall:               NewWriteController(cfg.Stall),
	}
	return e
}

// Config returns the engine configuration.
func (e *Engine) Config() Config { return e.cfg }

// SnapshotTracker exposes snapshot acquisition/release.
func (e *Engine) SnapshotTracker() *SnapshotTracker { return e.snapshots }

// WriteController exposes the write-stall state machine.
func (e *Engine) WriteController() *WriteController { return e.stall }

// CurrentVersion returns the immutable version visible at this instant.
func (e *Engine) CurrentVersion() *Version {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.version
}

// AllocateSeq returns the next monotonic write sequence number.
func (e *Engine) AllocateSeq() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastSeq++
	return e.lastSeq
}

// LastSeq returns the highest allocated sequence number.
func (e *Engine) LastSeq() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastSeq
}

// Put writes a value for key, honoring the current write-stall state.
func (e *Engine) Put(ctx context.Context, key UserKey, value []byte) error {
	if err := e.stall.WaitWritable(ctx); err != nil {
		return err
	}
	e.AllocateSeq()
	// The memtable itself is outside this package's scope; tests drive the
	// level layout through Flush/Ingest. This hook guarantees the stall
	// controller participates in the write admission path.
	return nil
}

// Flush atomically publishes one new L0 file containing the supplied entries
// and range tombstones, then re-evaluates write-stall pressure.
func (e *Engine) Flush(entries []Entry, rdel []RangeTombstone) *SSTable {
	e.mu.Lock()
	file := e.buildSSTableLocked(0, entries, rdel)
	e.version = cloneVersionWith(e.version, func(levels [][]*SSTable) {
		levels[0] = append(levels[0], file)
	})
	e.mu.Unlock()
	e.refreshStall()
	return file
}

// Ingest publishes an already-built file at an arbitrary level. It is a test
// and bulk-load helper: for L >= 1 the caller is responsible for keeping the
// level disjoint; CheckInvariants validates this afterwards.
func (e *Engine) Ingest(level int, entries []Entry, rdel []RangeTombstone) *SSTable {
	e.mu.Lock()
	file := e.buildSSTableLocked(level, entries, rdel)
	e.version = cloneVersionWith(e.version, func(levels [][]*SSTable) {
		levels[level] = append(levels[level], file)
		sortFiles(levels[level])
	})
	e.mu.Unlock()
	e.refreshStall()
	return file
}

func (e *Engine) buildSSTableLocked(level int, entries []Entry, rdel []RangeTombstone) *SSTable {
	sorted := append([]Entry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return compareInternal(sorted[i].InternalKey, sorted[j].InternalKey) < 0
	})
	var size int64
	for _, en := range sorted {
		size += entrySize(en)
	}
	for _, t := range rdel {
		size += tombstoneSize(t)
	}
	file := &SSTable{
		FileNumber:   e.allocFileNumberLocked(),
		Level:        level,
		Entries:      sorted,
		RangeDeletes: append([]RangeTombstone(nil), rdel...),
		SizeBytes:    size,
	}
	if len(sorted) > 0 {
		file.Smallest = sorted[0].UserKey
		file.Largest = sorted[len(sorted)-1].UserKey
	}
	if len(rdel) > 0 {
		for _, t := range rdel {
			if file.Smallest == "" || t.Start < file.Smallest {
				file.Smallest = t.Start
			}
			if t.End != "" && (file.Largest == "" || t.End > file.Largest) {
				file.Largest = prevKey(t.End)
			}
		}
	}
	return file
}

func (e *Engine) allocFileNumberLocked() uint64 {
	e.nextFileNumber++
	return e.nextFileNumber
}

func sortFiles(files []*SSTable) {
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Smallest != files[j].Smallest {
			return files[i].Smallest < files[j].Smallest
		}
		return files[i].Largest < files[j].Largest
	})
}

// cloneVersionWith returns a new Version with mut applied to fresh level
// slices, keeping the old version untouched for concurrent readers.
func cloneVersionWith(v *Version, mut func(levels [][]*SSTable)) *Version {
	nv := &Version{Levels: make([][]*SSTable, len(v.Levels))}
	for i := range v.Levels {
		nv.Levels[i] = append([]*SSTable(nil), v.Levels[i]...)
	}
	mut(nv.Levels)
	return nv
}

// PendingCompactionBytes reports the total size of files currently locked by
// active compaction jobs (the "being compacted" byte counter).
func (e *Engine) PendingCompactionBytes() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	var total int64
	for _, b := range e.beingCompactedBytes {
		total += b
	}
	return total
}

// L0FileCount returns the current number of level-zero files.
func (e *Engine) L0FileCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.version.Levels[0])
}

// ActiveJobCount returns how many compactions are currently running.
func (e *Engine) ActiveJobCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.jobs)
}

func (e *Engine) refreshStall() {
	e.mu.Lock()
	var pending int64
	for _, b := range e.beingCompactedBytes {
		pending += b
	}
	metrics := StallMetrics{
		L0FileCount:            len(e.version.Levels[0]),
		PendingCompactionBytes: pending,
	}
	e.mu.Unlock()
	e.stall.Evaluate(metrics)
}
