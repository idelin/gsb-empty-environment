// Package publisher implements atomic multi-file publication on top of a
// small filesystem abstraction. A single transaction updates any number of
// virtual files; a write-ahead journal records the new contents and the
// commit decision; recovery consults only the durable journal to either roll
// the transaction back completely or drive it to completion. Readers use the
// versioned manifest and therefore always observe one consistent set of
// files, never a per-file mixture of versions.
package publisher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

const (
	journalPath  = "journal"
	manifestPath = "manifest.json"
	manifestTmp  = "manifest.tmp"
	dataDir      = "data"
)

// Update sets the contents of one virtual file in a transaction.
type Update struct {
	Name string
	Data []byte
}

// Publisher serializes writers; readers (Snapshot) never block on it.
type Publisher struct {
	fs FS
	mu sync.Mutex
}

func New(fs FS) *Publisher { return &Publisher{fs: fs} }

// blobPath is the immutable, version-tagged storage path of one virtual file.
func blobPath(name string, txn uint64) string {
	return dataDir + "/" + name + "@" + strconv.FormatUint(txn, 10)
}

func validName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "/@\x00")
}

// Publish atomically replaces the contents of all given virtual files.
//
// Protocol:
//  1. journal begin + one put record per file (carrying the new content), fsync
//  2. journal commit record, fsync          <-- atomic commit point
//  3. apply: write versioned blobs, fsync, then swap the manifest by rename
//  4. checkpoint: reset the journal, fsync; garbage-collect old blobs
//
// A crash before the commit record is durable rolls everything back; a crash
// after it is driven to completion by Recover.
func (p *Publisher) Publish(updates []Update) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, u := range updates {
		if !validName(u.Name) {
			return fmt.Errorf("publisher: invalid file name %q", u.Name)
		}
	}
	recs, err := p.readJournal()
	if err != nil {
		return err
	}
	if len(recs) != 0 {
		return errors.New("publisher: journal not empty, run Recover first")
	}

	cur, err := p.readManifest()
	if err != nil {
		return err
	}
	txn := cur.Version + 1

	// 1. Intent: begin + full new contents.
	recs = []journalRecord{{Type: recBegin, Txn: txn}}
	for _, u := range updates {
		recs = append(recs, journalRecord{Type: recPut, Txn: txn, Name: u.Name, Data: u.Data})
	}
	if err := p.writeJournal(recs); err != nil {
		return err
	}

	// 2. Commit decision. This is the single atomic commit point.
	recs = append(recs, journalRecord{Type: recCommit, Txn: txn})
	if err := p.writeJournal(recs); err != nil {
		return err
	}

	// 3. Apply.
	if err := p.apply(txn, updates); err != nil {
		return err
	}

	// 4. Checkpoint + GC.
	if err := p.writeJournal(nil); err != nil {
		return err
	}
	p.gc(txn)
	return nil
}

// apply installs versioned blobs and swaps the manifest. It is idempotent and
// shared by Publish and recovery redo.
func (p *Publisher) apply(txn uint64, updates []Update) error {
	if err := p.fs.MkdirAll(dataDir); err != nil {
		return err
	}
	for _, u := range updates {
		final := blobPath(u.Name, txn)
		if err := writeFileSync(p.fs, final+".tmp", u.Data); err != nil {
			return err
		}
		if err := p.fs.Rename(final+".tmp", final); err != nil {
			return err
		}
	}
	if err := p.fs.SyncDir(dataDir); err != nil {
		return err
	}

	cur, err := p.readManifest()
	if err != nil {
		return err
	}
	next := Manifest{Version: txn, Files: map[string]FileMeta{}}
	for k, v := range cur.Files {
		next.Files[k] = v
	}
	for _, u := range updates {
		sum := sha256.Sum256(u.Data)
		next.Files[u.Name] = FileMeta{
			Path:   blobPath(u.Name, txn),
			SHA256: hex.EncodeToString(sum[:]),
			Size:   int64(len(u.Data)),
		}
	}
	buf, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileSync(p.fs, manifestTmp, buf); err != nil {
		return err
	}
	if err := p.fs.Rename(manifestTmp, manifestPath); err != nil {
		return err
	}
	return p.fs.SyncDir(".")
}

// Recover inspects the durable journal and finishes or rolls back the one
// pending transaction, if any. It is idempotent.
func (p *Publisher) Recover() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	recs, err := p.readJournal()
	if err != nil {
		return err
	}
	var (
		txn       uint64
		puts      []Update
		begun     bool
		committed bool
	)
	for _, r := range recs {
		switch r.Type {
		case recBegin:
			begun, txn = true, r.Txn
		case recPut:
			puts = append(puts, Update{Name: r.Name, Data: r.Data})
		case recCommit:
			committed = true
		}
	}
	if !begun {
		return nil // clean journal: nothing to decide
	}
	if !committed {
		// Full rollback: no commit decision was ever made durable, so the
		// old manifest is still the visible state. Discard staged blobs.
		for _, u := range puts {
			p.fs.Remove(blobPath(u.Name, txn))
			p.fs.Remove(blobPath(u.Name, txn) + ".tmp")
		}
		p.fs.Remove(manifestTmp)
		p.fs.SyncDir(dataDir)
		p.fs.SyncDir(".")
		return p.writeJournal(nil)
	}
	// Full completion: redo the apply (idempotent), then checkpoint.
	if err := p.apply(txn, puts); err != nil {
		return err
	}
	if err := p.writeJournal(nil); err != nil {
		return err
	}
	p.gc(txn)
	return nil
}

// gc removes blobs that are neither referenced by the current manifest nor
// part of the previous generation (kept for in-flight readers).
func (p *Publisher) gc(txn uint64) {
	cur, err := p.readManifest()
	if err != nil {
		return
	}
	keep := map[string]bool{}
	for _, m := range cur.Files {
		keep[m.Path] = true
	}
	names, err := p.fs.ReadDir(dataDir)
	if err != nil {
		return
	}
	for _, n := range names {
		full := dataDir + "/" + n
		if keep[full] {
			continue
		}
		fileTxn, ok := parseBlobTxn(n)
		// Keep the previous generation for in-flight readers; drop anything
		// older, anything from a rolled-back future txn, and stray tmp files.
		if ok && fileTxn <= txn && fileTxn+1 >= txn {
			continue
		}
		p.fs.Remove(full)
	}
	p.fs.SyncDir(dataDir)
}

func parseBlobTxn(base string) (uint64, bool) {
	i := strings.LastIndexByte(base, '@')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(base[i+1:], 10, 64)
	return n, err == nil
}

// --- journal & manifest IO ---

func (p *Publisher) writeJournal(recs []journalRecord) error {
	buf, err := encodeRecords(recs)
	if err != nil {
		return err
	}
	if err := writeFileSync(p.fs, journalPath, buf); err != nil {
		return err
	}
	return p.fs.SyncDir(".")
}

func (p *Publisher) readJournal() ([]journalRecord, error) {
	if !p.fs.Exists(journalPath) {
		return nil, nil
	}
	rc, err := p.fs.Open(journalPath)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return decodeRecords(b), nil
}

func (p *Publisher) readManifest() (Manifest, error) {
	m := Manifest{Files: map[string]FileMeta{}}
	if !p.fs.Exists(manifestPath) {
		return m, nil
	}
	rc, err := p.fs.Open(manifestPath)
	if err != nil {
		return m, err
	}
	defer rc.Close()
	if err := json.NewDecoder(rc).Decode(&m); err != nil {
		return m, err
	}
	if m.Files == nil {
		m.Files = map[string]FileMeta{}
	}
	return m, nil
}

func writeFileSync(fs FS, path string, data []byte) error {
	f, err := fs.Create(path)
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

// --- reader side ---

// Snapshot is a consistent, versioned view of all virtual files.
type Snapshot struct {
	fs FS
	m  Manifest
}

// Snapshot reads the current manifest. Because the manifest is installed by
// an atomic rename, a snapshot always references one complete generation.
func (p *Publisher) Snapshot() (*Snapshot, error) {
	m, err := p.readManifest()
	if err != nil {
		return nil, err
	}
	return &Snapshot{fs: p.fs, m: m}, nil
}

func (s *Snapshot) Version() uint64 { return s.m.Version }

func (s *Snapshot) Names() []string {
	names := make([]string, 0, len(s.m.Files))
	for n := range s.m.Files {
		names = append(names, n)
	}
	return names
}

// Get returns the contents of a virtual file in this snapshot, verifying its
// checksum against the manifest.
func (s *Snapshot) Get(name string) ([]byte, bool, error) {
	meta, ok := s.m.Files[name]
	if !ok {
		return nil, false, nil
	}
	rc, err := s.fs.Open(meta.Path)
	if err != nil {
		return nil, true, err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, true, err
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != meta.SHA256 {
		return nil, true, fmt.Errorf("publisher: checksum mismatch for %q", name)
	}
	return b, true, nil
}
