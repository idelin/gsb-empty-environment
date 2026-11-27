package oci

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"sort"
	"time"
)

// Open implements fs.FS. Symbolic links are followed inside the synthesized
// root; links that escape the root are denied.
func (f *FileSystem) Open(name string) (fs.File, error) {
	if f == nil || f.Root == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "/" {
		name = "."
	}
	if clean, err := CleanPath(name); err == nil {
		name = clean
	}
	if !fs.ValidPath(name) && name != "." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}

	node, _, err := f.ResolvePath(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: mapFSError(err)}
	}
	if node == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	info := nodeFileInfo{node: node}
	if node.IsDir() {
		children := make([]string, 0, len(node.Children))
		for childName := range node.Children {
			children = append(children, childName)
		}
		sort.Strings(children)
		entries := make([]fs.DirEntry, 0, len(children))
		for _, childName := range children {
			child := node.Children[childName]
			childInfo := nodeFileInfo{node: child}
			entries = append(entries, fs.FileInfoToDirEntry(childInfo))
		}
		return &virtualDir{info: info, entries: entries}, nil
	}

	return &virtualFile{
		info:   info,
		reader: bytes.NewReader(node.Content),
	}, nil
}

// ReadDir implements fs.ReadDirFS.
func (f *FileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	file, err := f.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	dir, ok := file.(fs.ReadDirFile)
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	return dir.ReadDir(-1)
}

// ReadFile is a root-path-tolerant convenience wrapper around fs.ReadFile.
func (f *FileSystem) ReadFile(name string) ([]byte, error) {
	clean, err := CleanPath(name)
	if err != nil {
		return nil, &fs.PathError{Op: "readfile", Path: name, Err: mapFSError(err)}
	}
	file, err := f.Open(clean)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

// StatInfo returns fs.FileInfo without changing the existing Node-returning
// Stat API.
func (f *FileSystem) StatInfo(name string) (fs.FileInfo, error) {
	node, _, err := f.ResolvePath(name)
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: mapFSError(err)}
	}
	return nodeFileInfo{node: node}, nil
}

func mapFSError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrPathEscape):
		return fs.ErrPermission
	case errors.Is(err, ErrInvalidPath):
		return fs.ErrInvalid
	default:
		return fs.ErrNotExist
	}
}

type nodeFileInfo struct {
	node *Node
}

func (i nodeFileInfo) Name() string {
	if i.node == nil || i.node.Path == "." {
		return "/"
	}
	return i.node.Name
}

func (i nodeFileInfo) Size() int64 {
	if i.node == nil {
		return 0
	}
	if i.node.Size != 0 {
		return i.node.Size
	}
	return int64(len(i.node.Content))
}

func (i nodeFileInfo) Mode() fs.FileMode {
	if i.node == nil {
		return 0
	}
	return i.node.OSMode()
}

func (i nodeFileInfo) ModTime() time.Time {
	if i.node == nil || i.node.ModTime.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return i.node.ModTime
}

func (i nodeFileInfo) IsDir() bool { return i.node != nil && i.node.IsDir() }

func (i nodeFileInfo) Sys() any { return i.node }

type virtualFile struct {
	info   fs.FileInfo
	reader *bytes.Reader
}

func (f *virtualFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *virtualFile) Read(p []byte) (int, error) { return f.reader.Read(p) }
func (f *virtualFile) Close() error               { return nil }

var _ io.Reader = (*virtualFile)(nil)

type virtualDir struct {
	info    fs.FileInfo
	entries []fs.DirEntry
	offset  int
}

func (d *virtualDir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *virtualDir) Close() error               { return nil }

func (d *virtualDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.Name(), Err: fs.ErrInvalid}
}

func (d *virtualDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if n <= 0 {
		entries := append([]fs.DirEntry(nil), d.entries[d.offset:]...)
		d.offset = len(d.entries)
		return entries, nil
	}
	if d.offset >= len(d.entries) {
		return nil, io.EOF
	}
	end := d.offset + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	entries := append([]fs.DirEntry(nil), d.entries[d.offset:end]...)
	d.offset = end
	if d.offset == len(d.entries) && n > len(entries) {
		return entries, io.EOF
	}
	return entries, nil
}

var (
	_ fs.FS          = (*FileSystem)(nil)
	_ fs.ReadDirFS   = (*FileSystem)(nil)
	_ fs.ReadDirFile = (*virtualDir)(nil)
)
