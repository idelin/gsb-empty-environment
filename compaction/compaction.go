package compaction

import (
	"sort"
	"strconv"
	"sync"
)

// CompactionResult summarizes one finished compaction.
type CompactionResult struct {
	JobID          uint64
	Level          int
	OutputLevel    int
	Inputs         []uint64
	Outputs        []*SSTable
	Subcompactions int
	DroppedEntries int
	Bottommost     bool
	SnapshotSafe   bool
}

// IsBottommost reports whether outputLevel is the last level, meaning no
// older layer can hold overlapping keys.
func (e *Engine) IsBottommost(outputLevel int) bool {
	return outputLevel >= e.cfg.NumLevels-1
}

// RunCompaction executes a previously picked job: it plans subcompactions,
// merges each span concurrently, applies range tombstones, drops obsolete
// versions only when safe and atomically installs the new Version. Locks are
// released on every exit path.
func (e *Engine) RunCompaction(job *Job) (*CompactionResult, error) {
	e.mu.Lock()
	if cur, ok := e.jobs[job.ID]; !ok || cur != job {
		e.mu.Unlock()
		return nil, ErrNoCompaction
	}
	job.Status = StatusRunning
	version := e.version
	bottommost := e.IsBottommost(job.OutputLevel)
	_, hasSnapshot := e.snapshots.OldestSeq()
	e.mu.Unlock()

	// Physical deletion is permitted only on the bottommost level with no
	// active snapshot anchoring old versions.
	dropObsolete := bottommost && !hasSnapshot

	// Planning reads only the immutable version captured above, so it never
	// contends with concurrent schedulers or writers.
	job.Subcompactions = e.planSubcompactions(job, version)
	job.Spans = e.spanRanges(job)

	results := make([]subResult, len(job.Subcompactions))
	var wg sync.WaitGroup
	for _, sub := range job.Subcompactions {
		wg.Add(1)
		go func(sub *Subcompaction) {
			defer wg.Done()
			results[sub.Index] = e.mergeSubcompaction(job, sub, dropObsolete)
		}(sub)
	}
	wg.Wait()

	return e.install(job, results, dropObsolete, bottommost, hasSnapshot)
}

func (e *Engine) spanRanges(job *Job) []KeyRange {
	subs := job.Subcompactions
	out := make([]KeyRange, len(subs))
	for i, s := range subs {
		lo := s.Lo
		hi := s.Hi
		if hi != "" {
			hi = prevKey(hi)
		}
		out[i] = KeyRange{Smallest: lo, Largest: hi}
	}
	return out
}

type subResult struct {
	entries        []Entry
	tombstones     []RangeTombstone
	droppedEntries int
	empty          bool
}

// mergeSubcompaction merges all input entries that fall in the span. Range
// tombstones are consulted per entry; obsolete versions are removed only in
// drop mode and retained verbatim otherwise.
func (e *Engine) mergeSubcompaction(job *Job, sub *Subcompaction, dropObsolete bool) subResult {
	var entries []Entry
	for _, f := range job.Inputs {
		for _, en := range f.Entries {
			if sub.InSpan(en.UserKey) {
				entries = append(entries, en)
			}
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return compareInternal(entries[i].InternalKey, entries[j].InternalKey) < 0
	})

	res := subResult{}
	if !dropObsolete {
		// Preserve mode: keep every version and every tombstone so an active
		// snapshot or a lower level can still observe them. Tombstones remain
		// physical records instead of being applied.
		res.entries = entries
		res.tombstones = append([]RangeTombstone(nil), sub.Tombstones...)
		if len(entries) == 0 && len(res.tombstones) == 0 {
			res.empty = true
		}
		return res
	}

	// Drop mode: entries are already sorted user-key ascending, seq
	// descending. For each user key keep only the newest surviving version;
	// anything shadowed by a newer value, a delete marker or a covering range
	// tombstone is physically removed.
	for i := 0; i < len(entries); {
		key := entries[i].UserKey
		j := i + 1
		for j < len(entries) && entries[j].UserKey == key {
			j++
		}
		newest := entries[i]
		shadowedByTombstone := false
		for _, t := range sub.Tombstones {
			if t.Covers(key) && t.Seq >= newest.Seq {
				shadowedByTombstone = true
				break
			}
		}
		res.droppedEntries += (j - i) - 1 // all older versions of the key
		switch {
		case shadowedByTombstone:
			res.droppedEntries++ // newest version is also covered
		case newest.Kind == KindDelete:
			res.droppedEntries++ // delete with no lower level: fully dropped
		default:
			res.entries = append(res.entries, newest)
		}
		i = j
	}
	// Range tombstones are consumed in drop mode because the bottommost level
	// has no older data to hide and no snapshot needs them.
	if len(res.entries) == 0 {
		res.empty = true
	}
	return res
}

// install validates the merged spans, builds output SSTables and publishes a
// new Version atomically, then releases all job locks.
func (e *Engine) install(job *Job, results []subResult, dropObsolete, bottommost, hasSnapshot bool) (*CompactionResult, error) {
	e.mu.Lock()

	inputIDs := make([]uint64, 0, len(job.Inputs))
	inputSet := fileNumberSet(job.Inputs)

	outputs := make([]*SSTable, 0, len(results))
	totalDropped := 0
	for idx, res := range results {
		totalDropped += res.droppedEntries
		if res.empty {
			continue
		}
		file := e.buildOutputLocked(job, idx, res, dropObsolete)
		outputs = append(outputs, file)
	}

	newVersion := cloneVersionWith(e.version, func(levels [][]*SSTable) {
		levels[job.Level] = removeFiles(levels[job.Level], inputSet)
		levels[job.OutputLevel] = removeFiles(levels[job.OutputLevel], inputSet)
		for _, out := range outputs {
			levels[job.OutputLevel] = append(levels[job.OutputLevel], out)
		}
		if job.OutputLevel > 0 {
			sortFiles(levels[job.OutputLevel])
		}
	})

	if job.OutputLevel > 0 {
		if err := validateLevelDisjoint(newVersion.Levels[job.OutputLevel]); err != nil {
			job.Status = StatusFailed
			e.releaseJobLocked(job)
			e.mu.Unlock()
			e.refreshStall()
			return nil, err
		}
	}

	for _, f := range job.Inputs {
		inputIDs = append(inputIDs, f.FileNumber)
	}

	e.version = newVersion
	job.Status = StatusInstalled
	job.Outputs = outputs
	job.DroppedEntries = totalDropped
	e.releaseJobLocked(job)
	result := &CompactionResult{
		JobID:          job.ID,
		Level:          job.Level,
		OutputLevel:    job.OutputLevel,
		Inputs:         inputIDs,
		Outputs:        outputs,
		Subcompactions: len(results),
		DroppedEntries: totalDropped,
		Bottommost:     bottommost,
		SnapshotSafe:   !dropObsolete,
	}
	e.mu.Unlock()
	e.refreshStall()
	return result, nil
}

func (e *Engine) buildOutputLocked(job *Job, idx int, res subResult, dropObsolete bool) *SSTable {
	entries := append([]Entry(nil), res.entries...)
	var rdel []RangeTombstone
	if !dropObsolete {
		rdel = append([]RangeTombstone(nil), res.tombstones...)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return compareInternal(entries[i].InternalKey, entries[j].InternalKey) < 0
	})

	var size int64
	for _, en := range entries {
		size += entrySize(en)
	}
	for _, t := range rdel {
		size += tombstoneSize(t)
	}

	sub := job.Subcompactions[idx]
	file := &SSTable{
		FileNumber:   e.allocFileNumberLocked(),
		Level:        job.OutputLevel,
		Entries:      entries,
		RangeDeletes: rdel,
		SizeBytes:    size,
	}
	file.Smallest = sub.Lo
	if len(entries) > 0 && entries[0].UserKey > file.Smallest {
		file.Smallest = entries[0].UserKey
	}
	if sub.Hi == "" {
		file.Largest = job.Range.Largest
	} else {
		file.Largest = prevKey(sub.Hi)
	}
	if len(entries) > 0 && entries[len(entries)-1].UserKey < file.Largest {
		file.Largest = entries[len(entries)-1].UserKey
	}
	if len(rdel) > 0 {
		for _, t := range rdel {
			if file.Smallest == "" || t.Start < file.Smallest {
				file.Smallest = t.Start
			}
			end := t.End
			if end == "" {
				end = UserKey("\xff\xff\xff\xff")
			}
			largest := prevKey(end)
			if file.Largest == "" || largest > file.Largest {
				file.Largest = largest
			}
		}
	}
	return file
}

func removeFiles(files []*SSTable, set map[uint64]struct{}) []*SSTable {
	out := files[:0]
	for _, f := range files {
		if _, drop := set[f.FileNumber]; !drop {
			out = append(out, f)
		}
	}
	return out
}

// validateLevelDisjoint enforces the L >= 1 invariant: files are key ordered
// and every adjacent pair is strictly non-overlapping.
func validateLevelDisjoint(files []*SSTable) error {
	for i := 1; i < len(files); i++ {
		prev := files[i-1]
		cur := files[i]
		if cur.Smallest <= prev.Largest {
			return &LevelInvariantError{
				Level: cur.Level,
				Left:  prev.FileNumber,
				Right: cur.FileNumber,
			}
		}
	}
	return nil
}

// LevelInvariantError reports a violation of the ordered/non-overlapping L>=1
// layout.
type LevelInvariantError struct {
	Level int
	Left  uint64
	Right uint64
}

func (err *LevelInvariantError) Error() string {
	return "compaction: level " + strconv.Itoa(err.Level) + " files " +
		strconv.FormatUint(err.Left, 10) + " and " + strconv.FormatUint(err.Right, 10) +
		" overlap or are out of order"
}

// CheckInvariants validates the strict ordering/non-overlap property for every
// level L >= 1 in the current version.
func (e *Engine) CheckInvariants() error {
	v := e.CurrentVersion()
	for level := 1; level < len(v.Levels); level++ {
		files := append([]*SSTable(nil), v.Levels[level]...)
		sortFiles(files)
		if err := validateLevelDisjoint(files); err != nil {
			return err
		}
	}
	return nil
}
