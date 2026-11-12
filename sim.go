package netsec

import (
	"sync"
	"time"
)

// SimThread is one simulated operating-system thread.
type SimThread struct {
	mu     sync.Mutex
	id     uint64
	gen    Gen
	pc     Addr
	frozen bool
	exited bool
	// FreezeDelay makes Freeze return before the thread is actually
	// frozen; the simulated scheduler flips the state later from Idle.
	freezeDelay   time.Duration
	pendingFreeze time.Time
}

func (t *SimThread) snapshot() (pc Addr, frozen bool, exited bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pc, t.frozen, t.exited
}

// PC returns the thread's current program counter.
func (t *SimThread) PC() Addr {
	pc, _, _ := t.snapshot()
	return pc
}

// IsFrozen reports whether the thread is currently frozen.
func (t *SimThread) IsFrozen() bool {
	_, frozen, _ := t.snapshot()
	return frozen
}

// IsExited reports whether the thread has terminated.
func (t *SimThread) IsExited() bool {
	_, _, exited := t.snapshot()
	return exited
}

// Exit terminates the thread so its ID can be handed to the next Spawn.
func (t *SimThread) Exit() {
	t.mu.Lock()
	t.exited = true
	t.frozen = false
	t.pendingFreeze = time.Time{}
	t.mu.Unlock()
}

// SimHost is an in-memory Host implementation for tests.
type SimHost struct {
	mu sync.Mutex

	// current maps each live ID to its current thread. Exited threads
	// remain in the map until the ID is reused.
	current map[uint64]*SimThread
	// generations keeps every spawned thread, including exited ones, so
	// stale references classify as exited instead of unknown.
	generations map[ThreadRef]*SimThread
	nextGen     map[uint64]Gen
	memory      map[Addr]byte
	memBounds   Region

	failFlush bool
	flushes   int

	// FreezeRequests counts every Freeze call, including retries that
	// find an already frozen thread.
	FreezeRequests int
	// ResumeRequests counts every Resume call.
	ResumeRequests int
}

// NewSimHost creates a host whose simulated code page spans memBounds.
func NewSimHost(memBounds Region) *SimHost {
	return &SimHost{
		current:     map[uint64]*SimThread{},
		generations: map[ThreadRef]*SimThread{},
		nextGen:     map[uint64]Gen{},
		memory:      map[Addr]byte{},
		memBounds:   memBounds,
	}
}

// Spawn creates a thread with the requested ID. If the ID previously
// belonged to an exited thread, the new thread starts at a fresh generation.
func (h *SimHost) Spawn(id uint64, pc Addr, freezeDelay time.Duration) *SimThread {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.spawnLocked(id, pc, freezeDelay)
}

func (h *SimHost) spawnLocked(id uint64, pc Addr, freezeDelay time.Duration) *SimThread {
	if old, ok := h.current[id]; ok && !old.exited {
		panic("netsec: SimHost.Spawn called with a live thread ID")
	}
	h.nextGen[id]++
	thread := &SimThread{
		id:          id,
		gen:         h.nextGen[id],
		pc:          pc,
		freezeDelay: freezeDelay,
	}
	h.current[id] = thread
	h.generations[ThreadRef{ID: id, Gen: thread.gen}] = thread
	return thread
}

// Thread retrieves the current occupant of an ID, or nil if it is gone.
func (h *SimHost) Thread(id uint64) *SimThread {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.current[id]
}

// Fill writes initial contents into the simulated code page.
func (h *SimHost) Fill(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, b := range data {
		h.memory[h.memBounds.Start+Addr(i)] = b
	}
}

func (h *SimHost) offset(addr Addr) (uint64, bool) {
	if addr < h.memBounds.Start || addr >= h.memBounds.End {
		return 0, false
	}
	return uint64(addr - h.memBounds.Start), true
}

func (h *SimHost) lookup(ref ThreadRef) (*SimThread, error) {
	thread, ok := h.generations[ref]
	if !ok {
		return nil, ErrUnknownThread
	}
	if thread.exited {
		return thread, nil
	}
	if current, ok := h.current[ref.ID]; !ok || current != thread {
		return thread, ErrUnknownThread
	}
	return thread, nil
}

func (h *SimHost) ListThreads() []ThreadRef {
	h.mu.Lock()
	defer h.mu.Unlock()

	refs := make([]ThreadRef, 0, len(h.current))
	for _, thread := range h.current {
		if !thread.exited {
			refs = append(refs, ThreadRef{ID: thread.id, Gen: thread.gen})
		}
	}
	sortRefs(refs)
	return refs
}

func (h *SimHost) Freeze(ref ThreadRef) error {
	h.mu.Lock()
	h.FreezeRequests++
	thread, err := h.lookup(ref)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	thread.mu.Lock()
	if thread.frozen {
		thread.mu.Unlock()
		h.mu.Unlock()
		return nil
	}
	if thread.freezeDelay > 0 && thread.pendingFreeze.IsZero() {
		thread.pendingFreeze = h.nowLocked().Add(thread.freezeDelay)
		thread.mu.Unlock()
		h.mu.Unlock()
		return nil
	}
	thread.frozen = true
	thread.mu.Unlock()
	h.mu.Unlock()
	return nil
}

func (h *SimHost) Status(ref ThreadRef) (ThreadStatus, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	thread, ok := h.generations[ref]
	if !ok {
		return StatusUnknown, ErrUnknownThread
	}
	thread.mu.Lock()
	defer thread.mu.Unlock()
	switch {
	case thread.exited:
		return StatusExited, nil
	default:
		if current, ok := h.current[ref.ID]; !ok || current != thread {
			return StatusExited, nil
		}
		if thread.frozen {
			return StatusFrozen, nil
		}
		return StatusRunning, nil
	}
}

func (h *SimHost) ReadPC(ref ThreadRef) (Addr, error) {
	h.mu.Lock()
	thread, err := h.lookup(ref)
	if err != nil {
		h.mu.Unlock()
		return 0, err
	}
	h.mu.Unlock()

	thread.mu.Lock()
	defer thread.mu.Unlock()
	if !thread.frozen {
		return 0, ErrThreadNotFrozen
	}
	return thread.pc, nil
}

func (h *SimHost) WritePC(ref ThreadRef, pc Addr) error {
	h.mu.Lock()
	thread, err := h.lookup(ref)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	h.mu.Unlock()

	thread.mu.Lock()
	defer thread.mu.Unlock()
	if !thread.frozen {
		return ErrThreadNotFrozen
	}
	thread.pc = pc
	return nil
}

func (h *SimHost) Resume(ref ThreadRef) error {
	h.mu.Lock()
	h.ResumeRequests++
	thread, ok := h.generations[ref]
	if !ok {
		h.mu.Unlock()
		return ErrUnknownThread
	}
	thread.mu.Lock()
	defer thread.mu.Unlock()
	h.mu.Unlock()

	if thread.exited {
		return nil
	}
	thread.frozen = false
	thread.pendingFreeze = time.Time{}
	return nil
}

func (h *SimHost) ReadMemory(addr Addr, n int) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.offset(addr); !ok {
		return nil, ErrBadAddress
	}
	end := addr + Addr(n)
	if end > h.memBounds.End {
		return nil, ErrBadAddress
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = h.memory[addr+Addr(i)]
	}
	return out, nil
}

func (h *SimHost) WriteMemory(addr Addr, data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	offset, ok := h.offset(addr)
	if !ok || offset+uint64(len(data)) > h.memBounds.Size() {
		return ErrBadAddress
	}
	for i, b := range data {
		h.memory[addr+Addr(i)] = b
	}
	return nil
}

// SetFlushFailure toggles instruction-cache flush failure injection.
func (h *SimHost) SetFlushFailure(fail bool) {
	h.mu.Lock()
	h.failFlush = fail
	h.mu.Unlock()
}

// FlushCount returns how many cache flushes have been attempted.
func (h *SimHost) FlushCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.flushes
}

func (h *SimHost) FlushICache(region Region) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.flushes++
	if h.failFlush {
		return ErrFlushFailed
	}
	return nil
}

func (h *SimHost) nowLocked() time.Time {
	return time.Now()
}

// Idle advances simulated scheduler state: pending freezes whose delay has
// elapsed take effect, and running threads execute OnRun while frozen
// threads stay parked. OnRun may spawn or exit threads safely.
func (h *SimHost) Idle(onRun func(*SimThread)) {
	h.mu.Lock()
	now := h.nowLocked()

	var runnable []*SimThread
	for _, thread := range h.current {
		thread.mu.Lock()
		if thread.exited {
			thread.mu.Unlock()
			continue
		}
		if !thread.pendingFreeze.IsZero() && !now.Before(thread.pendingFreeze) {
			thread.frozen = true
			thread.pendingFreeze = time.Time{}
		}
		if !thread.frozen {
			runnable = append(runnable, thread)
		}
		thread.mu.Unlock()
	}
	h.mu.Unlock()

	for _, thread := range runnable {
		if onRun != nil {
			onRun(thread)
		}
	}
}
