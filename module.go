package netsec

import "sync"

// Module is the view of a dynamically loaded module required by the lazy
// binder. Implementations must be safe for concurrent use.
type Module interface {
	// ID is the unique module name used in Symbol.Module.
	ID() string

	// Base and Size describe the executable mapping of the module. A
	// resolved address is valid only when Base <= addr < Base+Size.
	Base() uint64
	Size() uint64

	// VerifySignature reports whether the module's authenticity/integrity
	// signature is valid. The binder only publishes addresses from modules
	// that pass this check.
	VerifySignature() bool

	// Loaded reports whether the module is currently mapped.
	Loaded() bool
}

// BasicModule is a straightforward Module implementation suitable for loader
// hosts (and tests) that maintain the module table explicitly.
type BasicModule struct {
	mu       sync.RWMutex
	id       string
	base     uint64
	size     uint64
	sigValid bool
	loaded   bool
}

// NewBasicModule registers a module descriptor. The module starts in the
// loaded state.
func NewBasicModule(id string, base, size uint64, sigValid bool) *BasicModule {
	return &BasicModule{
		id:       id,
		base:     base,
		size:     size,
		sigValid: sigValid,
		loaded:   true,
	}
}

func (m *BasicModule) ID() string { return m.id }

func (m *BasicModule) Base() uint64 { return m.base }

func (m *BasicModule) Size() uint64 { return m.size }

func (m *BasicModule) VerifySignature() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sigValid
}

func (m *BasicModule) Loaded() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loaded
}

// SetSignatureValid flips the signature verification result, allowing a
// failed binding to be retried after the host refreshes trust material.
func (m *BasicModule) SetSignatureValid(valid bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sigValid = valid
}

func (m *BasicModule) unload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loaded = false
}

// Registry is the concurrency-safe table of currently loaded modules.
type Registry struct {
	mu   sync.RWMutex
	mods map[string]Module
}

// NewRegistry creates an empty module registry.
func NewRegistry() *Registry {
	return &Registry{mods: make(map[string]Module)}
}

// Add inserts a module. Adding an ID that already exists replaces the
// previous descriptor.
func (r *Registry) Add(m Module) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mods[m.ID()] = m
}

// Get returns the module if it is currently registered.
func (r *Registry) Get(id string) (Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.mods[id]
	return m, ok
}

// Unload removes a module from the registry and marks it unloaded. It
// returns false when no such module is registered.
func (r *Registry) Unload(id string) (Module, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.mods[id]
	if !ok {
		return nil, false
	}
	if u, ok := m.(interface{ unload() }); ok {
		u.unload()
	}
	delete(r.mods, id)
	return m, true
}

// List returns snapshots of the currently registered modules.
func (r *Registry) List() []Module {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Module, 0, len(r.mods))
	for _, m := range r.mods {
		out = append(out, m)
	}
	return out
}
