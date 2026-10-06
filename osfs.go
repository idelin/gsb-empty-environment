package kvlog

import (
	"os"
	"path/filepath"
)

// OSFS is the real-filesystem implementation of FS, rooted at a directory.
type OSFS struct {
	dir string
}

func NewOSFS(dir string) *OSFS { return &OSFS{dir: dir} }

func (fs *OSFS) OpenFile(name string) (File, error) {
	f, err := os.OpenFile(filepath.Join(fs.dir, name), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	return osFile{f}, nil
}

func (fs *OSFS) Rename(old, new string) error {
	return os.Rename(filepath.Join(fs.dir, old), filepath.Join(fs.dir, new))
}

func (fs *OSFS) Remove(name string) error {
	err := os.Remove(filepath.Join(fs.dir, name))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type osFile struct{ *os.File }

func (f osFile) Size() int64 {
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	return fi.Size()
}
