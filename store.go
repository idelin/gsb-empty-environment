package segvm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Change adds (or replaces) one named segment in a Publish call.
type Change struct {
	Name string
	Data []byte
}

// Manager owns one open view of a versioned segment store.
type Manager struct {
	fs       FS
	dir      string
	version  uint64
	prevHash string
	state    map[string]SegmentMeta // live set of the current version
	records  []*Record
	snaps    map[*Snapshot]bool
}

// Open opens (creating if necessary) the store at dir and fully validates
// it: version chain, hash chain, record CRCs, and — for the current
// version's live set — segment existence, size and content digest.
// An incomplete trailing log record is truncated; a complete corrupt
// record is a hard error.
func Open(fs FS, dir string) (*Manager, error) {
	m := &Manager{
		fs:       fs,
		dir:      dir,
		prevHash: genesisHash,
		state:    map[string]SegmentMeta{},
		snaps:    map[*Snapshot]bool{},
	}
	if err := fs.MkdirAll(m.segDir()); err != nil {
		return nil, err
	}
	data, err := readAll(fs, m.logPath())
	if err != nil {
		return nil, err
	}
	recs, validLen, err := scanLog(data)
	if err != nil {
		return nil, err
	}
	if validLen < len(data) {
		if err := m.truncateLog(int64(validLen)); err != nil {
			return nil, fmt.Errorf("segvm: repairing incomplete log tail: %v", err)
		}
	}
	for _, rec := range recs {
		m.apply(rec)
	}
	m.records = recs
	if n := len(recs); n > 0 {
		m.version = recs[n-1].Version
		m.prevHash = recordHash(recs[n-1])
	}
	// Content verification covers the current version's live set. Older
	// versions are validated structurally only, because GC may already
	// have reclaimed segments they alone referenced.
	names := make([]string, 0, len(m.state))
	for name := range m.state {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := m.verifySegment(m.state[name]); err != nil {
			return nil, fmt.Errorf("segvm: segment %q: %v", name, err)
		}
	}
	return m, nil
}

func (m *Manager) logPath() string { return path.Join(m.dir, "manifest.log") }
func (m *Manager) segDir() string  { return path.Join(m.dir, "segments") }
func (m *Manager) segPath(digest string) string {
	return path.Join(m.segDir(), digest+".seg")
}

// Version returns the current version number (0 = nothing published yet).
func (m *Manager) Version() uint64 { return m.version }

// Versions returns a copy of all validated log records.
func (m *Manager) Versions() []Record {
	out := make([]Record, len(m.records))
	for i, r := range m.records {
		out[i] = *r
	}
	return out
}

// Segments returns the live segment set of the current version.
func (m *Manager) Segments() map[string]SegmentMeta {
	out := make(map[string]SegmentMeta, len(m.state))
	for k, v := range m.state {
		out[k] = v
	}
	return out
}

// StateAt reconstructs the segment set after the given version
// (version 0 = empty). It errors if version > current.
func (m *Manager) StateAt(version uint64) (map[string]SegmentMeta, error) {
	if version > m.version {
		return nil, fmt.Errorf("segvm: version %d does not exist (current %d)", version, m.version)
	}
	state := map[string]SegmentMeta{}
	for _, rec := range m.records[:version] {
		for _, a := range rec.Adds {
			state[a.Name] = a
		}
		for _, r := range rec.Removes {
			delete(state, r)
		}
	}
	return state, nil
}

func (m *Manager) apply(rec *Record) {
	for _, a := range rec.Adds {
		m.state[a.Name] = a
	}
	for _, r := range rec.Removes {
		delete(m.state, r)
	}
}

// Publish atomically publishes a new version with the given added/replaced
// segments and removed names.
//
// Step order (each is a potential process-exit point; see README):
//
//	per new segment: create tmp -> write -> fsync -> close -> verify
//	                 (re-read + re-hash) -> rename into place -> fsync dir
//	commit:          append record -> fsync log   <-- commit point
//	                 -> fsync root dir
//
// Only after every referenced segment file is fully written AND verified
// is the record that references it appended. Before the log fsync the old
// version remains the visible one; after it, the new version is complete.
func (m *Manager) Publish(adds []Change, removes []string) (uint64, error) {
	if err := m.validate(adds, removes); err != nil {
		return 0, err
	}
	metas := make([]SegmentMeta, 0, len(adds))
	for _, a := range adds {
		sum := sha256.Sum256(a.Data)
		metas = append(metas, SegmentMeta{
			Name:   a.Name,
			Digest: hex.EncodeToString(sum[:]),
			Size:   int64(len(a.Data)),
		})
	}
	// Stage segment files first. A crash here only leaves reclaimable
	// tmp files / unreferenced segments; the manifest is untouched.
	for i, a := range adds {
		if cur, ok := m.state[a.Name]; ok && cur.Digest == metas[i].Digest {
			continue // identical content already referenced
		}
		if err := m.stageSegment(metas[i].Digest, a.Data); err != nil {
			return 0, err
		}
	}
	// Commit point: append + fsync the record.
	rec := &Record{Version: m.version + 1, Prev: m.prevHash, Adds: metas, Removes: removes}
	f, err := m.fs.OpenAppend(m.logPath())
	if err != nil {
		return 0, err
	}
	if _, err := f.Write(encodeRecord(rec)); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Sync(); err != nil { // <-- after this returns, the new version is durable
		f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	if err := m.fs.SyncDir(m.dir); err != nil {
		return 0, err
	}
	// In-memory state changes only after the durable commit.
	m.apply(rec)
	m.records = append(m.records, rec)
	m.version = rec.Version
	m.prevHash = recordHash(rec)
	return m.version, nil
}

func (m *Manager) validate(adds []Change, removes []string) error {
	seen := map[string]bool{}
	for _, a := range adds {
		if a.Name == "" || strings.ContainsAny(a.Name, "/\\") || strings.HasPrefix(a.Name, ".") {
			return fmt.Errorf("segvm: invalid segment name %q", a.Name)
		}
		if seen[a.Name] {
			return fmt.Errorf("segvm: duplicate segment name %q in publish", a.Name)
		}
		seen[a.Name] = true
	}
	for _, r := range removes {
		if seen[r] {
			return fmt.Errorf("segvm: segment %q both added and removed", r)
		}
		if _, ok := m.state[r]; !ok {
			return fmt.Errorf("segvm: cannot remove unknown segment %q", r)
		}
	}
	return nil
}

// stageSegment writes, fsyncs, verifies and renames one segment file.
// It is a no-op if an intact file for this digest already exists
// (content addressing deduplicates shared segments).
func (m *Manager) stageSegment(digest string, data []byte) error {
	if m.segmentValid(digest) {
		return nil
	}
	tmp := path.Join(m.segDir(), fmt.Sprintf("%s.tmp-%d", digest, time.Now().UnixNano()))
	f, err := m.fs.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Verify the staged bytes before anything may reference them.
	if err := m.verifyFile(tmp, digest, int64(len(data))); err != nil {
		m.fs.Remove(tmp)
		return fmt.Errorf("segvm: staged segment failed verification: %v", err)
	}
	if m.segmentValid(digest) { // appeared meanwhile; identical content
		m.fs.Remove(tmp)
		return nil
	}
	if err := m.fs.Rename(tmp, m.segPath(digest)); err != nil {
		return err
	}
	return m.fs.SyncDir(m.segDir())
}

// segmentValid reports whether the final segment file for digest exists
// and hashes to digest.
func (m *Manager) segmentValid(digest string) bool {
	data, err := readAll(m.fs, m.segPath(digest))
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == digest
}

func (m *Manager) verifyFile(path, wantDigest string, wantSize int64) error {
	data, err := readAll(m.fs, path)
	if err != nil {
		return err
	}
	if int64(len(data)) != wantSize {
		return fmt.Errorf("size mismatch: got %d, want %d", len(data), wantSize)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantDigest {
		return fmt.Errorf("digest mismatch: got %s, want %s", got, wantDigest)
	}
	return nil
}

func (m *Manager) verifySegment(meta SegmentMeta) error {
	return m.verifyFile(m.segPath(meta.Digest), meta.Digest, meta.Size)
}

func (m *Manager) truncateLog(size int64) error {
	f, err := m.fs.OpenAppend(m.logPath())
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Read returns the content of a segment of the current version, verified
// against its recorded digest.
func (m *Manager) Read(name string) ([]byte, error) {
	meta, ok := m.state[name]
	if !ok {
		return nil, fmt.Errorf("segvm: segment %q not in version %d", name, m.version)
	}
	return m.readSegment(meta)
}

func (m *Manager) readSegment(meta SegmentMeta) ([]byte, error) {
	data, err := readAll(m.fs, m.segPath(meta.Digest))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != meta.Digest || int64(len(data)) != meta.Size {
		return nil, fmt.Errorf("segvm: segment %q content does not match manifest digest", meta.Name)
	}
	return data, nil
}

// Snapshot is a stable read view of one version. While a Snapshot is open
// the reclaimer will not delete any segment file it references.
type Snapshot struct {
	m        *Manager
	Version  uint64
	Segments map[string]SegmentMeta
	closed   bool
}

// Snapshot pins the current version for reading.
func (m *Manager) Snapshot() *Snapshot {
	s := &Snapshot{m: m, Version: m.version, Segments: m.Segments()}
	m.snaps[s] = true
	return s
}

// Read returns verified content of a segment in this snapshot's version.
func (s *Snapshot) Read(name string) ([]byte, error) {
	if s.closed {
		return nil, fmt.Errorf("segvm: snapshot is closed")
	}
	meta, ok := s.Segments[name]
	if !ok {
		return nil, fmt.Errorf("segvm: segment %q not in version %d", name, s.Version)
	}
	return s.m.readSegment(meta)
}

// Close releases the snapshot's pins.
func (s *Snapshot) Close() {
	if !s.closed {
		delete(s.m.snaps, s)
		s.closed = true
	}
}

// GCResult reports what the reclaimer did.
type GCResult struct {
	Removed []string // unreferenced segment files deleted
	Tmp     []string // stale tmp files deleted
	Orphans []string // unknown files: identified, never served, never deleted
}

var segNameRE = regexp.MustCompile(`^[0-9a-f]{64}\.seg$`)

// GC deletes segment files not referenced by the current version or any
// open snapshot, plus stale tmp files from interrupted publishes.
// Unknown files are reported as orphans but left untouched; they are never
// mistaken for valid segments (all segment reads are digest-verified).
func (m *Manager) GC() (*GCResult, error) {
	live := map[string]bool{}
	for _, meta := range m.state {
		live[meta.Digest] = true
	}
	for s := range m.snaps {
		for _, meta := range s.Segments {
			live[meta.Digest] = true
		}
	}
	entries, err := m.fs.ReadDir(m.segDir())
	if err != nil {
		return nil, err
	}
	res := &GCResult{}
	for _, name := range entries {
		p := path.Join(m.segDir(), name)
		switch {
		case segNameRE.MatchString(name):
			digest := strings.TrimSuffix(name, ".seg")
			if !live[digest] {
				if err := m.fs.Remove(p); err != nil {
					return res, err
				}
				res.Removed = append(res.Removed, name)
			}
		case strings.Contains(name, ".tmp-"):
			if err := m.fs.Remove(p); err != nil {
				return res, err
			}
			res.Tmp = append(res.Tmp, name)
		default:
			res.Orphans = append(res.Orphans, name)
		}
	}
	return res, nil
}
