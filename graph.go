// Package signalguard implements a reactive Signal/Effect/Computed core with
// a cycle guard that detects and blocks circular updates.
package signalguard

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultMaxIterations is the default per-flush execution budget for a single
// observer. The guard blocks any observer that would run more than this many
// times within one flush generation.
const DefaultMaxIterations = 50

// signalNode is anything that can be read as a reactive dependency.
type signalNode interface {
	Name() string
}

// observer is anything that re-runs in response to dependency changes.
type observer interface {
	Name() string
	run()
	invalidate(src signalNode)
}

// triggerInfo records why an observer was scheduled: which signal was
// written, and which observer performed the write (nil for external writes).
type triggerInfo struct {
	by  observer
	via signalNode
}

// Graph owns all signals and observers and enforces the cycle guard.
// A Graph is not safe for concurrent use.
type Graph struct {
	maxIterations int

	running  []observer // execution call stack
	queue    []observer // scheduled observers
	queued   map[observer]bool
	flushing bool

	execCount map[observer]int
	deps      map[observer]map[signalNode]struct{}
	subs      map[signalNode]map[observer]struct{}
	trigger   map[observer]triggerInfo
}

// Option configures a Graph.
type Option func(*Graph)

// WithMaxIterations sets the per-flush execution budget per observer.
func WithMaxIterations(n int) Option {
	return func(g *Graph) { g.maxIterations = n }
}

// NewGraph creates a reactive graph guarded against circular updates.
func NewGraph(opts ...Option) *Graph {
	g := &Graph{
		maxIterations: DefaultMaxIterations,
		queued:        make(map[observer]bool),
		execCount:     make(map[observer]int),
		deps:          make(map[observer]map[signalNode]struct{}),
		subs:          make(map[signalNode]map[observer]struct{}),
		trigger:       make(map[observer]triggerInfo),
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

func (g *Graph) current() observer {
	if len(g.running) == 0 {
		return nil
	}
	return g.running[len(g.running)-1]
}

// trackRead records that the currently running observer read s.
func (g *Graph) trackRead(s signalNode) {
	o := g.current()
	if o == nil {
		return
	}
	set := g.deps[o]
	if set == nil {
		set = make(map[signalNode]struct{})
		g.deps[o] = set
	}
	if _, ok := set[s]; ok {
		return
	}
	set[s] = struct{}{}
	observers := g.subs[s]
	if observers == nil {
		observers = make(map[observer]struct{})
		g.subs[s] = observers
	}
	observers[o] = struct{}{}
}

// notify invalidates every subscriber of s and kicks off a flush when no
// observer is currently running.
func (g *Graph) notify(s signalNode, by observer) {
	for o := range g.subs[s] {
		g.trigger[o] = triggerInfo{by: by, via: s}
		o.invalidate(s)
	}
	g.maybeFlush()
}

func (g *Graph) schedule(o observer) {
	if g.queued[o] {
		return
	}
	g.queued[o] = true
	g.queue = append(g.queue, o)
}

func (g *Graph) maybeFlush() {
	if g.flushing || len(g.running) > 0 {
		return
	}
	g.flush()
}

func (g *Graph) flush() {
	if g.flushing {
		return
	}
	g.flushing = true
	defer func() { g.flushing = false }()
	for len(g.queue) > 0 {
		o := g.queue[0]
		g.queue = g.queue[1:]
		delete(g.queued, o)
		g.run(o)
	}
	// The flush generation ended cleanly: reset guard bookkeeping.
	g.execCount = make(map[observer]int)
	g.trigger = make(map[observer]triggerInfo)
}

// run executes o, enforcing both guard rules.
func (g *Graph) run(o observer) {
	// Rule 1: call-stack cycle. An observer that is already executing must
	// never be re-entered (direct or indirect synchronous recursion).
	for _, r := range g.running {
		if r == o {
			panic(g.newCallStackCycleError(o))
		}
	}
	// Rule 2: iteration threshold. An observer re-triggering itself (through
	// writes to its own dependencies) is blocked once it breaks the budget.
	g.execCount[o]++
	if g.execCount[o] > g.maxIterations {
		panic(g.newIterationError(o))
	}
	// Drop stale dependencies; they are re-tracked during this run.
	if old := g.deps[o]; old != nil {
		for s := range old {
			delete(g.subs[s], o)
		}
		delete(g.deps, o)
	}
	g.running = append(g.running, o)
	defer func() {
		g.running = g.running[:len(g.running)-1]
		if len(g.running) == 0 {
			g.maybeFlush()
		}
	}()
	o.run()
}

func (g *Graph) newCallStackCycleError(o observer) *CircularSignalUpdate {
	var chain []string
	for _, r := range g.running {
		chain = append(chain, fmt.Sprintf("%s executing", r.Name()))
	}
	chain = append(chain, fmt.Sprintf("%s re-entered while already on the call stack -> cycle", o.Name()))
	return &CircularSignalUpdate{
		Reason: "call-stack cycle detected: observer re-entered itself synchronously",
		Source: o.Name(),
		Chain:  chain,
	}
}

func (g *Graph) newIterationError(o observer) *CircularSignalUpdate {
	chain := g.causalChain(o)
	if deps := g.deps[o]; len(deps) > 0 {
		names := make([]string, 0, len(deps))
		for s := range deps {
			names = append(names, s.Name())
		}
		sort.Strings(names)
		chain = append(chain, fmt.Sprintf("%s reads its own dependencies: %s", o.Name(), strings.Join(names, ", ")))
	}
	return &CircularSignalUpdate{
		Reason:     fmt.Sprintf("iteration limit exceeded: %s keeps re-triggering itself", o.Name()),
		Source:     o.Name(),
		Chain:      chain,
		Iterations: g.execCount[o],
		Limit:      g.maxIterations,
	}
}

// causalChain walks the trigger records backwards from o to reconstruct who
// wrote what to cause each scheduling.
func (g *Graph) causalChain(o observer) []string {
	var chain []string
	cur := o
	for i := 0; i < 64; i++ {
		t, ok := g.trigger[cur]
		if !ok {
			chain = append(chain, fmt.Sprintf("%s (root trigger)", cur.Name()))
			break
		}
		if t.by == nil {
			chain = append(chain, fmt.Sprintf("%s triggered by external write to %s", cur.Name(), t.via.Name()))
			break
		}
		chain = append(chain, fmt.Sprintf("%s triggered by %s writing %s", cur.Name(), t.by.Name(), t.via.Name()))
		if t.by == cur {
			chain = append(chain, fmt.Sprintf("%s forms a self-loop through %s", t.by.Name(), t.via.Name()))
			break
		}
		cur = t.by
	}
	return chain
}
