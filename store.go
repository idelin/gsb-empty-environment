// Package slotstore is a fixed-slot record store with generational handles.
//
// Records live in fixed-size slots inside a caller-specified directory.
// A Handle (slot index + generation) identifies a record; deleting a record
// bumps the slot generation so stale handles can never observe the next
// object that reuses the slot. Free slots are chained and cross-validated
// against slot state on recovery; torn or half-written slots are detected
// via a CRC32C trailer and scrubbed.
package slotstore

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const (
	metaFileName = "superblock"
	dataFileName = "slots.dat"
)

var (
	ErrStaleHandle = errors.New("slotstore: stale handle (slot reused or deleted)")
	ErrNotOccupied = errors.New("slotstore: slot does not hold a live record")
	ErrStoreFull   = errors.New("slotstore: no free slots")
	ErrTooLarge    = errors.New("slotstore: payload exceeds slot capacity")
	ErrCrashed     = errors.New("slotstore: store is crashed (injected fault); reopen from disk")
	ErrHandleRange = errors.New("slotstore: handle slot index out of range")
)

// CorruptError reports on-disk damage that recovery cannot safely repair.
type CorruptError struct{ Reason string }

func (e *CorruptError) Error() string { return "slotstore: corrupt store: " + e.Reason }

// FaultPoint identifies an injection point in the write path.
type FaultPoint int

const (
	FaultBeforeSlotWrite FaultPoint = iota
	FaultAfterSlotWrite             // slot written, not yet fsynced
	FaultAfterSlotSync              // slot durable, superblock not yet updated
	FaultAfterSuperSync             // operation fully committed
)

// FaultInjector simulates process crashes / power loss at injection points.
// Returning a non-nil error makes the store abort the operation immediately
// (no cleanup, no further writes) and enter the crashed state, mimicking a
// process kill at that exact point. Reopen the directory to recover.
type FaultInjector interface {
	Inject(p FaultPoint) error
}

// Config controls store creation. SlotCount and PayloadCap only apply when
// initializing an empty directory; an existing store's on-disk geometry wins.
type Config struct {
	SlotCount  uint32
	PayloadCap uint32
	Injector   FaultInjector
}

// Handle is a generational reference to a record.
type Handle struct {
	Slot uint32
	Gen  uint64
}

// Store is a fixed-slot record store. Not safe to copy; use one *Store.
type Store struct {
	mu      sync.Mutex
	dir     string
	meta    *os.File
	data    *os.File
	inj     FaultInjector
	crashed bool

	slotCount  uint32
	payloadCap uint32
	stride     int64

	freeHead int32
	gen      []uint64
	free     []bool
	freeNext []int32
	freePrev []int32
}

// Open opens (or initializes) the store in dir. The returned RecoveryReport
// describes repairs performed while opening an existing store; it is nil for
// a freshly created store.
func Open(dir string, cfg Config) (*Store, *RecoveryReport, error) {
	if cfg.SlotCount == 0 {
		cfg.SlotCount = 128
	}
	if cfg.PayloadCap == 0 {
		cfg.PayloadCap = 256
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, nil, err
	}
	metaPath := filepath.Join(dir, metaFileName)
	dataPath := filepath.Join(dir, dataFileName)

	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		s, err := createStore(dir, metaPath, dataPath, cfg)
		return s, nil, err
	}
	return recoverStore(dir, metaPath, dataPath, cfg)
}

func createStore(dir, metaPath, dataPath string, cfg Config) (*Store, error) {
	s := &Store{
		dir:        dir,
		inj:        cfg.Injector,
		slotCount:  cfg.SlotCount,
		payloadCap: cfg.PayloadCap,
		stride:     int64(slotOverhead + cfg.PayloadCap),
		freeHead:   0,
		gen:        make([]uint64, cfg.SlotCount),
		free:       make([]bool, cfg.SlotCount),
		freeNext:   make([]int32, cfg.SlotCount),
		freePrev:   make([]int32, cfg.SlotCount),
	}
	var err error
	if s.data, err = os.OpenFile(dataPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644); err != nil {
		return nil, err
	}
	if s.meta, err = os.OpenFile(metaPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644); err != nil {
		s.data.Close()
		return nil, err
	}
	// Lay out the initial free chain: 0 -> 1 -> ... -> n-1 -> -1.
	buf := make([]byte, s.stride)
	for i := uint32(0); i < s.slotCount; i++ {
		s.free[i] = true
		s.freeNext[i] = int32(i) + 1
		s.freePrev[i] = int32(i) - 1
		if i == 0 {
			s.freePrev[i] = -1
		}
		if i == s.slotCount-1 {
			s.freeNext[i] = -1
		}
		encodeSlot(buf, slot{state: stateFree, gen: 0, next: s.freeNext[i], prev: s.freePrev[i]}, s.payloadCap)
		if _, err := s.data.WriteAt(buf, s.offset(i)); err != nil {
			s.Close()
			return nil, err
		}
	}
	if err := s.writeSuper(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.data.Sync(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) offset(i uint32) int64 { return int64(i) * s.stride }

// Dir returns the directory holding the store files.
func (s *Store) Dir() string { return s.dir }

// PayloadCap returns the maximum payload bytes per record.
func (s *Store) PayloadCap() uint32 { return s.payloadCap }

func (s *Store) inject(p FaultPoint) error {
	if s.inj == nil {
		return nil
	}
	if err := s.inj.Inject(p); err != nil {
		s.crashed = true
		return err
	}
	return nil
}

func (s *Store) writeSuper() error {
	buf := encodeSuper(superblock{slotCount: s.slotCount, payloadCap: s.payloadCap, freeHead: s.freeHead})
	if _, err := s.meta.WriteAt(buf, 0); err != nil {
		return err
	}
	return s.meta.Sync()
}

// Put stores payload in a free slot and returns its generational handle.
// Durability order: slot write, fsync(data), superblock update, fsync(meta).
// A crash anywhere leaves either the old or the new state, never garbage.
func (s *Store) Put(payload []byte) (Handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return Handle{}, ErrCrashed
	}
	if uint32(len(payload)) > s.payloadCap {
		return Handle{}, ErrTooLarge
	}
	if s.freeHead < 0 {
		return Handle{}, ErrStoreFull
	}
	idx := uint32(s.freeHead)
	gen := s.gen[idx]

	if err := s.inject(FaultBeforeSlotWrite); err != nil {
		return Handle{}, err
	}
	buf := make([]byte, s.stride)
	encodeSlot(buf, slot{state: stateOccupied, gen: gen, next: -1, prev: -1, payload: payload}, s.payloadCap)
	if _, err := s.data.WriteAt(buf, s.offset(idx)); err != nil {
		return Handle{}, err
	}
	if err := s.inject(FaultAfterSlotWrite); err != nil {
		return Handle{}, err
	}
	if err := s.data.Sync(); err != nil {
		return Handle{}, err
	}
	if err := s.inject(FaultAfterSlotSync); err != nil {
		return Handle{}, err
	}
	// Commit: pop the slot from the free chain.
	s.freeHead = s.freeNext[idx]
	if err := s.writeSuper(); err != nil {
		return Handle{}, err
	}
	if err := s.inject(FaultAfterSuperSync); err != nil {
		return Handle{}, err
	}
	// Backlink fixup: the new head's freePrev must be -1. A crash before
	// this lands is a repairable backlink mismatch, found at recovery.
	if s.freeHead >= 0 {
		nh := uint32(s.freeHead)
		encodeSlot(buf, slot{state: stateFree, gen: s.gen[nh], next: s.freeNext[nh], prev: -1}, s.payloadCap)
		if _, err := s.data.WriteAt(buf, s.offset(nh)); err != nil {
			return Handle{}, err
		}
		if err := s.data.Sync(); err != nil {
			return Handle{}, err
		}
		s.freePrev[nh] = -1
	}
	s.free[idx] = false
	s.freeNext[idx], s.freePrev[idx] = -1, -1
	return Handle{Slot: idx, Gen: gen}, nil
}

// Delete frees the slot referenced by h and bumps its generation, so h and
// any copies of it are permanently invalidated.
func (s *Store) Delete(h Handle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return ErrCrashed
	}
	if err := s.validate(h); err != nil {
		return err
	}
	idx := h.Slot
	newGen := s.gen[idx] + 1
	oldHead := s.freeHead

	if err := s.inject(FaultBeforeSlotWrite); err != nil {
		return err
	}
	buf := make([]byte, s.stride)
	encodeSlot(buf, slot{state: stateFree, gen: newGen, next: oldHead, prev: -1}, s.payloadCap)
	if _, err := s.data.WriteAt(buf, s.offset(idx)); err != nil {
		return err
	}
	if err := s.inject(FaultAfterSlotWrite); err != nil {
		return err
	}
	if err := s.data.Sync(); err != nil {
		return err
	}
	if err := s.inject(FaultAfterSlotSync); err != nil {
		return err
	}
	// Commit: push the slot onto the free chain.
	s.freeHead = int32(idx)
	if err := s.writeSuper(); err != nil {
		return err
	}
	if err := s.inject(FaultAfterSuperSync); err != nil {
		return err
	}
	// Backlink fixup: old head's freePrev now points at idx. A crash before
	// this lands is a repairable backlink mismatch, found at recovery.
	if oldHead >= 0 {
		s.freePrev[oldHead] = int32(idx)
		encodeSlot(buf, slot{state: stateFree, gen: s.gen[oldHead], next: s.freeNext[oldHead], prev: int32(idx)}, s.payloadCap)
		if _, err := s.data.WriteAt(buf, s.offset(uint32(oldHead))); err != nil {
			return err
		}
		if err := s.data.Sync(); err != nil {
			return err
		}
	}
	s.gen[idx] = newGen
	s.free[idx] = true
	s.freeNext[idx] = oldHead
	s.freePrev[idx] = -1
	return nil
}

func (s *Store) validate(h Handle) error {
	if h.Slot >= s.slotCount {
		return ErrHandleRange
	}
	if s.free[h.Slot] {
		return ErrStaleHandle
	}
	if s.gen[h.Slot] != h.Gen {
		return ErrStaleHandle
	}
	return nil
}

// Get returns the payload for a live handle, or ErrStaleHandle if the slot
// was deleted or reused since the handle was issued.
func (s *Store) Get(h Handle) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return nil, ErrCrashed
	}
	if err := s.validate(h); err != nil {
		return nil, err
	}
	sl, ok := s.readSlot(h.Slot)
	if !ok || sl.state != stateOccupied || sl.gen != h.Gen {
		return nil, ErrStaleHandle
	}
	return append([]byte(nil), sl.payload...), nil
}

func (s *Store) readSlot(i uint32) (slot, bool) {
	buf := make([]byte, s.stride)
	if _, err := s.data.ReadAt(buf, s.offset(i)); err != nil {
		return slot{}, false
	}
	return decodeSlot(buf, s.payloadCap)
}

// ForEach calls fn for every live record, in slot order.
func (s *Store) ForEach(fn func(h Handle, payload []byte)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := uint32(0); i < s.slotCount; i++ {
		if s.free[i] {
			continue
		}
		sl, ok := s.readSlot(i)
		if !ok || sl.state != stateOccupied {
			continue
		}
		fn(Handle{Slot: i, Gen: s.gen[i]}, append([]byte(nil), sl.payload...))
	}
}

// Close flushes and closes the store files.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err1 := s.data.Sync()
	err2 := s.meta.Sync()
	err3 := s.data.Close()
	err4 := s.meta.Close()
	for _, e := range []error{err1, err2, err3, err4} {
		if e != nil {
			return e
		}
	}
	return nil
}
