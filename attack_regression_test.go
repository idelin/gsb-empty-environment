package netsec

import (
	"errors"
	"reflect"
	"testing"
)

// TestSharedAliasCloseReadIsIdentityPreserving is the core attack regression.
//
// A buggy deoptimizer materializes the same virtual object twice.  Closing one
// alias then leaves the other open, so a read through the second alias
// succeeds.  This test therefore checks identity (IDs and pointers), not just
// the reconstructed field values.
func TestSharedAliasCloseReadIsIdentityPreserving(t *testing.T) {
	heap := NewHeap()
	in := Reconstruction{
		Registers: []Value{VirtualRef(7)},
		Stack:     []Value{VirtualRef(7), IntValue(99)},
		Objects: []VirtualObject{{
			ID: 7,
			Fields: map[string]Value{
				"value": IntValue(42),
				"self":  VirtualRef(7),
			},
		}},
	}

	frame, proof, err := Reconstruct(in, heap)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if err := proof.Verify(); err != nil {
		t.Fatalf("proof is not one-to-one: %v", err)
	}

	regID := mustObjectID(t, frame.Registers[0])
	stackID := mustObjectID(t, frame.Stack[0])
	if regID != stackID {
		t.Fatalf("shared virtual object was materialized twice: register object %d, stack object %d", regID, stackID)
	}
	if got, ok := proof.Object(7); !ok || got != regID {
		t.Fatalf("proof[7] = %d, %v; want %d, true", got, ok, regID)
	}

	regObj, ok := frame.Object(regID)
	if !ok {
		t.Fatalf("register object %d missing from frame", regID)
	}
	stackObj, ok := frame.Object(stackID)
	if !ok {
		t.Fatalf("stack object %d missing from frame", stackID)
	}
	if regObj != stackObj {
		t.Fatalf("aliases point at different object identities: %p != %p", regObj, stackObj)
	}
	if hosted, ok := heap.Object(regID); !ok || hosted != regObj {
		t.Fatalf("frame object is not the host object: %p vs %p", hosted, regObj)
	}

	selfID := mustObjectID(t, stackObj.Fields["self"])
	if selfID != regID {
		t.Fatalf("self reference = %d; want %d", selfID, regID)
	}
	if got := mustInt(t, frame.Stack[1]); got != 99 {
		t.Fatalf("unrelated stack slot = %d; want 99", got)
	}

	regObj.Close()
	if !stackObj.Closed {
		t.Fatal("closed flag is not visible through the stack alias")
	}
	if _, err := stackObj.Read("value"); !errors.Is(err, ErrObjectClosed) {
		t.Fatalf("read through alias after close: got %v; want ErrObjectClosed", err)
	}
	if _, err := heap.Read(stackID, "value"); !errors.Is(err, ErrObjectClosed) {
		t.Fatalf("host read through alias after close: got %v; want ErrObjectClosed", err)
	}
	if _, err := stackObj.Read("closed"); !errors.Is(err, ErrObjectClosed) {
		t.Fatalf("reading closed field must also be rejected: got %v", err)
	}
	if got, ok := stackObj.Field("closed"); !ok || mustInt(t, got) != 1 {
		t.Fatalf("closed field = %v, %v; want int(1), true", got, ok)
	}

	heap.Close(stackID)
	if !regObj.Closed {
		t.Fatal("closed flag is not visible through the register alias")
	}
}

func TestEqualFieldsDoNotProveEqualIdentity(t *testing.T) {
	fields := map[string]Value{"payload": IntValue(123), "state": IntValue(1)}
	in := Reconstruction{
		Registers: []Value{VirtualRef(1), VirtualRef(2), VirtualRef(1)},
		Objects: []VirtualObject{
			{ID: 1, Fields: cloneFields(fields)},
			{ID: 2, Fields: cloneFields(fields)},
		},
	}

	frame, proof, err := Reconstruct(in, NewHeap())
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	id1 := mustObjectID(t, frame.Registers[0])
	id2 := mustObjectID(t, frame.Registers[1])
	alias1 := mustObjectID(t, frame.Registers[2])
	if id1 == id2 {
		t.Fatalf("distinct virtual objects collapsed to one identity %d", id1)
	}
	if alias1 != id1 {
		t.Fatalf("repeat alias = %d; want %d", alias1, id1)
	}

	obj1, _ := frame.Object(id1)
	obj2, _ := frame.Object(id2)
	if obj1 == obj2 {
		t.Fatal("distinct virtual objects share one object pointer")
	}
	if !reflect.DeepEqual(obj1.Fields, obj2.Fields) {
		t.Fatalf("test setup error: fields differ: %#v vs %#v", obj1.Fields, obj2.Fields)
	}
	if proof[1] == proof[2] {
		t.Fatalf("proof collapsed identities: %d and %d", proof[1], proof[2])
	}

	obj1.Close()
	if obj2.Closed {
		t.Fatal("closing object 1 also closed distinct object 2")
	}
	if _, err := obj2.Read("payload"); err != nil {
		t.Fatalf("distinct object should remain readable: %v", err)
	}
}

func TestSharedAliasReleaseUsesSameIdentity(t *testing.T) {
	heap := NewHeap()
	in := Reconstruction{
		Registers: []Value{VirtualRef(3)},
		Stack:     []Value{VirtualRef(3), VirtualRef(3)},
		Objects: []VirtualObject{{
			ID:     3,
			Fields: map[string]Value{"payload": IntValue(8)},
		}},
	}

	frame, proof, err := Reconstruct(in, heap)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	regID := mustObjectID(t, frame.Registers[0])
	slotID := mustObjectID(t, frame.Stack[0])
	otherSlotID := mustObjectID(t, frame.Stack[1])
	if regID != slotID || regID != otherSlotID {
		t.Fatalf("aliases split: register=%d stack=%d stack2=%d", regID, slotID, otherSlotID)
	}
	if proof[3] != regID {
		t.Fatalf("proof[3] = %d; want %d", proof[3], regID)
	}

	obj, ok := frame.Object(regID)
	if !ok {
		t.Fatalf("object %d missing", regID)
	}
	heap.Release(obj)
	if _, live := heap.Object(slotID); live {
		t.Fatalf("object %d still live through slot alias after release", slotID)
	}
	if _, live := heap.Object(otherSlotID); live {
		t.Fatalf("object %d still live through second slot alias after release", otherSlotID)
	}
}

func TestSelfLoopAndMutualCycleKeepIdentity(t *testing.T) {
	in := Reconstruction{
		Registers: []Value{VirtualRef(1)},
		Objects: []VirtualObject{
			{ID: 1, Fields: map[string]Value{
				"self": VirtualRef(1),
				"peer": VirtualRef(2),
			}},
			{ID: 2, Fields: map[string]Value{
				"peer": VirtualRef(1),
			}},
		},
	}

	frame, proof, err := Reconstruct(in, NewHeap())
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	id1 := proof[1]
	id2 := proof[2]
	if id1 == id2 {
		t.Fatalf("cycle endpoints share identity %d", id1)
	}
	obj1, _ := frame.Object(id1)
	obj2, _ := frame.Object(id2)
	if got := mustObjectID(t, obj1.Fields["self"]); got != id1 {
		t.Fatalf("self loop = %d; want %d", got, id1)
	}
	if got := mustObjectID(t, obj1.Fields["peer"]); got != id2 {
		t.Fatalf("1.peer = %d; want %d", got, id2)
	}
	if got := mustObjectID(t, obj2.Fields["peer"]); got != id1 {
		t.Fatalf("2.peer = %d; want %d", got, id1)
	}
}

func cloneFields(in map[string]Value) map[string]Value {
	out := make(map[string]Value, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mustObjectID(t *testing.T, v Value) ObjectID {
	t.Helper()
	if v.Kind != KindHeap {
		t.Fatalf("value %v is not a concrete object reference", v)
	}
	return v.Heap
}

func mustInt(t *testing.T, v Value) int64 {
	t.Helper()
	if v.Kind != KindInt {
		t.Fatalf("value %v is not an integer", v)
	}
	return v.Int
}
