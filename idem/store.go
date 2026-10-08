package idem

import (
	"errors"
	"sync"
	"time"
)

// ErrCrash is injected by MemoryStore at an armed persistence cut point to
// simulate a process crash.
var ErrCrash = errors.New("idem: simulated crash at persistence cut point")

// KV is the business-state view handed to an Effect inside the atomic
// commit.
type KV interface {
	Get(key string) (string, bool)
	Set(key, value string)
}

// Effect applies the business side effects of a request. It runs inside the
// same atomic commit that persists the result, so the two can never be
// torn apart by a crash.
type Effect func(kv KV) error

// Store persists idempotency records and business effects. Every mutating
// method is one atomic persistence cut point.
type Store interface {
	// Get returns the record for id.
	Get(id string) (Record, bool, error)
	// Claim atomically creates or takes over an in-progress record. It
	// succeeds when no record exists or the existing in-progress lease
	// expired; the returned record reflects the state after the attempt.
	Claim(id, fingerprint, token string, now, leaseUntil time.Time) (Record, bool, error)
	// Release drops a claim whose fn failed before anything was applied.
	Release(id, token string) error
	// Commit atomically applies effect to the business state and stores
	// result on the record. It fails with ErrLostClaim if token no longer
	// holds the claim (fencing).
	Commit(id, token string, effect Effect, result []byte, expiresAt time.Time) error
	// Sweep converts completed records expired at now into tombstones.
	Sweep(now time.Time) (int, error)
	// Effect reads committed business state (for inspection and tests).
	Effect(key string) (string, bool, error)
}

// MemoryStore is a Store with a crash injector at every persistence cut
// point, used to enumerate crash scenarios in tests. A crash at a cut point
// either discards the write (crash-before) or persists it but loses the
// receipt (crash-after); the store then refuses all operations until
// Restart, modelling a process restart from durable state.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]Record
	effects map[string]string

	crashed bool
	cut     int
	failAt  int // 1-based cut point to crash at; 0 disables injection
	after   bool
}

// NewMemoryStore returns an empty store with the injector disarmed.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records: make(map[string]Record),
		effects: make(map[string]string),
	}
}

// ArmCrash makes the n-th persistence cut point from now crash the store.
// If after is true the cut point's write is persisted but its receipt is
// lost; otherwise the write is discarded.
func (m *MemoryStore) ArmCrash(n int, after bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cut = 0
	m.failAt = n
	m.after = after
}

// Disarm turns crash injection off.
func (m *MemoryStore) Disarm() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failAt = 0
}

// Restart simulates a process restart: durable state is kept, the crashed
// flag is cleared and injection is disarmed.
func (m *MemoryStore) Restart() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.crashed = false
	m.failAt = 0
}

// Cuts returns how many persistence cut points have been hit since the last
// ArmCrash (or store creation).
func (m *MemoryStore) Cuts() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cut
}

// beginCut must be called with m.mu held, before the mutation of a cut
// point. It reports whether this cut point is armed to crash-before.
func (m *MemoryStore) beginCut() (crashBefore bool, err error) {
	if m.crashed {
		return false, ErrCrash
	}
	m.cut++
	return m.failAt > 0 && m.cut == m.failAt && !m.after, nil
}

// endCut must be called with m.mu held, after the mutation of a cut
// point. It reports whether this cut point is armed to crash-after.
func (m *MemoryStore) endCut() (crashAfter bool) {
	return m.failAt > 0 && m.cut == m.failAt && m.after
}

func (m *MemoryStore) Get(id string) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.crashed {
		return Record{}, false, ErrCrash
	}
	rec, found := m.records[id]
	return rec, found, nil
}

func (m *MemoryStore) Claim(id, fingerprint, token string, now, leaseUntil time.Time) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	crashBefore, err := m.beginCut()
	if err != nil {
		return Record{}, false, err
	}
	if crashBefore {
		m.crashed = true
		return Record{}, false, ErrCrash
	}
	rec, found := m.records[id]
	claimed := false
	switch {
	case !found:
		claimed = true
	case rec.State == StateInProgress && !now.Before(rec.LeaseUntil):
		claimed = true // lease expired: take over
	}
	if claimed {
		rec = Record{
			ID:          id,
			Fingerprint: fingerprint,
			State:       StateInProgress,
			Token:       token,
			LeaseUntil:  leaseUntil,
		}
		m.records[id] = rec
	}
	if m.endCut() {
		m.crashed = true
		return rec, claimed, ErrCrash
	}
	return rec, claimed, nil
}

func (m *MemoryStore) Release(id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	crashBefore, err := m.beginCut()
	if err != nil {
		return err
	}
	if crashBefore {
		m.crashed = true
		return ErrCrash
	}
	if rec, found := m.records[id]; found && rec.State == StateInProgress && rec.Token == token {
		delete(m.records, id)
	}
	if m.endCut() {
		m.crashed = true
		return ErrCrash
	}
	return nil
}

func (m *MemoryStore) Commit(id, token string, effect Effect, result []byte, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	crashBefore, err := m.beginCut()
	if err != nil {
		return err
	}
	if crashBefore {
		m.crashed = true
		return ErrCrash
	}
	rec, found := m.records[id]
	if !found || rec.State != StateInProgress || rec.Token != token {
		return ErrLostClaim
	}
	// Effect and result change durable state in this single critical
	// section: they are either both visible after a crash or neither is.
	if effect != nil {
		if err := effect(kvView{m.effects}); err != nil {
			return err
		}
	}
	rec.State = StateCompleted
	rec.Result = result
	rec.ExpiresAt = expiresAt
	rec.Token = ""
	rec.LeaseUntil = time.Time{}
	m.records[id] = rec
	if m.endCut() {
		m.crashed = true
		return ErrCrash
	}
	return nil
}

func (m *MemoryStore) Sweep(now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	crashBefore, err := m.beginCut()
	if err != nil {
		return 0, err
	}
	if crashBefore {
		m.crashed = true
		return 0, ErrCrash
	}
	n := 0
	for id, rec := range m.records {
		if rec.State == StateCompleted && !now.Before(rec.ExpiresAt) {
			rec.State = StateTombstone
			rec.Result = nil
			rec.ExpiresAt = time.Time{}
			m.records[id] = rec
			n++
		}
	}
	if m.endCut() {
		m.crashed = true
		return n, ErrCrash
	}
	return n, nil
}

func (m *MemoryStore) Effect(key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.crashed {
		return "", false, ErrCrash
	}
	v, found := m.effects[key]
	return v, found, nil
}

type kvView struct{ m map[string]string }

func (k kvView) Get(key string) (string, bool) { v, ok := k.m[key]; return v, ok }
func (k kvView) Set(key, value string)         { k.m[key] = value }
