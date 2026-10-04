package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Segment is an immutable incremental snapshot: the per-key changes for log
// indexes (Base, Last]. Segments chain: the first segment after a checkpoint
// has Base == checkpoint.LastIndex.
type Segment struct {
	Base     uint64
	Last     uint64
	LastTerm uint64
	Changes  map[string]Change
}

// SegmentMeta describes a stored segment without its payload.
type SegmentMeta struct {
	Base  uint64
	Last  uint64
	Empty bool // true when a capture found no changes (nothing archived)
}

// Checkpoint is an immutable full snapshot of the state machine.
type Checkpoint struct {
	LastIndex uint64
	LastTerm  uint64
	Data      map[string]string
}

var (
	// ErrExists is returned when writing over an immutable object.
	ErrExists = errors.New("snapshot: object already exists")
	// ErrNotFound is returned when a segment is missing.
	ErrNotFound = errors.New("snapshot: segment not found")
	// ErrChecksum is returned when a stored object fails integrity check.
	ErrChecksum = errors.New("snapshot: checksum mismatch")
	// ErrGap is returned when the segment chain has a hole.
	ErrGap = errors.New("snapshot: segment chain gap")
)

// Archive stores immutable segments and checkpoints.
type Archive interface {
	PutSegment(seg Segment) error
	GetSegment(base uint64) (Segment, error)
	Segments() ([]SegmentMeta, error)
	DeleteSegmentsBefore(index uint64) error // drops segments with Last <= index
	PutCheckpoint(cp Checkpoint) error
	LatestCheckpoint() (Checkpoint, bool, error)
}

// --- integrity envelope ---

type envelope struct {
	Sum  uint32 `json:"sum"`
	Data []byte `json:"data"`
}

func marshalChecked(v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Sum: crc32.ChecksumIEEE(payload), Data: payload})
}

func unmarshalChecked(b []byte, v any) error {
	var e envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return err
	}
	if crc32.ChecksumIEEE(e.Data) != e.Sum {
		return ErrChecksum
	}
	return json.Unmarshal(e.Data, v)
}

// --- in-memory archive ---

// MemArchive is an in-memory Archive, useful for tests and embedded use.
type MemArchive struct {
	mu         sync.Mutex
	segments   map[uint64]Segment // keyed by Base
	checkpoint *Checkpoint
}

func NewMemArchive() *MemArchive {
	return &MemArchive{segments: make(map[uint64]Segment)}
}

func (a *MemArchive) PutSegment(seg Segment) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.segments[seg.Base]; ok {
		return fmt.Errorf("%w: segment base %d", ErrExists, seg.Base)
	}
	a.segments[seg.Base] = seg
	return nil
}

func (a *MemArchive) GetSegment(base uint64) (Segment, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seg, ok := a.segments[base]
	if !ok {
		return Segment{}, fmt.Errorf("%w: base %d", ErrNotFound, base)
	}
	return seg, nil
}

func (a *MemArchive) Segments() ([]SegmentMeta, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]SegmentMeta, 0, len(a.segments))
	for _, s := range a.segments {
		out = append(out, SegmentMeta{Base: s.Base, Last: s.Last})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Base < out[j].Base })
	return out, nil
}

func (a *MemArchive) DeleteSegmentsBefore(index uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for base, s := range a.segments {
		if s.Last <= index {
			delete(a.segments, base)
		}
	}
	return nil
}

func (a *MemArchive) PutCheckpoint(cp Checkpoint) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.checkpoint != nil && cp.LastIndex <= a.checkpoint.LastIndex {
		return fmt.Errorf("%w: checkpoint %d not newer than %d", ErrExists, cp.LastIndex, a.checkpoint.LastIndex)
	}
	a.checkpoint = &cp
	return nil
}

func (a *MemArchive) LatestCheckpoint() (Checkpoint, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.checkpoint == nil {
		return Checkpoint{}, false, nil
	}
	return *a.checkpoint, true, nil
}

// --- file archive ---

// FileArchive persists segments and checkpoints as checksummed files in a
// directory. Writes are atomic (tmp file + rename) and every object is
// verified against a CRC32 on read.
type FileArchive struct {
	mu  sync.Mutex
	dir string
}

func NewFileArchive(dir string) (*FileArchive, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileArchive{dir: dir}, nil
}

// Dir returns the archive directory.
func (a *FileArchive) Dir() string { return a.dir }

func segmentName(base, last uint64) string { return fmt.Sprintf("seg-%020d-%020d.snap", base, last) }
func checkpointName(index uint64) string   { return fmt.Sprintf("ckpt-%020d.ckpt", index) }

func (a *FileArchive) writeAtomic(name string, v any) error {
	b, err := marshalChecked(v)
	if err != nil {
		return err
	}
	final := filepath.Join(a.dir, name)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func (a *FileArchive) readChecked(name string, v any) error {
	b, err := os.ReadFile(filepath.Join(a.dir, name))
	if err != nil {
		return err
	}
	return unmarshalChecked(b, v)
}

func (a *FileArchive) PutSegment(seg Segment) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	name := segmentName(seg.Base, seg.Last)
	if _, err := os.Stat(filepath.Join(a.dir, name)); err == nil {
		return fmt.Errorf("%w: segment base %d", ErrExists, seg.Base)
	}
	return a.writeAtomic(name, seg)
}

func (a *FileArchive) GetSegment(base uint64) (Segment, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	matches, err := filepath.Glob(filepath.Join(a.dir, fmt.Sprintf("seg-%020d-*.snap", base)))
	if err != nil || len(matches) == 0 {
		return Segment{}, fmt.Errorf("%w: base %d", ErrNotFound, base)
	}
	var seg Segment
	if err := a.readChecked(filepath.Base(matches[0]), &seg); err != nil {
		return Segment{}, err
	}
	return seg, nil
}

func (a *FileArchive) Segments() ([]SegmentMeta, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	matches, err := filepath.Glob(filepath.Join(a.dir, "seg-*.snap"))
	if err != nil {
		return nil, err
	}
	out := make([]SegmentMeta, 0, len(matches))
	for _, m := range matches {
		var base, last uint64
		if _, err := fmt.Sscanf(filepath.Base(m), "seg-%020d-%020d.snap", &base, &last); err != nil {
			return nil, fmt.Errorf("snapshot: bad segment file %q: %v", m, err)
		}
		out = append(out, SegmentMeta{Base: base, Last: last})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Base < out[j].Base })
	return out, nil
}

func (a *FileArchive) DeleteSegmentsBefore(index uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	metas, err := a.segmentsLocked()
	if err != nil {
		return err
	}
	for _, m := range metas {
		if m.Last <= index {
			if err := os.Remove(filepath.Join(a.dir, segmentName(m.Base, m.Last))); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *FileArchive) segmentsLocked() ([]SegmentMeta, error) {
	matches, err := filepath.Glob(filepath.Join(a.dir, "seg-*.snap"))
	if err != nil {
		return nil, err
	}
	out := make([]SegmentMeta, 0, len(matches))
	for _, m := range matches {
		var base, last uint64
		if _, err := fmt.Sscanf(filepath.Base(m), "seg-%020d-%020d.snap", &base, &last); err != nil {
			return nil, err
		}
		out = append(out, SegmentMeta{Base: base, Last: last})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Base < out[j].Base })
	return out, nil
}

func (a *FileArchive) PutCheckpoint(cp Checkpoint) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	name := checkpointName(cp.LastIndex)
	if _, err := os.Stat(filepath.Join(a.dir, name)); err == nil {
		return fmt.Errorf("%w: checkpoint %d", ErrExists, cp.LastIndex)
	}
	return a.writeAtomic(name, cp)
}

func (a *FileArchive) LatestCheckpoint() (Checkpoint, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	matches, err := filepath.Glob(filepath.Join(a.dir, "ckpt-*.ckpt"))
	if err != nil || len(matches) == 0 {
		return Checkpoint{}, false, err
	}
	sort.Strings(matches)
	var cp Checkpoint
	if err := a.readChecked(filepath.Base(matches[len(matches)-1]), &cp); err != nil {
		return Checkpoint{}, false, err
	}
	return cp, true, nil
}
