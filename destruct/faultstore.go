package destruct

import (
	"context"
	"sync"
)

// Mutation labels identify each mutating store operation for fault scripts.
const (
	MutCreateVolume      = "CreateVolume"
	MutCompareSwapVolume = "CompareSwapVolume"
	MutCreateLease       = "CreateLease"
	MutCompareSwapLease  = "CompareSwapLease"
	MutCreateTxn         = "CreateTxn"
	MutCompareSwapTxn    = "CompareSwapTxn"
	MutPutConfirmation   = "PutConfirmation"
)

// FaultAction is what a FaultStore does when its fault script fires.
type FaultAction int

const (
	// FaultError makes the failing mutation return ErrInjectedFault without
	// applying it. The coordinator must treat this like any storage error and
	// retry; replaying the whole destruction run must converge.
	FaultError FaultAction = iota
	// FaultCrash simulates a process crash: all subsequent calls fail with
	// ErrInjectedCrash until the test builds a fresh FaultStore over the same
	// MemDisk. The triggering mutation is deliberately not applied.
	FaultCrash
)

type faultStep struct {
	op     string
	action FaultAction
	once   bool
}

// FaultStore wraps a Store and injects failures into mutating calls according
// to an ordered script. It is safe for concurrent use: mutation numbering is
// atomic, which is what the concurrent lease-revocation tests rely on.
type FaultStore struct {
	Store

	mu       sync.Mutex
	steps    []faultStep
	crashed  bool
	mutation int
	armed    bool
}

// NewFaultStore wraps an existing store.
func NewFaultStore(inner Store) *FaultStore {
	return &FaultStore{Store: inner}
}

// Arm enables script evaluation. Scripts installed while disarmed keep
// counting mutations but never fire, which lets tests finish all setup
// (volume and lease creation) before injecting destruction-time faults.
func (f *FaultStore) Arm() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed = true
}

// InjectFault appends a script entry. When one mutating call matches op it
// performs action; with once=true the entry is consumed after firing.
func (f *FaultStore) InjectFault(op string, action FaultAction, once bool) *FaultStore {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, faultStep{op: op, action: action, once: once})
	return f
}

// MutationCount returns how many mutating calls have been observed.
func (f *FaultStore) MutationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mutation
}

// Clear removes all script entries and resets the crash flag.
func (f *FaultStore) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = nil
	f.crashed = false
	f.armed = false
}

// gate runs before every mutation. It returns a non-nil error when the call
// must be short-circuited.
func (f *FaultStore) gate(op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutation++
	if f.crashed {
		return ErrInjectedCrash
	}
	if !f.armed {
		return nil
	}
	for i := len(f.steps) - 1; i >= 0; i-- {
		step := f.steps[i]
		if step.op != op {
			continue
		}
		if step.action == FaultCrash {
			f.crashed = true
		}
		if step.once {
			f.steps = append(f.steps[:i], f.steps[i+1:]...)
		}
		if step.action == FaultCrash {
			return ErrInjectedCrash
		}
		return ErrInjectedFault
	}
	return nil
}

// CreateVolume implements Store.
func (f *FaultStore) CreateVolume(ctx context.Context, v *VolumeRecord) error {
	if err := f.gate(MutCreateVolume); err != nil {
		return err
	}
	return f.Store.CreateVolume(ctx, v)
}

// CompareSwapVolume implements Store.
func (f *FaultStore) CompareSwapVolume(ctx context.Context, old, next *VolumeRecord) error {
	if err := f.gate(MutCompareSwapVolume); err != nil {
		return err
	}
	return f.Store.CompareSwapVolume(ctx, old, next)
}

// CreateLease implements Store.
func (f *FaultStore) CreateLease(ctx context.Context, l *LeaseRecord) error {
	if err := f.gate(MutCreateLease); err != nil {
		return err
	}
	return f.Store.CreateLease(ctx, l)
}

// CompareSwapLease implements Store.
func (f *FaultStore) CompareSwapLease(ctx context.Context, old, next *LeaseRecord) error {
	if err := f.gate(MutCompareSwapLease); err != nil {
		return err
	}
	return f.Store.CompareSwapLease(ctx, old, next)
}

// CreateTxn implements Store.
func (f *FaultStore) CreateTxn(ctx context.Context, t *DestroyTxn) error {
	if err := f.gate(MutCreateTxn); err != nil {
		return err
	}
	return f.Store.CreateTxn(ctx, t)
}

// CompareSwapTxn implements Store.
func (f *FaultStore) CompareSwapTxn(ctx context.Context, old, next *DestroyTxn) error {
	if err := f.gate(MutCompareSwapTxn); err != nil {
		return err
	}
	return f.Store.CompareSwapTxn(ctx, old, next)
}

// PutConfirmation implements Store.
func (f *FaultStore) PutConfirmation(ctx context.Context, r *ConfirmationRecord) error {
	if err := f.gate(MutPutConfirmation); err != nil {
		return err
	}
	return f.Store.PutConfirmation(ctx, r)
}
