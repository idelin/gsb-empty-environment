package netsec

import (
	"fmt"
	"os"
)

// FileSystem is the injectable filesystem seam used to freeze and read
// weights. Production callers normally leave Spec.FileSystem nil, which uses
// OSFileSystem. Tests can inject a filesystem that models races and
// replacement attacks without touching the host filesystem.
type FileSystem interface {
	OpenModelDir(path string) (DirHandle, error)
}

// DirHandle is an open directory handle. Implementations must resolve child
// names relative to the handle, not by re-opening the handle's original path.
type DirHandle interface {
	Lstat(name string) (Entry, error)
	OpenDir(name string) (DirHandle, error)
	OpenFile(name string) (FileHandle, error)
	Stat() (Entry, error)
	Close() error
}

// FileHandle is a read-only file handle. ReadAt must read from the open
// handle, including after the file's original path has been removed or
// replaced.
type FileHandle interface {
	ReadAt(p []byte, off int64) (int, error)
	Stat() (Entry, error)
	Close() error
}

// OSFileSystem uses *os.Root as the model-directory handle. os.Root keeps a
// descriptor for the directory, so deleting and recreating the directory's
// pathname does not redirect later operations. The implementation additionally
// checks the identity of every entry before and after opening it, which rejects
// symlink or replacement races on the paths we traverse.
type OSFileSystem struct{}

// DefaultFileSystem is used when Spec.FileSystem is nil.
var DefaultFileSystem FileSystem = OSFileSystem{}

func (OSFileSystem) OpenModelDir(path string) (DirHandle, error) {
	beforeInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("netsec: stat model directory: %w", err)
	}
	before, err := entryFromFileInfo(beforeInfo)
	if err != nil {
		return nil, err
	}
	if before.symlink() {
		return nil, fmt.Errorf("%w: model directory", ErrSymlink)
	}
	if !before.dir() {
		return nil, fmt.Errorf("%w: model directory", ErrNotDirectory)
	}

	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("netsec: open model directory: %w", err)
	}
	afterInfo, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("netsec: stat opened model directory: %w", err)
	}
	after, err := entryFromFileInfo(afterInfo)
	if err != nil {
		root.Close()
		return nil, err
	}
	if !after.dir() || !sameIdentity(before.Identity, after.Identity) {
		root.Close()
		return nil, fmt.Errorf("%w: model directory", ErrChangedDuringOpen)
	}
	return &osDir{root: root}, nil
}

type osDir struct {
	root *os.Root
}

func (d *osDir) Lstat(name string) (Entry, error) {
	info, err := d.root.Lstat(name)
	if err != nil {
		return Entry{}, err
	}
	return entryFromFileInfo(info)
}

func (d *osDir) OpenDir(name string) (DirHandle, error) {
	root, err := d.root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	return &osDir{root: root}, nil
}

func (d *osDir) OpenFile(name string) (FileHandle, error) {
	file, err := d.root.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	return &osFile{file: file}, nil
}

func (d *osDir) Stat() (Entry, error) {
	info, err := d.root.Stat(".")
	if err != nil {
		return Entry{}, err
	}
	return entryFromFileInfo(info)
}

func (d *osDir) Close() error {
	if d == nil || d.root == nil {
		return nil
	}
	return d.root.Close()
}

type osFile struct {
	file *os.File
}

func (f *osFile) ReadAt(p []byte, off int64) (int, error) {
	return f.file.ReadAt(p, off)
}

func (f *osFile) Stat() (Entry, error) {
	info, err := f.file.Stat()
	if err != nil {
		return Entry{}, err
	}
	return entryFromFileInfo(info)
}

func (f *osFile) Close() error {
	if f == nil || f.file == nil {
		return nil
	}
	return f.file.Close()
}
