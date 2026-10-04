package manifestmgr

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
)

const (
	segmentsDir = "segments"
	logPath     = "MANIFEST.LOG"
	tmpPrefix   = ".tmp-"
	segPrefix   = "seg-"
)

// Manager is a reopenable, versioned manifest manager. The zero value is
// not usable; obtain one via Create or Open.
type Manager struct {
	fs Adapter

	mu         sync.Mutex
	current    *Record
	lastDigest string // chain digest of the last committed frame
	versions   []*Record

	nextSnapID int
	pins       map[int]uint64 // snapshot id -> pinned version
	byVersion  map[uint64]int // version -> pin count
}

// Create initializes a fresh store (layout + empty log) and opens it.
func Create(fs Adapter) (*Manager, error) {
	if err := fs.MkdirAll(segmentsDir); err != nil {
		return nil, err
	}
	if fs.Exists(logPath) {
		return nil, errors.New("store already exists")
	}
	if err := fs.WriteFile(logPath, nil); err != nil {
		return nil, err
	}
	if err := fs.SyncFile(logPath); err != nil {
		return nil, err
	}
	if err := fs.SyncDir("."); err != nil {
		return nil, err
	}
	return Open(fs)
}

// Open opens an existing store, replays and validates the manifest log,
// truncates a torn tail, and verifies that every segment referenced by the
// current version exists with a matching digest.
func Open(fs Adapter) (*Manager, error) {
	if !fs.Exists(logPath) {
		return nil, errors.New("not a manifest store: MANIFEST.LOG missing")
	}
	raw, err := fs.ReadFile(logPath)
	if err != nil {
		return nil, err
	}
	records, goodLen, torn, err := replayLog(raw)
	if err != nil {
		return nil, err
	}
	if torn {
		// Tolerate only an incomplete trailing record: rewrite the log to
		// the last good offset and sync it.
		if err := fs.WriteFile(logPath, raw[:goodLen]); err != nil {
			return nil, fmt.Errorf("truncating torn log tail: %w", err)
		}
		if err := fs.SyncFile(logPath); err != nil {
			return nil, err
		}
		if err := fs.SyncDir("."); err != nil {
			return nil, err
		}
		raw = raw[:goodLen]
	}
	if err := validateChain(records, raw); err != nil {
		return nil, err
	}
	m := &Manager{
		fs:        fs,
		versions:  records,
		pins:      map[int]uint64{},
		byVersion: map[uint64]int{},
	}
	if len(records) > 0 {
		m.current = records[len(records)-1]
		// Chain digest of the last frame, recomputed from raw bytes.
		m.lastDigest = chainDigestOfLastFrame(raw)
	}
	// Verify on-disk segments for the current version: existence + digest.
	if m.current != nil {
		for _, s := range m.current.Segments {
			if err := m.verifySegment(s); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

func chainDigestOfLastFrame(raw []byte) string {
	off := 0
	last := ""
	for off < len(raw) {
		n := int(binary32(raw[off:]))
		frame := raw[off : off+4+n+4]
		last = payloadDigest(frame)
		off += len(frame)
	}
	return last
}

func binary32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// Version returns the current committed version (0 = empty store).
func (m *Manager) Version() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return 0
	}
	return m.current.Version
}

// Current returns a copy of the current record (nil if version 0).
func (m *Manager) Current() *Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneRecord(m.current)
}

// Versions returns copies of all committed records in order.
func (m *Manager) Versions() []*Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Record, len(m.versions))
	for i, r := range m.versions {
		out[i] = cloneRecord(r)
	}
	return out
}

func cloneRecord(r *Record) *Record {
	if r == nil {
		return nil
	}
	cp := *r
	cp.Added = append([]Segment(nil), r.Added...)
	cp.Removed = append([]string(nil), r.Removed...)
	cp.Segments = append([]Segment(nil), r.Segments...)
	return &cp
}

// verifySegment checks that a segment file exists and its content hashes to
// the recorded digest.
func (m *Manager) verifySegment(s Segment) error {
	p := path.Join(segmentsDir, s.ID+".seg")
	data, err := m.fs.ReadFile(p)
	if err != nil {
		return fmt.Errorf("version %d: segment %s missing: %v", m.current.Version, s.ID, err)
	}
	if got := DigestOf(data); got != s.Digest {
		return fmt.Errorf("version %d: segment %s digest mismatch (recorded %s, actual %s)",
			m.current.Version, s.ID, s.Digest, got)
	}
	if int64(len(data)) != s.Size {
		return fmt.Errorf("version %d: segment %s size mismatch", m.current.Version, s.ID)
	}
	return nil
}

// Publish atomically commits a new version that adds the given blobs
// (name -> content) and removes the given segment IDs. The commit protocol:
//
//  1. write each new segment to a temp file
//  2. fsync the temp file
//  3. read it back and verify its digest (only verified data may be referenced)
//  4. rename temp -> final segment name
//  5. fsync the segments directory
//  6. append the commit record to the log
//  7. fsync the log          <-- commit point
//  8. fsync the root directory
//
// A crash before step 7 completes leaves the previous version current; a
// crash at or after it makes the whole new version visible. There is no
// intermediate visible state.
func (m *Manager) Publish(adds map[string][]byte, removes []string) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stage and verify new segments before they may be referenced.
	var newSegs []Segment
	names := make([]string, 0, len(adds))
	for name := range adds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		content := adds[name]
		id := SegmentID(content)
		seg := Segment{ID: id, Digest: DigestOf(content), Size: int64(len(content))}
		final := path.Join(segmentsDir, id+".seg")
		if m.fs.Exists(final) {
			// Content-addressed: identical content already stored.
			newSegs = append(newSegs, seg)
			continue
		}
		tmp := path.Join(segmentsDir, tmpPrefix+id+".seg")
		if err := m.fs.WriteFile(tmp, content); err != nil { // step 1
			return 0, fmt.Errorf("write segment: %w", err)
		}
		if err := m.fs.SyncFile(tmp); err != nil { // step 2
			return 0, fmt.Errorf("sync segment: %w", err)
		}
		back, err := m.fs.ReadFile(tmp) // step 3
		if err != nil {
			return 0, fmt.Errorf("readback segment: %w", err)
		}
		if DigestOf(back) != seg.Digest {
			return 0, fmt.Errorf("segment %s failed verification after write", id)
		}
		if err := m.fs.Rename(tmp, final); err != nil { // step 4
			return 0, fmt.Errorf("rename segment: %w", err)
		}
		newSegs = append(newSegs, seg)
	}
	if err := m.fs.SyncDir(segmentsDir); err != nil { // step 5
		return 0, fmt.Errorf("sync segments dir: %w", err)
	}

	rec, err := buildRecord(m.current, newSegs, removes, m.lastDigest)
	if err != nil {
		return 0, err
	}
	frame, err := encodeFrame(rec)
	if err != nil {
		return 0, err
	}
	if err := m.fs.AppendFile(logPath, frame); err != nil { // step 6
		return 0, fmt.Errorf("append commit record: %w", err)
	}
	if err := m.fs.SyncFile(logPath); err != nil { // step 7: commit point
		return 0, fmt.Errorf("sync commit log: %w", err)
	}
	if err := m.fs.SyncDir("."); err != nil { // step 8
		return 0, fmt.Errorf("sync root dir: %w", err)
	}

	m.current = rec
	m.lastDigest = payloadDigest(frame)
	m.versions = append(m.versions, rec)
	return rec.Version, nil
}

// ---------------------------------------------------------------------------
// Snapshots
// ---------------------------------------------------------------------------

// Snapshot is a pinned read view of one version. While a Snapshot is open,
// the garbage collector will not delete the segments it references.
type Snapshot struct {
	mgr      *Manager
	id       int
	Version  uint64
	Segments []Segment
}

// AcquireSnapshot pins the current version for reading.
func (m *Manager) AcquireSnapshot() *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSnapID++
	snap := &Snapshot{mgr: m, id: m.nextSnapID}
	if m.current != nil {
		snap.Version = m.current.Version
		snap.Segments = append([]Segment(nil), m.current.Segments...)
		m.pins[snap.id] = snap.Version
		m.byVersion[snap.Version]++
	}
	return snap
}

// Read returns the content of a segment in this snapshot, verifying its
// digest against the manifest.
func (s *Snapshot) Read(segID string) ([]byte, error) {
	var want *Segment
	for i := range s.Segments {
		if s.Segments[i].ID == segID {
			want = &s.Segments[i]
			break
		}
	}
	if want == nil {
		return nil, fmt.Errorf("segment %s not in snapshot version %d", segID, s.Version)
	}
	data, err := s.mgr.fs.ReadFile(path.Join(segmentsDir, segID+".seg"))
	if err != nil {
		return nil, err
	}
	if DigestOf(data) != want.Digest {
		return nil, fmt.Errorf("segment %s digest mismatch", segID)
	}
	return data, nil
}

// Close releases the snapshot's pin.
func (s *Snapshot) Close() {
	s.mgr.mu.Lock()
	defer s.mgr.mu.Unlock()
	if v, ok := s.mgr.pins[s.id]; ok {
		delete(s.mgr.pins, s.id)
		s.mgr.byVersion[v]--
		if s.mgr.byVersion[v] <= 0 {
			delete(s.mgr.byVersion, v)
		}
	}
}

// ---------------------------------------------------------------------------
// Garbage collection
// ---------------------------------------------------------------------------

// GCReport describes what the collector did and found.
type GCReport struct {
	Deleted []string // segment/temp files removed
	Kept    []string // segment files still referenced
	Orphans []string // unknown files: identified, never touched, never treated as segments
}

// GC removes segment files not referenced by the current version or any
// pinned snapshot, plus leftover temp files. Unknown files in the segments
// directory are reported as orphans but are never deleted and never
// mistaken for valid segments.
func (m *Manager) GC() (*GCReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	live := map[string]bool{}
	addVersion := func(v *Record) {
		if v == nil {
			return
		}
		for _, s := range v.Segments {
			live[s.ID] = true
		}
	}
	addVersion(m.current)
	for ver := range m.byVersion {
		idx := int(ver) - 1
		if idx >= 0 && idx < len(m.versions) {
			addVersion(m.versions[idx])
		}
	}

	entries, err := m.fs.ListDir(segmentsDir)
	if err != nil {
		return nil, err
	}
	rep := &GCReport{}
	for _, name := range entries {
		p := path.Join(segmentsDir, name)
		switch {
		case strings.HasPrefix(name, tmpPrefix):
			// Leftover from an interrupted publish; never referenced.
			if err := m.fs.Remove(p); err != nil {
				return rep, fmt.Errorf("remove temp %s: %w", name, err)
			}
			rep.Deleted = append(rep.Deleted, name)
		case strings.HasPrefix(name, segPrefix) && strings.HasSuffix(name, ".seg"):
			id := strings.TrimSuffix(name, ".seg")
			if live[id] {
				rep.Kept = append(rep.Kept, name)
				continue
			}
			if err := m.fs.Remove(p); err != nil {
				return rep, fmt.Errorf("remove segment %s: %w", name, err)
			}
			rep.Deleted = append(rep.Deleted, name)
		default:
			// Unknown orphan: identify it, but do not delete it and do not
			// treat it as a segment.
			rep.Orphans = append(rep.Orphans, name)
		}
	}
	if err := m.fs.SyncDir(segmentsDir); err != nil {
		return rep, err
	}
	sort.Strings(rep.Deleted)
	sort.Strings(rep.Kept)
	sort.Strings(rep.Orphans)
	return rep, nil
}
