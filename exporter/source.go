package exporter

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrSnapshotExpired is returned by ReadPage when the requested snapshot no
// longer matches the source's current snapshot.
var ErrSnapshotExpired = errors.New("exporter: snapshot expired")

// Record is a single key/value row of the exported dataset.
type Record struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Page is one chunk of a snapshot scan.
type Page struct {
	Records []Record
	// NextToken resumes the scan after this page; empty means the scan is done.
	NextToken string
}

// Source is a paged, snapshot-consistent read interface.
type Source interface {
	// SnapshotID identifies the current immutable snapshot of the data.
	SnapshotID() string
	// ReadPage returns up to limit records after token ("" = from the start),
	// all pinned to the given snapshot.
	ReadPage(snapshotID, token string, limit int) (Page, error)
}

// MemSource is an in-memory Source. Every mutation rotates the snapshot ID,
// so reads pinned to an older snapshot fail with ErrSnapshotExpired.
type MemSource struct {
	mu        sync.RWMutex
	data      map[string]string
	version   int
	readCount int // counts ReadPage calls, for tests
}

func NewMemSource() *MemSource {
	return &MemSource{data: map[string]string{}, version: 1}
}

func (m *MemSource) SnapshotID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return fmt.Sprintf("snap-%d", m.version)
}

func (m *MemSource) Put(key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	m.version++
}

func (m *MemSource) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	m.version++
}

// ReadCount reports how many pages have been read (test instrumentation).
func (m *MemSource) ReadCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.readCount
}

func (m *MemSource) ReadPage(snapshotID, token string, limit int) (Page, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if snapshotID != fmt.Sprintf("snap-%d", m.version) {
		return Page{}, ErrSnapshotExpired
	}
	m.readCount++
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		if k > token {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	page := Page{Records: make([]Record, 0, len(keys))}
	for _, k := range keys {
		page.Records = append(page.Records, Record{Key: k, Value: m.data[k]})
	}
	if len(keys) > 0 {
		last := keys[len(keys)-1]
		// More records remain beyond this page?
		for k := range m.data {
			if k > last {
				page.NextToken = last
				break
			}
		}
	}
	return page, nil
}
