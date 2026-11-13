package netsec

import "sync"

// MemStore is an in-memory snapshot store implementing both ServerStore
// and DeviceStore. Only committed snapshots are ever visible to Load, so
// restoring an older snapshot models a storage rollback after power loss.
type MemStore struct {
	mu sync.Mutex

	// BeforeCommit, when set, runs before a commit is applied and may
	// inject a fault (crash) by returning an error.
	BeforeCommit func() error

	data    []byte
	commits int
}

func (m *MemStore) load() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		return nil, nil
	}
	return append([]byte(nil), m.data...), nil
}

func (m *MemStore) commit(b []byte) error {
	if m.BeforeCommit != nil {
		if err := m.BeforeCommit(); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = append([]byte(nil), b...)
	m.commits++
	return nil
}

// Commits reports how many snapshots were durably committed.
func (m *MemStore) Commits() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.commits
}

// Snapshot returns the current committed snapshot for later Restore.
func (m *MemStore) Snapshot() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.data...)
}

// Restore rolls the store back to a previously taken snapshot, modelling
// a terminal whose ordinary storage reverts after power loss.
func (m *MemStore) Restore(snap []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = append([]byte(nil), snap...)
}

func (m *MemStore) LoadServerState() ([]byte, error) { return m.load() }
func (m *MemStore) CommitServerState(b []byte) error { return m.commit(b) }
func (m *MemStore) LoadDeviceState() ([]byte, error) { return m.load() }
func (m *MemStore) CommitDeviceState(b []byte) error { return m.commit(b) }

// MemTrustedCounter is a trusted monotonic joinEpoch counter. It never
// rolls back and rejects non-increasing commits.
type MemTrustedCounter struct {
	mu sync.Mutex

	// BeforeCommit, when set, may inject a fault. A failed commit applies
	// nothing; the counter never decreases.
	BeforeCommit func() error

	v uint64
}

func (c *MemTrustedCounter) LoadEpoch() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.v, nil
}

func (c *MemTrustedCounter) CommitEpoch(v uint64) error {
	if c.BeforeCommit != nil {
		if err := c.BeforeCommit(); err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v <= c.v {
		return ErrMonotonicViolation
	}
	c.v = v
	return nil
}
