package eventbus

import (
	"runtime"
	"sync"
)

// Subscription is the weak handle tying a subscriber (component) to the bus.
//
// The bus never stores this pointer, so the handle's lifetime is controlled
// solely by the subscriber. When the subscriber is destroyed and the handle
// becomes unreachable, the GC finalizer automatically unregisters the
// listener — the WeakRef analogue.
type Subscription struct {
	id    uint64
	topic string
	bus   *Bus
	once  sync.Once
}

// setFinalizer arms the automatic cleanup. It lives in a separate function
// so the finalizer closure captures only plain values (bus, id, topic) and
// never the *Subscription itself — capturing it would keep it alive forever.
func setFinalizer(sub *Subscription, b *Bus, id uint64, topic string) {
	runtime.SetFinalizer(sub, func(*Subscription) {
		b.markDead(id, topic)
	})
}

// Unsubscribe detaches the listener immediately and disarms the finalizer.
// It is idempotent.
func (s *Subscription) Unsubscribe() {
	s.once.Do(func() {
		runtime.SetFinalizer(s, nil)
		s.bus.unsubscribe(s.id, s.topic)
	})
}

// Topic returns the topic this subscription is registered on.
func (s *Subscription) Topic() string { return s.topic }
