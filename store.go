package antientropy

import "sync"

const (
	StatusAlive = "alive"
	StatusDead  = "dead"
)

// NodeState is one replicated fact about a cluster node.
// Version is a logical clock; conflicts resolve last-writer-wins.
type NodeState struct {
	ID      string
	Status  string
	Version uint64
}

// Store is a thread-safe replica of the node-state table.
type Store struct {
	mu     sync.RWMutex
	states map[string]NodeState
}

func NewStore() *Store {
	return &Store{states: make(map[string]NodeState)}
}

// Apply installs st using last-writer-wins on Version.
// Returns true when the store actually changed.
func (s *Store) Apply(st NodeState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.states[st.ID]
	if ok && cur.Version >= st.Version {
		return false
	}
	s.states[st.ID] = st
	return true
}

func (s *Store) Get(id string) (NodeState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.states[id]
	return st, ok
}

func (s *Store) Snapshot() map[string]NodeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]NodeState, len(s.states))
	for k, v := range s.states {
		out[k] = v
	}
	return out
}

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.states)
}
