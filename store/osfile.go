package store

import "os"

// osFile adapts *os.File to the File interface.
type osFile struct{ f *os.File }

// OpenPath opens (or creates) a WAL file at path.
func OpenPath(path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	st, err := Open(&osFile{f: f})
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return st, nil
}

func (o *osFile) ReadAt(p []byte, off int64) (int, error)  { return o.f.ReadAt(p, off) }
func (o *osFile) WriteAt(p []byte, off int64) (int, error) { return o.f.WriteAt(p, off) }
func (o *osFile) Sync() error                              { return o.f.Sync() }
func (o *osFile) Truncate(size int64) error                { return o.f.Truncate(size) }
func (o *osFile) Close() error                             { return o.f.Close() }

func (o *osFile) Size() int64 {
	info, err := o.f.Stat()
	if err != nil {
		return 0
	}
	return info.Size()
}
