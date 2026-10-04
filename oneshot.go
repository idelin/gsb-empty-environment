package waitfirst

import "sync"

// OneShot is a one-shot Event. The first call to Fire completes it:
// callbacks registered before Fire are invoked by Fire, callbacks
// registered after Fire are invoked synchronously by Subscribe.
type OneShot struct {
	mu      sync.Mutex
	fired   bool
	nextID  int
	cbs     map[int]func()
	pending int // registrations neither delivered nor revoked
}

// NewOneShot returns an un-fired OneShot.
func NewOneShot() *OneShot {
	return &OneShot{cbs: make(map[int]func())}
}

// Subscribe implements Event.
func (e *OneShot) Subscribe(fn func()) func() {
	e.mu.Lock()
	if e.fired {
		e.mu.Unlock()
		fn()
		return func() {}
	}
	id := e.nextID
	e.nextID++
	e.cbs[id] = fn
	e.pending++
	e.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			if _, ok := e.cbs[id]; ok {
				delete(e.cbs, id)
				e.pending--
			}
			e.mu.Unlock()
		})
	}
}

// Fire completes the event and invokes every registered callback exactly
// once. Later calls are no-ops.
func (e *OneShot) Fire() {
	e.mu.Lock()
	if e.fired {
		e.mu.Unlock()
		return
	}
	e.fired = true
	cbs := make([]func(), 0, len(e.cbs))
	for _, cb := range e.cbs {
		cbs = append(cbs, cb)
	}
	e.cbs = make(map[int]func())
	e.pending = 0
	e.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
}

// Fired reports whether the event has completed.
func (e *OneShot) Fired() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fired
}

// Pending returns the number of registrations that have been neither
// delivered by Fire nor revoked by their unsubscribe function. After a
// correctly implemented wait returns, Pending must be 0; anything else
// means a subscription was left behind.
func (e *OneShot) Pending() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pending
}
