package cache

import "sync"

// Message is an invalidation/update notification. (Src, Seq) is a globally
// unique publisher-scoped sequence used for idempotent (duplicate-safe)
// delivery; Version is the data version the message announces for Key.
type Message struct {
	Src     string
	Key     string
	Version uint64
	Seq     uint64
}

// Subscription is a receive handle. Calling Unsubscribe releases it.
type Subscription struct {
	C     <-chan Message
	unsub func()
}

// Unsubscribe removes the subscription and closes its channel. It is safe
// to call more than once.
func (s Subscription) Unsubscribe() { s.unsub() }

// Notifier is an in-memory publish/subscribe bus for invalidation
// messages. Publish delay and failure are injectable to simulate slow or
// lossy notification channels in tests.
type Notifier struct {
	mu     sync.Mutex
	subs   map[int]chan Message
	nextID int
	// Delay, when non-nil, runs at the start of every Publish to simulate
	// notification latency.
	Delay func()
	// PublishErr, when non-nil, is consulted on every Publish; returning a
	// non-nil error simulates a lost notification.
	PublishErr func() error
}

// NewNotifier returns an empty Notifier.
func NewNotifier() *Notifier { return &Notifier{subs: map[int]chan Message{}} }

// Subscribe registers a buffered subscriber.
func (n *Notifier) Subscribe(buf int) Subscription {
	n.mu.Lock()
	id := n.nextID
	n.nextID++
	ch := make(chan Message, buf)
	n.subs[id] = ch
	n.mu.Unlock()
	var once sync.Once
	return Subscription{
		C: ch,
		unsub: func() {
			once.Do(func() {
				n.mu.Lock()
				defer n.mu.Unlock()
				if _, ok := n.subs[id]; ok {
					delete(n.subs, id)
					close(ch)
				}
			})
		},
	}
}

// Publish delivers m to all current subscribers (best effort: a full
// subscriber buffer is skipped, mirroring at-least-once semantics where
// redelivery is the producer's job).
func (n *Notifier) Publish(m Message) error {
	if n.Delay != nil {
		n.Delay()
	}
	if n.PublishErr != nil {
		if err := n.PublishErr(); err != nil {
			return err
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, ch := range n.subs {
		select {
		case ch <- m:
		default:
		}
	}
	return nil
}

// SubscriberCount reports the number of active subscriptions; it is used to
// verify that resources are released on Close.
func (n *Notifier) SubscriberCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.subs)
}
