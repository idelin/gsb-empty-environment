package blockgc

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Manifest is the versioned root list. Writers Publish new versions
// (adding or revoking roots); readers Pin the version they observe so the
// GC keeps its roots alive even after the manifest moves on. ActiveRoots is
// the union of the current version and every pinned version.
//
// Versions are persisted under manifest/ so a restart keeps the root
// history; pins belong to live reader sessions and are in-memory only.
type Manifest struct {
	dir string

	mu      sync.Mutex
	current int              // current version number, 0 = none yet
	roots   map[int][]string // version -> root IDs (lazy cache)
	pins    map[string]int   // reader ID -> pinned version
}

// OpenManifest opens (or creates) the manifest stored under dir/manifest.
func OpenManifest(dir string) (*Manifest, error) {
	mdir := filepath.Join(dir, "manifest")
	if err := os.MkdirAll(mdir, 0755); err != nil {
		return nil, err
	}
	m := &Manifest{
		dir:   mdir,
		roots: map[int][]string{},
		pins:  map[string]int{},
	}
	data, err := ioutil.ReadFile(filepath.Join(mdir, "CURRENT"))
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("corrupt CURRENT: %v", err)
	}
	m.current = v
	return m, nil
}

// Publish atomically installs a new root list and returns its version.
// Revoking a root is simply publishing a list without it.
func (m *Manifest) Publish(roots []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.current + 1
	data, err := json.Marshal(map[string][]string{"roots": roots})
	if err != nil {
		return 0, err
	}
	if err := writeFileAtomic(m.versionPath(v), data); err != nil {
		return 0, err
	}
	if err := writeFileAtomic(filepath.Join(m.dir, "CURRENT"), []byte(strconv.Itoa(v))); err != nil {
		return 0, err
	}
	m.current = v
	m.roots[v] = append([]string(nil), roots...)
	return v, nil
}

// Pin records that reader holds the current version; its roots stay
// protected until Unpin. Returns the pinned version.
func (m *Manifest) Pin(reader string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pins[reader] = m.current
	return m.current
}

// Unpin releases the reader's hold on its version.
func (m *Manifest) Unpin(reader string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pins, reader)
}

// ActiveRoots returns the union of roots across the current version and all
// pinned versions. This is the set the GC must treat as reachable sources.
func (m *Manifest) ActiveRoots() []string {
	m.mu.Lock()
	versions := map[int]bool{m.current: true}
	for _, v := range m.pins {
		versions[v] = true
	}
	m.mu.Unlock()

	seen := map[string]bool{}
	var out []string
	for v := range versions {
		for _, r := range m.rootsOf(v) {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}

func (m *Manifest) rootsOf(v int) []string {
	m.mu.Lock()
	if r, ok := m.roots[v]; ok {
		m.mu.Unlock()
		return r
	}
	m.mu.Unlock()

	data, err := ioutil.ReadFile(m.versionPath(v))
	if err != nil {
		return nil // version file gone: nothing to protect
	}
	var parsed struct {
		Roots []string `json:"roots"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	m.mu.Lock()
	m.roots[v] = parsed.Roots
	m.mu.Unlock()
	return parsed.Roots
}

func (m *Manifest) versionPath(v int) string {
	return filepath.Join(m.dir, fmt.Sprintf("v%06d.json", v))
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := ioutil.TempFile(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
