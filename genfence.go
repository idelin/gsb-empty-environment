// Package genfence implements a generation-fenced task executor.
//
// A Manager runs caller-injected tasks against the current generation.
// Switch seals the current generation and opens a new one. Results of
// tasks that belong to a sealed (old) generation are fenced out: they
// are rejected instead of being written into the new generation's
// cache. Publish and Switch are linearized by a single mutex, so a
// result racing a switch is either published into its own generation
// first or rejected — never lost silently, never written into the
// wrong generation. Old tasks always run their Cleanup hook, and new
// tasks never wait for old cleanup to finish.
package genfence

import "sync"

// Outcome describes how a task finished. It is passed to the task's
// Cleanup hook exactly once.
type Outcome struct {
	Key       string
	Gen       uint64 // generation the task joined
	Value     any    // computed value, if Compute succeeded
	Err       error  // non-nil if Compute failed
	Published bool   // Value was installed in generation Gen's cache
	Rejected  bool   // Value was fenced out by a generation switch
}

// Task is a unit of work. Compute and Cleanup are injected by the
// caller (tests inject functions, timing and failures here).
type Task struct {
	Key string
	// Compute produces the value to publish. A non-nil error skips
	// publishing entirely.
	Compute func() (any, error)
	// Cleanup always runs after Compute (and after the publish
	// attempt, if any), in the task's own goroutine. It may be nil.
	Cleanup func(Outcome)
}

// generation holds the per-generation cache and tracks its in-flight
// tasks so callers can wait for their cleanup to complete.
type generation struct {
	cache map[string]any
	wg    sync.WaitGroup
}

// Manager executes tasks against a current generation.
type Manager struct {
	mu   sync.Mutex
	gen  uint64
	cur  *generation
	past map[uint64]*generation // sealed generations
}

// NewManager returns a Manager started at generation 0.
func NewManager() *Manager {
	return &Manager{
		cur:  &generation{cache: make(map[string]any)},
		past: make(map[uint64]*generation),
	}
}

// Generation returns the current generation id.
func (m *Manager) Generation() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gen
}

// Start launches t in the current generation and returns the
// generation the task joined. The task runs in its own goroutine;
// Start never blocks on other tasks' compute or cleanup.
func (m *Manager) Start(t Task) uint64 {
	m.mu.Lock()
	gen := m.gen
	g := m.cur
	g.wg.Add(1)
	m.mu.Unlock()

	go m.run(g, gen, t)
	return gen
}

func (m *Manager) run(g *generation, gen uint64, t Task) {
	defer g.wg.Done()
	out := Outcome{Key: t.Key, Gen: gen}
	v, err := t.Compute()
	switch {
	case err != nil:
		out.Err = err
	default:
		out.Value = v
		out.Published = m.publish(gen, t.Key, v)
		out.Rejected = !out.Published
	}
	if t.Cleanup != nil {
		t.Cleanup(out)
	}
}

// publish installs v in generation gen's cache iff gen is still the
// current generation. It is linearized against Switch by m.mu.
func (m *Manager) publish(gen uint64, key string, v any) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gen != m.gen {
		return false
	}
	m.cur.cache[key] = v
	return true
}

// Switch seals the current generation and opens a new one, returning
// the new generation id. It never blocks on in-flight tasks of the
// old generation; their cleanup proceeds concurrently.
func (m *Manager) Switch() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.past[m.gen] = m.cur
	m.gen++
	m.cur = &generation{cache: make(map[string]any)}
	return m.gen
}

// WaitGeneration blocks until every task that joined generation gen
// has finished, including its Cleanup hook. It is meant for sealed
// generations; calling it on the current generation is only safe if
// no Start for that generation is still in progress. Waiting on an
// old generation never delays tasks of newer generations.
func (m *Manager) WaitGeneration(gen uint64) {
	m.mu.Lock()
	g, ok := m.past[gen]
	if !ok {
		if gen != m.gen {
			m.mu.Unlock()
			return
		}
		g = m.cur
	}
	m.mu.Unlock()
	g.wg.Wait()
}

// Snapshot returns a copy of generation gen's cache, whether sealed
// or current, and whether that generation exists.
func (m *Manager) Snapshot(gen uint64) (map[string]any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := m.cur
	if gen != m.gen {
		var ok bool
		g, ok = m.past[gen]
		if !ok {
			return nil, false
		}
	}
	out := make(map[string]any, len(g.cache))
	for k, v := range g.cache {
		out[k] = v
	}
	return out, true
}
