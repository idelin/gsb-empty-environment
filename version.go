package lsm

import (
	"bytes"
	"fmt"
	"sort"
	"sync"
)

// Version is an immutable view of the level hierarchy. L0 may contain
// overlapping files; every level >= 1 must be sorted by user key and contain
// pairwise non-overlapping files.
type Version struct {
	Levels [][]*FileMeta
}

// NewVersion returns an empty version with numLevels levels.
func NewVersion(numLevels int) *Version {
	if numLevels < 1 {
		numLevels = 1
	}
	return &Version{Levels: make([][]*FileMeta, numLevels)}
}

// NewVersionFromFiles builds a version from caller-provided level slices. The
// slices are copied. Levels >= 1 are sorted canonically.
func NewVersionFromFiles(levels [][]*FileMeta) *Version {
	v := &Version{Levels: make([][]*FileMeta, len(levels))}
	for level := range levels {
		v.Levels[level] = append([]*FileMeta(nil), levels[level]...)
		if level >= 1 {
			sortLevel(v.Levels[level])
		}
	}
	return v
}

// Clone returns a deep copy of the version.
func (v *Version) Clone() *Version {
	if v == nil {
		return nil
	}
	out := &Version{Levels: make([][]*FileMeta, len(v.Levels))}
	for level, files := range v.Levels {
		out.Levels[level] = make([]*FileMeta, len(files))
		for i, f := range files {
			out.Levels[level][i] = f.Clone()
		}
	}
	return out
}

// NumLevels returns the number of configured levels.
func (v *Version) NumLevels() int {
	if v == nil {
		return 0
	}
	return len(v.Levels)
}

// Files returns a shallow copy of the file slice for level.
func (v *Version) Files(level int) []*FileMeta {
	if v == nil || level < 0 || level >= len(v.Levels) {
		return nil
	}
	return append([]*FileMeta(nil), v.Levels[level]...)
}

// LevelSize returns the sum of file sizes in a level.
func (v *Version) LevelSize(level int) uint64 {
	if v == nil || level < 0 || level >= len(v.Levels) {
		return 0
	}
	var n uint64
	for _, f := range v.Levels[level] {
		n += f.EffectiveSize()
	}
	return n
}

// OverlappingFiles returns all files in level that intersect r, in level
// order.
func (v *Version) OverlappingFiles(level int, r KeyRange) []*FileMeta {
	if v == nil || level < 0 || level >= len(v.Levels) {
		return nil
	}
	var out []*FileMeta
	for _, f := range v.Levels[level] {
		if f.OverlapsRange(r) {
			out = append(out, f)
		}
	}
	return out
}

// LevelHasOverlap reports whether any file in level intersects r.
func (v *Version) LevelHasOverlap(level int, r KeyRange) bool {
	return len(v.OverlappingFiles(level, r)) > 0
}

// ValidateLevel verifies ordering and non-overlap invariants.
func (v *Version) ValidateLevel(level int) error {
	if v == nil || level < 0 || level >= len(v.Levels) {
		return fmt.Errorf("lsm: invalid level %d", level)
	}
	files := v.Levels[level]
	if level == 0 {
		for _, f := range files {
			if err := f.Validate(); err != nil {
				return err
			}
		}
		return nil
	}
	if !sort.SliceIsSorted(files, func(i, j int) bool {
		return fileLess(files[i], files[j])
	}) {
		return fmt.Errorf("lsm: level %d files are not sorted", level)
	}
	for i, f := range files {
		if err := f.Validate(); err != nil {
			return err
		}
		if i == 0 {
			continue
		}
		prev := files[i-1]
		if fileRangesOverlap(prev, f) {
			return fmt.Errorf("lsm: level %d files %d and %d overlap", level, prev.FileID, f.FileID)
		}
	}
	return nil
}

// Validate verifies all levels.
func (v *Version) Validate() error {
	if v == nil {
		return fmt.Errorf("lsm: nil version")
	}
	for level := range v.Levels {
		if err := v.ValidateLevel(level); err != nil {
			return err
		}
	}
	return nil
}

// ApplyCompaction returns a new version with inputs removed and outputs
// inserted at outputLevel. The receiver is not modified. An error is returned
// instead of installing a version that violates L>=1 ordering.
func (v *Version) ApplyCompaction(inputs, outputs []*FileMeta, outputLevel int) (*Version, error) {
	if v == nil {
		return nil, fmt.Errorf("lsm: nil version")
	}
	if outputLevel < 0 || outputLevel >= len(v.Levels) {
		return nil, fmt.Errorf("lsm: output level %d out of range", outputLevel)
	}
	next := v.Clone()
	removeByID := make(map[uint64]bool, len(inputs))
	removeByPtr := make(map[*FileMeta]bool, len(inputs))
	var zeroInputs []*FileMeta
	for _, f := range inputs {
		if f == nil {
			return nil, fmt.Errorf("lsm: nil compaction input")
		}
		if f.FileID != 0 {
			removeByID[f.FileID] = true
		} else {
			removeByPtr[f] = true
			zeroInputs = append(zeroInputs, f)
		}
	}
	foundID := make(map[uint64]bool, len(inputs))
	foundPtr := make(map[*FileMeta]bool, len(inputs))
	matchedZero := make(map[*FileMeta]bool, len(zeroInputs))
	for _, input := range zeroInputs {
		level := input.Level
		if level < 0 || level >= len(next.Levels) {
			continue
		}
		for _, candidate := range next.Levels[level] {
			if candidate == nil || candidate.FileID != 0 || matchedZero[candidate] || removeByPtr[candidate] {
				continue
			}
			if filesStructurallyEqual(candidate, input) {
				matchedZero[candidate] = true
				foundPtr[input] = true
				break
			}
		}
	}
	for level := range next.Levels {
		dst := next.Levels[level][:0]
		for _, f := range next.Levels[level] {
			if (f.FileID != 0 && removeByID[f.FileID]) || removeByPtr[f] || matchedZero[f] {
				if f.FileID != 0 {
					foundID[f.FileID] = true
				} else {
					foundPtr[f] = true
				}
				continue
			}
			dst = append(dst, f)
		}
		next.Levels[level] = dst
	}
	for _, f := range inputs {
		if f.FileID != 0 {
			if !foundID[f.FileID] {
				return nil, fmt.Errorf("lsm: compaction input file %d is not in the version", f.FileID)
			}
		} else if !foundPtr[f] {
			return nil, fmt.Errorf("lsm: compaction input is not in the version")
		}
	}
	for _, f := range outputs {
		if f == nil {
			return nil, fmt.Errorf("lsm: nil compaction output")
		}
		if f.Level != outputLevel {
			f.Level = outputLevel
		}
		next.Levels[outputLevel] = append(next.Levels[outputLevel], f)
	}
	sortLevel(next.Levels[outputLevel])
	if err := next.Validate(); err != nil {
		return nil, err
	}
	return next, nil
}

// VersionSet owns the current version and the snapshot list. It serialises
// version installation so a successful compaction becomes visible atomically.
type VersionSet struct {
	mu         sync.RWMutex
	current    *Version
	nextFileID uint64
	snapshots  map[SeqNum]int
}

// NewVersionSet constructs a version set. An optional initial version is
// cloned; with no argument an empty one-level version is used.
func NewVersionSet(versions ...*Version) *VersionSet {
	var v *Version
	if len(versions) > 0 {
		v = versions[0]
	}
	if v == nil {
		v = NewVersion(1)
	}
	vs := &VersionSet{current: v.Clone(), snapshots: make(map[SeqNum]int)}
	var maxID uint64
	for _, files := range vs.current.Levels {
		for _, f := range files {
			if f.FileID > maxID {
				maxID = f.FileID
			}
		}
	}
	vs.nextFileID = maxID + 1
	return vs
}

// Current returns the current immutable version.
func (vs *VersionSet) Current() *Version {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.current
}

// NextFileID reserves and returns a fresh file identifier.
func (vs *VersionSet) NextFileID() uint64 {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	id := vs.nextFileID
	vs.nextFileID++
	return id
}

// InstallCompaction atomically applies a compaction and unlocks nothing; the
// scheduler owns lock release.
func (vs *VersionSet) InstallCompaction(inputs, outputs []*FileMeta, outputLevel int) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	next, err := vs.current.ApplyCompaction(inputs, outputs, outputLevel)
	if err != nil {
		return err
	}
	vs.current = next
	for _, f := range outputs {
		if f.FileID >= vs.nextFileID {
			vs.nextFileID = f.FileID + 1
		}
	}
	return nil
}

// SetVersion replaces the current version after validating it.
func (vs *VersionSet) SetVersion(v *Version) error {
	if v == nil {
		return fmt.Errorf("lsm: nil version")
	}
	if err := v.Validate(); err != nil {
		return err
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.current = v.Clone()
	return nil
}

// AddSnapshot anchors all versions visible at seq.
func (vs *VersionSet) AddSnapshot(seq SeqNum) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.snapshots == nil {
		vs.snapshots = make(map[SeqNum]int)
	}
	vs.snapshots[seq]++
}

// ReleaseSnapshot releases one anchor at seq.
func (vs *VersionSet) ReleaseSnapshot(seq SeqNum) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if n := vs.snapshots[seq]; n > 1 {
		vs.snapshots[seq] = n - 1
	} else {
		delete(vs.snapshots, seq)
	}
}

// Snapshots returns the active snapshot sequence numbers in ascending order.
func (vs *VersionSet) Snapshots() []SeqNum {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	out := make([]SeqNum, 0, len(vs.snapshots))
	for seq := range vs.snapshots {
		out = append(out, seq)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// OldestSnapshot returns the smallest active snapshot.
func (vs *VersionSet) OldestSnapshot() (SeqNum, bool) {
	snaps := vs.Snapshots()
	if len(snaps) == 0 {
		return 0, false
	}
	return snaps[0], true
}

func sortLevel(files []*FileMeta) {
	sort.SliceStable(files, func(i, j int) bool { return fileLess(files[i], files[j]) })
}

func fileLess(a, b *FileMeta) bool {
	if a == nil {
		return b != nil
	}
	if b == nil {
		return false
	}
	if c := bytes.Compare(a.Smallest.UserKey, b.Smallest.UserKey); c != 0 {
		return c < 0
	}
	if a.FileID != b.FileID {
		return a.FileID < b.FileID
	}
	return bytes.Compare(a.Largest.UserKey, b.Largest.UserKey) < 0
}

func fileRangesOverlap(a, b *FileMeta) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Bounds().Overlaps(b.Bounds())
}

func filesStructurallyEqual(a, b *FileMeta) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.FileID == b.FileID &&
		a.Level == b.Level &&
		a.Size == b.Size &&
		a.Smallest.Seq == b.Smallest.Seq &&
		a.Smallest.Kind == b.Smallest.Kind &&
		a.Largest.Seq == b.Largest.Seq &&
		a.Largest.Kind == b.Largest.Kind &&
		bytes.Equal(a.Smallest.UserKey, b.Smallest.UserKey) &&
		bytes.Equal(a.Largest.UserKey, b.Largest.UserKey) &&
		len(a.Entries) == len(b.Entries) &&
		len(a.RangeTombstones) == len(b.RangeTombstones)
}
