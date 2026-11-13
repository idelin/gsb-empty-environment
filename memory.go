package netsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// NewState returns an empty, schema-versioned ordinary state with the chosen
// active slot. It is useful when a caller needs to seed a development store;
// production callers normally obtain state through the host operations.
func NewState(active SlotID) State {
	return emptyState(active)
}

// EncodeState validates and serializes ordinary state. The resulting bytes
// can be placed in any StateStore.
func EncodeState(state State) ([]byte, error) {
	if !state.Active.Valid() {
		return nil, fmt.Errorf("%w: invalid active slot", ErrInvalidState)
	}
	for slot := SlotID(0); slot < SlotCount; slot++ {
		ss := state.Slots[slot]
		if ss.Candidate && ss.Success {
			return nil, fmt.Errorf("%w: slot %s is both candidate and successful", ErrInvalidState, slot)
		}
		if slotHasManifest(ss) && ss.VersionIndex != ss.Manifest.RollbackIndex {
			return nil, fmt.Errorf("%w: slot %s version index mismatch", ErrInvalidState, slot)
		}
	}
	state.SchemaVersion = stateSchemaVersion
	return json.Marshal(state)
}

// DecodeState parses ordinary state produced by EncodeState.
func DecodeState(blob []byte) (State, error) {
	var state State
	if err := json.Unmarshal(blob, &state); err != nil {
		return State{}, fmt.Errorf("%w: %w", ErrInvalidState, err)
	}
	if state.SchemaVersion != stateSchemaVersion {
		return State{}, fmt.Errorf("%w: schema version %d", ErrInvalidState, state.SchemaVersion)
	}
	if !state.Active.Valid() {
		return State{}, fmt.Errorf("%w: invalid active slot", ErrInvalidState)
	}
	return state, nil
}

// MemoryFuse is a thread-safe reference implementation of RollbackFuse. It
// only ever increases the stored index.
type MemoryFuse struct {
	mu    sync.Mutex
	index uint32
}

func NewMemoryFuse(index uint32) *MemoryFuse {
	return &MemoryFuse{index: index}
}

func (f *MemoryFuse) ReadRollbackIndex() (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.index, nil
}

func (f *MemoryFuse) RaiseRollbackIndex(to uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if to < f.index {
		return fmt.Errorf("%w: %d < %d", ErrFuseRaise, to, f.index)
	}
	f.index = to
	return nil
}

// MemoryStore is a thread-safe StateStore that keeps every committed blob.
// It is also a convenient attacker model: RestoreSnapshot can select any
// historical blob after a simulated crash.
type MemoryStore struct {
	mu        sync.Mutex
	current   []byte
	snapshots [][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func NewMemoryStoreWithState(state State) (*MemoryStore, error) {
	blob, err := EncodeState(state)
	if err != nil {
		return nil, err
	}
	store := NewMemoryStore()
	store.current = append([]byte(nil), blob...)
	return store, nil
}

func (s *MemoryStore) Load() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil, ErrNoState
	}
	return append([]byte(nil), s.current...), nil
}

func (s *MemoryStore) Save(blob []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyOfBlob := append([]byte(nil), blob...)
	s.current = copyOfBlob
	s.snapshots = append(s.snapshots, copyOfBlob)
	return nil
}

// SnapshotCount returns the number of committed Save calls.
func (s *MemoryStore) SnapshotCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.snapshots)
}

// Snapshot returns one committed state blob. Index zero is the first Save.
func (s *MemoryStore) Snapshot(index int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.snapshots) {
		return nil, errors.New("netsec: snapshot index out of range")
	}
	return append([]byte(nil), s.snapshots[index]...), nil
}

// RestoreSnapshot simulates an attacker replacing ordinary state with an old
// committed snapshot. It does not touch the trusted fuse or the slot images.
func (s *MemoryStore) RestoreSnapshot(index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.snapshots) {
		return errors.New("netsec: snapshot index out of range")
	}
	s.current = append([]byte(nil), s.snapshots[index]...)
	return nil
}

// ResetToNoState simulates loss of all ordinary state.
func (s *MemoryStore) ResetToNoState() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = nil
}

// MemoryImages is a thread-safe in-memory SlotImages implementation.
type MemoryImages struct {
	mu     sync.Mutex
	images [SlotCount][]byte
}

func NewMemoryImages() *MemoryImages {
	return &MemoryImages{}
}

func (m *MemoryImages) WriteImage(slot SlotID, image []byte) error {
	if !slot.Valid() {
		return errors.New("netsec: invalid slot")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.images[slot] = append([]byte(nil), image...)
	return nil
}

func (m *MemoryImages) ReadImage(slot SlotID) ([]byte, error) {
	if !slot.Valid() {
		return nil, errors.New("netsec: invalid slot")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.images[slot] == nil {
		return nil, errors.New("netsec: slot is empty")
	}
	return append([]byte(nil), m.images[slot]...), nil
}

// SetImage seeds a slot for tests or development.
func (m *MemoryImages) SetImage(slot SlotID, image []byte) {
	_ = m.WriteImage(slot, image)
}

// Snapshot returns a copy of both slot payloads. A caller can keep it next to
// an ordinary State snapshot to model a coherent rollback of image storage.
func (m *MemoryImages) Snapshot() [SlotCount][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var snapshot [SlotCount][]byte
	for slot := SlotID(0); slot < SlotCount; slot++ {
		snapshot[slot] = append([]byte(nil), m.images[slot]...)
	}
	return snapshot
}

// Restore replaces both slot payloads with a previous Snapshot.
func (m *MemoryImages) Restore(snapshot [SlotCount][]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for slot := SlotID(0); slot < SlotCount; slot++ {
		m.images[slot] = append([]byte(nil), snapshot[slot]...)
	}
}
