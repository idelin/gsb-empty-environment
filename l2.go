package llcache

import (
	"sync"
	"time"
)

// L2 is the shared second-level cache. It owns the authoritative
// per-key versions and the global version watermark, and broadcasts
// an invalidation after every committed update. Delivery is
// asynchronous and may be delayed (WithNotifyDelay) to model a real
// pub/sub link.
type L2 struct {
	mu          sync.Mutex
	entries     map[string]Value
	watermark   uint64 // highest version ever committed; monotonic
	subs        []chan Invalidation
	notifyDelay time.Duration
	clock       Clock
	fail        FailHook

	closed    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup // in-flight delayed deliveries
}

// L2Option configures an L2.
type L2Option func(*L2)

// WithL2Clock injects the clock used to timestamp invalidations.
func WithL2Clock(c Clock) L2Option { return func(l *L2) { l.clock = c } }

// WithL2FailHook injects failures into L2 operations.
func WithL2FailHook(f FailHook) L2Option { return func(l *L2) { l.fail = f } }

// WithNotifyDelay sets the asynchronous invalidation delivery delay.
func WithNotifyDelay(d time.Duration) L2Option {
	return func(l *L2) { l.notifyDelay = d }
}

// NewL2 creates an empty L2.
func NewL2(opts ...L2Option) *L2 {
	l := &L2{
		entries: make(map[string]Value),
		clock:   time.Now,
		closed:  make(chan struct{}),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Update commits a new version for key. A version less than or equal
// to the committed one is rejected with ErrVersionRollback. On
// success an invalidation is broadcast to all subscribers, delivered
// after the configured notification delay.
func (l *L2) Update(key string, v Value) error {
	if err := l.checkFail("l2.update"); err != nil {
		return err
	}
	l.mu.Lock()
	if cur, ok := l.entries[key]; ok && v.Version <= cur.Version {
		l.mu.Unlock()
		return ErrVersionRollback
	}
	l.entries[key] = v
	if v.Version > l.watermark {
		l.watermark = v.Version
	}
	subs := append([]chan Invalidation(nil), l.subs...)
	l.mu.Unlock()

	msg := Invalidation{Key: key, Version: v.Version, At: l.clock()}
	l.wg.Add(1)
	go l.deliver(subs, msg)
	return nil
}

// Get returns the committed value for key.
func (l *L2) Get(key string) (Value, bool, error) {
	if err := l.checkFail("l2.get"); err != nil {
		return Value{}, false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.entries[key]
	return v, ok, nil
}

// Watermark returns the highest version ever committed. Nodes use it
// to detect missed notifications: a local state that cannot account
// for the watermark must be revalidated.
func (l *L2) Watermark() (uint64, error) {
	if err := l.checkFail("l2.watermark"); err != nil {
		return 0, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.watermark, nil
}

// Subscribe registers a channel receiving invalidation broadcasts.
func (l *L2) Subscribe(buf int) <-chan Invalidation {
	ch := make(chan Invalidation, buf)
	l.mu.Lock()
	l.subs = append(l.subs, ch)
	l.mu.Unlock()
	return ch
}

// Close stops accepting work and blocks until every in-flight
// (possibly delayed) delivery has finished, releasing all goroutines.
func (l *L2) Close() {
	l.closeOnce.Do(func() { close(l.closed) })
	l.wg.Wait()
}

func (l *L2) checkFail(op string) error {
	if l.fail == nil {
		return nil
	}
	return l.fail(op)
}

func (l *L2) deliver(subs []chan Invalidation, msg Invalidation) {
	defer l.wg.Done()
	if l.notifyDelay > 0 {
		t := time.NewTimer(l.notifyDelay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-l.closed:
			return
		}
	}
	for _, ch := range subs {
		select {
		case ch <- msg:
		case <-l.closed:
			return
		}
	}
}
