// Package reactiveguard implements a minimal reactive runtime (Signal /
// Computed / Effect) protected by a circular-update guard.
//
// The guard detects two classes of runaway reactive loops:
//
//  1. Call-stack cycles: a Computed (or Effect) that directly or
//     transitively reads itself while it is still being evaluated.
//  2. Iteration storms: an Effect (or Computed) that keeps re-triggering
//     itself by writing to one of its own dependencies, causing the
//     update cascade to exceed a configurable iteration threshold.
//
// When either condition is detected the guard panics with a
// *CircularSignalUpdate carrying a detailed causal-chain report. Use
// CatchCircular (or recover) to intercept it.
package reactiveguard

import (
	"fmt"
	"strings"
)

// DefaultMaxIterations is the default per-cascade run limit for a single
// Effect/Computed before the guard forcefully blocks the loop.
const DefaultMaxIterations = 50

// maxTraceEvents bounds how many causal-chain events are kept per cascade
// so the report stays readable for long-running loops.
const maxTraceEvents = 24

// CircularSignalUpdate is raised (via panic) when the guard detects a
// circular reactive update. It identifies the trigger source and carries
// the causal chain of reads/writes that led to the block.
type CircularSignalUpdate struct {
	// Reason classifies the detection: "call-stack cycle" or
	// "iteration threshold exceeded".
	Reason string
	// Source is the name of the Effect/Computed that triggered the block.
	Source string
	// Iterations is how many times the source had run within the current
	// update cascade when the guard fired (0 for call-stack cycles).
	Iterations int
	// Chain is the ordered causal chain of events leading to the block.
	Chain []string
}

// Error renders the human-readable circular causal-chain report.
func (e *CircularSignalUpdate) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "CircularSignalUpdate: %s\n", e.Reason)
	fmt.Fprintf(&b, "  trigger source: %s\n", e.Source)
	if e.Iterations > 0 {
		fmt.Fprintf(&b, "  iterations: %d (loop blocked at iteration %d)\n", e.Iterations, e.Iterations)
	}
	b.WriteString("  circular causal chain:\n")
	for i, step := range e.Chain {
		fmt.Fprintf(&b, "    %2d. %s\n", i+1, step)
	}
	return b.String()
}

// CatchCircular runs fn and intercepts a CircularSignalUpdate panic,
// returning it as an error. Any other panic is re-panicked.
func CatchCircular(fn func()) (err *CircularSignalUpdate, caught bool) {
	defer func() {
		if r := recover(); r != nil {
			if ce, ok := r.(*CircularSignalUpdate); ok {
				err, caught = ce, true
				return
			}
			panic(r)
		}
	}()
	fn()
	return nil, false
}

// Guard is the reactive runtime and loop-deadlock protector. All Signals,
// Computeds and Effects are registered on a Guard, which tracks
// dependencies, schedules update cascades and enforces the iteration
// threshold.
type Guard struct {
	maxIterations int

	stack      []*node        // evaluation stack (call-stack cycle detection)
	pending    []*node        // dirty nodes waiting to be (re)run
	queued     map[*node]bool // dedup set for pending
	flushing   bool           // true while a cascade flush is in progress
	iterations map[*node]int  // per-node run count within the cascade
	trace      []string       // causal-chain events of the cascade
}

// NewGuard creates a Guard. maxIterations <= 0 selects
// DefaultMaxIterations (50): a node is blocked when it would run for the
// 50th time inside a single update cascade.
func NewGuard(maxIterations int) *Guard {
	if maxIterations <= 0 {
		maxIterations = DefaultMaxIterations
	}
	return &Guard{
		maxIterations: maxIterations,
		queued:        make(map[*node]bool),
		iterations:    make(map[*node]int),
	}
}

// MaxIterations reports the configured iteration threshold.
func (g *Guard) MaxIterations() int { return g.maxIterations }

// record appends an event to the causal-chain trace of the current
// cascade, keeping only the tail once the trace grows too long.
func (g *Guard) record(event string) {
	g.trace = append(g.trace, event)
	if len(g.trace) > maxTraceEvents {
		// Keep the first event (the origin of the cascade) plus the tail.
		tail := append([]string{g.trace[0], "... (intermediate iterations elided) ..."},
			g.trace[len(g.trace)-maxTraceEvents+2:]...)
		g.trace = tail
	}
}

// trackRead is called by every Signal/Computed read. While a node is
// evaluating it registers the read as a dependency and subscribes the
// node to future writes of the source.
func (g *Guard) trackRead(src *source) {
	if len(g.stack) == 0 {
		return
	}
	reader := g.stack[len(g.stack)-1]
	if reader.deps[src] {
		return
	}
	reader.deps[src] = true
	src.subs[reader] = true
}

// write is called by every Signal/Computed write. It detects writes to
// the writer's own dependencies (the malicious self-update pattern),
// records the causal-chain event and schedules subscribers.
func (g *Guard) write(src *source) {
	if len(g.stack) > 0 {
		writer := g.stack[len(g.stack)-1]
		if writer.deps[src] {
			g.record(fmt.Sprintf("iteration %d: %s wrote %s %q -- SELF-DEPENDENCY: %q was read earlier in the same evaluation",
				g.iterations[writer], writer.label(), src.kind, src.name, src.name))
		} else {
			g.record(fmt.Sprintf("iteration %d: %s wrote %s %q",
				g.iterations[writer], writer.label(), src.kind, src.name))
		}
	} else {
		g.record(fmt.Sprintf("iteration 0: external write to %s %q", src.kind, src.name))
	}
	for sub := range src.subs {
		g.schedule(sub)
	}
	g.maybeFlush()
}

// schedule marks a node dirty and enqueues it for the cascade flush.
func (g *Guard) schedule(n *node) {
	if n.kind == kindComputed {
		// Computeds are lazy: mark dirty and propagate to subscribers
		// instead of running eagerly.
		if n.dirty {
			return
		}
		n.dirty = true
		for sub := range n.src.subs {
			g.schedule(sub)
		}
		return
	}
	if !g.queued[n] {
		g.queued[n] = true
		g.pending = append(g.pending, n)
	}
}

// maybeFlush starts a cascade flush when no evaluation is in progress.
func (g *Guard) maybeFlush() {
	if g.flushing || len(g.stack) > 0 {
		return
	}
	g.flush()
}

// flush runs queued nodes until the cascade settles or the guard blocks
// a runaway node at the iteration threshold.
func (g *Guard) flush() {
	g.flushing = true
	defer func() {
		// Reset cascade-scoped bookkeeping on both normal completion and
		// guard panic, so the Guard stays usable after a blocked loop.
		g.flushing = false
		g.pending = nil
		g.queued = make(map[*node]bool)
		g.iterations = make(map[*node]int)
		g.trace = nil
	}()
	for len(g.pending) > 0 {
		n := g.pending[0]
		g.pending = g.pending[1:]
		delete(g.queued, n)

		g.iterations[n]++
		if g.iterations[n] >= g.maxIterations {
			panic(g.iterationError(n))
		}
		g.record(fmt.Sprintf("iteration %d: running %s", g.iterations[n], n.label()))
		n.run()
	}
}

// iterationError builds the CircularSignalUpdate for a node that broke
// the iteration threshold, including the causal chain of the cascade.
func (g *Guard) iterationError(n *node) *CircularSignalUpdate {
	chain := append([]string{}, g.trace...)
	chain = append(chain, fmt.Sprintf("iteration %d: BLOCKED -- %s would run again; iteration threshold %d reached",
		g.iterations[n], n.label(), g.maxIterations))
	return &CircularSignalUpdate{
		Reason:     "iteration threshold exceeded: reactive loop blocked",
		Source:     n.label(),
		Iterations: g.iterations[n],
		Chain:      chain,
	}
}

// stackCycleError builds the CircularSignalUpdate for a node that
// re-entered itself on the evaluation stack (direct or transitive
// self-read), rendering the stack as the causal chain.
func (g *Guard) stackCycleError(n *node) *CircularSignalUpdate {
	start := 0
	for i, onStack := range g.stack {
		if onStack == n {
			start = i
			break
		}
	}
	var chain []string
	for _, onStack := range g.stack[start:] {
		chain = append(chain, fmt.Sprintf("evaluating %s", onStack.label()))
	}
	chain = append(chain, fmt.Sprintf("BLOCKED -- %s re-entered itself while still evaluating (circular dependency)",
		n.label()))
	return &CircularSignalUpdate{
		Reason: "call-stack cycle detected: circular dependency in evaluation",
		Source: n.label(),
		Chain:  chain,
	}
}
