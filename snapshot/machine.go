// Package snapshot provides low-pause incremental snapshots and immutable
// checkpoint archiving for a replicated state machine.
package snapshot

import (
	"errors"
	"fmt"
	"sync"
)

// Op is a single state-machine mutation applied at a log index.
type Op struct {
	Key    string
	Value  string
	Delete bool
}

// Put returns a put Op.
func Put(key, value string) Op { return Op{Key: key, Value: value} }

// Del returns a delete Op.
func Del(key string) Op { return Op{Key: key, Delete: true} }

// Change is the per-key delta recorded in an incremental segment.
type Change struct {
	Value   string
	Deleted bool
}

// Machine is an ordered KV state machine. It tracks, per key, the mutations
// since the last captured incremental segment so that CaptureIncremental is
// O(dirty keys) with a sub-microsecond critical section (a map swap).
type Machine struct {
	mu        sync.RWMutex
	data      map[string]string
	dirty     map[string]Change
	lastIndex uint64
	lastTerm  uint64
	covered   uint64 // log index covered by the last captured segment
}

func NewMachine() *Machine {
	return &Machine{
		data:  make(map[string]string),
		dirty: make(map[string]Change),
	}
}

// ErrOutOfOrder is returned when Apply receives an index != lastIndex+1.
var ErrOutOfOrder = errors.New("snapshot: apply out of order")

// Apply applies op at the given log index. Indexes must be contiguous
// starting at 1, matching the hole-free commit engine.
func (m *Machine) Apply(index, term uint64, op Op) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index != m.lastIndex+1 {
		return fmt.Errorf("%w: got %d, want %d", ErrOutOfOrder, index, m.lastIndex+1)
	}
	if op.Delete {
		delete(m.data, op.Key)
		m.dirty[op.Key] = Change{Deleted: true}
	} else {
		m.data[op.Key] = op.Value
		m.dirty[op.Key] = Change{Value: op.Value}
	}
	m.lastIndex = index
	m.lastTerm = term
	return nil
}

// Get returns the value for key.
func (m *Machine) Get(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	return v, ok
}

// Len returns the number of live keys.
func (m *Machine) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.data)
}

// LastIndex returns the last applied log index.
func (m *Machine) LastIndex() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastIndex
}

// LastTerm returns the term of the last applied log entry.
func (m *Machine) LastTerm() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastTerm
}

// Keys returns a snapshot of all live key/value pairs.
func (m *Machine) Keys() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.data))
	for k, v := range m.data {
		out[k] = v
	}
	return out
}

// swapDirty atomically detaches the dirty set for archiving and returns a
// rollback function: on archive failure the detached changes are merged back
// (without clobbering newer mutations); on success the covered watermark
// advances. The critical section is a single map swap.
func (m *Machine) swapDirty() (Segment, func(error)) {
	m.mu.Lock()
	seg := Segment{
		Base:     m.covered,
		Last:     m.lastIndex,
		LastTerm: m.lastTerm,
		Changes:  m.dirty,
	}
	m.dirty = make(map[string]Change)
	m.mu.Unlock()
	return seg, func(err error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if err != nil {
			for k, v := range seg.Changes {
				if _, ok := m.dirty[k]; !ok {
					m.dirty[k] = v
				}
			}
			return
		}
		m.covered = seg.Last
	}
}

// checkpoint copies the full state. It holds the read lock for the copy;
// incremental capture is the lightweight path, checkpoints are infrequent.
func (m *Machine) checkpoint() Checkpoint {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data := make(map[string]string, len(m.data))
	for k, v := range m.data {
		data[k] = v
	}
	return Checkpoint{LastIndex: m.lastIndex, LastTerm: m.lastTerm, Data: data}
}

// load installs a checkpoint and replays segments on top. The machine must
// be empty.
func (m *Machine) load(cp Checkpoint, segs []Segment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastIndex != 0 || len(m.data) != 0 {
		return errors.New("snapshot: restore into non-empty machine")
	}
	for k, v := range cp.Data {
		m.data[k] = v
	}
	m.lastIndex = cp.LastIndex
	m.lastTerm = cp.LastTerm
	for _, seg := range segs {
		for k, ch := range seg.Changes {
			if ch.Deleted {
				delete(m.data, k)
			} else {
				m.data[k] = ch.Value
			}
		}
		m.lastIndex = seg.Last
		m.lastTerm = seg.LastTerm
	}
	m.covered = m.lastIndex
	return nil
}
