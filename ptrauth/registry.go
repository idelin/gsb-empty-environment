package ptrauth

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// CodeFunc is the runtime entry point bound to an executable object. It is
// deliberately simple: the real runtime would bridge to machine code, tests
// supply an ordinary Go closure.
type CodeFunc func(arg uint64) uint64

// object tracks one address identity across generations. Retired entries are
// retained as tombstones so stale tags can be rejected after the address is
// freed and later reused.
type object struct {
	mu sync.RWMutex

	address    uint64
	size       uint64
	generation atomic.Uint64
	live       atomic.Bool

	// data keeps the backing allocation reachable for the GC and supplies
	// the dereference target. It is nil for code objects and for retired
	// generations.
	data []byte
	// code is non-nil only for live executable objects.
	code CodeFunc
}

// Object is a handle to a registered allocation. Free it through the
// Registry; do not copy and retain it beyond retirement.
type Object struct {
	registry *Registry
	inner    *object
}

// Address returns the object's base address.
func (o *Object) Address() uint64 { return o.inner.address }

// Size returns the object's span in bytes.
func (o *Object) Size() uint64 { return o.inner.size }

// Generation returns the current generation. Each free/reuse cycle advances
// it, invalidating every tag minted for an earlier generation.
func (o *Object) Generation() uint64 { return o.inner.generation.Load() }

// Live reports whether the current generation is still allocated.
func (o *Object) Live() bool { return o.inner.live.Load() }

// Registry owns the object table and the injected Authenticator. All its
// methods are safe for concurrent use.
type Registry struct {
	auth Authenticator

	// objects is keyed by base address. Retired objects stay in the map so
	// address reuse can be detected.
	mu      sync.RWMutex
	objects map[uint64]*object
}

// NewRegistry builds a Registry backed by the supplied authenticator.
func NewRegistry(auth Authenticator) *Registry {
	return &Registry{
		auth:    auth,
		objects: make(map[uint64]*object),
	}
}

// backingAddress returns the base address of a byte allocation. The
// conversion follows the unsafe.Pointer rules: the pointer is derived from
// the live slice and never stored for later arithmetic outside the object.
func backingAddress(backing []byte) uint64 {
	return uint64(uintptr(unsafe.Pointer(unsafe.SliceData(backing))))
}

// RegisterData registers a writable data allocation and returns its first
// generation. Re-registering the same address after Free reuses the
// tombstone entry under the generation already advanced by Free, so every
// tag minted before the free remains stale.
func (r *Registry) RegisterData(backing []byte) (*Object, error) {
	if len(backing) == 0 {
		return nil, ErrEmptyObject
	}
	address := backingAddress(backing)
	if address == 0 {
		return nil, ErrUnknownAddress
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing := r.objects[address]; existing != nil {
		existing.mu.Lock()
		defer existing.mu.Unlock()
		if existing.live.Load() {
			return nil, ErrAddressLive
		}
		existing.size = uint64(len(backing))
		existing.data = backing
		existing.code = nil
		existing.live.Store(true)
		return &Object{registry: r, inner: existing}, nil
	}

	obj := &object{
		address: address,
		size:    uint64(len(backing)),
		data:    backing,
	}
	obj.generation.Store(1)
	obj.live.Store(true)
	r.objects[address] = obj
	return &Object{registry: r, inner: obj}, nil
}

// RegisterCode registers an executable allocation at address with the given
// span and entry point.
func (r *Registry) RegisterCode(address, size uint64, entry CodeFunc) (*Object, error) {
	if address == 0 {
		return nil, ErrUnknownAddress
	}
	if size == 0 {
		return nil, ErrEmptyObject
	}
	if entry == nil {
		return nil, ErrNotExecutable
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing := r.objects[address]; existing != nil {
		existing.mu.Lock()
		defer existing.mu.Unlock()
		if existing.live.Load() {
			return nil, ErrAddressLive
		}
		existing.size = size
		existing.data = nil
		existing.code = entry
		existing.live.Store(true)
		return &Object{registry: r, inner: existing}, nil
	}

	obj := &object{
		address: address,
		size:    size,
		code:    entry,
	}
	obj.generation.Store(1)
	obj.live.Store(true)
	r.objects[address] = obj
	return &Object{registry: r, inner: obj}, nil
}

// Free retires the object's current generation. The table entry is retained
// as a tombstone; in-flight dereferences drain before the backing memory is
// detached. Free on an already-retired object fails with
// ErrGenerationRetired.
func (r *Registry) Free(obj *Object) error {
	if obj == nil || obj.registry != r || obj.inner == nil {
		return ErrUnknownAddress
	}
	target := obj.inner

	target.mu.Lock()
	defer target.mu.Unlock()

	if !target.live.Load() {
		return ErrGenerationRetired
	}
	target.live.Store(false)
	target.generation.Add(1)
	target.data = nil
	target.code = nil
	return nil
}
