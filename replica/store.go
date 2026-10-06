package replica

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Meta is the durable hard-state: current term and commit index.
type Meta struct {
	Term   uint64 `json:"term"`
	Commit uint64 `json:"commit"`
}

// Cut points where a crash or persistence fault can be injected. Save
// persists the log first, then the meta, so meta never references entries
// that are not durable.
const (
	PointSaveBegin     = "save.begin"
	PointLogPersisted  = "log.persisted"
	PointMetaPersisted = "meta.persisted"
	PointSaveDone      = "save.done"
)

// Hook is invoked at each persistence cut point. Returning an error fails
// the save; panicking simulates a power-loss crash (the process image is
// discarded by the caller, only durable files survive).
type Hook func(point string) error

// Store is a file-backed durable store. Each file is replaced atomically
// (write tmp + fsync + rename) so a crash never tears a file.
type Store struct {
	dir  string
	hook Hook
}

func OpenStore(dir string, hook Hook) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir, hook: hook}, nil
}

func (s *Store) logPath() string  { return filepath.Join(s.dir, "log.json") }
func (s *Store) metaPath() string { return filepath.Join(s.dir, "meta.json") }

func (s *Store) mark(point string) error {
	if s.hook == nil {
		return nil
	}
	return s.hook(point)
}

// Load recovers the durable state. Missing files mean a fresh replica.
func (s *Store) Load() (Meta, []Entry, error) {
	var meta Meta
	var entries []Entry
	if b, err := os.ReadFile(s.metaPath()); err == nil {
		if err := json.Unmarshal(b, &meta); err != nil {
			return meta, nil, err
		}
	} else if !os.IsNotExist(err) {
		return meta, nil, err
	}
	if b, err := os.ReadFile(s.logPath()); err == nil {
		if err := json.Unmarshal(b, &entries); err != nil {
			return meta, nil, err
		}
	} else if !os.IsNotExist(err) {
		return meta, nil, err
	}
	return meta, entries, nil
}

// Save durably persists entries and meta. It fails (or crashes, if the
// hook panics) before any acknowledgement may be sent.
func (s *Store) Save(meta Meta, entries []Entry) error {
	if err := s.mark(PointSaveBegin); err != nil {
		return err
	}
	if err := s.writeFileAtomic(s.logPath(), entries); err != nil {
		return err
	}
	if err := s.mark(PointLogPersisted); err != nil {
		return err
	}
	if err := s.writeFileAtomic(s.metaPath(), meta); err != nil {
		return err
	}
	if err := s.mark(PointMetaPersisted); err != nil {
		return err
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	return s.mark(PointSaveDone)
}

func (s *Store) writeFileAtomic(path string, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
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
	return os.Rename(tmp, path)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
