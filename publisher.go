// Package publisher implements atomic multi-file publishes with a
// write-ahead journal. A single Publish call updates several virtual files;
// readers always observe a consistent set of versions through a manifest
// that is swapped atomically, and crash recovery relies solely on the
// persisted journal to either roll the transaction back or complete it.
package publisher

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
)

const (
	journalName  = "journal"
	manifestName = "manifest"
	manifestTmp  = "manifest.tmp"
	filesDir     = "files"
)

// ErrNotFound is returned when a snapshot does not contain a file.
var ErrNotFound = errors.New("publisher: file not found")

// Manifest is the version list readers use to resolve a consistent set of
// file versions.
type Manifest struct {
	Seq   uint64            `json:"seq"`
	Files map[string]uint64 `json:"files"`
}

func (m Manifest) clone() Manifest {
	out := Manifest{Seq: m.Seq, Files: make(map[string]uint64, len(m.Files))}
	for name, ver := range m.Files {
		out.Files[name] = ver
	}
	return out
}

// journal record types.
const (
	recBegin  = "begin"
	recCommit = "commit"
	recDone   = "done"
)

// record is one journal entry. The begin record carries everything needed
// to redo the transaction: the full resulting manifest and the new file
// contents. Recovery therefore needs nothing but the journal.
type record struct {
	Type     string            `json:"type"`
	Seq      uint64            `json:"seq"`
	Manifest map[string]uint64 `json:"manifest,omitempty"`
	Files    map[string][]byte `json:"files,omitempty"`
}

// Publisher atomically publishes sets of virtual files into dir.
type Publisher struct {
	fs  FS
	dir string

	mu       sync.Mutex
	manifest Manifest
	pins     map[uint64]int // manifest seq -> open snapshot count
}

// Open opens (creating if necessary) the publisher rooted at dir and runs
// crash recovery from the persisted journal.
func Open(fs FS, dir string) (*Publisher, error) {
	if err := fs.MkdirAll(dir); err != nil {
		return nil, err
	}
	if err := fs.MkdirAll(path.Join(dir, filesDir)); err != nil {
		return nil, err
	}
	p := &Publisher{fs: fs, dir: dir, pins: make(map[uint64]int)}
	m, err := p.readManifest()
	if err != nil {
		return nil, err
	}
	p.manifest = m
	if err := p.recover(); err != nil {
		return nil, err
	}
	return p, nil
}

// Publish atomically applies updates: after recovery from any crash, either
// all of the updates are visible or none are.
func (p *Publisher) Publish(updates map[string][]byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	seq := p.manifest.Seq + 1
	next := p.manifest.clone()
	next.Seq = seq
	for name := range updates {
		next.Files[name] = seq
	}

	// 1. Journal the new contents and the resulting manifest.
	if err := p.appendRecord(record{Type: recBegin, Seq: seq, Manifest: next.Files, Files: updates}); err != nil {
		return err
	}
	// 2. Write and fsync every new version file, then the directory.
	if err := p.writeVersionFiles(seq, updates); err != nil {
		return err
	}
	// 3. Journal the commit decision. This record is the atomic switch
	//    recovery uses: present => complete, absent => roll back.
	if err := p.appendRecord(record{Type: recCommit, Seq: seq}); err != nil {
		return err
	}
	// 4. Swap the manifest atomically so readers see the new set at once.
	if err := p.writeManifest(next); err != nil {
		return err
	}
	// 5. Close out the transaction.
	if err := p.appendRecord(record{Type: recDone, Seq: seq}); err != nil {
		return err
	}

	old := p.manifest
	p.manifest = next
	p.gc(old)
	return nil
}

// Snapshot pins the current manifest so a reader sees one consistent set of
// versions, immune to concurrent publishes. Call Close when done.
func (p *Publisher) Snapshot() *Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := p.manifest.clone()
	p.pins[m.Seq]++
	return &Snapshot{pub: p, manifest: m}
}

// Manifest returns a copy of the current manifest.
func (p *Publisher) Manifest() Manifest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.manifest.clone()
}

// Snapshot is a pinned, consistent view of the published files.
type Snapshot struct {
	pub      *Publisher
	manifest Manifest
	closed   bool
}

// Seq returns the manifest sequence of this snapshot.
func (s *Snapshot) Seq() uint64 { return s.manifest.Seq }

// Files returns a copy of the name -> version map of this snapshot.
func (s *Snapshot) Files() map[string]uint64 {
	out := make(map[string]uint64, len(s.manifest.Files))
	for name, ver := range s.manifest.Files {
		out[name] = ver
	}
	return out
}

// Read returns the content of name as pinned by this snapshot.
func (s *Snapshot) Read(name string) ([]byte, error) {
	ver, ok := s.manifest.Files[name]
	if !ok {
		return nil, ErrNotFound
	}
	return s.pub.fs.ReadFile(versionPath(s.pub.dir, name, ver))
}

// Close releases the snapshot so its versions may be garbage collected.
func (s *Snapshot) Close() {
	if s.closed {
		return
	}
	s.closed = true
	s.pub.mu.Lock()
	defer s.pub.mu.Unlock()
	seq := s.manifest.Seq
	s.pub.pins[seq]--
	if s.pub.pins[seq] <= 0 {
		delete(s.pub.pins, seq)
	}
}

// --- journal ---

func (p *Publisher) appendRecord(r record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := p.fs.Append(path.Join(p.dir, journalName))
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
	return f.Close()
}

func (p *Publisher) readJournal() ([]record, error) {
	data, err := p.fs.ReadFile(path.Join(p.dir, journalName))
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []record
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			// A torn tail write is not a valid record; ignore it.
			break
		}
		records = append(records, r)
	}
	return records, nil
}

// recover inspects only the persisted journal and either completes or rolls
// back the last transaction.
func (p *Publisher) recover() error {
	records, err := p.readJournal()
	if err != nil {
		return err
	}
	var pending *record
	committed := false
	for i := range records {
		switch records[i].Type {
		case recBegin:
			r := records[i]
			pending = &r
			committed = false
		case recCommit:
			committed = true
		case recDone:
			pending = nil
			committed = false
		}
	}
	if pending == nil {
		return nil
	}
	if !committed {
		// Roll back: drop any partially written version files and the
		// staging manifest, then close the transaction in the journal.
		for name := range pending.Files {
			p.fs.Remove(versionPath(p.dir, name, pending.Seq))
		}
		p.fs.SyncDir(path.Join(p.dir, filesDir))
		p.fs.Remove(path.Join(p.dir, manifestTmp))
		p.fs.SyncDir(p.dir)
		return p.appendRecord(record{Type: recDone, Seq: pending.Seq})
	}
	// Complete: redo every step idempotently from the journaled contents.
	if err := p.writeVersionFiles(pending.Seq, pending.Files); err != nil {
		return err
	}
	m := Manifest{Seq: pending.Seq, Files: pending.Manifest}
	if err := p.writeManifest(m); err != nil {
		return err
	}
	if err := p.appendRecord(record{Type: recDone, Seq: pending.Seq}); err != nil {
		return err
	}
	p.manifest = m
	return nil
}

// --- storage helpers ---

func versionPath(dir, name string, seq uint64) string {
	return path.Join(dir, filesDir, fmt.Sprintf("%s.%d", name, seq))
}

func (p *Publisher) writeVersionFiles(seq uint64, updates map[string][]byte) error {
	names := make([]string, 0, len(updates))
	for name := range updates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := p.fs.Create(versionPath(p.dir, name, seq))
		if err != nil {
			return err
		}
		if _, err := f.Write(updates[name]); err != nil {
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
	}
	return p.fs.SyncDir(path.Join(p.dir, filesDir))
}

func (p *Publisher) writeManifest(m Manifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path.Join(p.dir, manifestTmp)
	f, err := p.fs.Create(tmp)
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
	if err := p.fs.Rename(tmp, path.Join(p.dir, manifestName)); err != nil {
		return err
	}
	return p.fs.SyncDir(p.dir)
}

func (p *Publisher) readManifest() (Manifest, error) {
	data, err := p.fs.ReadFile(path.Join(p.dir, manifestName))
	if err != nil {
		if isNotExist(err) {
			return Manifest{Files: map[string]uint64{}}, nil
		}
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	if m.Files == nil {
		m.Files = map[string]uint64{}
	}
	return m, nil
}

// gc removes version files superseded by the current manifest and not
// pinned by any open snapshot.
func (p *Publisher) gc(old Manifest) {
	minPinned := p.manifest.Seq
	for seq := range p.pins {
		if seq < minPinned {
			minPinned = seq
		}
	}
	for name, ver := range old.Files {
		if p.manifest.Files[name] == ver {
			continue
		}
		if ver >= minPinned {
			continue
		}
		p.fs.Remove(versionPath(p.dir, name, ver))
	}
}

func isNotExist(err error) bool {
	return err == errNotExist || strings.Contains(err.Error(), "no such file")
}

var errNotExist = errors.New("publisher: not exist")
