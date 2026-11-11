package timetrust

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LowerBoundStore persists only the trusted lower bound. Nothing else is
// durable: across a reboot the upper bound is deliberately forgotten, which
// forces the host to obtain a fresh signed proof before installing anything.
type LowerBoundStore interface {
	// Load returns the persisted lower bound. A zero time with a nil error
	// means no bound has ever been committed.
	Load() (time.Time, error)
	// Save commits lowerBound durably. Implementations must guarantee that a
	// returned error means the previous committed value is still what a
	// subsequent Load observes: a failed save may never partially overwrite
	// the old bound.
	Save(lowerBound time.Time) error
}

// ErrCorruptStore means persisted bytes cannot be decoded.
var ErrCorruptStore = errors.New("timetrust: corrupt lower-bound store")

const (
	fileStoreMagic = "TTRUSTLB01"
	fileStoreSize  = len(fileStoreMagic) + 8
)

// FileStore keeps the lower bound in a single file, replaced atomically via
// rename so an interrupted or failing write can never corrupt the previous
// commit. The encoding is a fixed-size prefix plus a big-endian Unix-nano
// timestamp.
type FileStore struct {
	path string
}

// NewFileStore opens (without creating) the store at path.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Load implements LowerBoundStore.
func (s *FileStore) Load() (time.Time, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("timetrust: loading lower bound: %w", err)
	}
	if len(data) != fileStoreSize || string(data[:len(fileStoreMagic)]) != fileStoreMagic {
		return time.Time{}, ErrCorruptStore
	}
	nanos := int64(binary.BigEndian.Uint64(data[len(fileStoreMagic):]))
	if nanos <= 0 {
		return time.Time{}, ErrCorruptStore
	}
	return time.Unix(0, nanos).UTC(), nil
}

// Save implements LowerBoundStore.
func (s *FileStore) Save(lowerBound time.Time) error {
	if lowerBound.IsZero() {
		return errors.New("timetrust: refusing to persist a zero lower bound")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("timetrust: preparing lower-bound directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".timetrust-*")
	if err != nil {
		return fmt.Errorf("timetrust: staging lower bound: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	var record [fileStoreSize]byte
	copy(record[:len(fileStoreMagic)], fileStoreMagic)
	binary.BigEndian.PutUint64(record[len(fileStoreMagic):],
		uint64(lowerBound.UnixNano()))
	if _, err := tmp.Write(record[:]); err != nil {
		tmp.Close()
		return fmt.Errorf("timetrust: writing lower bound: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("timetrust: syncing lower bound: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("timetrust: closing lower bound: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("timetrust: committing lower bound: %w", err)
	}
	return nil
}
