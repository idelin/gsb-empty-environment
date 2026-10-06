package scrubber

import "sync"

// PausableBackend wraps a Backend and lets a test freeze a read after
// the underlying Get has returned but before the caller (typically an
// Inspector) continues to its conditional mark. That window is exactly
// where updates, deletes and repeated inspections can interleave.
type PausableBackend struct {
	inner Backend

	mu       sync.Mutex
	armed    bool
	readDone chan struct{}
	resume   chan struct{}
}

// NewPausableBackend wraps inner.
func NewPausableBackend(inner Backend) *PausableBackend {
	return &PausableBackend{inner: inner}
}

// ArmPause makes the next Get call pause after reading from the inner
// backend. It returns a channel that is closed once the read is done
// and the call is frozen.
func (p *PausableBackend) ArmPause() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.armed = true
	p.readDone = make(chan struct{})
	p.resume = make(chan struct{})
	return p.readDone
}

// Resume releases a Get call frozen by ArmPause.
func (p *PausableBackend) Resume() {
	p.mu.Lock()
	resume := p.resume
	p.resume = nil
	p.mu.Unlock()
	if resume != nil {
		close(resume)
	}
}

func (p *PausableBackend) Get(key string) (Object, error) {
	obj, err := p.inner.Get(key)

	p.mu.Lock()
	if !p.armed {
		p.mu.Unlock()
		return obj, err
	}
	p.armed = false
	readDone, resume := p.readDone, p.resume
	p.mu.Unlock()

	close(readDone)
	<-resume
	return obj, err
}

func (p *PausableBackend) Put(key string, data []byte) (Object, error) {
	return p.inner.Put(key, data)
}

func (p *PausableBackend) Delete(key string) error {
	return p.inner.Delete(key)
}

func (p *PausableBackend) MarkHealthyIfVersion(key string, version uint64) error {
	return p.inner.MarkHealthyIfVersion(key, version)
}

func (p *PausableBackend) QuarantineIfVersion(key string, version uint64) error {
	return p.inner.QuarantineIfVersion(key, version)
}

func (p *PausableBackend) Quarantined(key string) bool {
	return p.inner.Quarantined(key)
}
