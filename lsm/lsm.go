
package lsm

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// DefaultFlushThreshold is the memtable size (in keys) that triggers a flush.
const DefaultFlushThreshold = 4096

// Engine is a minimal LSM-tree: an in-memory memtable plus a list of
// immutable on-disk SSTables (oldest first).
type Engine struct {
	dir       string
	mu        sync.RWMutex
	mem       map[string]string
	seq       int
	sstables  []string
	threshold int
}

// Open creates or reopens an engine rooted at dir, recovering the SSTable
// list left by a previous run.
func Open(dir string) (*Engine, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	e := &Engine{dir: dir, mem: make(map[string]string), threshold: DefaultFlushThreshold}
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, fi := range fis {
		name := fi.Name()
		if !strings.HasPrefix(name, "sst-") || !strings.HasSuffix(name, ".sst") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(name, "sst-%06d.sst", &n); err != nil {
			continue
		}
		e.sstables = append(e.sstables, name)
		if n > e.seq {
			e.seq = n
		}
	}
	sort.Strings(e.sstables)
	return e, nil
}

// SetFlushThreshold overrides the memtable flush threshold (mainly for tests).
func (e *Engine) SetFlushThreshold(n int) { e.threshold = n }

// Dir returns the engine's data directory.
func (e *Engine) Dir() string { return e.dir }

// SSTables returns the current SSTable file names, oldest first.
func (e *Engine) SSTables() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, len(e.sstables))
	copy(out, e.sstables)
	return out
}

// Put inserts or overwrites a key, flushing the memtable when it is full.
func (e *Engine) Put(key, value string) error {
	e.mu.RLock()
	full := len(e.mem) >= e.threshold
	e.mu.RUnlock()
	if full {
		if _, err := e.Flush(); err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.mem[key] = value
	e.mu.Unlock()
	return nil
}

// Get looks up a key in the memtable first, then in SSTables newest to oldest.
func (e *Engine) Get(key string) (string, bool, error) {
	e.mu.RLock()
	if v, ok := e.mem[key]; ok {
		e.mu.RUnlock()
		return v, true, nil
	}
	tables := make([]string, len(e.sstables))
	copy(tables, e.sstables)
	e.mu.RUnlock()

	for i := len(tables) - 1; i >= 0; i-- {
		m, err := ReadSSTable(filepath.Join(e.dir, tables[i]))
		if err != nil {
			return "", false, err
		}
		if v, ok := m[key]; ok {
			return v, true, nil
		}
	}
	return "", false, nil
}

// Flush writes the current memtable as a new immutable SSTable. It returns
// the new file name, or "" when the memtable was empty.
func (e *Engine) Flush() (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.mem) == 0 {
		return "", nil
	}
	e.seq++
	name := fmt.Sprintf("sst-%06d.sst", e.seq)
	if err := WriteSSTable(filepath.Join(e.dir, name), e.mem); err != nil {
		return "", err
	}
	e.sstables = append(e.sstables, name)
	e.mem = make(map[string]string)
	return name, nil
}

// Compact merges all SSTables into one (newest value wins) and deletes the
// old files. Deletion happens only after the merged table is durable.
func (e *Engine) Compact() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.sstables) < 2 {
		return nil
	}
	merged := make(map[string]string)
	for _, name := range e.sstables {
		m, err := ReadSSTable(filepath.Join(e.dir, name))
		if err != nil {
			return err
		}
		for k, v := range m {
			merged[k] = v
		}
	}
	e.seq++
	name := fmt.Sprintf("sst-%06d.sst", e.seq)
	if err := WriteSSTable(filepath.Join(e.dir, name), merged); err != nil {
		return err
	}
	old := e.sstables
	e.sstables = []string{name}
	for _, o := range old {
		if err := os.Remove(filepath.Join(e.dir, o)); err != nil {
			return err
		}
	}
	return nil
}
