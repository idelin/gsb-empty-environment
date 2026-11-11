package hlc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// State is the durable lower bound of the clock. Every timestamp returned to
// a caller is persisted as State before it is returned, so after a restart the
// clock can never again emit a timestamp equal to or lower than one already
// handed out (see Clock.Now and Clock.Receive).
type State struct {
	Physical int64
	Logical  uint16
}

// Store persists the clock's monotonic lower bound. Implementations must be
// durable once Save returns nil: a crash (power loss included) followed by
// Load must observe the saved state. Save is always called while the Clock
// lock is held, so stores do not need their own locking against the clock,
// but they may be called again after a failed attempt and must remain usable.
type Store interface {
	// Save durably persists s. A non-nil error means the state may not be
	// durable; the clock then discards the candidate value and keeps its
	// previous lower bound, so no un-persisted value is ever returned.
	Save(s State) error
	// Load returns the most recently persisted state. A missing store yields
	// the zero state and no error.
	Load() (State, error)
}

// stateMagic is the little "HLC1" prefix used by the on-disk state file.
var stateMagic = [4]byte{'H', 'L', 'C', '1'}

// stateFileSize is magic(4) + physical(8) + logical(2).
const stateFileSize = 4 + 8 + 2

// FileStore durably persists clock state in a single file.
//
// Writes go to a temp file in the same directory which is fsynced and then
// atomically renamed over the target, followed by an fsync of the directory.
// This guarantees that a crash leaves either the old or the new complete
// state, never a torn file.
type FileStore struct {
	path string
	dir  string
}

// NewFileStore creates a FileStore backed by path. Parent directories are
// created on demand.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path, dir: filepath.Dir(path)}
}

// Save implements Store.
func (f *FileStore) Save(s State) error {
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return fmt.Errorf("hlc: create state directory: %w", err)
	}

	tmp, err := os.CreateTemp(f.dir, ".hlc-state-*")
	if err != nil {
		return fmt.Errorf("hlc: create state temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	buf := make([]byte, stateFileSize)
	copy(buf[0:4], stateMagic[:])
	binary.BigEndian.PutUint64(buf[4:12], uint64(s.Physical))
	binary.BigEndian.PutUint16(buf[12:14], s.Logical)

	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("hlc: write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("hlc: fsync state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("hlc: close state: %w", err)
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		cleanup()
		return fmt.Errorf("hlc: rename state: %w", err)
	}
	if err := syncDir(f.dir); err != nil {
		return fmt.Errorf("hlc: fsync state directory: %w", err)
	}
	return nil
}

// Load implements Store.
func (f *FileStore) Load() (State, error) {
	file, err := os.Open(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("hlc: open state: %w", err)
	}
	defer file.Close()

	buf := make([]byte, stateFileSize)
	if _, err := io.ReadFull(file, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return State{}, fmt.Errorf("hlc: state file is truncated: %w", err)
		}
		return State{}, fmt.Errorf("hlc: read state: %w", err)
	}
	var magic [4]byte
	copy(magic[:], buf[0:4])
	if magic != stateMagic {
		return State{}, errors.New("hlc: state file has bad magic")
	}
	return State{
		Physical: int64(binary.BigEndian.Uint64(buf[4:12])),
		Logical:  binary.BigEndian.Uint16(buf[12:14]),
	}, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// MemStore is an in-memory Store, mainly for tests. Its state survives
// simulated process restarts (the same MemStore instance is reused), which
// models the disk outliving the process. Fail and FailNext inject failures.
type MemStore struct {
	state  State
	exists bool

	// Fail makes every Save return ErrInjected while true.
	Fail bool
	// FailOnce makes the next Save fail exactly once, regardless of Fail.
	FailOnce bool

	saves int
}

// ErrInjected is returned by fault-injecting stores in tests.
var ErrInjected = errors.New("hlc: injected store failure")

// Save implements Store.
func (m *MemStore) Save(s State) error {
	m.saves++
	if m.Fail || m.FailOnce {
		m.FailOnce = false
		return ErrInjected
	}
	m.state = s
	m.exists = true
	return nil
}

// Load implements Store.
func (m *MemStore) Load() (State, error) {
	if !m.exists {
		return State{}, nil
	}
	return m.state, nil
}

// Saves reports how many Save calls (failed or successful) happened.
func (m *MemStore) Saves() int { return m.saves }
