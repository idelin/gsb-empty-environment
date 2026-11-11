package shadow

import "testing"

func TestSbbBorrowPropagation(t *testing.T) {
	machine := NewMachine()
	left, err := machine.InputValue(0, Width8, 0x10)
	if err != nil {
		t.Fatal(err)
	}
	carry, err := machine.InputValue(1, Width1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.SetCarry(carry); err != nil {
		t.Fatal(err)
	}

	result, borrow, err := machine.Sbb(left, machine.Const(Width8, 0x10))
	if err != nil {
		t.Fatal(err)
	}
	if result.Uint64() != 0xFF {
		t.Fatalf("result = %#x, want 0xff", result.Uint64())
	}
	if mustBit(t, borrow, 0) != 1 {
		t.Fatalf("borrow = %d, want 1", mustBit(t, borrow, 0))
	}
	if !mustSources(t, borrow, 0).Contains(Source{Byte: 1, Bit: 0}) {
		t.Fatalf("borrow sources %v, want carry input source", mustSources(t, borrow, 0))
	}

	next, nextBorrow, err := machine.Sbb(machine.Const(Width8, 1), machine.Const(Width8, 0))
	if err != nil {
		t.Fatal(err)
	}
	if next.Uint64() != 0 {
		t.Fatalf("next result = %#x, want 0", next.Uint64())
	}
	if mustBit(t, nextBorrow, 0) != 0 {
		t.Fatalf("next borrow = %d, want 0", mustBit(t, nextBorrow, 0))
	}
	if !mustSources(t, next, 0).Contains(Source{Byte: 1, Bit: 0}) {
		t.Fatalf("borrow did not influence next result: %v", mustSources(t, next, 0))
	}
}

func Test32BitIndirectJumpSources(t *testing.T) {
	machine := NewMachine("r0")
	input, err := machine.InputValue(0, Width32, 0xDEADBEEF)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.WriteRegister("r0", input); err != nil {
		t.Fatal(err)
	}
	targetValue, err := machine.ReadRegister("r0", Width32)
	if err != nil {
		t.Fatal(err)
	}
	jump := machine.IndirectJump(targetValue)

	if jump.Address() != 0xDEADBEEF {
		t.Fatalf("address = %#x, want 0xdeadbeef", jump.Address())
	}
	if jump.Width() != Width32 || len(jump.BitSources()) != 32 {
		t.Fatalf("width=%d sources=%d, want 32/32", jump.Width(), len(jump.BitSources()))
	}
	for bit := 0; bit < 32; bit++ {
		if !mustJumpSources(t, jump, bit).Contains(Source{Byte: 0, Bit: uint8(bit)}) {
			t.Fatalf("target bit %d missing source: %v", bit, mustJumpSources(t, jump, bit))
		}
	}
}

func TestWidthAndRegisterErrors(t *testing.T) {
	machine := NewMachine("r0")
	defer func() {
		if recover() == nil {
			t.Fatal("unsupported constant width did not panic")
		}
	}()
	machine.Const(Width(7), 0)
}

func TestInvalidValueAndMemoryWidths(t *testing.T) {
	machine := NewMachine()
	if _, err := machine.InputValue(0, Width(7), 0); err == nil {
		t.Fatal("invalid input width did not fail")
	}
	if _, err := machine.ReadMemory(0, Width1); err == nil {
		t.Fatal("1-bit memory read did not fail")
	}
	if err := machine.WriteMemory(0, machine.Const(Width1, 0)); err == nil {
		t.Fatal("1-bit memory write did not fail")
	}
	if _, err := machine.ReadRegister("missing", Width8); err == nil {
		t.Fatal("unknown register read did not fail")
	}
	if err := machine.WriteRegister("missing", machine.Const(Width8, 0)); err == nil {
		t.Fatal("unknown register write did not fail")
	}
	if err := machine.SetCarry(machine.Const(Width8, 0)); err == nil {
		t.Fatal("8-bit carry did not fail")
	}
	if _, err := machine.Execute(Instruction{Op: Op(999)}); err == nil {
		t.Fatal("unknown IR operation did not fail")
	}

	input, err := machine.InputValue(0, Width8, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.Bit(8); err == nil {
		t.Fatal("value bit out of range did not fail")
	}
	if _, err := input.Sources(8); err == nil {
		t.Fatal("value sources out of range did not fail")
	}
	jump := machine.IndirectJump(input)
	if _, err := jump.Sources(8); err == nil {
		t.Fatal("jump sources out of range did not fail")
	}

	var invalid Value
	if _, _, err := machine.Add(input, invalid); err == nil {
		t.Fatal("invalid add operand did not fail")
	}
	if err := machine.WriteRegister("r0", invalid); err == nil {
		t.Fatal("invalid register write did not fail")
	}
	if err := machine.WriteMemory(0, invalid); err == nil {
		t.Fatal("invalid memory write did not fail")
	}
}
