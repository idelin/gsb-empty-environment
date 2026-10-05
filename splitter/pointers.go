package splitter

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// PointerStore commits per-key version pointers atomically (tmp + rename +
// directory fsync). A committed pointer is the only thing queries consult,
// which is what makes readers observe exactly one confirmed location even
// while a cross-target migration is in flight.
type PointerStore struct {
	dir string
}

// NewPointerStore opens (creating if needed) the pointer directory.
func NewPointerStore(dir string) (*PointerStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &PointerStore{dir: dir}, nil
}

func (p *PointerStore) pathFor(key string) string {
	return filepath.Join(p.dir, url.PathEscape(key)+".json")
}

// Commit durably installs ptr as the confirmed location for its key.
func (p *PointerStore) Commit(ptr Pointer) error {
	data, err := json.Marshal(ptr)
	if err != nil {
		return err
	}
	path := p.pathFor(ptr.Key)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return fsyncDir(p.dir)
}

// LoadAll reads every committed pointer.
func (p *PointerStore) LoadAll() (map[string]Pointer, error) {
	out := map[string]Pointer{}
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var ptr Pointer
		if err := json.Unmarshal(data, &ptr); err != nil {
			return nil, err
		}
		out[ptr.Key] = ptr
	}
	return out, nil
}
