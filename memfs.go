package kvlog

import (
	"io"
	"sync"
)

// MemFS is an in-memory FS with crash injection. Writes live in the open
// handle until Sync commits them to the durable map. Crash discards every
// write that was never synced, simulating sudden power loss. Rename is
// atomic and immediately durable.
type MemFS struct {
	mu      sync.Mutex
	files   map[string][]byte // durable state
	handles []*MemFile
}

func NewMemFS() *MemFS { return &MemFS{files: map[string][]byte{}} }

func (fs *MemFS) OpenFile(name string) (File, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	f := &MemFile{fs: fs, name: name}
	f.data = append([]byte(nil), fs.files[name]...)
	fs.handles = append(fs.handles, f)
	return f, nil
}

func (fs *MemFS) Rename(old, new string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	data, ok := fs.files[old]
	if !ok {
		return io.ErrUnexpectedEOF
	}
	fs.files[new] = data
	delete(fs.files, old)
	return nil
}

func (fs *MemFS) Remove(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	delete(fs.files, name)
	return nil
}

// Crash drops all buffered writes that were never Synced.
func (fs *MemFS) Crash() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, h := range fs.handles {
		h.data = append([]byte(nil), fs.files[h.name]...)
	}
}

// MemFile buffers writes until Sync makes them durable.
type MemFile struct {
	fs   *MemFS
	name string
	data []byte
}

func (f *MemFile) ReadAt(p []byte, off int64) (int, error) {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *MemFile) WriteAt(p []byte, off int64) (int, error) {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	end := off + int64(len(p))
	if end > int64(len(f.data)) {
		nd := make([]byte, end)
		copy(nd, f.data)
		f.data = nd
	}
	copy(f.data[off:], p)
	return len(p), nil
}

func (f *MemFile) Sync() error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	f.fs.files[f.name] = append([]byte(nil), f.data...)
	return nil
}

func (f *MemFile) Truncate(size int64) error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if size < int64(len(f.data)) {
		f.data = f.data[:size]
	}
	return nil
}

func (f *MemFile) Size() int64 {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	return int64(len(f.data))
}

func (f *MemFile) Close() error { return nil }
