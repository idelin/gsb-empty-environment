package lease

import (
	"errors"
	"sync"
	"time"
)

// MemStore is an in-memory Store. It models durable state across a
// simulated crash: keep the MemStore, discard the Receiver, and build a
// new Receiver over it.
type MemStore struct {
	mu        sync.Mutex
	recs      map[RecordKey]DeliveryRecord
	committed map[RecordKey]bool
	order     []RecordKey
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		recs:      make(map[RecordKey]DeliveryRecord),
		committed: make(map[RecordKey]bool),
	}
}

// PutStaged implements Store.
func (s *MemStore) PutStaged(rec DeliveryRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.recs[rec.Key]; !ok {
		s.order = append(s.order, rec.Key)
	}
	s.recs[rec.Key] = rec
	return nil
}

// MarkCommitted implements Store.
func (s *MemStore) MarkCommitted(key RecordKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.recs[key]; !ok {
		return ErrRecordNotFound
	}
	s.committed[key] = true
	return nil
}

// State implements Store.
func (s *MemStore) State(key RecordKey) (LocalState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.recs[key]; !ok {
		return LocalStaged, false, nil
	}
	if s.committed[key] {
		return LocalCommitted, true, nil
	}
	return LocalStaged, true, nil
}

// Staged implements Store.
func (s *MemStore) Staged() ([]DeliveryRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []DeliveryRecord
	for _, key := range s.order {
		if !s.committed[key] {
			out = append(out, s.recs[key])
		}
	}
	return out, nil
}

// MemInbox is an in-memory idempotent Inbox. It models the host's
// durable inbox across a simulated crash.
type MemInbox struct {
	mu sync.Mutex
	// data holds exactly one record per key: idempotent delivery.
	data map[RecordKey]DeliveryRecord
	// Applies counts Put calls that reached the durable map, including
	// idempotent repeats, so tests can assert delivery happened once.
	Applies int
}

// NewMemInbox returns an empty in-memory inbox.
func NewMemInbox() *MemInbox {
	return &MemInbox{data: make(map[RecordKey]DeliveryRecord)}
}

// Put implements Inbox.
func (m *MemInbox) Put(rec DeliveryRecord) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, already := m.data[rec.Key]
	if !already {
		m.data[rec.Key] = rec
	}
	m.Applies++
	return already, nil
}

// Lookup implements Inbox.
func (m *MemInbox) Lookup(key RecordKey) (DeliveryRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.data[key]
	return rec, ok, nil
}

// Len returns the number of distinct records the inbox holds.
func (m *MemInbox) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.data)
}

// PauseMode selects where a pausable wrapper blocks an operation.
type PauseMode int

const (
	// PauseBefore blocks the operation before it takes effect.
	PauseBefore PauseMode = iota
	// PauseAfterApply applies the operation's durable effect, then
	// blocks before returning. Releasing with an error simulates a
	// crash after the durable write but before the receipt arrived.
	PauseAfterApply
)

type pauseGate struct {
	mode    PauseMode
	ch      chan struct{}
	entered chan struct{}
	fired   bool
	err     error
}

// PausableInbox wraps a MemInbox with one-shot pause gates so tests can
// stop at every persistent write point of the commit protocol and then
// release the blocked call with an injected crash error.
type PausableInbox struct {
	inner *MemInbox

	mu   sync.Mutex
	gate *pauseGate
}

// NewPausableInbox wraps inner with pause gates.
func NewPausableInbox(inner *MemInbox) *PausableInbox {
	return &PausableInbox{inner: inner}
}

// Pause arms a one-shot gate: the next matching operation blocks.
func (p *PausableInbox) Pause(mode PauseMode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gate = &pauseGate{mode: mode, ch: make(chan struct{}), entered: make(chan struct{})}
}

// Entered returns a channel closed once the armed operation is blocked.
// Call it after Pause and before starting the operation.
func (p *PausableInbox) Entered() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gate == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return p.gate.entered
}

// Release unblocks the paused operation. A non-nil err is returned to
// the caller, simulating a crash; with PauseBefore the operation never
// took effect, with PauseAfterApply the durable effect already happened.
func (p *PausableInbox) Release(err error) {
	p.mu.Lock()
	g := p.gate
	if g != nil {
		p.gate = nil
	}
	p.mu.Unlock()
	if g == nil {
		panic("lease: Release without armed gate")
	}
	g.err = err
	close(g.ch)
}

func (p *PausableInbox) wait(mode PauseMode) error {
	p.mu.Lock()
	g := p.gate
	if g == nil || g.fired || g.mode != mode {
		p.mu.Unlock()
		return nil
	}
	g.fired = true // one-shot
	p.mu.Unlock()
	close(g.entered)
	<-g.ch
	return g.err
}

// Put implements Inbox.
func (p *PausableInbox) Put(rec DeliveryRecord) (bool, error) {
	if err := p.wait(PauseBefore); err != nil {
		return false, err
	}
	already, err := p.inner.Put(rec)
	if err != nil {
		return false, err
	}
	if err := p.wait(PauseAfterApply); err != nil {
		return already, err
	}
	return already, nil
}

// Lookup implements Inbox.
func (p *PausableInbox) Lookup(key RecordKey) (DeliveryRecord, bool, error) {
	if err := p.wait(PauseBefore); err != nil {
		return DeliveryRecord{}, false, err
	}
	return p.inner.Lookup(key)
}

// PausableStore wraps a MemStore with one-shot pause gates on named
// write points, so tests can crash between any two persistent writes.
type PausableStore struct {
	inner *MemStore

	mu    sync.Mutex
	gates map[string]*pauseGate
}

// Store write points that can be paused.
const (
	WritePutStaged     = "PutStaged"
	WriteMarkCommitted = "MarkCommitted"
)

// NewPausableStore wraps inner with pause gates.
func NewPausableStore(inner *MemStore) *PausableStore {
	return &PausableStore{inner: inner, gates: make(map[string]*pauseGate)}
}

// PauseOn arms a one-shot gate on a write point (WritePutStaged or
// WriteMarkCommitted).
func (s *PausableStore) PauseOn(op string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gates[op] = &pauseGate{ch: make(chan struct{}), entered: make(chan struct{})}
}

// Entered returns a channel closed once the armed operation is blocked.
func (s *PausableStore) Entered(op string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gates[op] == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return s.gates[op].entered
}

// Release unblocks the paused operation, optionally failing it with err.
func (s *PausableStore) Release(op string, err error) {
	s.mu.Lock()
	g := s.gates[op]
	delete(s.gates, op)
	s.mu.Unlock()
	if g == nil {
		panic("lease: Release without armed gate")
	}
	g.err = err
	close(g.ch)
}

func (s *PausableStore) wait(op string) error {
	s.mu.Lock()
	g := s.gates[op]
	if g == nil || g.fired {
		s.mu.Unlock()
		return nil
	}
	g.fired = true // one-shot
	s.mu.Unlock()
	close(g.entered)
	<-g.ch
	return g.err
}

// PutStaged implements Store.
func (s *PausableStore) PutStaged(rec DeliveryRecord) error {
	if err := s.wait(WritePutStaged); err != nil {
		return err
	}
	return s.inner.PutStaged(rec)
}

// MarkCommitted implements Store.
func (s *PausableStore) MarkCommitted(key RecordKey) error {
	if err := s.wait(WriteMarkCommitted); err != nil {
		return err
	}
	return s.inner.MarkCommitted(key)
}

// State implements Store.
func (s *PausableStore) State(key RecordKey) (LocalState, bool, error) {
	return s.inner.State(key)
}

// Staged implements Store.
func (s *PausableStore) Staged() ([]DeliveryRecord, error) {
	return s.inner.Staged()
}

// ErrInjected is a conventional crash error for Release calls in tests.
var ErrInjected = errors.New("lease: injected crash")

// FakeClock is a manually advanced clock for overlap-window tests.
type FakeClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewFakeClock starts a fake clock at t.
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{t: t} }

// Now returns the fake current time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance moves the fake clock forward.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}
