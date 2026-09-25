package chunkstore

import (
	"errors"
	"sync"
)

// ErrInjected is returned by Store operations when an armed fault point fires.
var ErrInjected = errors.New("chunkstore: injected fault")

// FaultInjector simulates IO failures at named points inside the Store.
// It is used by tests and chaos tooling; production code leaves it nil.
//
// Known fault points:
//
//	"block.write"  after each block file is durably persisted during Put
//	"meta.commit"  after the metadata transaction image is written+fsynced,
//	               before the atomic rename that commits it
//	"reclaim"      before each zero-reference block file is unlinked
type FaultInjector struct {
	mu   sync.Mutex
	arms map[string]int // point -> successful passes remaining before it fires once
}

func NewFaultInjector() *FaultInjector {
	return &FaultInjector{arms: make(map[string]int)}
}

// ArmAfter arms point so that the next `successes` passes succeed and the
// following one fails once with ErrInjected, then the point disarms.
func (f *FaultInjector) ArmAfter(point string, successes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.arms[point] = successes
}

func (f *FaultInjector) maybeFail(point string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.arms[point]
	if !ok {
		return nil
	}
	if n > 0 {
		f.arms[point] = n - 1
		return nil
	}
	delete(f.arms, point)
	return ErrInjected
}
