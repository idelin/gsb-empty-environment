package netsec

import (
	"fmt"
	"sync"
)

// Object is a host-heap object used by the test host.  Reconstruction fills
// only objects it allocated itself; existing objects are never passed to the
// fill phase.
type Object struct {
	ID     ObjectID
	Closed bool
	Fields map[string]Value
}

// IsClosed reports whether the object has been closed.
func (o *Object) IsClosed() bool {
	if o == nil {
		return false
	}
	if o.Closed {
		return true
	}
	if v, ok := o.Fields["closed"]; ok && v.Kind == KindInt && v.Int != 0 {
		return true
	}
	return false
}

// Close marks the object closed.  The flag is stored on the object, not on an
// alias, so every reconstruction slot that refers to this identifier observes
// the same closed state.
func (o *Object) Close() {
	if o == nil {
		return
	}
	o.Closed = true
	if o.Fields == nil {
		o.Fields = make(map[string]Value, 1)
	}
	o.Fields["closed"] = IntValue(1)
}

// Read returns a field from an open object.  Reading any field of a closed
// object is rejected.
func (o *Object) Read(name string) (Value, error) {
	if o == nil {
		return Value{}, ErrMissingField
	}
	if o.IsClosed() {
		return Value{}, fmt.Errorf("%w: object %d", ErrObjectClosed, o.ID)
	}
	v, ok := o.Fields[name]
	if !ok {
		return Value{}, fmt.Errorf("%w: object %d field %q", ErrMissingField, o.ID, name)
	}
	return v, nil
}

// Field returns a field without applying the closed-object read policy.  It is
// useful for inspecting metadata and for assertions in tests.
func (o *Object) Field(name string) (Value, bool) {
	if o == nil || o.Fields == nil {
		return Value{}, false
	}
	v, ok := o.Fields[name]
	return v, ok
}

// Heap is a small host heap that is also a canonical Allocate/Release
// allocator.  It exists so the reconstruction package has an executable test
// host; engines may supply any allocator supported by Reconstruct.
type Heap struct {
	mu sync.Mutex

	objects  map[ObjectID]*Object
	existing map[ObjectID]struct{}

	next         ObjectID
	failAt       int
	allocationNo int

	releaseCalls int
	released     []ObjectID
}

// NewHeap returns an empty heap.  The first automatically allocated identifier
// is 1.
func NewHeap() *Heap {
	return &Heap{
		objects:  make(map[ObjectID]*Object),
		existing: make(map[ObjectID]struct{}),
		next:     1,
	}
}

// NewHeapWithExisting returns a heap containing the supplied host objects.
// Those objects are marked existing and can never be released by Heap.
func NewHeapWithExisting(objects ...*Object) *Heap {
	h := NewHeap()
	for _, o := range objects {
		_ = h.AddExisting(o)
	}
	return h
}

// AddExisting registers an object that predates the reconstruction under test.
func (h *Heap) AddExisting(o *Object) error {
	if o == nil {
		return ErrNilObject
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if prior, ok := h.objects[o.ID]; ok && prior != o {
		return fmt.Errorf("%w: object id %d", ErrDuplicateObjectID, o.ID)
	}
	if o.Fields == nil {
		o.Fields = make(map[string]Value)
	}
	h.objects[o.ID] = o
	h.existing[o.ID] = struct{}{}
	if o.ID >= h.next {
		h.next = o.ID + 1
	}
	return nil
}

// SetFailureAt makes the nth subsequent allocation fail (1-based).  A value of
// zero disables failure injection.
func (h *Heap) SetFailureAt(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failAt = n
	h.allocationNo = 0
}

// AllocationCalls reports the number of allocation attempts.
func (h *Heap) AllocationCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.allocationNo
}

// ReleaseCalls reports how many newly allocated objects were released.
func (h *Heap) ReleaseCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.releaseCalls
}

// ReleasedIDs returns a snapshot of released object identifiers.
func (h *Heap) ReleasedIDs() []ObjectID {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ObjectID, len(h.released))
	copy(out, h.released)
	return out
}

// Len reports the number of live objects, including existing objects.
func (h *Heap) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.objects)
}

// Object returns a live object by identifier.
func (h *Heap) Object(id ObjectID) (*Object, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	o, ok := h.objects[id]
	return o, ok
}

// Allocate implements the canonical allocator interface.
func (h *Heap) Allocate() (*Object, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.allocationNo++
	if h.failAt > 0 && h.allocationNo == h.failAt {
		return nil, ErrInjectedAllocationFailure
	}
	for {
		id := h.next
		h.next++
		if _, used := h.objects[id]; used {
			continue
		}
		o := &Object{ID: id, Fields: make(map[string]Value)}
		h.objects[id] = o
		return o, nil
	}
}

// Release implements the canonical allocator interface.  Existing objects are
// deliberately ignored: reconstruction must never release a host object.
func (h *Heap) Release(o *Object) {
	if o == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, isExisting := h.existing[o.ID]; isExisting {
		return
	}
	if live, ok := h.objects[o.ID]; !ok || live != o {
		return
	}
	delete(h.objects, o.ID)
	h.releaseCalls++
	h.released = append(h.released, o.ID)
}

// Close marks an object closed.
func (h *Heap) Close(id ObjectID) error {
	o, ok := h.Object(id)
	if !ok {
		return fmt.Errorf("%w: object %d", ErrMissingField, id)
	}
	o.Close()
	return nil
}

// Read reads a field from an object, refusing reads through a closed object.
func (h *Heap) Read(id ObjectID, name string) (Value, error) {
	o, ok := h.Object(id)
	if !ok {
		return Value{}, fmt.Errorf("%w: object %d", ErrMissingField, id)
	}
	return o.Read(name)
}
