package packer

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
)

// ManifestEntry records the state of one packaged path for incremental
// runs.
type ManifestEntry struct {
	Type  string `json:"type"`
	Size  int64  `json:"size,omitempty"`
	Mtime int64  `json:"mtime,omitempty"`
	Link  string `json:"link,omitempty"`
}

// Manifest is the persisted snapshot of a previous packaging run.
type Manifest struct {
	Entries map[string]ManifestEntry `json:"entries"`
}

// LoadManifest reads a manifest; a missing file yields an error so callers
// can treat the first run as a full package.
func LoadManifest(path string) (Manifest, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	if m.Entries == nil {
		m.Entries = map[string]ManifestEntry{}
	}
	return m, nil
}

// SaveManifest writes the manifest as indented JSON.
func SaveManifest(path string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}

// BuildManifest snapshots the given entries under root. Files record size
// and mtime for change detection; symlinks record only their target.
func BuildManifest(root string, entries []Entry) (Manifest, error) {
	m := Manifest{Entries: map[string]ManifestEntry{}}
	for _, e := range entries {
		me := ManifestEntry{Type: e.Type.String(), Link: e.LinkTarget}
		if e.Type == EntryFile {
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(e.Path)))
			if err != nil {
				return m, err
			}
			me.Size = info.Size()
			me.Mtime = info.ModTime().UnixNano()
		}
		m.Entries[e.Path] = me
	}
	return m, nil
}

// Diff compares the freshly built manifest against the previous one and
// returns the paths that were added or changed, and the paths that were
// deleted, both in sorted order.
func (old Manifest) Diff(cur Manifest) (changed, deleted []string) {
	for path, me := range cur.Entries {
		prev, ok := old.Entries[path]
		if !ok || prev != me {
			changed = append(changed, path)
		}
	}
	for path := range old.Entries {
		if _, ok := cur.Entries[path]; !ok {
			deleted = append(deleted, path)
		}
	}
	sort.Strings(changed)
	sort.Strings(deleted)
	return changed, deleted
}
