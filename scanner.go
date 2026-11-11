package netsec

import (
	"io/fs"
	"path/filepath"
	"syscall"
)

// Scanner performs a full directory walk. It is pausable: the hooks are
// invoked synchronously while the walk is suspended, which lets tests
// (and backpressure policies) inject events, overflows or on-disk
// mutations at exact points of the scan.
type Scanner struct {
	Root string

	// BeforeEntry is called with the root-relative path before each
	// entry is stat'ed. Blocking here suspends the scan.
	BeforeEntry func(rel string)

	// BeforeFinish is called after the last entry, before Scan returns.
	// It models the scan-end race window.
	BeforeFinish func()
}

// Scan walks Root and returns one Entry per non-directory file.
func (s *Scanner) Scan() ([]Entry, error) {
	var entries []Entry
	err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == s.Root {
			return nil
		}
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		if s.BeforeEntry != nil {
			s.BeforeEntry(rel)
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, Entry{Path: rel, ObjectID: objectIDOf(info)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.BeforeFinish != nil {
		s.BeforeFinish()
	}
	return entries, nil
}

func objectIDOf(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}
