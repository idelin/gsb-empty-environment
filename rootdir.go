package netsec

import (
	"fmt"
	"io/fs"
	"os"
	"sort"
)

// Open opens the directory at path and returns it as a Dir capability.
// A symlink at path is rejected rather than followed.
func Open(path string) (Dir, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("open %s: is a symbolic link", path)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("open %s: not a directory", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &rootDir{root: root}, nil
}

// rootDir is a Dir backed by an os.Root: every operation is resolved
// relative to the opened directory and cannot escape it, not even
// through a symlink planted mid-traversal.
type rootDir struct {
	root *os.Root
}

func (d *rootDir) ReadDir() ([]Entry, error) {
	file, err := d.root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("readdir: %w", err)
	}
	defer file.Close()
	ents, err := file.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("readdir: %w", err)
	}
	entries := make([]Entry, 0, len(ents))
	for _, ent := range ents {
		entries = append(entries, Entry{Name: ent.Name(), Type: typeFromMode(ent.Type())})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func (d *rootDir) Lstat(name string) (Info, error) {
	if !validName(name) {
		return Info{}, fmt.Errorf("lstat %q: %w", name, fs.ErrInvalid)
	}
	fi, err := d.root.Lstat(name)
	if err != nil {
		return Info{}, err
	}
	return Info{ID: identityOf(fi), Type: typeFromMode(fi.Mode())}, nil
}

func (d *rootDir) OpenDir(name string) (Dir, error) {
	if !validName(name) {
		return nil, fmt.Errorf("opendir %q: %w", name, fs.ErrInvalid)
	}
	// OpenRoot refuses a link that would escape the directory. A link
	// pointing back inside is caught by the caller's identity check:
	// the opened directory never carries the link's identity.
	sub, err := d.root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	return &rootDir{root: sub}, nil
}

func (d *rootDir) Stat() (Info, error) {
	fi, err := d.root.Stat(".")
	if err != nil {
		return Info{}, err
	}
	return Info{ID: identityOf(fi), Type: typeFromMode(fi.Mode())}, nil
}

func (d *rootDir) Unlink(name string) error {
	if !validName(name) {
		return fmt.Errorf("unlink %q: %w", name, fs.ErrInvalid)
	}
	return d.root.Remove(name)
}

func (d *rootDir) Rmdir(name string) error {
	if !validName(name) {
		return fmt.Errorf("rmdir %q: %w", name, fs.ErrInvalid)
	}
	return d.root.Remove(name)
}

func (d *rootDir) Close() error {
	return d.root.Close()
}

func typeFromMode(mode fs.FileMode) FileType {
	switch {
	case mode&fs.ModeSymlink != 0:
		return TypeSymlink
	case mode.IsDir():
		return TypeDir
	case mode.IsRegular():
		return TypeFile
	default:
		return TypeOther
	}
}
