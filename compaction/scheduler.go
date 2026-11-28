package compaction

import (
	"sort"
)

// JobStatus labels the lifecycle stage of a compaction job.
type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusInstalled JobStatus = "installed"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
)

// Job is one compaction execution. It owns locks on every file in Inputs for
// its whole lifetime so no other job can pick the same files.
type Job struct {
	ID          uint64
	Level       int
	OutputLevel int
	Inputs      []*SSTable
	Range       KeyRange
	TotalBytes  int64

	// Spans are the half-open subcompaction key intervals, computed from the
	// input/output boundary keys. A single span means no split was needed.
	Spans []KeyRange
	// Subcompactions mirrors Spans with the clipped range tombstones.
	Subcompactions []*Subcompaction

	Status  JobStatus
	Outputs []*SSTable

	// DroppedEntries counts versions physically removed (bottommost, no
	// snapshot). Retained versions never increment it.
	DroppedEntries int
}

// LevelScore is the amplification urgency for one level.
type LevelScore struct {
	Level           int
	Score           float64
	LevelBytes      int64
	CompactingBytes int64
	TargetBytes     int64
}

// LevelScores computes the urgency of every compactable level.
//
// L0 score is the file count divided by l0Trigger. For L >= 1 the bytes
// already locked by an active compaction are subtracted from the level size
// before dividing by the level's target byte budget.
func (e *Engine) LevelScores() []LevelScore {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.levelScoresLocked()
}

func (e *Engine) levelScoresLocked() []LevelScore {
	// All levels are scored, including the bottommost one: bottommost
	// compaction is the only place obsolete versions and range tombstones can
	// be physically reclaimed.
	scores := make([]LevelScore, 0, e.cfg.NumLevels)
	for level := 0; level < e.cfg.NumLevels; level++ {
		files := e.version.Levels[level]
		var levelBytes int64
		for _, f := range files {
			levelBytes += f.SizeBytes
		}
		compacting := e.beingCompactedBytes[level]

		var score float64
		var target int64
		if level == 0 {
			target = int64(e.cfg.L0Trigger)
			score = float64(len(files)) / float64(e.cfg.L0Trigger)
		} else {
			target = e.cfg.LevelTargetBytes[level]
			if target <= 0 {
				continue
			}
			effective := levelBytes - compacting
			if effective < 0 {
				effective = 0
			}
			score = float64(effective) / float64(target)
		}
		// The bottommost level never moves data downward. It only needs a
		// compaction when it carries reclaimable garbage (duplicate versions,
		// deletes or range tombstones); otherwise scoring it would make the
		// scheduler pick it forever on an unchanged layout.
		if level == e.cfg.NumLevels-1 && !e.levelHasReclaimableLocked(level) {
			score = 0
		}
		scores = append(scores, LevelScore{
			Level:           level,
			Score:           score,
			LevelBytes:      levelBytes,
			CompactingBytes: compacting,
			TargetBytes:     target,
		})
	}
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].Score != scores[j].Score {
			return scores[i].Score > scores[j].Score
		}
		return scores[i].Level < scores[j].Level
	})
	return scores
}

// PickCompaction selects the highest-scoring conflict-free compaction and
// atomically locks its input files. Conflicting candidates are skipped in
// favor of the next eligible one. ErrNoCompaction is returned when nothing
// can be scheduled.
func (e *Engine) PickCompaction() (*Job, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	scores := e.levelScoresLocked()
	for _, ls := range scores {
		if ls.Score < 1.0 {
			continue
		}
		level := ls.Level
		if level == 0 {
			if job := e.pickL0Locked(); job != nil {
				return job, nil
			}
			continue
		}
		if job := e.pickLevelLocked(level); job != nil {
			return job, nil
		}
	}
	return nil, ErrNoCompaction
}

// pickL0Locked walks L0 seeds in key order. For each seed it pulls in the
// transitive overlap closure of L0 files, then every overlapping L1 file.
// The first closure that is conflict-free wins.
func (e *Engine) pickL0Locked() *Job {
	l0 := append([]*SSTable(nil), e.version.Levels[0]...)
	sortFiles(l0)
	seenClosures := map[uint64]struct{}{}

	for _, seed := range l0 {
		if _, locked := e.lockedFiles[seed.FileNumber]; locked {
			continue
		}
		closure := map[uint64]*SSTable{seed.FileNumber: seed}
		// Transitive closure: adding a file can extend the range and overlap
		// yet more L0 files.
		for changed := true; changed; {
			changed = false
			r := fileRange(mapFiles(closure))
			for _, f := range l0 {
				if _, ok := closure[f.FileNumber]; ok {
					continue
				}
				if f.Overlaps(r) {
					closure[f.FileNumber] = f
					changed = true
				}
			}
		}

		inputs := mapFiles(closure)
		r := fileRange(inputs)
		for _, f := range e.version.Levels[1] {
			if f.Overlaps(r) {
				inputs = append(inputs, f)
			}
		}

		key := closureKey(inputs)
		if _, dup := seenClosures[key]; dup {
			continue
		}
		seenClosures[key] = struct{}{}

		if e.candidateConflictsLocked(0, r, inputs) {
			continue
		}
		return e.lockJobLocked(0, 1, inputs)
	}
	return nil
}

// pickLevelLocked implements L >= 1 picking. Each file of the level is tried
// as a seed; overlapping L+1 files are pulled in and conflict-free same-level
// neighbors are expanded while the optional byte budget allows.
func (e *Engine) pickLevelLocked(level int) *Job {
	seedFiles := append([]*SSTable(nil), e.version.Levels[level]...)
	sortFiles(seedFiles)
	nextLevel := level + 1

	for _, seed := range seedFiles {
		if _, locked := e.lockedFiles[seed.FileNumber]; locked {
			continue
		}
		chosen := map[uint64]*SSTable{seed.FileNumber: seed}

		// Expand the immediate same-level neighbors on both sides, then
		// re-pull next-level overlaps until the set stabilizes. A locked
		// neighbor blocks expansion in that direction instead of being
		// force-included.
		for changed := true; changed; {
			changed = false
			r := fileRange(mapFiles(chosen))
			var pred, succ *SSTable
			for _, f := range seedFiles {
				if _, ok := chosen[f.FileNumber]; ok {
					continue
				}
				if _, locked := e.lockedFiles[f.FileNumber]; locked {
					continue
				}
				switch {
				case f.Largest < r.Smallest:
					if pred == nil || f.Smallest > pred.Smallest {
						pred = f
					}
				case f.Smallest > r.Largest:
					if succ == nil || f.Smallest < succ.Smallest {
						succ = f
					}
				}
			}
			for _, f := range []*SSTable{pred, succ} {
				if f == nil {
					continue
				}
				if e.cfg.MaxCompactionBytes > 0 {
					var total int64
					for _, g := range chosen {
						total += g.SizeBytes
					}
					if total+f.SizeBytes > e.cfg.MaxCompactionBytes {
						continue
					}
				}
				chosen[f.FileNumber] = f
				changed = true
			}
		}

		inputs := mapFiles(chosen)
		r := fileRange(inputs)
		if nextLevel < e.cfg.NumLevels {
			for _, f := range e.version.Levels[nextLevel] {
				if f.Overlaps(r) {
					inputs = append(inputs, f)
				}
			}
		}

		if e.candidateConflictsLocked(level, r, inputs) {
			continue
		}
		return e.lockJobLocked(level, minInt(nextLevel, e.cfg.NumLevels-1), inputs)
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// candidateConflictsLocked enforces the two hard rules: no input file may be
// locked by another job, and an L0->L1 job may not intersect the key range of
// any in-flight L0->L1 job.
func (e *Engine) candidateConflictsLocked(level int, r KeyRange, inputs []*SSTable) bool {
	for _, f := range inputs {
		if owner, locked := e.lockedFiles[f.FileNumber]; locked {
			_ = owner
			return true
		}
	}
	if level == 0 {
		for _, active := range e.l0Ranges {
			if active.r.Overlaps(r) {
				return true
			}
		}
	}
	return false
}

// lockJobLocked reserves all input files and registers the job.
func (e *Engine) lockJobLocked(level, outputLevel int, inputs []*SSTable) *Job {
	e.nextFileNumber++
	job := &Job{
		ID:          e.nextFileNumber,
		Level:       level,
		OutputLevel: outputLevel,
		Inputs:      append([]*SSTable(nil), inputs...),
		Range:       fileRange(inputs),
		Status:      StatusPending,
	}
	for _, f := range inputs {
		job.TotalBytes += f.SizeBytes
		e.lockedFiles[f.FileNumber] = job.ID
		e.beingCompactedBytes[f.Level] += f.SizeBytes
	}
	if level == 0 {
		e.l0Ranges = append(e.l0Ranges, activeL0{jobID: job.ID, r: job.Range})
	}
	e.jobs[job.ID] = job
	return job
}

// releaseJobLocked undoes lockJobLocked after failure/cancellation.
func (e *Engine) releaseJobLocked(job *Job) {
	for _, f := range job.Inputs {
		if owner, ok := e.lockedFiles[f.FileNumber]; ok && owner == job.ID {
			delete(e.lockedFiles, f.FileNumber)
			e.beingCompactedBytes[f.Level] -= f.SizeBytes
			if e.beingCompactedBytes[f.Level] <= 0 {
				delete(e.beingCompactedBytes, f.Level)
			}
		}
	}
	if job.Level == 0 {
		filtered := e.l0Ranges[:0]
		for _, a := range e.l0Ranges {
			if a.jobID != job.ID {
				filtered = append(filtered, a)
			}
		}
		e.l0Ranges = filtered
	}
	delete(e.jobs, job.ID)
}

// Job looks up an active or recently created job by id.
func (e *Engine) Job(id uint64) (*Job, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, ok := e.jobs[id]
	return job, ok
}

// levelHasReclaimableLocked reports whether a bottommost level contains
// anything a bottommost compaction could physically remove: duplicate
// versions of a user key, a delete marker or a range tombstone.
func (e *Engine) levelHasReclaimableLocked(level int) bool {
	for _, f := range e.version.Levels[level] {
		if len(f.RangeDeletes) > 0 {
			return true
		}
		prevKey := UserKey("")
		for _, en := range f.Entries {
			if en.UserKey == prevKey {
				return true // multiple versions of the same key
			}
			if en.Kind == KindDelete {
				return true
			}
			prevKey = en.UserKey
		}
	}
	return false
}

func mapFiles(m map[uint64]*SSTable) []*SSTable {
	out := make([]*SSTable, 0, len(m))
	for _, f := range m {
		out = append(out, f)
	}
	sortFiles(out)
	return out
}

func closureKey(files []*SSTable) uint64 {
	// Deterministic identity for a file set: xor of file numbers is stable
	// regardless of ordering; combined with sorting it disambiguates runs.
	var h uint64 = 1469598103934665603
	ids := make([]uint64, 0, len(files))
	for _, f := range files {
		ids = append(ids, f.FileNumber)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		h ^= id
		h *= 1099511628211
	}
	return h
}
