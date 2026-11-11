package receiver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// errFileNotExist normalizes the platform's not-exist error so callers can test
// for a missing state file without importing os in every file.
var errFileNotExist = errors.New("file does not exist")

func statePath(dir string) string { return filepath.Join(dir, stateFileName) }
func lockPath(dir string) string  { return filepath.Join(dir, lockFileName) }
func conflictPath(dir string, n int64) string {
	return filepath.Join(dir, fmt.Sprintf("%s%d%s", conflictPrefix, n, conflictSuffix))
}

// readFile returns the file contents, translating os.ErrNotExist.
func readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errFileNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("receiver: read %s: %w", filepath.Base(path), err)
	}
	return data, nil
}

// writeFileAtomic performs the crash-safe durable commit:
//
//  1. write data to a fresh temp file in the same directory,
//  2. fsync the temp file (flush its contents),
//  3. invoke crashHook if installed (simulating power loss pre-rename),
//  4. atomically rename temp -> target,
//  5. fsync the directory so the rename itself is durable.
//
// A crash before the rename leaves only a temp file (cleaned up on next Open);
// a crash at or after the rename leaves the complete new file in place because
// rename is atomic on a single filesystem.
func writeFileAtomic(dir, target string, data []byte, crashHook func()) error {
	tmp, err := os.CreateTemp(dir, tempStatePattern)
	if err != nil {
		return fmt.Errorf("receiver: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		removeIgnored(tmpName)
		return fmt.Errorf("receiver: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		removeIgnored(tmpName)
		return fmt.Errorf("receiver: fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		removeIgnored(tmpName)
		return fmt.Errorf("receiver: close temp file: %w", err)
	}

	if crashHook != nil {
		crashHook()
	}

	if err := os.Rename(tmpName, target); err != nil {
		removeIgnored(tmpName)
		return fmt.Errorf("receiver: atomic rename: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	return nil
}

// syncDir opens a directory and fsyncs it, making a preceding rename durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("receiver: open dir for fsync: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("receiver: fsync directory: %w", err)
	}
	return nil
}

// cleanupTempFiles removes leftover temp files from interrupted writes. It is
// safe to run on every Open because a temp file can never be the authoritative
// state (the rename is what publishes state).
func (r *Receiver) cleanupTempFiles() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return fmt.Errorf("receiver: scan state dir: %w", err)
	}
	var leftovers []string
	for _, entry := range entries {
		name := entry.Name()
		if isTempFile(name) {
			leftovers = append(leftovers, filepath.Join(r.dir, name))
		}
	}
	sort.Strings(leftovers)
	for _, path := range leftovers {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("receiver: remove stale temp file: %w", err)
		}
	}
	return nil
}

// isTempFile reports whether name matches the temp-file naming scheme.
func isTempFile(name string) bool {
	matched, err := filepath.Match(tempStatePattern, name)
	return err == nil && matched
}

func removeIgnored(path string) {
	_ = os.Remove(path)
}
