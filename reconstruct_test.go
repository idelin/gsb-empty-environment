package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestRootlessVirtualObjectIsNotMaterialized(t *testing.T) {
	heap := NewHeap()
	in := Reconstruction{
		Registers: []Value{VirtualRef(1)},
		Objects: []VirtualObject{
			{ID: 1, Fields: map[string]Value{"ok": IntValue(1)}},
			{ID: 2, Fields: map[string]Value{"self": VirtualRef(2)}},
		},
	}

	frame, proof, err := Reconstruct(in, heap)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if heap.AllocationCalls() != 1 {
		t.Fatalf("allocation calls = %d; want 1 (rootless object must not allocate)", heap.AllocationCalls())
	}
	if len(proof) != 1 {
		t.Fatalf("proof has %d entries; want 1: %#v", len(proof), proof)
	}
	if _, ok := proof[2]; ok {
		t.Fatalf("rootless virtual 2 appears in proof: %#v", proof)
	}
	if len(frame.Objects) != 1 {
		t.Fatalf("frame has %d new objects; want 1", len(frame.Objects))
	}
	if _, ok := proof[1]; !ok {
		t.Fatalf("reachable virtual 1 missing from proof: %#v", proof)
	}
}

func TestDuplicateDefinitionRejectedBeforeAllocation(t *testing.T) {
	heap := NewHeap()
	in := Reconstruction{
		Registers: []Value{VirtualRef(1)},
		Objects: []VirtualObject{
			{ID: 1},
			{ID: 1, Fields: map[string]Value{"x": IntValue(1)}},
		},
	}

	frame, proof, err := Reconstruct(in, heap)
	if !errors.Is(err, ErrDuplicateVirtual) {
		t.Fatalf("error = %v; want ErrDuplicateVirtual", err)
	}
	if frame != nil || proof != nil {
		t.Fatalf("duplicate definition published frame=%v proof=%v", frame, proof)
	}
	if heap.AllocationCalls() != 0 {
		t.Fatalf("allocation calls = %d; want 0", heap.AllocationCalls())
	}
}

func TestTooManyVirtualDefinitionsRejected(t *testing.T) {
	objects := make([]VirtualObject, 65)
	for i := range objects {
		objects[i].ID = VirtualID(i)
	}
	in := Reconstruction{
		Registers: []Value{VirtualRef(0)},
		Objects:   objects,
	}
	heap := NewHeap()
	frame, proof, err := Reconstruct(in, heap)
	if !errors.Is(err, ErrTooManyVirtualObjects) {
		t.Fatalf("error = %v; want ErrTooManyVirtualObjects", err)
	}
	if frame != nil || proof != nil {
		t.Fatal("invalid input published a frame or proof")
	}
	if heap.AllocationCalls() != 0 {
		t.Fatalf("allocation calls = %d; want 0", heap.AllocationCalls())
	}
}

func TestExactly64VirtualDefinitionsSucceed(t *testing.T) {
	objects := make([]VirtualObject, MaxVirtualObjects)
	for i := range objects {
		id := VirtualID(i)
		next := VirtualID((i + 1) % MaxVirtualObjects)
		objects[i] = VirtualObject{ID: id, Fields: map[string]Value{"next": VirtualRef(next)}}
	}
	in := Reconstruction{
		Registers: []Value{VirtualRef(0)},
		Objects:   objects,
	}
	heap := NewHeap()
	frame, proof, err := Reconstruct(in, heap)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if len(proof) != MaxVirtualObjects {
		t.Fatalf("proof entries = %d; want %d", len(proof), MaxVirtualObjects)
	}
	if len(frame.Objects) != MaxVirtualObjects {
		t.Fatalf("new objects = %d; want %d", len(frame.Objects), MaxVirtualObjects)
	}
	if heap.AllocationCalls() != MaxVirtualObjects {
		t.Fatalf("allocation calls = %d; want %d", heap.AllocationCalls(), MaxVirtualObjects)
	}
}

func TestMissingVirtualReferenceRejectedBeforeAllocation(t *testing.T) {
	cases := []struct {
		name string
		in   Reconstruction
	}{
		{
			name: "root",
			in: Reconstruction{
				Registers: []Value{VirtualRef(9)},
			},
		},
		{
			name: "field",
			in: Reconstruction{
				Registers: []Value{VirtualRef(1)},
				Objects: []VirtualObject{{
					ID:     1,
					Fields: map[string]Value{"bad": VirtualRef(9)},
				}},
			},
		},
		{
			name: "rootless malformed definition",
			in: Reconstruction{
				Registers: []Value{VirtualRef(1)},
				Objects: []VirtualObject{
					{ID: 1},
					{ID: 2, Fields: map[string]Value{"bad": VirtualRef(9)}},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			heap := NewHeap()
			frame, proof, err := Reconstruct(tc.in, heap)
			if !errors.Is(err, ErrMissingVirtual) {
				t.Fatalf("error = %v; want ErrMissingVirtual", err)
			}
			if frame != nil || proof != nil {
				t.Fatal("missing reference published a frame or proof")
			}
			if heap.AllocationCalls() != 0 {
				t.Fatalf("allocation calls = %d; want 0", heap.AllocationCalls())
			}
		})
	}
}

func TestInvalidKindsAndClosedFieldRejected(t *testing.T) {
	heap := NewHeap()
	frame, proof, err := Reconstruct(Reconstruction{
		Registers: []Value{{Kind: ValueKind(99)}},
	}, heap)
	if !errors.Is(err, ErrInvalidValueKind) {
		t.Fatalf("bad kind error = %v; want ErrInvalidValueKind", err)
	}
	if frame != nil || proof != nil || heap.AllocationCalls() != 0 {
		t.Fatal("invalid kind had side effects")
	}

	frame, proof, err = Reconstruct(Reconstruction{
		Registers: []Value{VirtualRef(1)},
		Objects: []VirtualObject{{
			ID:     1,
			Fields: map[string]Value{"closed": HeapRef(500)},
		}},
	}, heap)
	if !errors.Is(err, ErrInvalidClosedField) {
		t.Fatalf("closed field error = %v; want ErrInvalidClosedField", err)
	}
	if frame != nil || proof != nil || heap.AllocationCalls() != 0 {
		t.Fatal("invalid closed field had side effects")
	}
}

func TestAllocationFailureAtEveryPointRollsBack(t *testing.T) {
	const reachable = 4
	makeInput := func() Reconstruction {
		objects := make([]VirtualObject, reachable)
		for i := range objects {
			id := VirtualID(i + 1)
			fields := map[string]Value{"n": IntValue(int64(i))}
			if i+1 < reachable {
				fields["next"] = VirtualRef(id + 1)
			}
			objects[i] = VirtualObject{ID: id, Fields: fields}
		}
		return Reconstruction{
			Registers: []Value{VirtualRef(1)},
			Objects:   objects,
			Existing:  []ObjectID{500},
		}
	}

	baseline := map[string]Value{"keep": IntValue(7)}
	for failAt := 1; failAt <= reachable; failAt++ {
		t.Run(fmt.Sprintf("fail-at-%d", failAt), func(t *testing.T) {
			existing := &Object{ID: 500, Fields: cloneFields(baseline)}
			heap := NewHeapWithExisting(existing)
			heap.SetFailureAt(failAt)

			frame, proof, err := Reconstruct(makeInput(), heap)
			if !errors.Is(err, ErrAllocationFailed) {
				t.Fatalf("error = %v; want ErrAllocationFailed", err)
			}
			if !errors.Is(err, ErrInjectedAllocationFailure) {
				t.Fatalf("error = %v; want injected failure cause", err)
			}
			if frame != nil || proof != nil {
				t.Fatalf("failed reconstruction published frame=%v proof=%v", frame, proof)
			}
			if got := heap.AllocationCalls(); got != failAt {
				t.Fatalf("allocation calls = %d; want %d", got, failAt)
			}
			if got := heap.ReleaseCalls(); got != failAt-1 {
				t.Fatalf("release calls = %d; want %d", got, failAt-1)
			}
			if got := heap.Len(); got != 1 {
				t.Fatalf("live objects = %d; want only the existing object", got)
			}
			if got, ok := heap.Object(500); !ok || got != existing {
				t.Fatalf("existing object was released or replaced: %v, %v", got, ok)
			}
			if !reflect.DeepEqual(existing.Fields, baseline) {
				t.Fatalf("existing object fields changed: %#v; want %#v", existing.Fields, baseline)
			}
			for _, id := range heap.ReleasedIDs() {
				if id == 500 {
					t.Fatalf("rollback released existing object 500")
				}
			}
		})
	}

	t.Run("no-failure", func(t *testing.T) {
		existing := &Object{ID: 500, Fields: cloneFields(baseline)}
		heap := NewHeapWithExisting(existing)
		heap.SetFailureAt(reachable + 1)
		frame, proof, err := Reconstruct(makeInput(), heap)
		if err != nil {
			t.Fatalf("Reconstruct: %v", err)
		}
		if len(proof) != reachable || len(frame.Objects) != reachable {
			t.Fatalf("proof=%d objects=%d; want %d", len(proof), len(frame.Objects), reachable)
		}
		if heap.ReleaseCalls() != 0 {
			t.Fatalf("successful reconstruction released %d objects", heap.ReleaseCalls())
		}
		if got := heap.Len(); got != reachable+1 {
			t.Fatalf("live objects = %d; want %d", got, reachable+1)
		}
	})
}

func TestExistingObjectsAreNotCopiedOrModified(t *testing.T) {
	existing := &Object{
		ID:     500,
		Fields: map[string]Value{"keep": IntValue(7), "nested": HeapRef(501)},
	}
	existingNested := &Object{ID: 501, Fields: map[string]Value{"keep": IntValue(1)}}
	before := cloneFields(existing.Fields)
	heap := NewHeapWithExisting(existing, existingNested)
	in := Reconstruction{
		Registers: []Value{HeapRef(500), VirtualRef(1)},
		Stack:     []Value{IntValue(-3)},
		Objects: []VirtualObject{{
			ID: 1,
			Fields: map[string]Value{
				"existing": HeapRef(500),
				"n":        IntValue(11),
			},
		}},
		Existing: []ObjectID{501},
	}

	frame, proof, err := Reconstruct(in, heap)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if got := mustObjectID(t, frame.Registers[0]); got != 500 {
		t.Fatalf("existing register ref = %d; want 500", got)
	}
	if _, ok := frame.Objects[500]; ok {
		t.Fatal("existing object was copied into the new-object table")
	}
	if len(frame.Objects) != 1 {
		t.Fatalf("new objects = %d; want 1", len(frame.Objects))
	}
	newID := proof[1]
	newObj, ok := frame.Object(newID)
	if !ok {
		t.Fatalf("virtual 1 object %d missing", newID)
	}
	if got := mustObjectID(t, newObj.Fields["existing"]); got != 500 {
		t.Fatalf("field existing ref = %d; want 500", got)
	}
	if got := mustInt(t, newObj.Fields["n"]); got != 11 {
		t.Fatalf("field n = %d; want 11", got)
	}
	if !reflect.DeepEqual(existing.Fields, before) {
		t.Fatalf("existing fields changed: %#v; want %#v", existing.Fields, before)
	}
	if heap.AllocationCalls() != 1 {
		t.Fatalf("allocation calls = %d; want 1 (existing object must not allocate)", heap.AllocationCalls())
	}
}

func TestClosedStateDuringMaterialization(t *testing.T) {
	cases := []struct {
		name   string
		object VirtualObject
		closed bool
	}{
		{
			name:   "closed flag",
			object: VirtualObject{ID: 1, Closed: true, Fields: map[string]Value{"x": IntValue(1)}},
			closed: true,
		},
		{
			name:   "closed field one",
			object: VirtualObject{ID: 1, Fields: map[string]Value{"closed": IntValue(1)}},
			closed: true,
		},
		{
			name:   "closed field nonzero",
			object: VirtualObject{ID: 1, Fields: map[string]Value{"closed": IntValue(2)}},
			closed: true,
		},
		{
			name:   "closed field zero",
			object: VirtualObject{ID: 1, Fields: map[string]Value{"closed": IntValue(0)}},
			closed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame, proof, err := Reconstruct(Reconstruction{
				Registers: []Value{VirtualRef(1)},
				Objects:   []VirtualObject{tc.object},
			}, NewHeap())
			if err != nil {
				t.Fatalf("Reconstruct: %v", err)
			}
			obj, ok := frame.Object(proof[1])
			if !ok {
				t.Fatal("materialized object missing")
			}
			if obj.Closed != tc.closed {
				t.Fatalf("Closed = %v; want %v", obj.Closed, tc.closed)
			}
			_, readErr := obj.Read("x")
			if tc.closed && !errors.Is(readErr, ErrObjectClosed) {
				t.Fatalf("read closed object error = %v; want ErrObjectClosed", readErr)
			}
			if !tc.closed && readErr != nil && !errors.Is(readErr, ErrMissingField) {
				t.Fatalf("read open object error = %v", readErr)
			}
		})
	}
}

func TestFieldFillingDoesNotCallAllocator(t *testing.T) {
	heap := NewHeap()
	objects := make([]VirtualObject, 10)
	for i := range objects {
		objects[i] = VirtualObject{
			ID:     VirtualID(i + 1),
			Fields: map[string]Value{"n": IntValue(int64(i)), "peer": VirtualRef(VirtualID((i+1)%10 + 1))},
		}
	}
	_, proof, err := Reconstruct(Reconstruction{
		Registers: []Value{VirtualRef(1)},
		Objects:   objects,
	}, heap)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if len(proof) != 10 {
		t.Fatalf("proof entries = %d; want 10", len(proof))
	}
	if heap.AllocationCalls() != 10 {
		t.Fatalf("allocation calls = %d; field filling must not allocate", heap.AllocationCalls())
	}
	if heap.ReleaseCalls() != 0 {
		t.Fatalf("release calls = %d; want 0", heap.ReleaseCalls())
	}
}

func TestIdentifierAllocatorAdapterAndRollback(t *testing.T) {
	for failAt := 1; failAt <= 3; failAt++ {
		t.Run(fmt.Sprintf("fail-at-%d", failAt), func(t *testing.T) {
			alloc := &testIDAllocator{next: 1, failAt: failAt}
			frame, proof, err := Reconstruct(chainReconstruction(3), alloc)
			if !errors.Is(err, ErrAllocationFailed) {
				t.Fatalf("error = %v; want ErrAllocationFailed", err)
			}
			if frame != nil || proof != nil {
				t.Fatal("failed ID allocator published a frame")
			}
			if len(alloc.released) != failAt-1 {
				t.Fatalf("released = %v; want %d entries", alloc.released, failAt-1)
			}
		})
	}

	alloc := &testIDAllocator{next: 1}
	frame, proof, err := Reconstruct(chainReconstruction(3), alloc)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if len(proof) != 3 || len(frame.Objects) != 3 {
		t.Fatalf("proof=%d objects=%d; want 3", len(proof), len(frame.Objects))
	}
}

func TestReflectionAllocatorAdapter(t *testing.T) {
	alloc := &uintAllocator{next: 10, failAt: 2}
	frame, proof, err := Reconstruct(chainReconstruction(3), alloc)
	if !errors.Is(err, ErrAllocationFailed) {
		t.Fatalf("error = %v; want ErrAllocationFailed", err)
	}
	if frame != nil || proof != nil {
		t.Fatal("failed reflection allocator published a frame")
	}
	if !reflect.DeepEqual(alloc.released, []uint64{10}) {
		t.Fatalf("released = %v; want [10]", alloc.released)
	}

	alloc = &uintAllocator{next: 10}
	frame, proof, err = Reconstruct(chainReconstruction(3), alloc)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if len(proof) != 3 || len(frame.Objects) != 3 {
		t.Fatalf("proof=%d objects=%d; want 3", len(proof), len(frame.Objects))
	}
}

func TestConcreteIdentifierCollisionWithExistingIsNotReleased(t *testing.T) {
	existing := &Object{ID: 100, Fields: map[string]Value{"keep": IntValue(1)}}
	heap := NewHeapWithExisting(existing)
	bad := &fixedObjectAllocator{objects: []*Object{{ID: 100, Fields: make(map[string]Value)}}}
	in := Reconstruction{
		Registers: []Value{VirtualRef(1)},
		Objects:   []VirtualObject{{ID: 1}},
		Existing:  []ObjectID{100},
	}

	frame, proof, err := Reconstruct(in, bad)
	if !errors.Is(err, ErrObjectIDCollision) {
		t.Fatalf("error = %v; want ErrObjectIDCollision", err)
	}
	if frame != nil || proof != nil {
		t.Fatal("collision published a frame")
	}
	if len(bad.released) != 0 {
		t.Fatalf("colliding object was released: %v", bad.released)
	}
	if _, ok := heap.Object(100); !ok {
		t.Fatal("existing object disappeared")
	}
}

func TestDuplicateConcreteIdentifiersRollBackAll(t *testing.T) {
	bad := &fixedObjectAllocator{objects: []*Object{
		{ID: 10, Fields: make(map[string]Value)},
		{ID: 10, Fields: make(map[string]Value)},
	}}
	in := Reconstruction{
		Registers: []Value{VirtualRef(1), VirtualRef(2)},
		Objects:   []VirtualObject{{ID: 1}, {ID: 2}},
	}
	frame, proof, err := Reconstruct(in, bad)
	if !errors.Is(err, ErrDuplicateObjectID) {
		t.Fatalf("error = %v; want ErrDuplicateObjectID", err)
	}
	if frame != nil || proof != nil {
		t.Fatal("duplicate object id published a frame")
	}
	if len(bad.released) != 2 {
		t.Fatalf("released = %v; want both allocations", bad.released)
	}
}

func TestNoAllocatorAndEmptyFrame(t *testing.T) {
	frame, proof, err := Reconstruct(Reconstruction{
		Registers: []Value{IntValue(4), HeapRef(9)},
		Stack:     []Value{IntValue(-1)},
	}, nil)
	if !errors.Is(err, ErrNoAllocator) {
		t.Fatalf("nil allocator error = %v; want ErrNoAllocator", err)
	}
	if frame != nil || proof != nil {
		t.Fatal("nil allocator published output")
	}

	var typedNil *Heap
	frame, proof, err = Reconstruct(Reconstruction{Registers: []Value{IntValue(4)}}, typedNil)
	if !errors.Is(err, ErrNoAllocator) {
		t.Fatalf("typed nil allocator error = %v; want ErrNoAllocator", err)
	}
	if frame != nil || proof != nil {
		t.Fatal("typed nil allocator published output")
	}

	frame, proof, err = Reconstruct(Reconstruction{
		Registers: []Value{IntValue(4), HeapRef(9)},
		Stack:     []Value{IntValue(-1)},
	}, NewHeap())
	if err != nil {
		t.Fatalf("empty Reconstruct: %v", err)
	}
	if len(proof) != 0 {
		t.Fatalf("proof = %#v; want empty", proof)
	}
	if mustInt(t, frame.Registers[0]) != 4 {
		t.Fatalf("register 0 = %v; want 4", frame.Registers[0])
	}
	if mustObjectID(t, frame.Registers[1]) != 9 {
		t.Fatalf("register 1 = %v; want object(9)", frame.Registers[1])
	}
	if mustInt(t, frame.Stack[0]) != -1 {
		t.Fatalf("stack 0 = %v; want -1", frame.Stack[0])
	}
}

func TestProofRejectsIdentityCollapse(t *testing.T) {
	proof := Proof{1: 100, 2: 100}
	if err := proof.Verify(); !errors.Is(err, ErrDuplicateObjectID) {
		t.Fatalf("Verify error = %v; want ErrDuplicateObjectID", err)
	}
}

func chainReconstruction(n int) Reconstruction {
	objects := make([]VirtualObject, n)
	for i := range objects {
		id := VirtualID(i + 1)
		fields := map[string]Value{"n": IntValue(int64(i))}
		if i+1 < n {
			fields["next"] = VirtualRef(id + 1)
		}
		objects[i] = VirtualObject{ID: id, Fields: fields}
	}
	return Reconstruction{
		Registers: []Value{VirtualRef(1)},
		Objects:   objects,
	}
}

type testIDAllocator struct {
	next     ObjectID
	failAt   int
	calls    int
	released []ObjectID
}

func (a *testIDAllocator) Allocate() (ObjectID, error) {
	a.calls++
	if a.failAt > 0 && a.calls == a.failAt {
		return 0, ErrInjectedAllocationFailure
	}
	id := a.next
	a.next++
	return id, nil
}

func (a *testIDAllocator) Release(id ObjectID) { a.released = append(a.released, id) }

type uintAllocator struct {
	next     uint64
	failAt   int
	calls    int
	released []uint64
}

func (a *uintAllocator) Alloc() (uint64, error) {
	a.calls++
	if a.failAt > 0 && a.calls == a.failAt {
		return 0, ErrInjectedAllocationFailure
	}
	id := a.next
	a.next++
	return id, nil
}

func (a *uintAllocator) Free(id uint64) { a.released = append(a.released, id) }

type fixedObjectAllocator struct {
	objects  []*Object
	next     int
	released []*Object
}

func (a *fixedObjectAllocator) Allocate() (*Object, error) {
	if a.next >= len(a.objects) {
		return nil, ErrInjectedAllocationFailure
	}
	o := a.objects[a.next]
	a.next++
	return o, nil
}

func (a *fixedObjectAllocator) Release(o *Object) { a.released = append(a.released, o) }
