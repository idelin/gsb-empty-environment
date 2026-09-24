// Package pathindex maintains an index from stable object IDs to
// multi-level paths. The whole index is persisted as a single JSON
// state file inside a caller-specified directory; every mutation is
// committed atomically via write-temp + fsync + rename + dir-fsync.
package pathindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

// rootID is the reserved ID of the virtual root node. The root always
// exists, has no name, and cannot be moved or deleted.
const rootID = ""

const (
	stateFileName = "state.json"
	tmpFileName   = "state.json.tmp"
)

// node is one entry of the index. The ID is the map key and never
// changes for the lifetime of the object, including across renames.
type node struct {
	Parent string `json:"parent"`
	Name   string `json:"name"`
}

// diskState is the on-disk representation of the index.
type diskState struct {
	Version uint64          `json:"version"`
	Nodes   map[string]node `json:"nodes"`
}

// FailPoint identifies a crash-injection hook used by tests and demos
// to simulate process crashes / power loss at precise commit stages.
type FailPoint string

const (
	// FailOff disables fault injection.
	FailOff FailPoint = ""
	// FailBeforeCommit aborts before the temp file is written.
	// On-disk and in-memory state are both untouched.
	FailBeforeCommit FailPoint = "beforeCommit"
	// FailAfterTmpWrite aborts after the temp file is written and
	// fsynced but before the rename. The old state.json is intact;
	// the leftover temp file is discarded on the next Open.
	FailAfterTmpWrite FailPoint = "afterTmpWrite"
	// FailAfterRename aborts after the atomic rename but before the
	// directory fsync. The new state is already visible; recovery
	// will load it. This models a crash in the commit "gap".
	FailAfterRename FailPoint = "afterRename"
)

// ErrInjected is returned by any operation aborted by a FailPoint.
var ErrInjected = errors.New("pathindex: injected failure")

// Store is the index handle. A Store is single-writer: do not use one
// Store from multiple goroutines or multiple processes concurrently.
type Store struct {
	dir     string
	version uint64
	nodes   map[string]node

	// Path cache, invalidated by version: entries are only served
	// while cacheVersion == version.
	cache        map[string]string
	cacheVersion uint64

	// FailAt, when set, makes the next commit abort at the given
	// point with ErrInjected. Exported for tests/fault injection.
	FailAt FailPoint
}

// Open loads (or initializes) the index stored in dir. Recovery runs
// here: leftover temp files from a crashed commit are removed, and
// state.json always holds the last fully committed version.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	// Recovery: a temp file can only come from a commit that crashed
	// before rename, so it is safe to discard.
	tmp := filepath.Join(dir, tmpFileName)
	if _, err := os.Stat(tmp); err == nil {
		if err := os.Remove(tmp); err != nil {
			return nil, fmt.Errorf("pathindex: remove stale temp file: %v", err)
		}
	}

	s := &Store{dir: dir, cache: make(map[string]string)}
	data, err := ioutil.ReadFile(filepath.Join(dir, stateFileName))
	if os.IsNotExist(err) {
		s.nodes = map[string]node{rootID: {}}
		s.version = 0
		if err := s.commitLocked(s.snapshot()); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var st diskState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("pathindex: corrupt state file: %v", err)
	}
	if st.Nodes == nil {
		st.Nodes = map[string]node{}
	}
	if _, ok := st.Nodes[rootID]; !ok {
		st.Nodes[rootID] = node{}
	}
	s.nodes = st.Nodes
	s.version = st.Version
	return s, nil
}

// Version returns the current committed version. It increases by one
// on every successful mutation and is the invalidation key of the
// path cache.
func (s *Store) Version() uint64 { return s.version }

func (s *Store) snapshot() diskState {
	next := make(map[string]node, len(s.nodes))
	for id, n := range s.nodes {
		next[id] = n
	}
	return diskState{Version: s.version, Nodes: next}
}

// commitLocked durably persists next and, only on full success, swaps
// it into memory. Any failure (real or injected) leaves both the
// on-disk state.json and the in-memory state untouched, except for
// FailAfterRename where the new state is already on disk (recovery
// will observe it, which is still atomic: old or new, never torn).
func (s *Store) commitLocked(next diskState) error {
	if s.FailAt == FailBeforeCommit {
		return ErrInjected
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, tmpFileName)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil { // flush temp file before rename
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if s.FailAt == FailAfterTmpWrite {
		return ErrInjected
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, stateFileName)); err != nil {
		return err
	}
	if s.FailAt == FailAfterRename {
		return ErrInjected
	}
	if err := syncDir(s.dir); err != nil { // make the rename itself durable
		return err
	}
	s.nodes = next.Nodes
	s.version = next.Version
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// path computes the absolute path of id, serving and filling the
// version-keyed cache.
func (s *Store) path(id string) (string, error) {
	if s.cacheVersion != s.version {
		s.cache = make(map[string]string)
		s.cacheVersion = s.version
	}
	if p, ok := s.cache[id]; ok {
		return p, nil
	}
	if _, ok := s.nodes[id]; !ok {
		return "", fmt.Errorf("pathindex: unknown id %q", id)
	}
	var parts []string
	for cur := id; cur != rootID; {
		n := s.nodes[cur]
		parts = append(parts, n.Name)
		cur = n.Parent
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	p := "/" + strings.Join(parts, "/")
	s.cache[id] = p
	return p, nil
}
