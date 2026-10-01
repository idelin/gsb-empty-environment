package timetravel

import (
	"reflect"
	"sync"
)

// Computed is a derived reactive value. Its compute function is
// re-evaluated by the controller after every committed mutation and
// after every time-travel jump, so it always reflects the current
// version of the signals it reads.
type Computed[T any] struct {
	mu        sync.RWMutex
	value     T
	compute   func() T
	listeners []func(T)
}

// NewComputed creates a Computed and registers it with the controller.
// The compute function should read its dependencies via Signal.Get (or
// other Computed.Get); it is evaluated eagerly and then kept in sync.
func NewComputed[T any](ctrl *Controller, compute func() T) *Computed[T] {
	c := &Computed[T]{compute: compute, value: compute()}
	ctrl.registerComputed(c)
	return c
}

// Get returns the current derived value.
func (c *Computed[T]) Get() T {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.value
}

// Subscribe registers a listener fired whenever the derived value
// actually changes. Returns an unsubscribe function.
func (c *Computed[T]) Subscribe(fn func(T)) (unsubscribe func()) {
	c.mu.Lock()
	c.listeners = append(c.listeners, fn)
	c.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			for i, l := range c.listeners {
				if funcEqual(l, fn) {
					c.listeners = append(c.listeners[:i], c.listeners[i+1:]...)
					return
				}
			}
		})
	}
}

// recompute is called by the controller after commits and restores.
// Subscribers are notified only when the value actually changed.
func (c *Computed[T]) recompute() {
	v := c.compute()
	c.mu.Lock()
	if reflect.DeepEqual(c.value, v) {
		c.mu.Unlock()
		return
	}
	c.value = v
	listeners := append([]func(T){}, c.listeners...)
	c.mu.Unlock()
	for _, l := range listeners {
		l(v)
	}
}
