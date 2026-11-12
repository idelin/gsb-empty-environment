package netsec

import "errors"

// ErrNotExist is returned by FS.Lookup when the entry is absent.
var ErrNotExist = errors.New("netsec: path does not exist")

// EntryType classifies a directory entry.
type EntryType uint8

const (
	EntryFile EntryType = iota
	EntryDirectory
	EntrySymlink
)

// Entry is a single directory entry as reported by the host.
type Entry struct {
	Type EntryType
	// Target is the symlink target, only meaningful when Type == EntrySymlink.
	Target string
}

// FS is the host-supplied view of the directory tree below the preopen
// root. dir is a resolved, root-relative directory path ("" is the root)
// and name is a single path segment inside it.
type FS interface {
	Lookup(dir, name string) (Entry, error)
}

// MemFS is an in-memory FS, convenient for tests and for runtimes that
// cache directory metadata.
type MemFS struct {
	entries map[string]Entry
}

func NewMemFS() *MemFS {
	return &MemFS{entries: map[string]Entry{"": {Type: EntryDirectory}}}
}

func (m *MemFS) AddDir(path string) { m.entries[path] = Entry{Type: EntryDirectory} }

func (m *MemFS) AddFile(path string) { m.entries[path] = Entry{Type: EntryFile} }

func (m *MemFS) AddSymlink(path, target string) {
	m.entries[path] = Entry{Type: EntrySymlink, Target: target}
}

func (m *MemFS) Lookup(dir, name string) (Entry, error) {
	key := name
	if dir != "" {
		key = dir + "/" + name
	}
	e, ok := m.entries[key]
	if !ok {
		return Entry{}, ErrNotExist
	}
	return e, nil
}
