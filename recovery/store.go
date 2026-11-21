package recovery

import (
	"errors"
	"fmt"
	"sync"
)

// StableStore is the storage-host boundary. Implementations must return a
// fixed-size frame for every slot, including never-written slots (zero-filled
// is appropriate). A nil error from Write means the complete frame is durable.
type StableStore interface {
	Read(region Region, slot, length int) ([]byte, error)
	Write(region Region, slot int, frame []byte) error
}

// StorageGeometry is implemented by stores that can describe their fixed
// geometry. New validates the lane layout against it when available.
type StorageGeometry interface {
	RegionSlots(region Region) int
	FrameSize() int
}

// FailureKind controls the behavior of a one-shot MemStore failure.
type FailureKind uint8

const (
	// FailBeforeWrite writes no bytes and returns an error.
	FailBeforeWrite FailureKind = iota
	// FailPartialWrite durably writes the first Bytes bytes and then returns an
	// error. The result is a torn frame.
	FailPartialWrite
	// FailAfterWrite durably writes the frame but returns an error, modelling an
	// ambiguous acknowledgment.
	FailAfterWrite
	// FailRead returns an error without changing storage.
	FailRead
)

// Failure is a one-shot, publicly scripted storage fault.
type Failure struct {
	Op     Op
	Region Region
	Slot   int // -1 matches any slot
	Kind   FailureKind
	Bytes  int
	Err    error
}

// WriteRecord describes an attempted write without exposing a content API.
// Data is exported for test assertions that ciphertext contains no plaintext.
type WriteRecord struct {
	Region Region
	Slot   int
	Length int
	Data   []byte
	Err    error
}

// ReadRecord describes an attempted read.
type ReadRecord struct {
	Region Region
	Slot   int
	Length int
	Err    error
}

// MemStore is a deterministic, fixed-capacity, two-region in-memory store used
// by the protocol tests and by the crash verifier. It is safe for concurrent
// use.
type MemStore struct {
	mu        sync.RWMutex
	slots     int
	frameSize int
	regions   [2][][]byte
	failures  []Failure
	writes    []WriteRecord
	reads     []ReadRecord
	ackHook   func()
}

// NewMemStore creates an empty two-region store.
func NewMemStore(regionSlots, frameSize int) (*MemStore, error) {
	if regionSlots <= 0 || frameSize < minFrameSize {
		return nil, fmt.Errorf("%w: slots=%d frameSize=%d", ErrConfig, regionSlots, frameSize)
	}
	s := &MemStore{slots: regionSlots, frameSize: frameSize}
	for r := range s.regions {
		s.regions[r] = make([][]byte, regionSlots)
	}
	return s, nil
}

func (s *MemStore) RegionSlots(region Region) int {
	if region != RegionA && region != RegionB {
		return 0
	}
	return s.slots
}

func (s *MemStore) FrameSize() int { return s.frameSize }

// QueueFailure installs a one-shot failure. The first later operation matching
// Op, Region, and Slot consumes it.
func (s *MemStore) QueueFailure(f Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.Op != OpRead && f.Op != OpWrite {
		f.Op = OpWrite
	}
	if f.Err == nil {
		f.Err = errors.New("simulated storage failure")
	}
	if f.Op == OpWrite && f.Kind == FailPartialWrite && f.Bytes < 0 {
		f.Bytes = 0
	}
	s.failures = append(s.failures, f)
}

// ClearFailures removes all queued failures.
func (s *MemStore) ClearFailures() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = nil
}

// SetAckHook installs a callback invoked after a successful (nil-error) write.
func (s *MemStore) SetAckHook(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ackHook = fn
}

func (s *MemStore) Read(region Region, slot, length int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	failure, ok := s.takeFailure(OpRead, region, slot)
	if ok {
		s.reads = append(s.reads, ReadRecord{Region: region, Slot: slot, Length: length, Err: failure.Err})
		return nil, failure.Err
	}
	if err := s.validate(region, slot); err != nil {
		s.reads = append(s.reads, ReadRecord{Region: region, Slot: slot, Length: length, Err: err})
		return nil, err
	}
	if length < 0 || length > s.frameSize {
		s.reads = append(s.reads, ReadRecord{Region: region, Slot: slot, Length: length, Err: ErrGeometry})
		return nil, ErrGeometry
	}
	out := make([]byte, length)
	if frame := s.regions[region][slot]; frame != nil {
		copy(out, frame)
	}
	s.reads = append(s.reads, ReadRecord{Region: region, Slot: slot, Length: length})
	return out, nil
}

func (s *MemStore) Write(region Region, slot int, frame []byte) error {
	s.mu.Lock()
	failure, hasFailure := s.takeFailure(OpWrite, region, slot)
	if err := s.validate(region, slot); err != nil {
		s.writes = append(s.writes, WriteRecord{Region: region, Slot: slot, Length: len(frame), Err: err})
		s.mu.Unlock()
		return err
	}
	if len(frame) != s.frameSize {
		s.writes = append(s.writes, WriteRecord{Region: region, Slot: slot, Length: len(frame), Err: ErrGeometry})
		s.mu.Unlock()
		return ErrGeometry
	}

	var retErr error
	if hasFailure {
		switch failure.Kind {
		case FailBeforeWrite:
			retErr = failure.Err
		case FailPartialWrite:
			n := failure.Bytes
			if n < 0 {
				n = 0
			}
			if n > len(frame) {
				n = len(frame)
			}
			dst := make([]byte, s.frameSize)
			copy(dst, frame[:n])
			s.regions[region][slot] = dst
			retErr = failure.Err
		case FailAfterWrite:
			dst := make([]byte, s.frameSize)
			copy(dst, frame)
			s.regions[region][slot] = dst
			retErr = failure.Err
		default:
			retErr = failure.Err
		}
	} else {
		dst := make([]byte, s.frameSize)
		copy(dst, frame)
		s.regions[region][slot] = dst
	}
	record := WriteRecord{
		Region: region,
		Slot:   slot,
		Length: len(frame),
		Data:   append([]byte(nil), frame...),
		Err:    retErr,
	}
	s.writes = append(s.writes, record)
	hook := s.ackHook
	s.mu.Unlock()

	if retErr == nil && hook != nil {
		hook()
	}
	return retErr
}

func (s *MemStore) validate(region Region, slot int) error {
	if region != RegionA && region != RegionB {
		return fmt.Errorf("%w: invalid region", ErrGeometry)
	}
	if slot < 0 || slot >= s.slots {
		return fmt.Errorf("%w: slot %d outside [0,%d)", ErrGeometry, slot, s.slots)
	}
	return nil
}

func (s *MemStore) takeFailure(op Op, region Region, slot int) (Failure, bool) {
	for i, f := range s.failures {
		if f.Op != op {
			continue
		}
		if f.Region != region {
			continue
		}
		if f.Slot >= 0 && f.Slot != slot {
			continue
		}
		s.failures = append(s.failures[:i], s.failures[i+1:]...)
		return f, true
	}
	return Failure{}, false
}

// WriteLog returns a copy of all attempted writes.
func (s *MemStore) WriteLog() []WriteRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]WriteRecord, len(s.writes))
	for i, rec := range s.writes {
		rec.Data = append([]byte(nil), rec.Data...)
		out[i] = rec
	}
	return out
}

// ReadLog returns a copy of all attempted reads.
func (s *MemStore) ReadLog() []ReadRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ReadRecord, len(s.reads))
	copy(out, s.reads)
	return out
}

// RawState is a deep copy of both fixed log regions.
type RawState struct {
	Slots     int
	FrameSize int
	A         [][]byte
	B         [][]byte
}

// Snapshot captures raw ciphertext for crash-prefix enumeration.
func (s *MemStore) Snapshot() RawState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return RawState{
		Slots:     s.slots,
		FrameSize: s.frameSize,
		A:         cloneSlots(s.regions[RegionA]),
		B:         cloneSlots(s.regions[RegionB]),
	}
}

func cloneSlots(src [][]byte) [][]byte {
	out := make([][]byte, len(src))
	for i, frame := range src {
		if frame != nil {
			out[i] = append([]byte(nil), frame...)
		}
	}
	return out
}

// RestoreRaw replaces store content with raw encrypted state.
func (s *MemStore) RestoreRaw(state RawState) error {
	if state.Slots != s.slots || state.FrameSize != s.frameSize {
		return ErrGeometry
	}
	if len(state.A) != s.slots || len(state.B) != s.slots {
		return ErrGeometry
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.regions[RegionA] = cloneSlots(state.A)
	s.regions[RegionB] = cloneSlots(state.B)
	return nil
}

// Clone returns an independent store with the same ciphertext and geometry.
func (s *MemStore) Clone() (*MemStore, error) {
	state := s.Snapshot()
	return NewMemStoreFromRaw(state)
}

// NewMemStoreFromRaw creates a store from a raw crash-prefix image.
func NewMemStoreFromRaw(state RawState) (*MemStore, error) {
	clone, err := NewMemStore(state.Slots, state.FrameSize)
	if err != nil {
		return nil, err
	}
	if err := clone.RestoreRaw(state); err != nil {
		return nil, err
	}
	return clone, nil
}

// Seed writes raw ciphertext into a slot. It is intended for constructing
// adversarial and foreign-tenant initial states.
func (s *MemStore) Seed(region Region, slot int, frame []byte) error {
	return s.Write(region, slot, frame)
}
