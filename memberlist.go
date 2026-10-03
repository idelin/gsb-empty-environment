package noderouter

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrUnknownNode is returned when operating on a node that is not a member.
var ErrUnknownNode = errors.New("noderouter: unknown node")

// MemberList is a concurrency-safe, versioned registry of cluster members.
// It is the local replica of the cluster membership on every node.
type MemberList struct {
	mu    sync.RWMutex
	self  string
	nodes map[string]NodeState
	now   func() time.Time // injectable clock for tests
}

// NewMemberList creates a MemberList whose local node has the given ID.
func NewMemberList(selfID string) *MemberList {
	return &MemberList{
		self:  selfID,
		nodes: make(map[string]NodeState),
		now:   time.Now,
	}
}

// Join registers the local node with its initial metadata and labels.
func (m *MemberList) Join(addr string, meta Metadata, labels map[string]string) NodeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]string, len(labels))
	for k, v := range labels {
		cp[k] = v
	}
	s := NodeState{
		ID:        m.self,
		Addr:      addr,
		Labels:    cp,
		Meta:      meta,
		Version:   1,
		UpdatedAt: m.now(),
	}
	m.nodes[m.self] = s
	return s.Clone()
}

// UpdateLocal applies a mutation to the local node's metadata, bumping the
// version. It is the only path that advances the local node's version clock,
// which makes concurrent updates from multiple goroutines safe: every
// mutation is serialized under the lock and carries a strictly newer version.
func (m *MemberList) UpdateLocal(mutate func(meta *Metadata)) (NodeState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.nodes[m.self]
	if !ok {
		return NodeState{}, ErrUnknownNode
	}
	mutate(&s.Meta)
	s.Version++
	s.UpdatedAt = m.now()
	m.nodes[m.self] = s
	return s.Clone(), nil
}

// Merge integrates a batch of states received via gossip (or any other
// replication channel). Each entry is arbitrated by version; stale entries
// are dropped. It returns the number of states actually applied.
func (m *MemberList) Merge(incoming []NodeState) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	applied := 0
	for _, s := range incoming {
		cur, ok := m.nodes[s.ID]
		if !ok || s.supersedes(cur) {
			m.nodes[s.ID] = s.Clone()
			applied++
		}
	}
	return applied
}

// Get returns a copy of the state of one member.
func (m *MemberList) Get(id string) (NodeState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.nodes[id]
	return s.Clone(), ok
}

// Snapshot returns a copy of every known member state, sorted by ID.
func (m *MemberList) Snapshot() []NodeState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]NodeState, 0, len(m.nodes))
	for _, s := range m.nodes {
		out = append(out, s.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Self returns the local node's state.
func (m *MemberList) Self() (NodeState, bool) { return m.Get(m.self) }

// Len reports how many members are known.
func (m *MemberList) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.nodes)
}
