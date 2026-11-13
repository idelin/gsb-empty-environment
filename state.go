package netsec

import "sync"

// Operation identifies the logical operation recorded in a transaction
// intent journal.
type Operation uint8

const (
	OpRead Operation = iota + 1
	OpWrite
)

// Intent is persisted before the old path is read.  It fixes the old leaf
// (where the block is expected to be found) and the freshly drawn new leaf.
// Recovery must replay exactly this intent and must not draw another leaf.
type Intent struct {
	Tx        uint64
	Op        Operation
	BlockID   BlockID
	OldLeaf   int
	NewLeaf   int
	WriteData []byte
}

func (i *Intent) clone() *Intent {
	if i == nil {
		return nil
	}
	out := *i
	out.WriteData = append([]byte(nil), i.WriteData...)
	return &out
}

// Prepared is the crash-consistent journal entry written immediately before
// staging a path write.  Buckets are byte-identical on replay because nonce
// derivation is deterministic.
type Prepared struct {
	Tx            uint64
	PathLeaf      int
	PathNodes     []int
	Buckets       []WireBucket
	NewRoot       [32]byte
	NextPositions map[BlockID]int
	NextStash     []Block
}

func (p *Prepared) clone() *Prepared {
	if p == nil {
		return nil
	}
	out := *p
	out.PathNodes = append([]int(nil), p.PathNodes...)
	out.Buckets = make([]WireBucket, len(p.Buckets))
	for i := range p.Buckets {
		out.Buckets[i] = p.Buckets[i].clone()
	}
	out.NextStash = cloneBlocks(p.NextStash)
	out.NextPositions = clonePositions(p.NextPositions)
	return &out
}

// PersistentState is replaced atomically by StateStore.Commit.  Root, Txn,
// Positions, and Stash are the last committed state; Intent and Prepared are
// uncommitted journals used for crash recovery.
type PersistentState struct {
	Positions map[BlockID]int
	Stash     []Block
	Root      [32]byte
	Txn       uint64
	Intent    *Intent
	Prepared  *Prepared
}

// Clone returns a deep copy suitable for handing to a store.
func (s *PersistentState) Clone() *PersistentState {
	if s == nil {
		return nil
	}
	out := &PersistentState{
		Positions: clonePositions(s.Positions),
		Stash:     cloneBlocks(s.Stash),
		Root:      s.Root,
		Txn:       s.Txn,
		Intent:    s.Intent.clone(),
		Prepared:  s.Prepared.clone(),
	}
	return out
}

// Snapshot is a read-only view of a client's persisted state for tests and
// operational introspection.
type Snapshot struct {
	Positions   map[BlockID]int
	Stash       []Block
	Root        [32]byte
	Txn         uint64
	Intent      *Intent
	Prepared    *Prepared
	Pending     bool
	StashBlocks int
}

// StateStore must implement atomic replacement of the whole persistent state.
// A failed Commit must leave the previously committed value untouched.
type StateStore interface {
	Load() (*PersistentState, error)
	Commit(*PersistentState) error
}

// MemoryStore is an in-memory atomic StateStore.  It also keeps a commit
// history so tests can inspect the exact intent that preceded a path access.
type MemoryStore struct {
	mu      sync.Mutex
	current *PersistentState
	history []*PersistentState
	failN   int
	commits int
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Load returns a deep copy of the current state, or nil for an empty store.
func (m *MemoryStore) Load() (*PersistentState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current.Clone(), nil
}

// Commit atomically replaces the state.  If a failure was requested with
// FailNextCommit, it returns an error without changing any stored value.
func (m *MemoryStore) Commit(state *PersistentState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commits++
	if m.failN > 0 {
		m.failN--
		return errInjectedCommit
	}
	m.current = state.Clone()
	m.history = append(m.history, m.current.Clone())
	return nil
}

// FailNextCommit makes the next Commit fail atomically.
func (m *MemoryStore) FailNextCommit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failN++
}

// CommitCount returns the number of Commit calls, including failed calls.
func (m *MemoryStore) CommitCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.commits
}

// History returns deep copies of every successfully committed state.
func (m *MemoryStore) History() []*PersistentState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*PersistentState, len(m.history))
	for i := range m.history {
		out[i] = m.history[i].Clone()
	}
	return out
}

// State returns a deep copy of the latest committed state.
func (m *MemoryStore) State() *PersistentState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current.Clone()
}

func clonePositions(in map[BlockID]int) map[BlockID]int {
	if in == nil {
		return make(map[BlockID]int)
	}
	out := make(map[BlockID]int, len(in))
	for id, leaf := range in {
		out[id] = leaf
	}
	return out
}

func cloneBlocks(in []Block) []Block {
	if len(in) == 0 {
		return nil
	}
	out := make([]Block, len(in))
	for i := range in {
		out[i] = in[i].clone()
	}
	return out
}

func stashMap(blocks []Block) map[BlockID]Block {
	out := make(map[BlockID]Block, len(blocks))
	for _, b := range blocks {
		out[b.ID] = b
	}
	return out
}

func stashSlice(blocks map[BlockID]Block) []Block {
	out := make([]Block, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, b.clone())
	}
	return out
}

var errInjectedCommit = &commitError{}

type commitError struct{}

func (*commitError) Error() string { return "netsec: injected state commit failure" }
