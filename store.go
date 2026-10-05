// Package publish implements a multi-file publishing tool with atomic
// version switching. Readers always observe a complete old version or a
// complete new version, never a mixture.
//
// On-disk layout under the store root:
//
//	current             pointer file holding the committed version id
//	staging/<id>/       a version being written (invisible to readers)
//	versions/<id>/      committed, immutable version directories
//	quarantine/         uncommitted/corrupt directories moved here by
//	                    recovery (never deleted outright)
//
// Commit protocol:
//  1. write data files + manifest (sha256 per file) into staging/<id>
//  2. validate the staged directory against its manifest
//  3. rename staging/<id> -> versions/<id>   (atomic directory commit)
//  4. write pointer tmp file, rename -> current (atomic version switch)
//
// Readers pin a version id at Acquire time and read straight from the
// immutable version directory, so handles acquired before a publish keep
// serving the old version for as long as they are held.
package publish

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
)

const (
	stagingDirName    = "staging"
	versionsDirName   = "versions"
	quarantineDirName = "quarantine"
	pointerFileName   = "current"
	pointerTmpName    = "current.tmp"
	manifestFileName  = "manifest.json"
)

var (
	ErrNoVersion    = errors.New("publish: no committed version")
	ErrEmptyPublish = errors.New("publish: no files to publish")
)

// Store manages a versioned data directory.
type Store struct {
	root string

	hook atomic.Value // stores Hook

	mu      sync.RWMutex
	current string // committed version id, "" when the store is empty
	seq     int    // next version sequence number
	report  *RecoveryReport
}

// Open opens (creating if necessary) the store at root and runs recovery.
func Open(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &Store{root: abs, report: &RecoveryReport{}}
	for _, d := range []string{stagingDirName, versionsDirName, quarantineDirName} {
		if err := os.MkdirAll(filepath.Join(abs, d), 0o755); err != nil {
			return nil, err
		}
	}
	if err := s.recover(); err != nil {
		return nil, fmt.Errorf("publish: recovery failed: %w", err)
	}
	return s, nil
}

// Root returns the store root directory.
func (s *Store) Root() string { return s.root }

// Current returns the id of the currently committed version, or "".
func (s *Store) Current() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Report returns the report produced by recovery during Open.
func (s *Store) Report() RecoveryReport { return *s.report }

// Publish stages, validates and atomically publishes files as a new
// version. On any error (including injected crashes) the previously
// committed version remains current and readers are unaffected.
func (s *Store) Publish(files map[string][]byte) (string, error) {
	if len(files) == 0 {
		return "", ErrEmptyPublish
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if err := checkFileName(name); err != nil {
			return "", err
		}
		names = append(names, name)
	}
	sort.Strings(names)

	id := s.allocID()
	stageDir := filepath.Join(s.root, stagingDirName, id)
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return "", err
	}

	// Step 1: stage data files and the manifest.
	manifest := Manifest{Files: make(map[string]string, len(files))}
	for _, name := range names {
		if err := s.inject(OpWriteDataFile); err != nil {
			return "", err
		}
		data := files[name]
		if err := writeFileSync(filepath.Join(stageDir, name), data, 0o644); err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		manifest.Files[name] = hex.EncodeToString(sum[:])
	}
	if err := s.inject(OpWriteManifest); err != nil {
		return "", err
	}
	if err := writeManifest(stageDir, manifest); err != nil {
		return "", err
	}
	if err := syncDir(stageDir); err != nil {
		return "", err
	}

	// Step 2: validate the staged directory before it becomes visible.
	if err := s.inject(OpValidate); err != nil {
		return "", err
	}
	if err := validateVersionDir(stageDir); err != nil {
		return "", fmt.Errorf("publish: validation failed: %w", err)
	}

	// Step 3: atomically commit the version directory.
	if err := s.inject(OpRenameStage); err != nil {
		return "", err
	}
	versionDir := filepath.Join(s.root, versionsDirName, id)
	if err := os.Rename(stageDir, versionDir); err != nil {
		return "", err
	}
	if err := syncDir(filepath.Join(s.root, versionsDirName)); err != nil {
		return "", err
	}

	// Step 4: atomically switch the pointer.
	if err := s.inject(OpWritePointer); err != nil {
		return "", err
	}
	ptrTmp := filepath.Join(s.root, pointerTmpName)
	// Remove a leftover tmp file from an earlier interrupted publish;
	// it is a recognized artifact of our own protocol.
	if err := os.Remove(ptrTmp); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := writePointerFile(ptrTmp, id); err != nil {
		return "", err
	}
	if err := s.inject(OpRenamePointer); err != nil {
		return "", err
	}
	if err := os.Rename(ptrTmp, filepath.Join(s.root, pointerFileName)); err != nil {
		return "", err
	}
	if err := syncDir(s.root); err != nil {
		return "", err
	}

	s.mu.Lock()
	s.current = id
	s.mu.Unlock()
	return id, nil
}

func (s *Store) allocID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("v%06d", s.seq)
}

func writePointerFile(path, id string) error {
	if err := writeFileSync(path, []byte(id+"\n"), 0o644); err != nil {
		return err
	}
	return nil
}

func checkFileName(name string) error {
	if name == "" || name == "." || name == ".." || name == manifestFileName {
		return fmt.Errorf("publish: invalid file name %q", name)
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("publish: file name %q must not contain path separators", name)
	}
	return nil
}
