// Package eventbus implements a weak-reference reactive publish/subscribe
// hub with automatic deregistration and memory-leak detection.
//
// Subscriptions are tied to the lifetime of an owner object (typically a
// UI component or controller). When the owner becomes unreachable and is
// reclaimed by the garbage collector, a runtime cleanup (the Go analogue
// of FinalizationRegistry) automatically removes the subscription from
// the bus, so no stale (zombie) listeners accumulate and no cross-page
// memory leaks occur.
package eventbus

import (
	"runtime"
	"sync"
	"time"
)

// Event is a single message delivered to subscribers.
type Event struct {
	Topic   string
	Payload interface{}
}

// Handler processes a published event.
type Handler func(Event)

// subscription is the bus-internal record for one registered handler.
type subscription struct {
	id        uint64
	topic     string
	handler   Handler
	createdAt time.Time
}

// Bus is a reactive publish/subscribe hub. It is safe for concurrent use.
type Bus struct {
	mu     sync.Mutex
	topics map[string]map[uint64]*subscription
	seq    uint64
}

// New returns an empty event bus.
func New() *Bus {
	return &Bus{topics: make(map[string]map[uint64]*subscription)}
}

// cleanupArg is the state handed to the GC cleanup callback. It must
// never reference the owner object, otherwise the owner would be kept
// alive forever and the cleanup would never run.
type cleanupArg struct {
	bus   *Bus
	topic string
	id    uint64
}

// Subscribe registers h for topic and binds the subscription's lifetime
// to owner. Once owner is garbage collected, the subscription is removed
// automatically; there is no need to call Unsubscribe (though it is still
// supported for eager teardown).
//
// owner must be a pointer to the component that logically owns the
// subscription. The handler must not strongly capture owner, for the same
// reason a JS weak-ref callback must not close over its target: doing so
// would keep the owner alive and defeat automatic deregistration.
//
// Caveat: owner should be a real heap object. Go's tiny allocator packs
// objects smaller than 16 bytes into shared blocks, and a cleanup can
// only be queued once the whole block is dead — tiny owners may therefore
// be deregistered late or never.
func Subscribe[T any](bus *Bus, topic string, owner *T, h Handler) *Subscription {
	id := bus.add(topic, h)
	cleanup := runtime.AddCleanup(owner, func(arg cleanupArg) {
		arg.bus.remove(arg.topic, arg.id)
	}, cleanupArg{bus: bus, topic: topic, id: id})
	return &Subscription{bus: bus, topic: topic, id: id, cleanup: cleanup}
}

// SubscribeStrong registers h for topic without any owner. The
// subscription stays active until Unsubscribe is called explicitly.
func (b *Bus) SubscribeStrong(topic string, h Handler) *Subscription {
	id := b.add(topic, h)
	return &Subscription{bus: b, topic: topic, id: id}
}

// Subscription is a handle to one registered handler.
type Subscription struct {
	bus     *Bus
	topic   string
	id      uint64
	once    sync.Once
	cleanup runtime.Cleanup
}

// Unsubscribe eagerly removes the subscription and disables the pending
// GC cleanup. It is idempotent.
func (s *Subscription) Unsubscribe() {
	s.once.Do(func() {
		if s.cleanup != (runtime.Cleanup{}) {
			s.cleanup.Stop()
		}
		s.bus.remove(s.topic, s.id)
	})
}

func (b *Bus) add(topic string, h Handler) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	id := b.seq
	set, ok := b.topics[topic]
	if !ok {
		set = make(map[uint64]*subscription)
		b.topics[topic] = set
	}
	set[id] = &subscription{id: id, topic: topic, handler: h, createdAt: time.Now()}
	return id
}

func (b *Bus) remove(topic string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	set, ok := b.topics[topic]
	if !ok {
		return
	}
	delete(set, id)
	if len(set) == 0 {
		delete(b.topics, topic)
	}
}

// Publish delivers an event to a snapshot of the current subscribers of
// topic and returns how many handlers received it.
func (b *Bus) Publish(topic string, payload interface{}) int {
	b.mu.Lock()
	handlers := make([]Handler, 0, len(b.topics[topic]))
	for _, s := range b.topics[topic] {
		handlers = append(handlers, s.handler)
	}
	b.mu.Unlock()
	ev := Event{Topic: topic, Payload: payload}
	for _, h := range handlers {
		h(ev)
	}
	return len(handlers)
}

// SubscriberCount reports the number of active subscribers for topic.
func (b *Bus) SubscriberCount(topic string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.topics[topic])
}

// TotalSubscribers reports the number of active subscribers across all topics.
func (b *Bus) TotalSubscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := 0
	for _, set := range b.topics {
		total += len(set)
	}
	return total
}

// Counts snapshots the active subscriber count per topic.
func (b *Bus) Counts() map[string]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]int, len(b.topics))
	for topic, set := range b.topics {
		out[topic] = len(set)
	}
	return out
}

// SubscriberInfo describes one active subscription for diagnostics.
type SubscriberInfo struct {
	ID    uint64
	Topic string
	Age   time.Duration
}

// Subscribers lists the active subscriptions of topic, oldest first.
func (b *Bus) Subscribers(topic string) []SubscriberInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	infos := make([]SubscriberInfo, 0, len(b.topics[topic]))
	for _, s := range b.topics[topic] {
		infos = append(infos, SubscriberInfo{ID: s.id, Topic: topic, Age: now.Sub(s.createdAt)})
	}
	return infos
}
