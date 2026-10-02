// Package eventbus implements a weak-reference reactive publish/subscribe
// hub with a built-in memory-leak detector.
//
// The bus mirrors the semantics of JavaScript's WeakRef +
// FinalizationRegistry: subscribers hold a *Subscription handle, and the bus
// itself never keeps a strong reference to it. Once the subscriber is
// destroyed and no external strong reference remains, the garbage collector
// reclaims the handle, a finalizer marks the listener as dead, and the bus
// automatically removes it. A watchdog sweeps stale ("zombie") listeners
// and emits warnings so cross-page / cross-component leaks are surfaced
// instead of silently accumulating.
package eventbus

import (
	"log"
	"os"
	"sync"
	"time"
)

// Handler processes a published payload.
type Handler func(payload interface{})

// Bus is a reactive publish/subscribe center whose subscriptions are held
// weakly. It is safe for concurrent use.
type Bus struct {
	mu        sync.Mutex
	topics    map[string]map[uint64]Handler
	pending   map[uint64]deadEntry // finalized subscribers awaiting sweep
	nextID    uint64
	cleaned   uint64
	zombieAge time.Duration
	logger    *log.Logger
}

// deadEntry records a subscriber whose handle was reclaimed by the GC but
// whose listener has not been swept out of the bus yet.
type deadEntry struct {
	topic string
	since time.Time
}

// Option customizes a Bus.
type Option func(*Bus)

// WithZombieTimeout sets how long a finalized subscription may linger in
// the pending queue before it is reported as a zombie. Default: 1s.
func WithZombieTimeout(d time.Duration) Option {
	return func(b *Bus) { b.zombieAge = d }
}

// WithLogger sets the logger used for zombie warnings.
// Default: a logger writing to stderr.
func WithLogger(l *log.Logger) Option {
	return func(b *Bus) { b.logger = l }
}

// New creates an empty Bus.
func New(opts ...Option) *Bus {
	b := &Bus{
		topics:    make(map[string]map[uint64]Handler),
		pending:   make(map[uint64]deadEntry),
		zombieAge: time.Second,
		logger:    log.New(os.Stderr, "[eventbus] ", log.LstdFlags),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Subscribe registers h for topic and returns a *Subscription handle.
//
// The bus keeps no strong reference to the returned handle: when the caller
// drops it and the GC reclaims it, the listener is automatically
// unregistered. Call Unsubscribe for deterministic, immediate removal.
func (b *Bus) Subscribe(topic string, h Handler) *Subscription {
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	if b.topics[topic] == nil {
		b.topics[topic] = make(map[uint64]Handler)
	}
	b.topics[topic][id] = h
	b.mu.Unlock()

	sub := &Subscription{id: id, topic: topic, bus: b}
	// FinalizationRegistry analogue: fires once the handle is unreachable.
	setFinalizer(sub, b, id, topic)
	return sub
}

// Publish delivers payload to every live listener of topic and returns the
// number of listeners invoked. Handlers run outside the bus lock, in
// subscription-map order (unspecified).
func (b *Bus) Publish(topic string, payload interface{}) int {
	b.mu.Lock()
	var handlers []Handler
	for _, h := range b.topics[topic] {
		handlers = append(handlers, h)
	}
	b.mu.Unlock()

	for _, h := range handlers {
		h(payload)
	}
	return len(handlers)
}

// markDead is invoked by the subscription finalizer. It enqueues the dead
// listener for sweeping instead of mutating the topic maps directly, mirroring
// how a FinalizationRegistry cleanup callback is queued asynchronously.
func (b *Bus) markDead(id uint64, topic string) {
	b.mu.Lock()
	b.pending[id] = deadEntry{topic: topic, since: time.Now()}
	b.mu.Unlock()
}

// unsubscribe removes a listener immediately (explicit Unsubscribe path).
func (b *Bus) unsubscribe(id uint64, topic string) {
	b.mu.Lock()
	b.removeLocked(id, topic)
	delete(b.pending, id)
	b.mu.Unlock()
}

func (b *Bus) removeLocked(id uint64, topic string) {
	if handlers, ok := b.topics[topic]; ok {
		delete(handlers, id)
		if len(handlers) == 0 {
			delete(b.topics, topic)
		}
	}
}

// Stats is a snapshot of the bus's bookkeeping counters.
type Stats struct {
	Active  int    // live listeners still registered on topics
	Pending int    // finalized listeners awaiting sweep
	Cleaned uint64 // listeners swept since bus creation
}

// Stats returns a consistent snapshot of the bus counters.
func (b *Bus) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	active := 0
	for _, handlers := range b.topics {
		active += len(handlers)
	}
	return Stats{Active: active, Pending: len(b.pending), Cleaned: b.cleaned}
}

// ActiveCount reports how many listeners are currently registered.
func (b *Bus) ActiveCount() int { return b.Stats().Active }
