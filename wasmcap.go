// Package wasmcap lets a Go host expose a confined set of capabilities to
// untrusted Wasm plugins through versioned function-table instances.
//
// Every instance of a module carries a monotonically increasing version.
// Captured function references and tables are bound to the instance version
// that created them; unloading a module kills its instances and bumps their
// versions, so stale references can never ride a freshly instantiated
// instance of the same module.
package wasmcap

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrOutOfBounds          = errors.New("wasmcap: index out of bounds")
	ErrNilSlot              = errors.New("wasmcap: null table slot")
	ErrSignatureMismatch    = errors.New("wasmcap: signature mismatch")
	ErrCapabilityMismatch   = errors.New("wasmcap: slot capability does not match function capability")
	ErrCapabilityNotGranted = errors.New("wasmcap: capability not granted")
	ErrStaleInstance        = errors.New("wasmcap: reference belongs to a stale instance version")
	ErrCrossModule          = errors.New("wasmcap: function belongs to a different module")
	ErrSlotOwnership        = errors.New("wasmcap: slot not owned by this instance")
)

// ValueType is a Wasm value type used in signatures.
type ValueType byte

const (
	I32 ValueType = iota
	I64
	F32
	F64
)

// Signature describes the parameter and result types of a function.
type Signature struct {
	Params  []ValueType
	Results []ValueType
}

// Equal reports whether two signatures are identical.
func (s Signature) Equal(o Signature) bool {
	if len(s.Params) != len(o.Params) || len(s.Results) != len(o.Results) {
		return false
	}
	for i := range s.Params {
		if s.Params[i] != o.Params[i] {
			return false
		}
	}
	for i := range s.Results {
		if s.Results[i] != o.Results[i] {
			return false
		}
	}
	return true
}

// Capability names a host-provided ability that may be granted to a module.
type Capability string

// Host owns capability grants and the global instance-version counter.
type Host struct {
	mu      sync.Mutex
	grants  map[*Module]map[Capability]struct{}
	version atomic.Uint64
}

// NewHost returns an empty host with no modules and no grants.
func NewHost() *Host {
	return &Host{grants: make(map[*Module]map[Capability]struct{})}
}

// LoadModule registers a new module with the host.
func (h *Host) LoadModule(name string) *Module {
	return &Module{host: h, name: name, live: make(map[*Instance]struct{})}
}

// Grant allows m to use capability c.
func (h *Host) Grant(m *Module, c Capability) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.grants[m]
	if !ok {
		set = make(map[Capability]struct{})
		h.grants[m] = set
	}
	set[c] = struct{}{}
}

// Revoke withdraws capability c from m, effective immediately.
func (h *Host) Revoke(m *Module, c Capability) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.grants[m]; ok {
		delete(set, c)
	}
}

// Authorized reports whether m currently holds capability c.
func (h *Host) Authorized(m *Module, c Capability) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.grants[m]
	if !ok {
		return false
	}
	_, ok = set[c]
	return ok
}

func (h *Host) nextVersion() uint64 {
	return h.version.Add(1)
}

// Module is a loaded plugin. Instances created from it share its grants.
type Module struct {
	host *Host
	name string

	mu   sync.Mutex
	live map[*Instance]struct{}
}

// Name returns the module name given at load time.
func (m *Module) Name() string { return m.name }

// Instantiate creates a fresh instance with a new, globally unique version.
func (m *Module) Instantiate() *Instance {
	inst := &Instance{module: m}
	inst.version.Store(m.host.nextVersion())
	inst.alive.Store(true)
	m.mu.Lock()
	m.live[inst] = struct{}{}
	m.mu.Unlock()
	return inst
}

// Unload kills every live instance of the module. Surviving references
// (captured functions, tables) become permanently stale because their
// instance is marked dead; any future instance carries a fresh,
// globally unique version.
func (m *Module) Unload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for inst := range m.live {
		inst.alive.Store(false)
		delete(m.live, inst)
	}
}

// Instance is one versioned activation of a module.
type Instance struct {
	module  *Module
	version atomic.Uint64
	alive   atomic.Bool
}

// Version returns the instance's current version.
func (i *Instance) Version() uint64 { return i.version.Load() }

// Alive reports whether the instance's module has not been unloaded.
func (i *Instance) Alive() bool { return i.alive.Load() }

// checkLive verifies that a reference captured at version v still belongs
// to the current, living instance.
func (i *Instance) checkLive(v uint64) error {
	if !i.alive.Load() || i.version.Load() != v {
		return ErrStaleInstance
	}
	return nil
}

// authorized reports whether the instance's module currently holds c.
func (i *Instance) authorized(c Capability) bool {
	return i.module.host.Authorized(i.module, c)
}
