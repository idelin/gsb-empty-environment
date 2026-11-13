package netsec

import (
	"fmt"
	"sync"
)

// MemoryState is the lifecycle state of source memory.
type MemoryState string

const (
	MemoryRunning    MemoryState = "running"
	MemoryFrozen     MemoryState = "frozen"
	MemoryTerminated MemoryState = "terminated"
	MemoryReleased   MemoryState = "released"
)

// Memory option helpers.
type MemoryOption func(*Memory)

// WithSourceInstance assigns the concrete source incarnation identity.
func WithSourceInstance(id InstanceID) MemoryOption {
	return func(m *Memory) {
		m.instance = id
	}
}

// WithMemoryInjector installs the event/fault dispatcher used by memory
// operations.
func WithMemoryInjector(d *EventDispatcher) MemoryOption {
	return func(m *Memory) {
		m.events = d
	}
}

// WriteBarrier is called after a write has been admitted but before it is
// applied.  It is primarily useful for deterministic concurrency tests: a
// barrier can hold a write in flight while a freeze starts.
type WriteBarrier func(WriteAdmission)

// WriteAdmission describes a write that has already passed the freeze gate.
type WriteAdmission struct {
	Page PageID
	Kind WriteKind
	Data []byte
}

// WithWriteBarrier installs a write admission barrier.
func WithWriteBarrier(barrier WriteBarrier) MemoryOption {
	return func(m *Memory) {
		m.writeBarrier = barrier
	}
}

type pageState struct {
	vm     VMID
	gen    Generation
	digest Digest
	data   []byte
	dirty  bool
	valid  bool
}

// Source is the historical name for a source memory image.  Memory is the
// canonical type; the alias keeps the migration-host vocabulary convenient.
type Source = Memory

// NewSource constructs a source memory image with an explicit instance ID.
func NewSource(vm VMID, instance InstanceID, opts ...MemoryOption) *Source {
	opts = append(opts, WithSourceInstance(instance))
	return NewMemory(vm, opts...)
}

// Memory is a concurrency-safe, 64-page source memory image.  Every write
// atomically increments the page generation, replaces the digest and bytes,
// and sets dirty.
type Memory struct {
	mu   sync.Mutex
	cond *sync.Cond

	vm       VMID
	instance InstanceID
	epoch    Epoch

	pages      [MaxPages]pageState
	dirtyCount int

	frozen     bool
	freezing   bool
	sealed     bool
	terminated bool
	released   bool

	inFlight int
	writes   uint64

	freezePoint  *FreezePoint
	events       *EventDispatcher
	writeBarrier WriteBarrier
}

// NewMemory creates a source memory image.  The instance identity defaults to
// "source:<vm>"; use WithSourceInstance to make it globally unique.
func NewMemory(vm VMID, opts ...MemoryOption) *Memory {
	m := &Memory{vm: vm, instance: InstanceID("source:" + string(vm))}
	m.cond = sync.NewCond(&m.mu)
	for i := 0; i < MaxPages; i++ {
		m.pages[i].vm = vm
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	if m.instance == "" {
		m.instance = InstanceID("source:" + string(vm))
	}
	if m.events == nil {
		m.events = NewEventDispatcher(nil)
	}
	return m
}

// VM returns the memory owner identity.
func (m *Memory) VM() VMID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vm
}

// InstanceID returns the source instance identity.
func (m *Memory) InstanceID() InstanceID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instance
}

// Epoch returns the current global snapshot epoch.
func (m *Memory) Epoch() Epoch {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.epoch
}

// State returns the lifecycle state.
func (m *Memory) State() MemoryState {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.released:
		return MemoryReleased
	case m.terminated:
		return MemoryTerminated
	case m.frozen:
		return MemoryFrozen
	default:
		return MemoryRunning
	}
}

// Frozen reports whether new writes are blocked.
func (m *Memory) Frozen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.frozen
}

// Terminated reports whether the source has been permanently terminated.
func (m *Memory) Terminated() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.terminated || m.released
}

// EventDispatcher returns the memory's dispatcher.
func (m *Memory) EventDispatcher() *EventDispatcher {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.events
}

// WritePage performs a vCPU write.
func (m *Memory) WritePage(page PageID, data []byte) (Generation, error) {
	return m.WritePageKind(page, data, WriteVCPU)
}

// WriteDevicePage performs a device-DMA write.  It obeys the same generation
// and freeze rules as vCPU writes.
func (m *Memory) WriteDevicePage(page PageID, data []byte) (Generation, error) {
	return m.WritePageKind(page, data, WriteDMA)
}

// WritePageKind performs a source write.  Admission and state mutation are
// separated so that a freeze can observe and wait for an admitted write
// without holding the memory lock while the write barrier runs.
func (m *Memory) WritePageKind(page PageID, data []byte, kind WriteKind) (Generation, error) {
	if int(page) >= MaxPages {
		return 0, fmt.Errorf("%w: %d", ErrPageOutOfRange, page)
	}
	if kind == "" {
		kind = WriteVCPU
	}

	m.mu.Lock()
	if m.released {
		m.mu.Unlock()
		return 0, ErrReleased
	}
	if m.terminated {
		m.mu.Unlock()
		return 0, ErrTerminated
	}
	if m.frozen {
		m.mu.Unlock()
		return 0, ErrFrozen
	}
	m.inFlight++
	m.mu.Unlock()

	admission := WriteAdmission{Page: page, Kind: kind, Data: append([]byte(nil), data...)}
	if err := m.event(EventWriteBegin, EventPhaseBefore, EventFields{
		Page: page, Kind: kind, Data: admission.Data,
	}); err != nil {
		m.endWrite()
		return 0, err
	}

	if barrier := m.loadWriteBarrier(); barrier != nil {
		barrier(admission)
	}

	m.mu.Lock()
	// This write was admitted before any freeze.  It must be allowed to
	// complete even if the source became frozen while the barrier ran.  The
	// freeze waits for inFlight to reach zero before taking its final
	// snapshot.
	ps := &m.pages[page]
	ps.valid = true
	ps.vm = m.vm
	ps.gen++
	ps.data = append(ps.data[:0], admission.Data...)
	ps.digest = DigestBytes(ps.data)
	ps.dirty = true
	m.dirtyCount = m.countDirtyLocked()
	m.writes++
	gen := ps.gen
	m.inFlight--
	m.cond.Broadcast()
	m.mu.Unlock()

	if err := m.event(EventWriteCommit, EventPhaseAfter, EventFields{
		Page: page, Kind: kind, Generation: gen, Digest: DigestBytes(admission.Data),
	}); err != nil {
		return gen, err
	}
	return gen, nil
}

func (m *Memory) loadWriteBarrier() WriteBarrier {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeBarrier
}

func (m *Memory) endWrite() {
	m.mu.Lock()
	m.inFlight--
	m.cond.Broadcast()
	m.mu.Unlock()
}

func (m *Memory) countDirtyLocked() int {
	n := 0
	for i := 0; i < MaxPages; i++ {
		if m.pages[i].valid && m.pages[i].dirty {
			n++
		}
	}
	return n
}

// ReadPage returns a copy of the current source page bytes.
func (m *Memory) ReadPage(page PageID) ([]byte, error) {
	if int(page) >= MaxPages {
		return nil, fmt.Errorf("%w: %d", ErrPageOutOfRange, page)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.released {
		return nil, ErrReleased
	}
	ps := m.pages[page]
	if !ps.valid {
		return nil, ErrPageNotPresent
	}
	return append([]byte(nil), ps.data...), nil
}

// PageInfo returns the current source state for a page.
func (m *Memory) PageInfo(page PageID) (PageInfo, error) {
	if int(page) >= MaxPages {
		return PageInfo{}, fmt.Errorf("%w: %d", ErrPageOutOfRange, page)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.released {
		return PageInfo{}, ErrReleased
	}
	ps := m.pages[page]
	return PageInfo{
		Page:       page,
		VM:         ps.vm,
		Generation: ps.gen,
		Digest:     ps.digest,
		Dirty:      ps.dirty,
		Valid:      ps.valid,
	}, nil
}

// AllPageInfos returns a copy of all page states.
func (m *Memory) AllPageInfos() [MaxPages]PageInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out [MaxPages]PageInfo
	for i := 0; i < MaxPages; i++ {
		ps := m.pages[i]
		out[i] = PageInfo{
			Page:       PageID(i),
			VM:         ps.vm,
			Generation: ps.gen,
			Digest:     ps.digest,
			Dirty:      ps.dirty,
			Valid:      ps.valid,
		}
	}
	return out
}

// DirtyPages returns the current dirty page IDs in ascending order.
func (m *Memory) DirtyPages() []PageID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dirtyPagesLocked()
}

func (m *Memory) dirtyPagesLocked() []PageID {
	out := make([]PageID, 0, m.dirtyCount)
	for i := 0; i < MaxPages; i++ {
		if m.pages[i].valid && m.pages[i].dirty {
			out = append(out, PageID(i))
		}
	}
	return out
}

// SnapshotDirty atomically advances the global epoch and captures the dirty
// set, each dirty page's generation, digest and immutable bytes.
//
// A pre-copy round must copy from this snapshot and then call
// AcknowledgeCopied with the snapshot generation.  The acknowledgement only
// retires the dirty bit if the current source generation is still exactly the
// copied generation.
func (m *Memory) SnapshotDirty() (DirtySnapshot, error) {
	if err := m.event(EventRoundSnapshot, EventPhaseBefore, EventFields{}); err != nil {
		return DirtySnapshot{}, err
	}
	m.mu.Lock()
	if m.released {
		m.mu.Unlock()
		return DirtySnapshot{}, ErrReleased
	}
	if m.terminated {
		m.mu.Unlock()
		return DirtySnapshot{}, ErrTerminated
	}
	if m.frozen {
		m.mu.Unlock()
		return DirtySnapshot{}, ErrFrozen
	}
	m.epoch++
	snap := DirtySnapshot{
		Epoch:      m.epoch,
		VM:         m.vm,
		PageIDs:    m.dirtyPagesLocked(),
		Frozen:     false,
		SnapshotID: uint64(m.epoch),
	}
	snap.Pages = make([]PageSnapshot, 0, len(snap.PageIDs))
	for i := 0; i < MaxPages; i++ {
		ps := m.pages[i]
		snap.All[i] = PageInfo{
			Page: PageID(i), VM: ps.vm, Generation: ps.gen,
			Digest: ps.digest, Dirty: ps.dirty, Valid: ps.valid,
		}
	}
	for _, id := range snap.PageIDs {
		ps := m.pages[id]
		snap.Pages = append(snap.Pages, PageSnapshot{
			Page:       id,
			VM:         ps.vm,
			Generation: ps.gen,
			Digest:     ps.digest,
			Data:       append([]byte(nil), ps.data...),
		})
	}
	m.mu.Unlock()
	if err := m.event(EventRoundSnapshot, EventPhaseAfter, EventFields{Epoch: snap.Epoch}); err != nil {
		return snap, err
	}
	return snap, nil
}

// AcknowledgeCopied retires a dirty bit only if page's current generation is
// still exactly copiedGeneration.  A concurrent write therefore always wins:
// the dirty bit remains set and the next round must copy the newer generation.
func (m *Memory) AcknowledgeCopied(page PageID, copiedGeneration Generation) (bool, error) {
	if int(page) >= MaxPages {
		return false, fmt.Errorf("%w: %d", ErrPageOutOfRange, page)
	}
	if err := m.event(EventDirtyAck, EventPhaseBefore, EventFields{
		Page: page, Generation: copiedGeneration,
	}); err != nil {
		return false, err
	}
	m.mu.Lock()
	cleared := false
	if !m.released && !m.terminated {
		ps := &m.pages[page]
		if ps.valid && ps.dirty && ps.gen == copiedGeneration {
			ps.dirty = false
			m.dirtyCount = m.countDirtyLocked()
			cleared = true
		}
	}
	m.mu.Unlock()
	if err := m.event(EventDirtyAck, EventPhaseAfter, EventFields{
		Page: page, Generation: copiedGeneration, Cleared: cleared,
	}); err != nil {
		return cleared, err
	}
	return cleared, nil
}

// Freeze blocks all new writes, waits for every already-admitted write to
// complete, advances the epoch, and captures the final dirty set plus all page
// generations and digests.  Freeze is idempotent: a second call returns the
// same freeze point without advancing the epoch.
func (m *Memory) Freeze() (FreezePoint, error) {
	if err := m.event(EventFreezeBegin, EventPhaseBefore, EventFields{}); err != nil {
		return FreezePoint{}, err
	}
	m.mu.Lock()
	if m.released {
		m.mu.Unlock()
		return FreezePoint{}, ErrReleased
	}
	if m.terminated {
		m.mu.Unlock()
		return FreezePoint{}, ErrTerminated
	}
	for m.freezing {
		m.cond.Wait()
	}
	if m.freezePoint != nil {
		out := m.freezePoint.Clone()
		m.mu.Unlock()
		return out, nil
	}
	m.frozen = true
	m.freezing = true
	m.mu.Unlock()

	if err := m.event(EventFreezeWait, EventPhaseBefore, EventFields{}); err != nil {
		m.mu.Lock()
		m.frozen = false
		m.freezing = false
		m.cond.Broadcast()
		m.mu.Unlock()
		return FreezePoint{}, err
	}

	m.mu.Lock()
	waited := 0
	for m.inFlight > 0 {
		waited++
		m.cond.Wait()
	}
	m.epoch++
	fp := FreezePoint{Epoch: m.epoch, VM: m.vm}
	for i := 0; i < MaxPages; i++ {
		ps := m.pages[i]
		fp.All[i] = PageInfo{
			Page:       PageID(i),
			VM:         ps.vm,
			Generation: ps.gen,
			Digest:     ps.digest,
			Dirty:      ps.dirty,
			Valid:      ps.valid,
		}
		if ps.valid && ps.dirty {
			fp.Dirty = append(fp.Dirty, PageSnapshot{
				Page:       PageID(i),
				VM:         ps.vm,
				Generation: ps.gen,
				Digest:     ps.digest,
				Data:       append([]byte(nil), ps.data...),
			})
		}
	}
	m.freezePoint = &fp
	m.freezing = false
	m.cond.Broadcast()
	cloned := fp.Clone()
	m.mu.Unlock()

	if err := m.event(EventFreezeDone, EventPhaseAfter, EventFields{
		Epoch: fp.Epoch, WaitedWrites: uint64(waited),
	}); err != nil {
		return cloned, err
	}
	return cloned, nil
}

// Thaw resumes a frozen source.  After a successful cutover the freeze is
// sealed and direct thawing is refused; only Migrator.Abort (which first
// proves the target never started) may release a sealed freeze.
func (m *Memory) Thaw() error {
	return m.thaw(false)
}

func (m *Memory) thaw(force bool) error {
	if err := m.event(EventThaw, EventPhaseBefore, EventFields{}); err != nil {
		return err
	}
	m.mu.Lock()
	switch {
	case m.released:
		m.mu.Unlock()
		return ErrReleased
	case m.terminated:
		m.mu.Unlock()
		return ErrTerminated
	case !m.frozen:
		m.mu.Unlock()
		return ErrNotFrozen
	case m.freezing:
		m.mu.Unlock()
		return fmt.Errorf("%w: freeze is still in progress", ErrInvalidState)
	case m.sealed && !force:
		m.mu.Unlock()
		return ErrSealed
	}
	m.frozen = false
	m.sealed = false
	m.freezePoint = nil
	m.mu.Unlock()
	if err := m.event(EventThaw, EventPhaseAfter, EventFields{}); err != nil {
		return err
	}
	return nil
}

// thawForAbort is the only path that may release a sealed cutover freeze.  It
// is called by Migrator after proving the target is not live.
func (m *Memory) thawForAbort() error {
	return m.thaw(true)
}

// sealForCutover makes the frozen final state resistant to accidental local
// thawing.  The target-start path keeps the seal in place.
func (m *Memory) sealForCutover() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.released:
		return ErrReleased
	case m.terminated:
		return ErrTerminated
	case !m.frozen:
		return fmt.Errorf("%w: cutover must freeze the source first", ErrInvalidState)
	}
	m.sealed = true
	return nil
}

// markTerminated is only called after a validated target startup confirmation.
func (m *Memory) markTerminated() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.released:
		return ErrReleased
	case m.terminated:
		return ErrTerminated
	case !m.frozen:
		return fmt.Errorf("%w: source must remain frozen until startup confirmation", ErrInvalidState)
	}
	m.terminated = true
	return nil
}

// Release discards source memory.  It fails until the source has been
// permanently terminated by a validated startup confirmation.
func (m *Memory) Release() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.released {
		return ErrReleased
	}
	if !m.terminated {
		return ErrNotTerminated
	}
	for i := 0; i < MaxPages; i++ {
		m.pages[i] = pageState{vm: m.vm}
	}
	m.dirtyCount = 0
	m.freezePoint = nil
	m.released = true
	return nil
}

// ReuseMemory is an alias for Release.  Both names enforce the same rule:
// memory may not be recycled before startup confirmation.
func (m *Memory) ReuseMemory() error {
	return m.Release()
}

// Reuse is an alias for Release.
func (m *Memory) Reuse() error {
	return m.Release()
}

// Free is an alias for Release.
func (m *Memory) Free() error {
	return m.Release()
}

func (m *Memory) event(name EventName, phase EventPhase, f EventFields) error {
	m.mu.Lock()
	d := m.events
	m.mu.Unlock()
	if d == nil {
		return nil
	}
	return d.Emit(name, phase, f)
}

// eventLockedSafe is used where a deferred unlock is active.  The dispatcher
// is copied first in the caller's critical section; emitting outside the lock
// is not possible in SnapshotDirty without restructuring, so this helper keeps
// the callback under the memory lock deliberately only for the post-snapshot
// notification, which must not mutate memory.
