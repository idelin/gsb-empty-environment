package lsm

import (
	"errors"
	"sort"
	"sync"
)

// ErrStaleManifest is returned when the manifest changed between compaction
// selection and publish; the caller should re-select and retry.
var ErrStaleManifest = errors.New("lsm: manifest changed before publish")

// Manifest tracks which segments are live. Commits are atomic: a reader
// observing the manifest sees either the complete old set or the complete
// new set of segments.
type Manifest struct {
	mu      sync.Mutex
	version uint64
	live    map[uint64]*Segment
	// CommitHook, when set, runs inside the commit critical section before
	// any change is applied. A non-nil return aborts the commit atomically.
	// It exists to exercise publish-failure paths in tests.
	CommitHook func() error
}

// NewManifest returns an empty manifest.
func NewManifest() *Manifest {
	return &Manifest{live: make(map[uint64]*Segment)}
}

// Version returns the current manifest version.
func (m *Manifest) Version() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version
}

// Live returns the currently live segments, ordered by ID.
func (m *Manifest) Live() []*Segment {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Segment, 0, len(m.live))
	for _, s := range m.live {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// AddSegment publishes a single new segment (used for initial loads and
// memtable flushes outside of compaction).
func (m *Manifest) AddSegment(s *Segment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.live[s.ID] = s
	m.version++
}

// Commit atomically replaces the remove set with the add set, provided the
// manifest is still at expect. On any failure nothing changes.
func (m *Manifest) Commit(expect uint64, add []*Segment, remove []uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.CommitHook != nil {
		if err := m.CommitHook(); err != nil {
			return err
		}
	}
	if expect != m.version {
		return ErrStaleManifest
	}
	for _, id := range remove {
		delete(m.live, id)
	}
	for _, s := range add {
		m.live[s.ID] = s
	}
	m.version++
	return nil
}
