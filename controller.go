// Package timetravel provides a cross-component reactive state controller
// with immutable versioned snapshots and time-travel (undo/redo).
//
// A Controller owns a set of Signal values. Every mutation commits an
// immutable snapshot of the whole signal tree, so the controller can
// jump back and forth to any historical version via Undo, Redo and GoTo.
// Computed values derive from signals and are recomputed automatically
// after every commit and every time-travel jump, keeping downstream
// consumers (UI, renderers, etc.) in sync.
package timetravel

import "sync"

// signaler is the internal view the Controller has of a Signal.
type signaler interface {
	id() int
	snapshotValue() any
	restore(v any)
}

// computer is the internal view the Controller has of a Computed.
type computer interface {
	recompute()
}

// Controller is the reactive state container and time-travel engine.
type Controller struct {
	mu         sync.Mutex
	signals    []signaler
	computeds  []computer
	history    []map[int]any // history[i] is the immutable snapshot of version i
	cursor     int           // current version
	batchDepth int
	dirty      bool
	listeners  []func(version int)
	nextID     int
}

// NewController creates an empty Controller.
func NewController() *Controller {
	return &Controller{}
}

func (c *Controller) register(s signaler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.signals = append(c.signals, s)
	// Backfill existing snapshots with the signal's initial value so that
	// travelling to a version that predates the signal still restores a
	// complete, consistent tree.
	for _, snap := range c.history {
		snap[s.id()] = s.snapshotValue()
	}
}

func (c *Controller) registerComputed(cm computer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.computeds = append(c.computeds, cm)
}

// mutate runs fn as one versioned change and commits a new snapshot.
// Inside a Batch the commit is deferred until the outermost batch ends.
func (c *Controller) mutate(fn func()) {
	c.mu.Lock()
	if c.history == nil {
		// Baseline version 0: the tree before the first mutation.
		c.history = append(c.history, c.captureLocked())
	}
	c.mu.Unlock()

	fn() // applies the change and fires signal-level subscribers

	c.mu.Lock()
	if c.batchDepth > 0 {
		c.dirty = true
		c.mu.Unlock()
		return
	}
	c.recomputeLocked()
	version := c.commitLocked()
	listeners := append([]func(int){}, c.listeners...)
	c.mu.Unlock()

	for _, l := range listeners {
		l(version)
	}
}

// Batch groups multiple mutations into a single committed version.
func (c *Controller) Batch(fn func()) {
	c.mu.Lock()
	c.batchDepth++
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.batchDepth--
		if c.batchDepth == 0 && c.dirty {
			c.dirty = false
			c.recomputeLocked()
			version := c.commitLocked()
			listeners := append([]func(int){}, c.listeners...)
			c.mu.Unlock()
			for _, l := range listeners {
				l(version)
			}
			return
		}
		c.mu.Unlock()
	}()

	fn()
}

// captureLocked copies the current value of every signal into a new
// immutable snapshot map.
func (c *Controller) captureLocked() map[int]any {
	snap := make(map[int]any, len(c.signals))
	for _, s := range c.signals {
		snap[s.id()] = s.snapshotValue()
	}
	return snap
}

// commitLocked truncates any redo branch and appends the current tree
// as the newest version. It returns the new version number.
func (c *Controller) commitLocked() int {
	c.history = append(c.history[:c.cursor+1], c.captureLocked())
	c.cursor++
	return c.cursor
}

func (c *Controller) recomputeLocked() {
	for _, cm := range c.computeds {
		cm.recompute()
	}
}

// Undo steps one version back. It reports whether the jump happened.
func (c *Controller) Undo() bool {
	c.mu.Lock()
	target := c.cursor - 1
	c.mu.Unlock()
	return c.GoTo(target)
}

// Redo steps one version forward. It reports whether the jump happened.
func (c *Controller) Redo() bool {
	c.mu.Lock()
	target := c.cursor + 1
	c.mu.Unlock()
	return c.GoTo(target)
}

// GoTo jumps to an arbitrary historical version, restoring every signal
// to its snapshot value, recomputing all Computed values and notifying
// subscribers so downstream UI re-renders stay in sync.
func (c *Controller) GoTo(version int) bool {
	c.mu.Lock()
	if version < 0 || version >= len(c.history) || version == c.cursor {
		c.mu.Unlock()
		return false
	}
	c.cursor = version
	snap := c.history[c.cursor]
	signals := append([]signaler{}, c.signals...)
	c.mu.Unlock()

	for _, s := range signals {
		s.restore(snap[s.id()])
	}

	c.mu.Lock()
	c.recomputeLocked()
	listeners := append([]func(int){}, c.listeners...)
	c.mu.Unlock()

	for _, l := range listeners {
		l(version)
	}
	return true
}

// Version returns the current version number (0 is the baseline).
func (c *Controller) Version() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cursor
}

// Len returns the number of recorded versions, including the baseline.
func (c *Controller) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.history)
}

// CanUndo reports whether there is an earlier version to jump to.
func (c *Controller) CanUndo() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cursor > 0
}

// CanRedo reports whether there is a later version to jump to.
func (c *Controller) CanRedo() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cursor < len(c.history)-1
}

// OnChange registers a listener fired after every committed version,
// including versions reached via Undo/Redo/GoTo. It is the hook a UI
// layer uses to trigger re-rendering. Returns an unsubscribe function.
func (c *Controller) OnChange(fn func(version int)) (unsubscribe func()) {
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
