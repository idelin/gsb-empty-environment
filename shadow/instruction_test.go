package shadow

import "testing"

func TestExecuteIRIndirectJump(t *testing.T) {
	machine := NewMachine("r0", "r1")
	low, err := machine.InputValue(0, Width8, 0x78)
	if err != nil {
		t.Fatal(err)
	}
	high, err := machine.InputValue(1, Width8, 0x56)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.WriteMemory(0x30, low); err != nil {
		t.Fatal(err)
	}
	if err := machine.WriteMemory(0x31, high); err != nil {
		t.Fatal(err)
	}

	instructions := []Instruction{
		{
			Op: OpLoad,
			Dst: Operand{
				Kind:     OperandRegister,
				Register: "r0",
				Width:    Width16,
			},
			SrcA: Operand{
				Kind:          OperandMemory,
				MemoryAddress: machine.Const(Width32, 0x30),
				Width:         Width16,
			},
		},
		{
			Op: OpXor,
			Dst: Operand{
				Kind:     OperandRegister,
				Register: "r1",
				Width:    Width16,
			},
			SrcA: Operand{
				Kind:     OperandRegister,
				Register: "r1",
				Width:    Width16,
			},
			SrcB: Operand{
				Kind:     OperandRegister,
				Register: "r1",
				Width:    Width16,
			},
		},
		{
			Op: OpAdd,
			Dst: Operand{
				Kind:     OperandRegister,
				Register: "r0",
				Width:    Width16,
			},
			SrcA: Operand{
				Kind:     OperandRegister,
				Register: "r0",
				Width:    Width16,
			},
			SrcB: Operand{
				Kind:      OperandImmediate,
				Immediate: machine.Const(Width16, 4),
				Width:     Width16,
			},
		},
		{
			Op: OpIndirectJump,
			SrcA: Operand{
				Kind:     OperandRegister,
				Register: "r0",
				Width:    Width16,
			},
		},
	}

	var target *JumpTarget
	for _, instruction := range instructions {
		target, err = machine.Execute(instruction)
		if err != nil {
			t.Fatal(err)
		}
	}
	if target == nil {
		t.Fatal("indirect jump did not return a target")
	}
	if target.Address() != 0x567C {
		t.Fatalf("target = %#x, want 0x567c", target.Address())
	}
	for bit := 0; bit < 8; bit++ {
		if !mustJumpSources(t, *target, bit).Contains(Source{Byte: 0, Bit: uint8(bit)}) {
			t.Fatalf("target bit %d missing low-byte source", bit)
		}
	}
	for bit := 8; bit < 16; bit++ {
		if !mustJumpSources(t, *target, bit).Contains(Source{Byte: 1, Bit: uint8(bit - 8)}) {
			t.Fatalf("target bit %d missing high-byte source", bit)
		}
	}
}

func TestExecuteIRDynamicStoreAddressCoverage(t *testing.T) {
	run := func(machine *Machine, selector uint8) (Value, error) {
		value, err := machine.InputValue(0, Width8, 0xB7)
		if err != nil {
			return Value{}, err
		}
		address, err := machine.InputValue(1, Width32, uint64(0x40|uint64(selector&1)))
		if err != nil {
			return Value{}, err
		}
		if _, err := machine.Execute(Instruction{
			Op: OpStore,
			SrcA: Operand{
				Kind:      OperandImmediate,
				Immediate: value,
				Width:     Width8,
			},
			SrcB: Operand{
				Kind:          OperandMemory,
				MemoryAddress: address,
				Width:         Width8,
			},
		}); err != nil {
			return Value{}, err
		}
		return machine.ReadMemory(0x40, Width8)
	}

	machine := NewMachine()
	baseline, err := run(machine, 0)
	if err != nil {
		t.Fatal(err)
	}
	mutatedMachine := NewMachine()
	mutated, err := run(mutatedMachine, 1)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Uint64() != 0xB7 || mutated.Uint64() != 0 {
		t.Fatalf("baseline=%#x mutated=%#x", baseline.Uint64(), mutated.Uint64())
	}
	if !mustSources(t, baseline, 0).Contains(Source{Byte: 1, Bit: 0}) {
		t.Fatalf("store address source missing: %v", mustSources(t, baseline, 0))
	}
}
