package unionfs

import (
	"io"
	"io/fs"
	"os"
	"path"
	"time"
)

var (
	_ io.ReaderAt = (*fileHandle)(nil)
	_ io.Seeker   = (*fileHandle)(nil)
)

// fileHandle is a lazy random-access view onto one regular file inside one
// layer. All reads are issued directly against the backing io.ReaderAt.
type fileHandle struct {
	node *node
	sr   *io.SectionReader
}

func (f *fileHandle) Stat() (fs.FileInfo, error) { return &fileInfo{node: f.node}, nil }
func (f *fileHandle) Close() error               { return nil }
func (f *fileHandle) Read(p []byte) (int, error) { return f.sr.Read(p) }
func (f *fileHandle) ReadAt(p []byte, off int64) (int, error) {
	return f.sr.ReadAt(p, off)
}
func (f *fileHandle) Seek(offset int64, whence int) (int64, error) {
	return f.sr.Seek(offset, whence)
}

type dirHandle struct {
	union *Union
	node  *node
	read  bool
}

func (d *dirHandle) Stat() (fs.FileInfo, error) { return &fileInfo{node: d.node}, nil }
func (d *dirHandle) Close() error               { return nil }
func (d *dirHandle) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.node.path, Err: fs.ErrInvalid}
}

func (d *dirHandle) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := d.union.ReadDir(d.node.path)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		if d.read {
			return nil, nil
		}
		d.read = true
		return entries, nil
	}
	if d.read {
		return nil, io.EOF
	}
	d.read = true
	if n < len(entries) {
		return entries[:n], nil
	}
	return entries, nil
}

type symlinkHandle struct{ node *node }

func (s *symlinkHandle) Stat() (fs.FileInfo, error) { return &fileInfo{node: s.node}, nil }
func (s *symlinkHandle) Close() error               { return nil }
func (s *symlinkHandle) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: s.node.path, Err: fs.ErrInvalid}
}

type errorHandle struct {
	node *node
	err  error
}

func (e *errorHandle) Stat() (fs.FileInfo, error) { return &fileInfo{node: e.node}, nil }
func (e *errorHandle) Close() error               { return nil }
func (e *errorHandle) Read([]byte) (int, error)   { return 0, e.err }

// fileInfo implements both fs.FileInfo and fs.DirEntry.
type fileInfo struct{ node *node }

func (f *fileInfo) Name() string {
	if f.node.path == "." {
		return "."
	}
	return baseName(f.node.path)
}
func (f *fileInfo) Size() int64                { return f.node.size }
func (f *fileInfo) Mode() fs.FileMode          { return f.node.mode }
func (f *fileInfo) ModTime() time.Time         { return f.node.modTime }
func (f *fileInfo) IsDir() bool                { return f.node.typ == typeDir }
func (f *fileInfo) Sys() any                   { return nil }
func (f *fileInfo) Type() fs.FileMode          { return f.node.mode.Type() }
func (f *fileInfo) Info() (fs.FileInfo, error) { return f, nil }

func baseName(p string) string { return path.Base(p) }

var (
	_ fs.FileInfo = (*fileInfo)(nil)
	_ fs.DirEntry = (*fileInfo)(nil)
	_ os.FileInfo = (*fileInfo)(nil)
)
