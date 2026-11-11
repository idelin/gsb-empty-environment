package shadow

import "testing"

func TestTwoByteBitFlipCoverage(t *testing.T) {
	t.Run("16-bit carry propagation", func(t *testing.T) {
		run := func(machine *Machine, low, high uint8) (Value, error) {
			lowValue, err := machine.InputValue(0, Width8, uint64(low))
			if err != nil {
				return Value{}, err
			}
			highValue, err := machine.InputValue(1, Width8, uint64(high))
			if err != nil {
				return Value{}, err
			}
			if err := machine.WriteMemory(0, lowValue); err != nil {
				return Value{}, err
			}
			if err := machine.WriteMemory(1, highValue); err != nil {
				return Value{}, err
			}
			value, err := machine.ReadMemory(0, Width16)
			if err != nil {
				return Value{}, err
			}
			result, _, err := machine.Add(value, machine.Const(Width16, 1))
			return result, err
		}

		for low := 0; low < 256; low++ {
			machine := NewMachine()
			baseline, err := run(machine, uint8(low), 0x42)
			if err != nil {
				t.Fatal(err)
			}
			mutatedMachine := NewMachine()
			mutated, err := run(mutatedMachine, uint8(low^1), 0x42)
			if err != nil {
				t.Fatal(err)
			}
			expected := uint64(low) | uint64(0x42)<<8
			expected = uint64(uint16(expected + 1))
			mutatedExpected := uint64(low^1) | uint64(0x42)<<8
			mutatedExpected = uint64(uint16(mutatedExpected + 1))
			assertConcrete(t, baseline, expected)
			assertConcrete(t, mutated, mutatedExpected)
			assertBitFlipCoverage(t, 0, 0, baseline, mutated)
		}

		for high := 0; high < 256; high++ {
			machine := NewMachine()
			baseline, err := run(machine, 0xFF, uint8(high))
			if err != nil {
				t.Fatal(err)
			}
			mutatedMachine := NewMachine()
			mutated, err := run(mutatedMachine, 0xFF, uint8(high^1))
			if err != nil {
				t.Fatal(err)
			}
			expected := uint64(uint16(0xFF + 1 + uint64(high)<<8))
			mutatedExpected := uint64(uint16(0xFF + 1 + uint64(high^1)<<8))
			assertConcrete(t, baseline, expected)
			assertConcrete(t, mutated, mutatedExpected)
			assertBitFlipCoverage(t, 1, 0, baseline, mutated)
		}
	})

	t.Run("variable shift", func(t *testing.T) {
		run := func(machine *Machine, valueInput, countInput uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(valueInput))
			if err != nil {
				return Value{}, err
			}
			count, err := machine.InputValue(1, Width8, uint64(countInput&7))
			if err != nil {
				return Value{}, err
			}
			return machine.ShiftLeft(value, count), nil
		}

		for input := 0; input < 256; input++ {
			machine := NewMachine()
			baseline, err := run(machine, uint8(input), 2)
			if err != nil {
				t.Fatal(err)
			}
			mutatedMachine := NewMachine()
			mutated, err := run(mutatedMachine, uint8(input^1), 2)
			if err != nil {
				t.Fatal(err)
			}
			assertConcrete(t, baseline, (uint64(input)<<2)&0xFF)
			assertConcrete(t, mutated, (uint64(input^1)<<2)&0xFF)
			assertBitFlipCoverage(t, 0, 0, baseline, mutated)
		}

		for input := 0; input < 256; input++ {
			machine := NewMachine()
			baseline, err := run(machine, 0x0F, uint8(input))
			if err != nil {
				t.Fatal(err)
			}
			for countBit := 0; countBit < 3; countBit++ {
				mutatedMachine := NewMachine()
				mutated, err := run(mutatedMachine, 0x0F, uint8(input^(1<<countBit)))
				if err != nil {
					t.Fatal(err)
				}
				assertConcrete(t, baseline, (uint64(0x0F)<<(uint64(input)&7))&0xFF)
				assertConcrete(t, mutated, (uint64(0x0F)<<(uint64(input^(1<<countBit))&7))&0xFF)
				assertBitFlipCoverage(t, 1, countBit, baseline, mutated)
			}
		}
	})
}

func TestDynamicMemoryAddressBitFlipCoverage(t *testing.T) {
	run := func(machine *Machine, addressInput uint8) (Value, error) {
		valueAt10, err := machine.InputValue(0, Width8, 0xAA)
		if err != nil {
			return Value{}, err
		}
		valueAt11, err := machine.InputValue(1, Width8, 0x55)
		if err != nil {
			return Value{}, err
		}
		if err := machine.WriteMemory(0x10, valueAt10); err != nil {
			return Value{}, err
		}
		if err := machine.WriteMemory(0x11, valueAt11); err != nil {
			return Value{}, err
		}

		address, err := machine.InputValue(2, Width8, uint64(0x10|uint64(addressInput&1)))
		if err != nil {
			return Value{}, err
		}
		return machine.ReadMemoryAt(address, Width8)
	}

	for input := 0; input < 256; input++ {
		machine := NewMachine()
		baseline, err := run(machine, uint8(input))
		if err != nil {
			t.Fatal(err)
		}
		mutatedMachine := NewMachine()
		mutated, err := run(mutatedMachine, uint8(input^1))
		if err != nil {
			t.Fatal(err)
		}

		expected := uint64(0xAA)
		if input&1 != 0 {
			expected = 0x55
		}
		mutatedExpected := uint64(0xAA)
		if (input^1)&1 != 0 {
			mutatedExpected = 0x55
		}
		assertConcrete(t, baseline, expected)
		assertConcrete(t, mutated, mutatedExpected)
		assertBitFlipCoverage(t, 2, 0, baseline, mutated)
	}
}

func TestIndirectJumpBitSources(t *testing.T) {
	machine := NewMachine("r0")
	low, err := machine.InputValue(0, Width8, 0x34)
	if err != nil {
		t.Fatal(err)
	}
	high, err := machine.InputValue(1, Width8, 0x12)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.WriteMemory(0x20, low); err != nil {
		t.Fatal(err)
	}
	if err := machine.WriteMemory(0x21, high); err != nil {
		t.Fatal(err)
	}
	target, err := machine.ReadMemory(0x20, Width16)
	if err != nil {
		t.Fatal(err)
	}
	jump := machine.IndirectJump(target)
	if jump.Address() != 0x1234 {
		t.Fatalf("address = %#x, want 0x1234", jump.Address())
	}

	for bit := 0; bit < 8; bit++ {
		if !mustJumpSources(t, jump, bit).Contains(Source{Byte: 0, Bit: uint8(bit)}) {
			t.Fatalf("target bit %d missing low input source: %v", bit, mustJumpSources(t, jump, bit))
		}
	}
	for bit := 8; bit < 16; bit++ {
		if !mustJumpSources(t, jump, bit).Contains(Source{Byte: 1, Bit: uint8(bit - 8)}) {
			t.Fatalf("target bit %d missing high input source: %v", bit, mustJumpSources(t, jump, bit))
		}
	}

	mutatedMachine := NewMachine("r0")
	mutatedLow, err := mutatedMachine.InputValue(0, Width8, 0x35)
	if err != nil {
		t.Fatal(err)
	}
	mutatedHigh, err := mutatedMachine.InputValue(1, Width8, 0x12)
	if err != nil {
		t.Fatal(err)
	}
	if err := mutatedMachine.WriteMemory(0x20, mutatedLow); err != nil {
		t.Fatal(err)
	}
	if err := mutatedMachine.WriteMemory(0x21, mutatedHigh); err != nil {
		t.Fatal(err)
	}
	mutatedTarget, err := mutatedMachine.ReadMemory(0x20, Width16)
	if err != nil {
		t.Fatal(err)
	}
	mutatedJump := mutatedMachine.IndirectJump(mutatedTarget)
	for bit := 0; bit < 16; bit++ {
		if jump.BitSources()[bit].Bit != mutatedJump.BitSources()[bit].Bit &&
			!mustJumpSources(t, jump, bit).Contains(Source{Byte: 0, Bit: 0}) {
			t.Fatalf("flipped target bit %d not covered by source set", bit)
		}
	}
}
