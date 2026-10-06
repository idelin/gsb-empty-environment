// Package fsdiff compares two directory trees using stable inode IDs.
//
// Identity is keyed on inode, not content: a file that is renamed in place
// is reported as a move, while a file that is deleted and recreated with
// identical content (new inode) is reported as a remove plus an add.
// Hard-linked files (one inode, many paths) are diffed as path sets.
package fsdiff

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// Entry describes one path in a snapshot.
type Entry struct {
	Path  string // slash-separated path relative to the snapshot root
	Inode uint64
	IsDir bool
	Hash  string // hex sha256 of content; empty for directories
}

// Snapshot is an inode-indexed view of a directory tree.
type Snapshot struct {
	Entries map[string]Entry    // by relative path
	Inodes  map[uint64][]string // inode -> sorted relative paths (len > 1 means hard links)
}

// HashContent returns the content hash used for regular files.
func HashContent(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func inodeOf(info os.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Ino, true
}

// Scan walks root and builds a snapshot. Symlinks are not followed and are
// recorded by their link-target hash so target changes are detected.
func Scan(root string) (*Snapshot, error) {
	s := &Snapshot{Entries: map[string]Entry{}, Inodes: map[uint64][]string{}}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		ino, ok := inodeOf(info)
		if !ok {
			return nil // cannot identify; skip
		}
		e := Entry{Path: rel, Inode: ino, IsDir: info.IsDir()}
		switch {
		case info.IsDir():
		case info.Mode().IsRegular():
			h, err := hashFile(p)
			if err != nil {
				return err
			}
			e.Hash = h
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			e.Hash = HashContent([]byte("symlink:" + target))
		default:
			return nil // sockets, devices, etc. are ignored
		}
		s.Entries[rel] = e
		s.Inodes[ino] = append(s.Inodes[ino], rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for ino := range s.Inodes {
		sort.Strings(s.Inodes[ino])
	}
	return s, nil
}

// Build assembles a snapshot from explicit entries (used by tests and by the
// virtual tree when reconstructing a target).
func Build(entries []Entry) *Snapshot {
	s := &Snapshot{Entries: map[string]Entry{}, Inodes: map[uint64][]string{}}
	for _, e := range entries {
		s.Entries[e.Path] = e
		s.Inodes[e.Inode] = append(s.Inodes[e.Inode], e.Path)
	}
	for ino := range s.Inodes {
		sort.Strings(s.Inodes[ino])
	}
	return s
}

// Equal reports whether two snapshots are identical in paths, inodes and hashes.
func (s *Snapshot) Equal(o *Snapshot) bool {
	if len(s.Entries) != len(o.Entries) {
		return false
	}
	for p, e := range s.Entries {
		oe, ok := o.Entries[p]
		if !ok || oe != e {
			return false
		}
	}
	return true
}

func sortedPaths(m map[string]Entry) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
