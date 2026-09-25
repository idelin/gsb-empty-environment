// Package rootstore implements atomic publication of a root pointer through
// two metadata slots carrying sequence numbers and checksums, on top of an
// injectable persistence layer.
package rootstore

import (
	"os"
	"path/filepath"
)

// File is the minimal durable-file contract the store relies on.
//
// Assumptions the implementation depends on (see README.md):
//   - WriteAt of <= SlotSize bytes is the atomic publish unit; a torn write
//     is always detectable through the slot checksum.
//   - After Sync returns nil, previously written bytes are recoverable
//     across a power loss (the hardware does not lie about flush).
//   - Sync is an ordering barrier: bytes synced before a later write are
//     recoverable whenever that later write is recoverable.
type File interface {
	ReadAt(p []byte, off int64) (int, error)
	WriteAt(p []byte, off int64) (int, error)
	Sync() error
	Size() (int64, error)
	Close() error
}

// FS opens named files inside a caller-specified directory.
type FS interface {
	Open(name string) (File, error)
}

// OSFS stores files in a real directory on the operating system.
type OSFS struct {
	Dir string
}

func (fs OSFS) Open(name string) (File, error) {
	f, err := os.OpenFile(filepath.Join(fs.Dir, name), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	return &osFile{f: f}, nil
}

type osFile struct{ f *os.File }

func (o *osFile) ReadAt(p []byte, off int64) (int, error)  { return o.f.ReadAt(p, off) }
func (o *osFile) WriteAt(p []byte, off int64) (int, error) { return o.f.WriteAt(p, off) }
func (o *osFile) Sync() error                              { return o.f.Sync() }
func (o *osFile) Size() (int64, error) {
	st, err := o.f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
func (o *osFile) Close() error { return o.f.Close() }
