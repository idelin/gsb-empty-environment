package cidx

import "sync"

// Mapping is a validated, read-only index image. Its lifetime is governed
// by borrows: Close only requests teardown, which is deferred until the
// last borrow (view or cursor) is released. Once a close is requested, new
// borrows are refused, but existing borrowers keep working.
type Mapping struct {
	gen    uint64
	layout *Layout
	guard  *Guarded

	mu             sync.Mutex
	borrows        int
	closeRequested bool
	tornDown       bool
	done           chan struct{}

	// queryHook, when non-nil, runs at the start of each View.Query.
	// It exists so tests can build deterministic interleavings.
	queryHook func()
}

func newMapping(gen uint64, data []byte, lay *Layout) *Mapping {
	return &Mapping{
		gen:    gen,
		layout: lay,
		guard:  NewGuarded(data, lay),
		done:   make(chan struct{}),
	}
}

// Generation identifies the mapping image. A handle bound to one
// generation can never observe bytes of another.
func (m *Mapping) Generation() uint64 { return m.gen }

// Done closes when the mapping is actually torn down.
func (m *Mapping) Done() <-chan struct{} { return m.done }

// TornDown reports whether teardown has completed.
func (m *Mapping) TornDown() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tornDown
}

func (m *Mapping) borrow() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closeRequested {
		return ErrMappingClosing
	}
	m.borrows++
	return nil
}

func (m *Mapping) release() {
	m.mu.Lock()
	m.borrows--
	m.maybeTeardownLocked()
	m.mu.Unlock()
}

// Close requests teardown. It completes immediately if nothing is
// borrowed, otherwise it is deferred until the last release.
func (m *Mapping) Close() {
	m.mu.Lock()
	m.closeRequested = true
	m.maybeTeardownLocked()
	m.mu.Unlock()
}

func (m *Mapping) maybeTeardownLocked() {
	if m.closeRequested && m.borrows == 0 && !m.tornDown {
		m.tornDown = true
		close(m.done)
	}
}

func (m *Mapping) recordExtent(i int) (uint64, uint64) {
	start := m.layout.DataOffset + uint64(m.layout.Offsets[i])
	end := m.layout.DataOffset + uint64(m.layout.Offsets[i+1])
	return start, end
}

func (m *Mapping) readKey(i int) ([]byte, error) {
	start, end := m.recordExtent(i)
	raw, err := m.guard.ReadAt(start, end-start)
	if err != nil {
		return nil, err
	}
	key, _, err := parseRecord(raw)
	return key, err
}

func (m *Mapping) readRecord(i int) (key, value []byte, err error) {
	start, end := m.recordExtent(i)
	raw, err := m.guard.ReadAt(start, end-start)
	if err != nil {
		return nil, nil, err
	}
	return parseRecord(raw)
}

// Store owns the current mapping and swaps it atomically on Load. A failed
// load never disturbs the current mapping. A successful load requests the
// old mapping's close, which completes only when its last borrower is gone;
// old handles keep reading the old image and can never bind to the new one.
type Store struct {
	mu  sync.Mutex
	cur *Mapping
	gen uint64
}

func NewStore() *Store { return &Store{} }

// Load validates data and, only if it is fully trustworthy, swaps it in as
// the current mapping. data is copied, so the caller may reuse the buffer.
func (s *Store) Load(data []byte) error {
	lay, err := Check(data)
	if err != nil {
		return err
	}
	data = append([]byte(nil), data...)
	m := newMapping(s.gen+1, data, lay)
	s.mu.Lock()
	s.gen++
	m.gen = s.gen
	old := s.cur
	s.cur = m
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

// Mapping returns the current mapping, or nil if none is loaded.
func (s *Store) Mapping() *Mapping {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// CurrentGeneration returns the generation of the current mapping.
func (s *Store) CurrentGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

// View opens a read-only view on the current mapping.
func (s *Store) View() (*View, error) {
	m := s.Mapping()
	if m == nil {
		return nil, ErrNoMapping
	}
	return OpenView(m)
}
