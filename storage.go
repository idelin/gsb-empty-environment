package replsim

import (
	"errors"
	"sync"
)

// State is the durable state of a Node.
type State struct {
	CurrentTerm uint64
	Log         []Entry
	CommitIndex uint64
}

func cloneState(s State) State {
	s.Log = cloneEntries(s.Log)
	return s
}

// Storage persists node state. Store must be durable before it returns.
type Storage interface {
	Load() (State, error)
	Store(State) error
}

// ErrCrash is returned by MemStorage when a simulated crash aborts a Store.
var ErrCrash = errors.New("replsim: simulated crash during persist")

// MemStorage is an in-memory Storage with test hooks for injected persist
// failures and crash cut-points.
type MemStorage struct {
	mu       sync.Mutex
	state    State
	persists int

	// FailNext makes the next Store return an error without applying it.
	FailNext bool
	// CrashAfter, when >= 0, makes the (CrashAfter+1)-th Store simulate a
	// crash: the write is lost, ErrCrash is returned, and the flag is
	// disarmed so the simulation continues after recovery.
	CrashAfter int
	crashed    bool
}

// NewMemStorage returns an empty MemStorage.
func NewMemStorage() *MemStorage {
	return &MemStorage{CrashAfter: -1}
}

// Load returns the durable state.
func (m *MemStorage) Load() (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneState(m.state), nil
}

// Store replaces the durable state, unless a failure or crash is injected.
func (m *MemStorage) Store(s State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailNext {
		m.FailNext = false
		return errors.New("replsim: injected persist failure")
	}
	if m.CrashAfter >= 0 && m.persists == m.CrashAfter {
		m.CrashAfter = -1
		m.crashed = true
		return ErrCrash
	}
	m.state = cloneState(s)
	m.persists++
	return nil
}

// PersistCount returns the number of successful Store calls.
func (m *MemStorage) PersistCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.persists
}

// Crashed reports whether a simulated crash has occurred and not yet been
// cleared with Recover.
func (m *MemStorage) Crashed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.crashed
}

// Recover clears the crashed flag, modelling a restart that keeps the
// durable state.
func (m *MemStorage) Recover() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.crashed = false
}
