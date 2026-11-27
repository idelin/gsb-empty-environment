package minijit

import "testing"

func TestTwoPhaseMaterializationCyclesAndIdentity(t *testing.T) {
	// Virtual ids:
	//   0 = A {n:1, next: -> v1, peer: -> v2}
	//   1 = B {back: -> v0}            (mutual cycle A<->B)
	//   2 = C {left: -> v0, right: -> v0} (diamond sharing one identity)
	virtuals := []VirtualObject{
		{
			ID: 0, Class: "A",
			Fields: []FieldInput{
				{Name: "n", Value: LiteralInput(IntValue(1))},
				{Name: "next", Value: VirtualInput(1)},
				{Name: "peer", Value: VirtualInput(2)},
			},
		},
		{
			ID: 1, Class: "B",
			Fields: []FieldInput{{Name: "back", Value: VirtualInput(0)}},
		},
		{
			ID: 2, Class: "C",
			Fields: []FieldInput{
				{Name: "left", Value: VirtualInput(0)},
				{Name: "right", Value: VirtualInput(0)},
			},
		},
	}

	fn := NewFunction("inner", 1, 2)
	fn.Emit(OpReturn, 0, 0)
	// Two independent inlined frames both reference virtual id 0.
	inner := &FrameState{
		Function: fn, PC: 0,
		Locals: []Input{VirtualInput(0), LiteralInput(IntValue(7))},
		Stack:  []Input{VirtualInput(0)},
	}
	outerFn := NewFunction("outer", 0, 1)
	outerFn.Emit(OpReturn, 0, 0)
	root := &FrameState{
		Function: outerFn, PC: 0,
		Locals: []Input{VirtualInput(0)},
		Inner:  inner,
	}
	mf := &MachineFrame{Slots: []Value{}, Virtuals: virtuals, Root: root}

	mat := NewMaterializer(mf)
	refs, err := mat.Materialize()
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("want 3 heap objects, got %d", len(refs))
	}

	a, b, c := refs[0], refs[1], refs[2]
	// Mutual cycle resolved without infinite recursion.
	next, _ := a.Field("next")
	if !StrictEqual(next, ObjectValue(b)) {
		t.Fatal("A.next must === B")
	}
	back, _ := b.Field("back")
	if !StrictEqual(back, ObjectValue(a)) {
		t.Fatal("B.back must === A")
	}
	// Diamond preserves identity.
	left, _ := c.Field("left")
	right, _ := c.Field("right")
	if !StrictEqual(left, right) || !StrictEqual(left, ObjectValue(a)) {
		t.Fatal("C.left and C.right must both === A")
	}
	peer, _ := a.Field("peer")
	if !StrictEqual(peer, ObjectValue(c)) {
		t.Fatal("A.peer must === C")
	}
	n, _ := a.Field("n")
	if !n.IsInt() || n.AsInt() != 1 {
		t.Fatalf("scalar field n = %s, want 1", n)
	}

	// Idempotent: second call returns the same map and identities.
	refs2, err := mat.Materialize()
	if err != nil || len(refs2) != 3 {
		t.Fatalf("repeat materialize: %v %d", err, len(refs2))
	}
	if refs2[0] != a {
		t.Fatal("materialization must be stable across repeated calls")
	}

	// Frame reconstruction: both frames' slots resolve to the same heap ref.
	res, err := Deopt(NewProgram(), &DeoptMetadata{Machine: mf, Reason: "test"})
	if err != nil {
		t.Fatal(err)
	}
	frames := res.Interpreter.Frames()
	if len(frames) != 2 {
		t.Fatalf("want 2 reconstructed frames, got %d", len(frames))
	}
	outerLocal := frames[0].Locals[0]
	innerLocal := frames[1].Locals[0]
	innerStack := frames[1].Stack[0]
	for _, v := range []Value{outerLocal, innerLocal, innerStack} {
		if !v.IsObject() {
			t.Fatalf("cross-frame reference must materialize to object, got %s", v)
		}
	}
	if !StrictEqual(outerLocal, innerLocal) || !StrictEqual(innerLocal, innerStack) {
		t.Fatal("every cross-frame reference to virtual id 0 must resolve to the identical heap object")
	}
	if frames[1].Locals[1].AsInt() != 7 {
		t.Fatal("non-reference local not reconstructed correctly")
	}
}

func TestMaterializeWithMachineSlots(t *testing.T) {
	slotVal := StringValue("from-slot")
	virtuals := []VirtualObject{{
		ID: 0, Class: "O",
		Fields: []FieldInput{
			{Name: "s", Value: SlotInput(0)},
			{Name: "i", Value: LiteralInput(IntValue(9))},
		},
	}}
	fn := NewFunction("f", 0, 1)
	fn.Emit(OpReturn, 0, 0)
	mf := &MachineFrame{
		Slots:    []Value{slotVal},
		Virtuals: virtuals,
		Root:     &FrameState{Function: fn, PC: 0, Locals: []Input{VirtualInput(0)}},
	}
	refs, err := NewMaterializer(mf).Materialize()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := refs[0].Field("s")
	if !StrictEqual(got, slotVal) {
		t.Fatalf("slot field = %s", got)
	}
}
