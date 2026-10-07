package seq

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"
)

// FileStore is a Store backed by a single JSON file. Cross-process mutual
// exclusion is provided by an flock on a sibling lock file, and each update
// is written to a temp file, fsynced, and atomically renamed into place.
type FileStore struct {
	path     string
	lockPath string
}

// NewFileStore returns a FileStore persisting state at path.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path, lockPath: path + ".lock"}
}

// Load implements Store.
func (s *FileStore) Load() (State, error) {
	var st State
	err := s.locked(func() error {
		var err error
		st, err = s.load()
		return err
	})
	return st, err
}

// CompareAndSwap implements Store.
func (s *FileStore) CompareAndSwap(expect, next State) (bool, error) {
	var ok bool
	err := s.locked(func() error {
		cur, err := s.load()
		if err != nil {
			return err
		}
		if cur != expect {
			return nil
		}
		if err := s.persist(next); err != nil {
			return err
		}
		ok = true
		return nil
	})
	return ok, err
}

func (s *FileStore) locked(fn func() error) error {
	f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (s *FileStore) load() (State, error) {
	data, err := ioutil.ReadFile(s.path)
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, err
	}
	return st, nil
}

func (s *FileStore) persist(st State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}
