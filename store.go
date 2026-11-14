package netsec

import (
	"sync"
	"time"
)

// CodeRecord is the atomically stored state of one issued verification code.
// The code is bound to a phone number, a purpose, a send batch and a time step.
type CodeRecord struct {
	Phone    string
	Purpose  string
	BatchID  string
	CodeHash string
	Step     int64
	Consumed bool
	Attempts int
}

type ConsumeStatus int

const (
	ConsumeOK ConsumeStatus = iota
	ConsumeNotFound
	ConsumeAlreadyConsumed
	ConsumeExpired
	ConsumeMismatch
	ConsumeLocked
)

type SendStatus int

const (
	SendOK SendStatus = iota
	SendTooFrequent
	SendLimited
)

// Store is the shared atomic backend every gateway talks to. All methods must
// execute check-and-mutate as one atomic operation so that concurrent calls
// from different gateways cannot interleave. A Redis implementation would use
// Lua scripts; MemoryStore uses a single mutex.
type Store interface {
	PutCode(rec CodeRecord) error
	ConsumeCode(phone, purpose, codeHash string, step, skew int64, maxAttempts int) (ConsumeStatus, error)
	DeleteCodesForPhone(phone string) error
	AcquireSendSlot(key string, now time.Time, minInterval time.Duration, window int64, maxPerWindow int) (SendStatus, error)
	AcquireRebindSlot(account string, window int64, maxPerWindow int) (bool, error)
	SetBinding(account, phone string) error
	Binding(account string) (string, bool, error)
}

func codeKey(phone, purpose string) string {
	return phone + "\x00" + purpose
}

type sendState struct {
	last    time.Time
	hasLast bool
	window  int64
	count   int
}

type rebindState struct {
	window int64
	count  int
}

// MemoryStore is an in-memory Store for single-process deployments and tests.
type MemoryStore struct {
	mu       sync.Mutex
	codes    map[string]CodeRecord
	sends    map[string]*sendState
	rebinds  map[string]*rebindState
	bindings map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		codes:    make(map[string]CodeRecord),
		sends:    make(map[string]*sendState),
		rebinds:  make(map[string]*rebindState),
		bindings: make(map[string]string),
	}
}

func (m *MemoryStore) PutCode(rec CodeRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codes[codeKey(rec.Phone, rec.Purpose)] = rec
	return nil
}

// ConsumeCode validates and consumes the latest code for (phone, purpose) in
// one atomic step. Only the first successful caller observes ConsumeOK.
func (m *MemoryStore) ConsumeCode(phone, purpose, codeHash string, step, skew int64, maxAttempts int) (ConsumeStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := codeKey(phone, purpose)
	rec, ok := m.codes[key]
	if !ok {
		return ConsumeNotFound, nil
	}
	if rec.Consumed {
		return ConsumeAlreadyConsumed, nil
	}
	if step > rec.Step+skew {
		delete(m.codes, key)
		return ConsumeExpired, nil
	}
	rec.Attempts++
	if rec.CodeHash != codeHash {
		if rec.Attempts >= maxAttempts {
			delete(m.codes, key)
			return ConsumeLocked, nil
		}
		m.codes[key] = rec
		return ConsumeMismatch, nil
	}
	rec.Consumed = true
	m.codes[key] = rec
	return ConsumeOK, nil
}

func (m *MemoryStore) DeleteCodesForPhone(phone string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, rec := range m.codes {
		if rec.Phone == phone {
			delete(m.codes, key)
		}
	}
	return nil
}

func (m *MemoryStore) AcquireSendSlot(key string, now time.Time, minInterval time.Duration, window int64, maxPerWindow int) (SendStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sends[key]
	if !ok {
		st = &sendState{}
		m.sends[key] = st
	}
	if st.hasLast && now.Sub(st.last) < minInterval {
		return SendTooFrequent, nil
	}
	if st.window != window {
		st.window = window
		st.count = 0
	}
	if st.count >= maxPerWindow {
		return SendLimited, nil
	}
	st.count++
	st.last = now
	st.hasLast = true
	return SendOK, nil
}

func (m *MemoryStore) AcquireRebindSlot(account string, window int64, maxPerWindow int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.rebinds[account]
	if !ok {
		st = &rebindState{}
		m.rebinds[account] = st
	}
	if st.window != window {
		st.window = window
		st.count = 0
	}
	if st.count >= maxPerWindow {
		return false, nil
	}
	st.count++
	return true, nil
}

func (m *MemoryStore) SetBinding(account, phone string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bindings[account] = phone
	return nil
}

func (m *MemoryStore) Binding(account string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	phone, ok := m.bindings[account]
	return phone, ok, nil
}
