package netsec

import (
	"context"
	"fmt"
	"sync"
)

// MemoryStore is an in-memory transactional Store.
//
// Each label has its own lock, so transactions on different labels run in
// parallel while transactions touching the same label are fully serialized.
// The callback observes and returns deep copies, making it impossible for a
// caller to mutate committed records outside a transaction.
type MemoryStore struct {
	mu     sync.Mutex
	labels map[string]*labelSlot

	// failUpdate, when non-nil, is consulted before every commit. Returning an
	// error aborts the transaction; it is intended for fault-injection tests.
	failUpdate func(label string, next *LabelRecord) error
}

type labelSlot struct {
	mu sync.Mutex
	// rec is nil while the label is absent.
	rec *LabelRecord
}

// NewMemoryStore creates an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{labels: make(map[string]*labelSlot)}
}

// setFailHook installs a pre-commit fault hook (test helper).
func (m *MemoryStore) setFailHook(hook func(label string, next *LabelRecord) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failUpdate = hook
}

// Update implements Store.
func (m *MemoryStore) Update(ctx context.Context, label string, fn func(rec *LabelRecord) (*LabelRecord, error)) (*LabelRecord, error) {
	if label == "" {
		return nil, fmt.Errorf("%w: empty label", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	slot := m.slot(label)
	slot.mu.Lock()
	defer slot.mu.Unlock()

	current := cloneRecord(slot.rec)
	next, err := fn(current)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if next == nil {
		// Read-only transaction: return the snapshot without mutating.
		return cloneRecord(slot.rec), nil
	}
	if next.Label != label {
		return nil, fmt.Errorf("%w: record label %q does not match key %q", ErrIllegalTransition, next.Label, label)
	}
	if slot.rec != nil && sameRecord(slot.rec, next) {
		// Idempotent no-op: keep the record and its version untouched.
		return cloneRecord(slot.rec), nil
	}
	if err := validateTransition(slot.rec, next); err != nil {
		return nil, err
	}

	m.mu.Lock()
	hook := m.failUpdate
	m.mu.Unlock()
	if hook != nil {
		if err := hook(label, cloneRecord(next)); err != nil {
			return nil, err
		}
	}

	if slot.rec == nil {
		next.Version = 1
	} else {
		next.Version = slot.rec.Version + 1
	}
	slot.rec = cloneRecord(next)
	return cloneRecord(slot.rec), nil
}

// Label implements Store and returns a deep copy, or (nil, nil) when absent.
func (m *MemoryStore) Label(ctx context.Context, label string) (*LabelRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slot := m.slot(label)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return cloneRecord(slot.rec), nil
}

func (m *MemoryStore) slot(label string) *labelSlot {
	m.mu.Lock()
	defer m.mu.Unlock()
	slot, ok := m.labels[label]
	if !ok {
		slot = &labelSlot{}
		m.labels[label] = slot
	}
	return slot
}

// validateTransition enforces the label state machine inside the storage
// layer as a second line of defense behind the service logic.
func validateTransition(prev, next *LabelRecord) error {
	if prev == nil {
		if next.State == StateReserved || next.State == StatePublished {
			return nil
		}
		return illegal(prev, next)
	}
	switch prev.State {
	case StateReserved:
		// Re-acquisition after expiry, publish, release or same-request retry.
		if next.State == StateReserved || next.State == StatePublished || next.State == StateRevoked {
			return nil
		}
	case StatePublished:
		// An immutable label stays published forever.
		if next.State == StatePublished {
			return nil
		}
	case StateRevoked:
		if next.State == StateReserved || next.State == StatePublished {
			return nil
		}
	}
	return illegal(prev, next)
}

func sameRecord(a, b *LabelRecord) bool {
	return a.State == b.State &&
		a.Digest == b.Digest &&
		a.RequestID == b.RequestID &&
		a.ExpiresAt.Equal(b.ExpiresAt)
}

func illegal(prev, next *LabelRecord) error {
	prevState := StateAbsent
	if prev != nil {
		prevState = prev.State
	}
	return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, prevState, next.State)
}

func cloneRecord(rec *LabelRecord) *LabelRecord {
	if rec == nil {
		return nil
	}
	cp := *rec
	return &cp
}
