// Package logtail implements a log tailer together with a replayable
// file-operation model of the underlying filesystem.
//
// The virtual filesystem (FS) gives every file a stable identity (Ino)
// that survives renames, records every mutation as an Op so that any
// session can be replayed, and tracks open handles so tests can assert
// that every handle is eventually closed.
package logtail

import (
	"errors"
	"fmt"
	"io"
)

// Ino uniquely identifies a file instance for its whole lifetime,
// independent of any path it is (or was) reachable under.
type Ino uint64

// OpKind enumerates the filesystem mutations the model supports.
type OpKind int

const (
	OpCreate OpKind = iota
	OpAppend
	OpTruncate
	OpRename
	OpRemove
)

func (k OpKind) String() string {
	switch k {
	case OpCreate:
		return "create"
	case OpAppend:
		return "append"
	case OpTruncate:
		return "truncate"
	case OpRename:
		return "rename"
	case OpRemove:
		return "remove"
	}
	return "unknown"
}

// Op is one recorded filesystem mutation. A session is fully described
// by its []Op and can be replayed with Replay.
type Op struct {
	Kind OpKind
	Path string
	To   string // rename destination
	Data []byte // create/append payload
}

func (o Op) String() string {
	switch o.Kind {
	case OpCreate, OpAppend:
		return fmt.Sprintf("%s %q %q", o.Kind, o.Path, o.Data)
	case OpRename:
		return fmt.Sprintf("%s %q %q", o.Kind, o.Path, o.To)
	default:
		return fmt.Sprintf("%s %q", o.Kind, o.Path)
	}
}

var (
	ErrNotExist = errors.New("logtail: file does not exist")
	ErrExist    = errors.New("logtail: file already exists")
)

type file struct {
	ino  Ino
	data []byte
}

// FS is an in-memory filesystem with POSIX-rename semantics.
type FS struct {
	byPath map[string]*file
	byIno  map[Ino]*file // includes unlinked files still referenced by handles
	next   Ino
	ops    []Op
	live   int // currently open handles
}

func NewFS() *FS {
	return &FS{byPath: make(map[string]*file), byIno: make(map[Ino]*file)}
}

func (fs *FS) record(op Op) { fs.ops = append(fs.ops, op) }

// Ops returns the recorded mutation log, suitable for Replay.
func (fs *FS) Ops() []Op { return append([]Op(nil), fs.ops...) }

func (fs *FS) Create(path string, data []byte) error {
	if _, ok := fs.byPath[path]; ok {
		return ErrExist
	}
	fs.next++
	f := &file{ino: fs.next, data: append([]byte(nil), data...)}
	fs.byPath[path] = f
	fs.byIno[f.ino] = f
	fs.record(Op{Kind: OpCreate, Path: path, Data: append([]byte(nil), data...)})
	return nil
}

func (fs *FS) Append(path string, data []byte) error {
	f, ok := fs.byPath[path]
	if !ok {
		return ErrNotExist
	}
	f.data = append(f.data, data...)
	fs.record(Op{Kind: OpAppend, Path: path, Data: append([]byte(nil), data...)})
	return nil
}

// Truncate resets the file to zero length. Its identity is unchanged.
func (fs *FS) Truncate(path string) error {
	f, ok := fs.byPath[path]
	if !ok {
		return ErrNotExist
	}
	f.data = f.data[:0]
	fs.record(Op{Kind: OpTruncate, Path: path})
	return nil
}

// Rename moves the file to a new path, atomically replacing any file
// already there (POSIX semantics). The file keeps its identity.
func (fs *FS) Rename(from, to string) error {
	f, ok := fs.byPath[from]
	if !ok {
		return ErrNotExist
	}
	delete(fs.byPath, from)
	fs.byPath[to] = f
	fs.record(Op{Kind: OpRename, Path: from, To: to})
	return nil
}

func (fs *FS) Remove(path string) error {
	if _, ok := fs.byPath[path]; !ok {
		return ErrNotExist
	}
	delete(fs.byPath, path)
	fs.record(Op{Kind: OpRemove, Path: path})
	return nil
}

// Apply replays one recorded operation.
func (fs *FS) Apply(op Op) error {
	switch op.Kind {
	case OpCreate:
		return fs.Create(op.Path, op.Data)
	case OpAppend:
		return fs.Append(op.Path, op.Data)
	case OpTruncate:
		return fs.Truncate(op.Path)
	case OpRename:
		return fs.Rename(op.Path, op.To)
	case OpRemove:
		return fs.Remove(op.Path)
	}
	return fmt.Errorf("logtail: unknown op kind %d", int(op.Kind))
}

// Replay rebuilds a filesystem by applying a recorded op log.
func Replay(ops []Op) (*FS, error) {
	fs := NewFS()
	for i, op := range ops {
		if err := fs.Apply(op); err != nil {
			return nil, fmt.Errorf("logtail: replay op %d (%v): %v", i, op, err)
		}
	}
	return fs, nil
}

// Stat reports the identity and size of the file at path.
func (fs *FS) Stat(path string) (ino Ino, size int64, ok bool) {
	f, ok := fs.byPath[path]
	if !ok {
		return 0, 0, false
	}
	return f.ino, int64(len(f.data)), true
}

// Snapshot returns the current content of a file by identity, even if
// it has been unlinked or renamed away. Used by the checking model.
func (fs *FS) Snapshot(ino Ino) ([]byte, bool) {
	f, ok := fs.byIno[ino]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), f.data...), true
}

// OpenHandles reports how many handles are currently open.
func (fs *FS) OpenHandles() int { return fs.live }

// Handle is an open file description: it keeps reading the same file
// identity regardless of renames, and observes truncations as EOF.
type Handle struct {
	fs     *FS
	f      *file
	off    int64
	closed bool
}

func (fs *FS) Open(path string) (*Handle, error) {
	f, ok := fs.byPath[path]
	if !ok {
		return nil, ErrNotExist
	}
	fs.live++
	return &Handle{fs: fs, f: f}, nil
}

func (h *Handle) Read(p []byte) (int, error) {
	if h.closed {
		return 0, errors.New("logtail: read on closed handle")
	}
	if h.off >= int64(len(h.f.data)) {
		return 0, io.EOF
	}
	n := copy(p, h.f.data[h.off:])
	h.off += int64(n)
	return n, nil
}

// Offset is the cursor position: bytes consumed from the file so far.
func (h *Handle) Offset() int64 { return h.off }

func (h *Handle) Close() error {
	if h.closed {
		return errors.New("logtail: double close")
	}
	h.closed = true
	h.fs.live--
	return nil
}
