package taskpkg

import (
	"crypto/ed25519"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrPublisherClosed is returned after the publisher has been shut down.
var ErrPublisherClosed = errors.New("taskpkg: publisher closed")

type publishItem struct {
	key   CacheKey
	va    *VerifiedAnswer
	errCh chan error
}

// PausablePublisher serializes cache publication through one worker. While
// paused, accepted answers queue up without touching the cache; on resume
// every queued answer is re-evaluated against the trust generation that is
// current at publish time, so a revoked key can never sneak into the cache.
type PausablePublisher struct {
	store *TrustStore
	cache *Cache

	mu     sync.Mutex
	paused bool
	closed bool
	cond   *sync.Cond

	queue []*publishItem
	wake  chan struct{}
	done  chan struct{}

	inflight atomic.Int64
}

// NewPausablePublisher starts a publisher feeding cache from store.
func NewPausablePublisher(store *TrustStore, cache *Cache) *PausablePublisher {
	p := &PausablePublisher{
		store: store,
		cache: cache,
		wake:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	go p.run()
	return p
}

// Pause blocks publication; queued answers wait for Resume.
func (p *PausablePublisher) Pause() {
	p.mu.Lock()
	p.paused = true
	p.mu.Unlock()
}

// Resume unblocks the worker; waiting answers are re-checked on the spot.
func (p *PausablePublisher) Resume() {
	p.mu.Lock()
	p.paused = false
	p.cond.Broadcast()
	p.mu.Unlock()
}

// Paused reports the current gate state.
func (p *PausablePublisher) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

// QueueLen reports how many accepted answers await publication.
func (p *PausablePublisher) QueueLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queue)
}

// Idle reports whether no answer is queued or in flight.
func (p *PausablePublisher) Idle() bool {
	return p.inflight.Load() == 0
}

// WaitIdle polls until the queue drains or timeout elapses.
func (p *PausablePublisher) WaitIdle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.inflight.Load() == 0 {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return p.inflight.Load() == 0
}

// Submit enqueues a verified answer for asynchronous publication.
func (p *PausablePublisher) Submit(key CacheKey, va *VerifiedAnswer) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrPublisherClosed
	}
	p.inflight.Add(1)
	p.queue = append(p.queue, &publishItem{key: key, va: va})
	p.signalLocked()
	return nil
}

// PublishNow enqueues an answer and blocks until it has been published.
func (p *PausablePublisher) PublishNow(key CacheKey, va *VerifiedAnswer) error {
	errCh := make(chan error, 1)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPublisherClosed
	}
	p.inflight.Add(1)
	p.queue = append(p.queue, &publishItem{key: key, va: va, errCh: errCh})
	p.signalLocked()
	p.mu.Unlock()
	return <-errCh
}

// Close resumes the worker, drains the queue, and stops the goroutine.
func (p *PausablePublisher) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.paused = false
	p.cond.Broadcast()
	p.signalLocked()
	p.mu.Unlock()
	<-p.done
}

func (p *PausablePublisher) run() {
	defer close(p.done)
	for {
		item := p.next()
		if item == nil {
			return
		}
		p.gate(item)
	}
}

func (p *PausablePublisher) next() *publishItem {
	for {
		p.mu.Lock()
		if len(p.queue) > 0 {
			item := p.queue[0]
			p.queue = p.queue[1:]
			p.mu.Unlock()
			return item
		}
		if p.closed {
			p.mu.Unlock()
			return nil
		}
		wake := p.wake
		p.mu.Unlock()
		<-wake
	}
}

func (p *PausablePublisher) gate(item *publishItem) {
	p.mu.Lock()
	for p.paused && !p.closed {
		p.cond.Wait()
	}
	p.mu.Unlock()

	err := p.publish(item)
	if item.errCh != nil {
		item.errCh <- err
	}
	p.inflight.Add(-1)
}

// publish applies the revocation check against the generation that is
// current at publication time, not the generation the query was pinned to.
func (p *PausablePublisher) publish(item *publishItem) error {
	va := item.va
	ans := va.Answer

	path, err := BuildTrustPath(p.store, va.Pin, ans.Signer)
	if err != nil {
		return err
	}

	entry := &Entry{
		Data: append([]byte(nil), ans.RRData...),
		Path: path,
	}

	if path.Active {
		cur, err := p.store.Current(va.Pin.Zone)
		if err != nil {
			return err
		}
		pub, ok := cur.PublicKey(ans.Signer)
		if !ok {
			path.Active = false
			entry.State = Diagnostic
			entry.Reason = "signer fingerprint absent from current declaration"
			p.cache.Publish(item.key, entry)
			return nil
		}
		if !ed25519.Verify(pub, signedPayload(ans), ans.Signature) {
			return ErrBadSignature
		}
		entry.State = Authoritative
	} else {
		entry.State = Diagnostic
		entry.Reason = "signer fingerprint revoked: not retained by current trust generation"
	}

	p.cache.Publish(item.key, entry)
	return nil
}

func (p *PausablePublisher) signalLocked() {
	close(p.wake)
	p.wake = make(chan struct{})
}
