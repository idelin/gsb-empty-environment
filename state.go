package antientropy

import (
	"fmt"
	"sort"
	"sync"
)

// Status describes the liveness of a cluster member.
type Status string

const (
	StatusAlive   Status = "alive"
	StatusOffline Status = "offline"
)

// State is a single versioned membership fact about a node.
// Version acts as a Lamport-style clock: higher always wins.
type State struct {
	NodeID  string
	Status  Status
	Version uint64
}

func (s State) String() string {
	return fmt.Sprintf("%s=%s@v%d", s.NodeID, s.Status, s.Version)
}

// Store is a last-write-wins replicated state store guarded by version.
type Store struct {
	mu   sync.RWMutex
	data map[string]State
}

func NewStore() *Store {
	return &Store{data: make(map[string]State)}
}

// Apply merges a state using last-write-wins on Version.
// It reports whether the local store was actually changed.
func (s *Store) Apply(st State) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.data[st.NodeID]
	if ok && cur.Version >= st.Version {
		return false
	}
	s.data[st.NodeID] = st
	return true
}

// Get returns the current state for a node id.
func (s *Store) Get(nodeID string) (State, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.data[nodeID]
	return st, ok
}

// Snapshot returns all states sorted by node id.
func (s *Store) Snapshot() []State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]State, 0, len(s.data))
	for _, st := range s.data {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// StatesInBuckets returns the states whose keys fall into the given buckets.
func (s *Store) StatesInBuckets(buckets []int) []State {
	want := make(map[int]bool, len(buckets))
	for _, b := range buckets {
		want[b] = true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []State
	for _, st := range s.data {
		if want[bucketOf(st.NodeID)] {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// RootHash returns the Merkle root hash of the current content.
func (s *Store) RootHash() uint64 {
	return BuildTree(s.Snapshot()).Hash
}
