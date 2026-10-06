package mask

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"sync"
)

const storeVersion = 1

type storeFile struct {
	Version int               `json:"version"`
	Entries map[string]string `json:"entries"`
}

// Store is the persistent mapping table from original values to masked
// tokens. New allocations are staged in a transaction and only become
// visible to other Store instances after a successful Commit.
type Store struct {
	mu          sync.Mutex
	path        string
	committed   map[string]string
	used        map[string]string
	pending     map[string]string
	pendingUsed map[string]string
}

// OpenStore loads the mapping table at path. A leftover temporary file
// from an interrupted commit is discarded, recovering the last fully
// committed state.
func OpenStore(path string) (*Store, error) {
	s := &Store{
		path:        path,
		committed:   make(map[string]string),
		used:        make(map[string]string),
		pending:     make(map[string]string),
		pendingUsed: make(map[string]string),
	}
	tmp := path + ".tmp"
	if _, err := os.Stat(tmp); err == nil {
		if err := os.Remove(tmp); err != nil {
			return nil, fmt.Errorf("mask: cannot remove stale temp file %s: %v", tmp, err)
		}
	}
	data, err := ioutil.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("mask: corrupt store %s: %v", path, err)
	}
	for orig, tok := range f.Entries {
		s.committed[orig] = tok
		s.used[tok] = orig
	}
	return s, nil
}

// Lookup returns the token already assigned to orig, checking staged
// (uncommitted) allocations first.
func (s *Store) Lookup(orig string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok, ok := s.pending[orig]; ok {
		return tok, true
	}
	tok, ok := s.committed[orig]
	return tok, ok
}

// Allocate stages a token for orig derived from base. If base is already
// taken by a different original (a digest collision), a numeric suffix is
// assigned: base, base-2, base-3, ...
func (s *Store) Allocate(orig, base string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok, ok := s.pending[orig]; ok {
		return tok
	}
	if tok, ok := s.committed[orig]; ok {
		return tok
	}
	tok := base
	for n := 2; ; n++ {
		owner, taken := s.used[tok]
		if !taken {
			owner, taken = s.pendingUsed[tok]
		}
		if !taken || owner == orig {
			break
		}
		tok = base + "-" + strconv.Itoa(n)
	}
	s.pending[orig] = tok
	s.pendingUsed[tok] = orig
	return tok
}

// Commit atomically publishes all staged allocations. On any failure the
// previously committed state remains intact and no partial mapping is
// visible on disk or in memory.
func (s *Store) Commit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	merged := make(map[string]string, len(s.committed)+len(s.pending))
	for k, v := range s.committed {
		merged[k] = v
	}
	for k, v := range s.pending {
		merged[k] = v
	}
	data, err := json.Marshal(storeFile{Version: storeVersion, Entries: merged})
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	s.committed = merged
	s.used = make(map[string]string, len(merged))
	for orig, tok := range merged {
		s.used[tok] = orig
	}
	s.pending = make(map[string]string)
	s.pendingUsed = make(map[string]string)
	return nil
}

// Rollback discards all staged allocations.
func (s *Store) Rollback() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = make(map[string]string)
	s.pendingUsed = make(map[string]string)
}
