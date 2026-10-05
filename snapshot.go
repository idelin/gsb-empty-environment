package publish

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Snapshot is a read handle pinned to one committed version. All reads
// come from that version's immutable directory, so a Snapshot keeps
// serving its original version even after newer versions are published.
type Snapshot struct {
	Version  string
	dir      string
	manifest Manifest
	names    []string
}

// Acquire returns a read handle for the currently committed version.
func (s *Store) Acquire() (*Snapshot, error) {
	s.mu.RLock()
	id := s.current
	s.mu.RUnlock()
	if id == "" {
		return nil, ErrNoVersion
	}
	dir := filepath.Join(s.root, versionsDirName, id)
	m, err := loadManifest(dir)
	if err != nil {
		return nil, fmt.Errorf("publish: version %s unreadable: %w", id, err)
	}
	names := make([]string, 0, len(m.Files))
	for name := range m.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return &Snapshot{Version: id, dir: dir, manifest: m, names: names}, nil
}

// Files lists the data files of the pinned version.
func (sn *Snapshot) Files() []string {
	out := make([]string, len(sn.names))
	copy(out, sn.names)
	return out
}

// Open opens one file of the pinned version for reading.
func (sn *Snapshot) Open(name string) (*os.File, error) {
	if _, ok := sn.manifest.Files[name]; !ok {
		return nil, fmt.Errorf("publish: %q not in version %s", name, sn.Version)
	}
	return os.Open(filepath.Join(sn.dir, name))
}

// ReadFile reads one file of the pinned version.
func (sn *Snapshot) ReadFile(name string) ([]byte, error) {
	if _, ok := sn.manifest.Files[name]; !ok {
		return nil, fmt.Errorf("publish: %q not in version %s", name, sn.Version)
	}
	return os.ReadFile(filepath.Join(sn.dir, name))
}

// ReadAll reads every file of the pinned version.
func (sn *Snapshot) ReadAll() (map[string][]byte, error) {
	out := make(map[string][]byte, len(sn.names))
	for _, name := range sn.names {
		data, err := sn.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out[name] = data
	}
	return out, nil
}
