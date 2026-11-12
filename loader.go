package netsec

import "sync"

// graph is one immutable generation of the fixed version graph. Every entry is
// built once by a committed transaction and never mutated afterwards; the
// resolution cache therefore stays valid for the whole lifetime of the
// generation.
type graph struct {
	gen      int
	modules  map[ModuleID]moduleState
	bindings map[Ref]*Binding
	lookups  int
	mu       sync.RWMutex
	cache    map[Ref]cacheEntry
}

// moduleState records that an identity exists in the version graph and whether
// it is authorized. Existence and authorization are distinct: a known but
// unauthorized module must yield ErrUnauthorized, not ErrUnknownModule.
type moduleState struct {
	authorized bool
}

type cacheEntry struct {
	addr  Address
	err   error
	trail []ModuleID
}

func newGraph(gen int) *graph {
	return &graph{
		gen:      gen,
		modules:  make(map[ModuleID]moduleState),
		bindings: make(map[Ref]*Binding),
		cache:    make(map[Ref]cacheEntry),
	}
}

// clone produces an independent copy for a transaction. Mutations made by the
// transaction cannot affect readers of the current generation.
func (g *graph) clone(nextGen int) *graph {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ng := newGraph(nextGen)
	for m, st := range g.modules {
		ng.modules[m] = st
	}
	for r, b := range g.bindings {
		cp := *b
		ng.bindings[r] = &cp
	}
	return ng
}

func (g *graph) validate() error {
	for ref, b := range g.bindings {
		if err := b.validate(); err != nil {
			return fail("commit", ref, err)
		}
		if _, ok := g.modules[ref.Module]; !ok {
			return fail("commit", ref, ErrUnknownModule)
		}
		if !b.Concrete {
			if _, ok := g.modules[b.To.Module]; !ok {
				return fail("commit", b.To, ErrUnknownModule)
			}
		}
	}
	return nil
}

// Loader is the Go loading host that resolves imported symbols. It holds one
// fixed version graph at a time. Resolve always walks the generation that was
// current when the call started, even if another goroutine commits an update
// transaction concurrently.
type Loader struct {
	mu sync.RWMutex
	g  *graph
}

// NewLoader creates a host backed by an empty version graph.
func NewLoader() *Loader {
	return &Loader{g: newGraph(0)}
}

// Generation returns the version-graph generation currently installed. Each
// successful Commit advances it by one.
func (l *Loader) Generation() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.g.gen
}

// snapshot pins one immutable generation for a resolution call.
func (l *Loader) snapshot() *graph {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.g
}

// Tx is an update transaction. It works on a private copy of the version
// graph and becomes visible only after Commit succeeds. Discard drops it.
type Tx struct {
	loader *Loader
	base   *graph
	next   *graph
	closed bool
}

// Begin starts an update transaction based on the current generation.
func (l *Loader) Begin() *Tx {
	base := l.snapshot()
	return &Tx{loader: l, base: base, next: base.clone(base.gen + 1)}
}

// AddModule introduces a module identity. Same name with a different digest is
// a separate identity. A re-added identity must keep the same digest.
func (t *Tx) AddModule(m ModuleID, authorized bool) error {
	if t.closed {
		return fail("add-module", Ref{Module: m}, ErrTxClosed)
	}
	if !validModule(m) {
		return fail("add-module", Ref{Module: m}, ErrInvalidRef)
	}
	if st, ok := t.next.modules[m]; ok {
		if st.authorized != authorized {
			return fail("add-module", Ref{Module: m}, ErrModuleExists)
		}
		return nil
	}
	t.next.modules[m] = moduleState{authorized: authorized}
	return nil
}

// SetAuthorized flips the authorization flag of an existing module identity.
func (t *Tx) SetAuthorized(m ModuleID, authorized bool) error {
	if t.closed {
		return fail("set-authorized", Ref{Module: m}, ErrTxClosed)
	}
	if _, ok := t.next.modules[m]; !ok {
		return fail("set-authorized", Ref{Module: m}, ErrUnknownModule)
	}
	t.next.modules[m] = moduleState{authorized: authorized}
	return nil
}

// Bind installs or replaces a symbol binding inside a module.
func (t *Tx) Bind(module ModuleID, symbol string, b Binding) error {
	ref := Ref{Module: module, Symbol: symbol}
	if t.closed {
		return fail("bind", ref, ErrTxClosed)
	}
	if !ref.valid() {
		return fail("bind", ref, ErrInvalidRef)
	}
	if err := b.validate(); err != nil {
		return fail("bind", ref, err)
	}
	if _, ok := t.next.modules[module]; !ok {
		return fail("bind", ref, ErrUnknownModule)
	}
	if !b.Concrete {
		if _, ok := t.next.modules[b.To.Module]; !ok {
			return fail("bind", b.To, ErrUnknownModule)
		}
	}
	cp := b
	t.next.bindings[ref] = &cp
	return nil
}

// Commit validates the whole edited graph and atomically installs it as the
// next generation. On failure the transaction is discarded and the previous
// generation stays in force.
func (t *Tx) Commit() error {
	if t.closed {
		return ErrTxClosed
	}
	t.closed = true
	if err := t.next.validate(); err != nil {
		return err
	}
	t.loader.mu.Lock()
	t.loader.g = t.next
	t.loader.mu.Unlock()
	return nil
}

// Discard abandons the transaction without touching the installed graph.
func (t *Tx) Discard() {
	t.closed = true
}

// BaseGeneration reports which generation the transaction forked from.
func (t *Tx) BaseGeneration() int { return t.base.gen }
