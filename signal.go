package timetravel

import (
	"reflect"
	"sync"
)

// funcEqual reports whether two func values are the same function.
func funcEqual(a, b any) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// Signal is a reactive state cell owned by a Controller. Every Set is
// recorded by the controller as a new immutable snapshot version.
//
// Values are stored by copy: value types (numbers, strings, structs
// without reference fields) are snapshot-safe out of the box. If T
// contains references (slices, maps, pointers), treat them as immutable
// and replace rather than mutate in place, so history stays intact.
type Signal[T any] struct {
	ctrl      *Controller
	signalID  int
	mu        sync.RWMutex
	value     T
	listeners []func(T)
}

// NewSignal creates a Signal with the given initial value and registers
// it with the controller.
func NewSignal[T any](ctrl *Controller, initial T) *Signal[T] {
	ctrl.mu.Lock()
	id := ctrl.nextID
	ctrl.nextID++
	ctrl.mu.Unlock()

	s := &Signal[T]{ctrl: ctrl, signalID: id, value: initial}
	ctrl.register(s)
	return s
}

// Get returns the current value.
func (s *Signal[T]) Get() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// Set replaces the value and commits a new snapshot version.
func (s *Signal[T]) Set(v T) {
	s.ctrl.mutate(func() {
		s.mu.Lock()
		s.value = v
		listeners := append([]func(T){}, s.listeners...)
		s.mu.Unlock()
		for _, l := range listeners {
			l(v)
		}
	})
}

// Update replaces the value with fn(old) and commits a new version.
func (s *Signal[T]) Update(fn func(T) T) {
	s.Set(fn(s.Get()))
}

// Subscribe registers a listener fired with the new value after every
// change, including changes caused by time-travel restores. Returns an
// unsubscribe function.
func (s *Signal[T]) Subscribe(fn func(T)) (unsubscribe func()) {
	s.mu.Lock()
	s.listeners = append(s.listeners, fn)
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for i, l := range s.listeners {
				if funcEqual(l, fn) {
					s.listeners = append(s.listeners[:i], s.listeners[i+1:]...)
					return
				}
			}
		})
	}
}

func (s *Signal[T]) id() int { return s.signalID }

func (s *Signal[T]) snapshotValue() any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// restore is called by the controller during time travel. It writes the
// snapshot value back and notifies subscribers without committing a new
// version.
func (s *Signal[T]) restore(v any) {
	val := v.(T)
	s.mu.Lock()
	s.value = val
	listeners := append([]func(T){}, s.listeners...)
	s.mu.Unlock()
	for _, l := range listeners {
		l(val)
	}
}
