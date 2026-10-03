// Package timetravel implements a reactive state controller with immutable
// snapshot trees and time-travel (Undo/Redo) across all registered Signals.
//
// Every mutation committed through the Controller records an immutable
// snapshot of ALL Signals, forming a snapshot tree. Undo/Redo (or GoTo)
// restores any historical snapshot and notifies subscribers so that UI or
// downstream consumers re-render in sync. Computed values derive lazily from
// Signals and are automatically invalidated on every state transition.
package timetravel

import (
	"fmt"
	"sync"
)

// Snapshot is an immutable node in the history tree. It captures the values
// of every Signal registered with the Controller at commit time.
type Snapshot struct {
	ID       int
	Parent   *Snapshot
	Children []*Snapshot
	values   map[int]any // never mutated after commit
}

// Value returns the recorded value of a Signal at this snapshot.
func (s *Snapshot) Value(sigID int) (any, bool) {
	v, ok := s.values[sigID]
	return v, ok
}

// signaler is the internal view of a Signal used by the Controller.
type signaler interface {
	id() int
	snapshotValue() any
	restore(any)
}

// Controller owns all Signals and manages the snapshot history tree.
type Controller struct {
	mu         sync.RWMutex
	signals    map[int]signaler
	nextSigID  int
	nextSnapID int

	root    *Snapshot
	current *Snapshot

	version uint64 // bumped on every commit/restore; invalidates Computed caches

	listeners  []func()
	batchDepth int
	pending    bool
}

// NewController creates an empty controller with a root (initial) snapshot.
func NewController() *Controller {
	c := &Controller{signals: make(map[int]signaler)}
	c.root = &Snapshot{ID: 0, values: map[int]any{}}
	c.current = c.root
	return c
}

// Current returns the snapshot the controller is currently positioned at.
func (c *Controller) Current() *Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// Root returns the initial snapshot (before any mutation).
func (c *Controller) Root() *Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.root
}

// Subscribe registers fn to be called after every state transition
// (commit, undo, redo, goto). This is the hook UI/downstream consumers use
// to re-render in sync with the restored state.
func (c *Controller) Subscribe(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = append(c.listeners, fn)
}

func (c *Controller) register(s signaler) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	sid := c.nextSigID
	c.nextSigID++
	c.signals[sid] = s
	// Backfill the not-yet-committed baseline so Undo-to-root restores this
	// signal's initial value. Committed snapshots are never mutated.
	if c.current == c.root && c.nextSnapID == 0 {
		c.root.values[sid] = s.snapshotValue()
	}
	return sid
}

// commitLocked appends a new immutable snapshot as a child of the current one.
func (c *Controller) commitLocked() {
	vals := make(map[int]any, len(c.signals))
	for id, s := range c.signals {
		vals[id] = s.snapshotValue()
	}
	node := &Snapshot{
		ID:     c.nextSnapID + 1,
		Parent: c.current,
		values: vals,
	}
	c.nextSnapID++
	c.current.Children = append(c.current.Children, node)
	c.current = node
	c.version++
}

// notify invokes subscribers outside the mutex.
func (c *Controller) notify() {
	c.mu.RLock()
	listeners := append([]func(){}, c.listeners...)
	c.mu.RUnlock()
	for _, fn := range listeners {
		fn()
	}
}

// afterMutation commits a snapshot unless inside a Batch, then notifies.
func (c *Controller) afterMutation() {
	c.mu.Lock()
	if c.batchDepth > 0 {
		c.pending = true
		c.mu.Unlock()
		return
	}
	c.commitLocked()
	c.mu.Unlock()
	c.notify()
}

// Batch groups multiple Signal.Set calls into a single snapshot commit.
func (c *Controller) Batch(fn func()) {
	c.mu.Lock()
	c.batchDepth++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.batchDepth--
		flush := c.batchDepth == 0 && c.pending
		if flush {
			c.pending = false
			c.commitLocked()
		}
		c.mu.Unlock()
		if flush {
			c.notify()
		}
	}()
	fn()
}

// Undo steps back n snapshots. Returns false if there is not enough history.
func (c *Controller) Undo(n int) bool {
	c.mu.Lock()
	target := c.current
	for i := 0; i < n; i++ {
		if target.Parent == nil {
			c.mu.Unlock()
			return false
		}
		target = target.Parent
	}
	c.applyLocked(target)
	c.mu.Unlock()
	c.notify()
	return true
}

// Redo steps forward n snapshots, following the most recently created child
// branch at each fork. Returns false if there is not enough future history.
func (c *Controller) Redo(n int) bool {
	c.mu.Lock()
	target := c.current
	for i := 0; i < n; i++ {
		if len(target.Children) == 0 {
			c.mu.Unlock()
			return false
		}
		target = target.Children[len(target.Children)-1]
	}
	c.applyLocked(target)
	c.mu.Unlock()
	c.notify()
	return true
}

// GoTo jumps to an arbitrary snapshot by ID, restoring all Signals.
func (c *Controller) GoTo(snapshotID int) bool {
	c.mu.Lock()
	node := c.findLocked(c.root, snapshotID)
	if node == nil {
		c.mu.Unlock()
		return false
	}
	c.applyLocked(node)
	c.mu.Unlock()
	c.notify()
	return true
}

func (c *Controller) findLocked(node *Snapshot, id int) *Snapshot {
	if node.ID == id {
		return node
	}
	for _, ch := range node.Children {
		if found := c.findLocked(ch, id); found != nil {
			return found
		}
	}
	return nil
}

// applyLocked restores every Signal from the target snapshot and bumps the
// version so all Computed caches are invalidated.
func (c *Controller) applyLocked(target *Snapshot) {
	for id, s := range c.signals {
		if v, ok := target.values[id]; ok {
			s.restore(v)
		}
	}
	c.current = target
	c.version++
}

// Version is the monotonically increasing state version.
func (c *Controller) Version() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// Signal is a reactive state cell registered with a Controller.
type Signal[T any] struct {
	ctl *Controller
	sid int
	mu  sync.RWMutex
	val T
}

// NewSignal creates a Signal with an initial value. The initial value is
// captured into the controller's baseline snapshot.
func NewSignal[T any](ctl *Controller, initial T) *Signal[T] {
	s := &Signal[T]{ctl: ctl, val: initial}
	s.sid = ctl.register(s)
	return s
}

func (s *Signal[T]) id() int { return s.sid }

func (s *Signal[T]) snapshotValue() any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.val
}

func (s *Signal[T]) restore(v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.val = v.(T)
}

// Get reads the current value.
func (s *Signal[T]) Get() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.val
}

// Set writes a new value and commits a new immutable snapshot.
func (s *Signal[T]) Set(v T) {
	s.mu.Lock()
	s.val = v
	s.mu.Unlock()
	s.ctl.afterMutation()
}

// String implements fmt.Stringer for debugging.
func (s *Signal[T]) String() string {
	return fmt.Sprintf("Signal(%v)", s.Get())
}

// Computed is a derived reactive value. It re-evaluates lazily whenever the
// controller's state version has advanced since the last read, so restores
// via Undo/Redo automatically propagate to Computed values.
type Computed[T any] struct {
	ctl          *Controller
	fn           func() T
	mu           sync.Mutex
	cached       T
	cacheVersion uint64
	valid        bool
}

// NewComputed creates a derived value from fn. fn should read other Signals.
func NewComputed[T any](ctl *Controller, fn func() T) *Computed[T] {
	return &Computed[T]{ctl: ctl, fn: fn}
}

// Get returns the derived value, recomputing if the state changed.
func (c *Computed[T]) Get() T {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.ctl.Version()
	if c.valid && c.cacheVersion == v {
		return c.cached
	}
	c.cached = c.fn()
	c.cacheVersion = v
	c.valid = true
	return c.cached
}
