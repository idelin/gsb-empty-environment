package inspect

import (
	"context"
	"sync"
)

// PausableBackend wraps a Backend and lets a test pause an in-flight Get
// right after the object has been read but before the caller continues
// (e.g. before validation and the atomic status update). This makes
// read/modify interleavings deterministic in tests.
type PausableBackend struct {
	Backend

	mu   sync.Mutex
	gate *readGate
}

type readGate struct {
	entered chan struct{}
	proceed chan struct{}
}

// NewPausableBackend wraps backend.
func NewPausableBackend(backend Backend) *PausableBackend {
	return &PausableBackend{Backend: backend}
}

// ArmRead pauses the next Get after it has read the object. It returns a
// channel that is closed once the read is parked, and a release function
// that lets it continue. ArmRead must not be called again before the
// previous gate was released.
func (p *PausableBackend) ArmRead() (parked <-chan struct{}, release func()) {
	g := &readGate{entered: make(chan struct{}), proceed: make(chan struct{})}
	p.mu.Lock()
	p.gate = g
	p.mu.Unlock()
	var once sync.Once
	return g.entered, func() { once.Do(func() { close(g.proceed) }) }
}

// Get implements Backend, honouring an armed read gate.
func (p *PausableBackend) Get(ctx context.Context, key string) (Object, error) {
	obj, err := p.Backend.Get(ctx, key)
	if err != nil {
		return Object{}, err
	}
	p.mu.Lock()
	g := p.gate
	p.gate = nil
	p.mu.Unlock()
	if g != nil {
		close(g.entered)
		select {
		case <-g.proceed:
		case <-ctx.Done():
			return Object{}, ctx.Err()
		}
	}
	return obj, nil
}
