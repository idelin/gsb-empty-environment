// Package fsdiff compares two directory-tree snapshots and produces a
// minimal, inode-identity-based diff that can be replayed on a virtual
// inode tree to reconstruct the target state.
//
// Identity rule: an inode (dev, ino) is the stable identity of a file
// system object. Equal content with a different inode is NOT a match:
// it is reported as a delete plus a create. A single inode reachable
// through multiple paths (hard links) is diffed by its path set.
package fsdiff

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// ID is the stable identity of a file system object.
type ID struct {
	Dev uint64
	Ino uint64

	// Seq distinguishes distinct objects that reuse the same (Dev, Ino)
	// within a constructed (non-scanned) snapshot. Scans leave it zero.
	Seq uint64
}

// Kind classifies an inode.
type Kind uint8

const (
	KindFile Kind = iota
	KindDir
	KindSymlink
)

func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDir:
		return "dir"
	case KindSymlink:
		return "symlink"
	}
	return "unknown"
}

// Inode describes one file system object independently of its paths.
type Inode struct {
	ID     ID
	Kind   Kind
	Mode   os.FileMode // permission bits only
	Digest string      // "sha256:..." for files, "symlink:<target>" for symlinks, "" for dirs
	Paths  []string    // sorted slash-separated paths relative to the root; exactly one for dirs
}

// Snapshot is an immutable view of a directory tree keyed by inode.
type Snapshot struct {
	Root    ID
	Inodes  map[ID]*Inode
	Entries map[ID]map[string]ID // dir inode -> base name -> child inode
	byPath  map[string]ID
}

// Scan walks root and builds a Snapshot using real (dev, ino) pairs.
func Scan(root string) (*Snapshot, error) {
	s := &Snapshot{
		Inodes:  make(map[ID]*Inode),
		Entries: make(map[ID]map[string]ID),
		byPath:  make(map[string]ID),
	}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("fsdiff: cannot stat %q: unsupported platform", p)
		}
		id := ID{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			rel = ""
		}
		rel = filepath.ToSlash(rel)

		ino, seen := s.Inodes[id]
		if !seen {
			ino = &Inode{ID: id, Mode: info.Mode().Perm()}
			switch {
			case info.Mode().IsDir():
				ino.Kind = KindDir
				s.Entries[id] = make(map[string]ID)
			case info.Mode().IsRegular():
				ino.Kind = KindFile
				d, err := hashFile(p)
				if err != nil {
					return err
				}
				ino.Digest = d
			case info.Mode()&os.ModeSymlink != 0:
				ino.Kind = KindSymlink
				target, err := os.Readlink(p)
				if err != nil {
					return err
				}
				ino.Digest = "symlink:" + target
			default:
				return fmt.Errorf("fsdiff: %q: unsupported file type %v", p, info.Mode().Type())
			}
			s.Inodes[id] = ino
		}
		ino.Paths = append(ino.Paths, rel)
		s.byPath[rel] = id
		if rel == "" {
			s.Root = id
			return nil
		}
		dir := path.Dir(rel)
		if dir == "." || dir == "/" {
			dir = ""
		}
		parent := s.byPath[dir]
		if dir, ok := s.Entries[parent]; ok {
			dir[path.Base(rel)] = id
		} else {
			return fmt.Errorf("fsdiff: parent of %q not scanned", rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, ino := range s.Inodes {
		sort.Strings(ino.Paths)
	}
	return s, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// parentOf returns the inode ID of the directory containing path p.
func (s *Snapshot) parentOf(p string) ID {
	dir := path.Dir(p)
	if dir == "." || dir == "/" {
		dir = ""
	}
	return s.byPath[dir]
}

// lookup resolves a slash-separated relative path to an inode ID.
func (s *Snapshot) lookup(p string) (ID, bool) {
	id, ok := s.byPath[p]
	return id, ok
}

// depth counts path components.
func depth(p string) int {
	if p == "" {
		return 0
	}
	return strings.Count(p, "/") + 1
}
