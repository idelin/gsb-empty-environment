// Package atomicpub implements a multi-file publish tool. Readers always see
// either the complete old version or the complete new version, never a mix.
//
// Layout on disk:
//
//	root/
//	  CURRENT            pointer file: name of the live version directory
//	  v-000001/          committed version: data files + MANIFEST.json
//	  v-000002/
//	  tmp-v-000003-x/    staging directory of an in-flight publish
//
// Publish writes and validates all files in a staging directory, commits it
// with an atomic rename to v-NNNNNN, then flips CURRENT with an atomic
// rename. A version becomes visible only at the pointer flip, and the flip
// is a single atomic operation, so readers never observe mixed content.
package atomicpub

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	manifestName = "MANIFEST.json"
	pointerName  = "CURRENT"
	pointerTmp   = "CURRENT.tmp"
	versionPref  = "v-"
	tmpPrefix    = "tmp-"
)

// ErrValidation is returned when staged data fails verification before commit.
var ErrValidation = errors.New("atomicpub: validation failed")

// ErrEmpty is returned by Acquire when no version has been published yet.
var ErrEmpty = errors.New("atomicpub: no version published")

// File is one named blob to publish.
type File struct {
	Name string
	Data []byte
}

type fileMeta struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Version string              `json:"version"`
	Files   map[string]fileMeta `json:"files"`
}

// Report describes what recovery found and did at Open time.
type Report struct {
	RemovedStaging    []string // staging dirs of interrupted publishes (known layout, removed)
	RemovedPointerTmp bool     // leftover CURRENT.tmp removed
	CorruptVersions   []string // v-* dirs failing validation (left on disk, never used)
	UnknownEntries    []string // entries we do not own (left untouched)
	PointerRepaired   bool     // CURRENT was missing/corrupt/dangling and was rewritten
	Current           string   // live version after recovery, "" if none
}

// Store is a versioned, atomically published file set.
type Store struct {
	dir string
	fs  FS

	mu          sync.Mutex
	current     string
	nextVersion int
	readers     map[string]int // version -> live reader count
	report      Report
	pubMu       sync.Mutex // serializes Publish
}

// Open opens (creating if needed) the store rooted at dir and runs recovery.
// fs may be nil for the default OS implementation.
func Open(dir string, fs FS) (*Store, error) {
	if fs == nil {
		fs = osFS{}
	}
	s := &Store{dir: dir, fs: fs, readers: map[string]int{}}
	if err := fs.MkdirAll(dir); err != nil {
		return nil, err
	}
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

// Report returns the recovery report produced by Open.
func (s *Store) Report() Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.report
}

// Current returns the name of the live version, or "" if none.
func (s *Store) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

func versionName(n int) string { return fmt.Sprintf("v-%06d", n) }

func parseVersion(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, versionPref)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 || fmt.Sprintf("%06d", n) != rest {
		return 0, false
	}
	return n, true
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func validateFileNames(files []File) error {
	seen := map[string]bool{}
	for _, f := range files {
		if f.Name == "" || f.Name == manifestName ||
			strings.ContainsAny(f.Name, "/\\") || f.Name == "." || f.Name == ".." {
			return fmt.Errorf("%w: bad file name %q", ErrValidation, f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("%w: duplicate file %q", ErrValidation, f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

// Publish validates and atomically publishes files as a new version.
// On success it returns the new version name. On any error the previously
// published version remains live and untouched; interrupted staging
// directories are reclaimed by recovery (Open) or the next Publish.
func (s *Store) Publish(files []File) (string, error) {
	if err := validateFileNames(files); err != nil {
		return "", err
	}
	s.pubMu.Lock()
	defer s.pubMu.Unlock()

	s.sweep()

	s.mu.Lock()
	n := s.nextVersion
	s.mu.Unlock()
	version := versionName(n)

	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	staging := filepath.Join(s.dir, tmpPrefix+version+"-"+hex.EncodeToString(nonce[:]))
	final := filepath.Join(s.dir, version)

	if err := s.fs.MkdirAll(staging); err != nil {
		return "", err
	}

	// 1. Write every data file into staging (fsynced by WriteFile).
	m := manifest{Version: version, Files: map[string]fileMeta{}}
	for _, f := range files {
		if err := s.fs.WriteFile(filepath.Join(staging, f.Name), f.Data); err != nil {
			return "", fmt.Errorf("write %s: %w", f.Name, err)
		}
		m.Files[f.Name] = fileMeta{Size: int64(len(f.Data)), SHA256: sha256Hex(f.Data)}
	}

	// 2. Validate: re-read every staged file and verify its checksum before
	//    anything is committed or published.
	for _, f := range files {
		got, err := s.fs.ReadFile(filepath.Join(staging, f.Name))
		if err != nil {
			return "", fmt.Errorf("validate %s: %w", f.Name, err)
		}
		if sha256Hex(got) != m.Files[f.Name].SHA256 {
			return "", fmt.Errorf("%w: %s checksum mismatch after write", ErrValidation, f.Name)
		}
	}

	// 3. Write the manifest: the version's commit record.
	mdata, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	if err := s.fs.WriteFile(filepath.Join(staging, manifestName), mdata); err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}
	if err := s.fs.SyncDir(staging); err != nil {
		return "", fmt.Errorf("sync staging: %w", err)
	}

	// 4. Commit: atomically rename staging to its final version name.
	if err := s.fs.Rename(staging, final); err != nil {
		return "", fmt.Errorf("commit %s: %w", version, err)
	}
	if err := s.fs.SyncDir(s.dir); err != nil {
		return "", fmt.Errorf("sync root: %w", err)
	}

	// 5. Publish: atomically flip the pointer. Only now can readers see it.
	tmp := filepath.Join(s.dir, pointerTmp)
	s.fs.Remove(tmp) // leftover from a previous crash, if any
	if err := s.fs.WriteFile(tmp, []byte(version+"\n")); err != nil {
		return "", fmt.Errorf("write pointer: %w", err)
	}
	if err := s.fs.Rename(tmp, filepath.Join(s.dir, pointerName)); err != nil {
		return "", fmt.Errorf("publish pointer: %w", err)
	}
	if err := s.fs.SyncDir(s.dir); err != nil {
		return "", fmt.Errorf("sync root: %w", err)
	}

	s.mu.Lock()
	s.current = version
	s.nextVersion = n + 1
	s.mu.Unlock()

	s.gc()
	return version, nil
}

// sweep removes staging directories left by interrupted publishes and
// committed versions that were never published (crash between commit and
// pointer flip). It runs with pubMu held, so no publish is in flight and
// s.current cannot change concurrently; readers can only pin s.current, so
// an unpinned non-current version can never gain a reader during the sweep.
// Only our own known layouts (tmp-* staging, committed v-* with manifest)
// are removed; anything else is left untouched.
func (s *Store) sweep() {
	s.mu.Lock()
	protected := map[string]bool{s.current: true}
	for v := range s.readers {
		protected[v] = true
	}
	s.mu.Unlock()

	entries, err := s.fs.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, tmpPrefix) {
			s.fs.RemoveAll(filepath.Join(s.dir, name))
			continue
		}
		if _, ok := parseVersion(name); !ok || protected[name] {
			continue
		}
		if _, err := s.fs.Stat(filepath.Join(s.dir, name, manifestName)); err != nil {
			continue // not our committed format; never delete
		}
		s.mu.Lock()
		_, pinned := s.readers[name]
		s.mu.Unlock()
		if !pinned {
			s.fs.RemoveAll(filepath.Join(s.dir, name))
		}
	}
}

// Reader is a consistent read handle pinned to one version. It stays usable
// after newer versions are published; Close releases the pin.
type Reader struct {
	store   *Store
	version string
	closed  bool
	mu      sync.Mutex
}

// Acquire returns a reader pinned to the current version.
func (s *Store) Acquire() (*Reader, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == "" {
		return nil, ErrEmpty
	}
	s.readers[s.current]++
	return &Reader{store: s, version: s.current}, nil
}

// Version returns the version this reader is pinned to.
func (r *Reader) Version() string { return r.version }

// Read returns the named file from the pinned version, verified against the
// version manifest so a reader never observes torn content.
func (r *Reader) Read(name string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("atomicpub: reader closed")
	}
	data, err := r.store.fs.ReadFile(filepath.Join(r.store.dir, r.version, name))
	if err != nil {
		return nil, err
	}
	m, err := r.store.readManifest(r.version)
	if err != nil {
		return nil, err
	}
	meta, ok := m.Files[name]
	if !ok {
		return nil, fmt.Errorf("atomicpub: %s not in version %s", name, r.version)
	}
	if int64(len(data)) != meta.Size || sha256Hex(data) != meta.SHA256 {
		return nil, fmt.Errorf("%w: %s in %s", ErrValidation, name, r.version)
	}
	return data, nil
}

// Close releases the version pin, allowing garbage collection.
func (r *Reader) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()

	s := r.store
	s.mu.Lock()
	s.readers[r.version]--
	if s.readers[r.version] <= 0 {
		delete(s.readers, r.version)
	}
	s.mu.Unlock()
	s.gc()
	return nil
}

// gc removes committed versions that are strictly older than the live
// version and not pinned by any reader. Versions newer than the live one are
// never touched here: they may belong to an in-flight publish that has
// committed but not yet flipped the pointer (crash leftovers are reclaimed
// by sweep at the next Publish instead). Only directories in our known
// committed format (valid v-NNNNNN name containing a manifest) are eligible;
// anything else is left alone.
//
// A version older than the live version can never gain a new reader
// (Acquire pins only s.current, which never moves backwards), so the pin
// check under the lock is decisive.
func (s *Store) gc() {
	s.mu.Lock()
	curNum, ok := parseVersion(s.current)
	s.mu.Unlock()
	if !ok {
		return
	}

	entries, err := s.fs.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		num, ok := parseVersion(e.Name())
		if !ok || num >= curNum {
			continue
		}
		if _, err := s.fs.Stat(filepath.Join(s.dir, e.Name(), manifestName)); err != nil {
			continue // not our committed format; never delete
		}
		s.mu.Lock()
		_, pinned := s.readers[e.Name()]
		s.mu.Unlock()
		if !pinned {
			s.fs.RemoveAll(filepath.Join(s.dir, e.Name()))
		}
	}
	s.fs.SyncDir(s.dir)
}

func (s *Store) readManifest(version string) (*manifest, error) {
	data, err := s.fs.ReadFile(filepath.Join(s.dir, version, manifestName))
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// validateVersion checks that a committed version directory is complete:
// parseable manifest, and every listed file present with matching checksum.
func (s *Store) validateVersion(version string) bool {
	m, err := s.readManifest(version)
	if err != nil || m.Version != version {
		return false
	}
	for name, meta := range m.Files {
		data, err := s.fs.ReadFile(filepath.Join(s.dir, version, name))
		if err != nil || int64(len(data)) != meta.Size || sha256Hex(data) != meta.SHA256 {
			return false
		}
	}
	return true
}

// recover inspects the root directory and restores a consistent live pointer.
//
//   - tmp-* staging dirs are interrupted publishes of known layout: removed.
//   - CURRENT.tmp is an interrupted pointer flip: removed.
//   - v-* dirs are validated; corrupt ones are kept on disk but never used.
//   - unknown entries are never deleted, only reported.
//   - CURRENT is repaired if missing, unparsable, or pointing at a version
//     that is missing or corrupt; the newest valid version becomes live.
func (s *Store) recover() error {
	entries, err := s.fs.ReadDir(s.dir)
	if err != nil {
		return err
	}

	var rep Report
	var valid []int
	maxSeen := 0

	for _, e := range entries {
		name := e.Name()
		switch {
		case name == pointerName:
			// handled below
		case name == pointerTmp:
			if err := s.fs.Remove(filepath.Join(s.dir, name)); err == nil {
				rep.RemovedPointerTmp = true
			}
		case strings.HasPrefix(name, tmpPrefix) && e.IsDir():
			if err := s.fs.RemoveAll(filepath.Join(s.dir, name)); err != nil {
				return fmt.Errorf("remove staging %s: %w", name, err)
			}
			rep.RemovedStaging = append(rep.RemovedStaging, name)
		default:
			if n, ok := parseVersion(name); ok && e.IsDir() {
				if n > maxSeen {
					maxSeen = n
				}
				if s.validateVersion(name) {
					valid = append(valid, n)
				} else {
					rep.CorruptVersions = append(rep.CorruptVersions, name)
				}
			} else {
				rep.UnknownEntries = append(rep.UnknownEntries, name)
			}
		}
	}
	sort.Ints(valid)

	// Resolve the live version: trust CURRENT only if it names a valid version.
	live := ""
	if data, err := s.fs.ReadFile(filepath.Join(s.dir, pointerName)); err == nil {
		name := strings.TrimSpace(string(data))
		if n, ok := parseVersion(name); ok && containsInt(valid, n) {
			live = name
		}
	}
	if live == "" && len(valid) > 0 {
		live = versionName(valid[len(valid)-1])
		rep.PointerRepaired = true
		tmp := filepath.Join(s.dir, pointerTmp)
		if err := s.fs.WriteFile(tmp, []byte(live+"\n")); err != nil {
			return fmt.Errorf("repair pointer: %w", err)
		}
		if err := s.fs.Rename(tmp, filepath.Join(s.dir, pointerName)); err != nil {
			return fmt.Errorf("repair pointer: %w", err)
		}
		if err := s.fs.SyncDir(s.dir); err != nil {
			return err
		}
	}

	rep.Current = live
	s.mu.Lock()
	s.current = live
	s.nextVersion = maxSeen + 1
	if s.nextVersion < 1 {
		s.nextVersion = 1
	}
	s.report = rep
	s.mu.Unlock()
	return nil
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
