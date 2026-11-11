package shadow

import "testing"

func TestSingleByteBitFlipExhaustion(t *testing.T) {
	t.Run("and", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			result, err := machine.And(value, machine.Const(Width8, 0x3C))
			if err != nil {
				return Value{}, err
			}
			assertConcrete(t, result, uint64(input&0x3C))
			return result, nil
		})
	})

	t.Run("or", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			result, err := machine.Or(value, machine.Const(Width8, 0x81))
			if err != nil {
				return Value{}, err
			}
			assertConcrete(t, result, uint64(input|0x81))
			return result, nil
		})
	})

	t.Run("not", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			result := machine.Not(value)
			assertConcreteAndSources(t, result, uint64(^input), func(bit int) SourceSet {
				return Sources(Source{Byte: 0, Bit: uint8(bit)})
			})
			return result, nil
		})
	})

	t.Run("add", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			result, _, err := machine.Add(value, machine.Const(Width8, 1))
			if err != nil {
				return Value{}, err
			}
			assertConcrete(t, result, uint64(uint8(input+1)))
			return result, nil
		})
	})

	t.Run("sub", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			result, _, err := machine.Sub(value, machine.Const(Width8, 15))
			if err != nil {
				return Value{}, err
			}
			assertConcrete(t, result, uint64(uint8(input-15)))
			return result, nil
		})
	})

	t.Run("adc carry input", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			carry, err := machine.InputValue(0, Width1, uint64(input&1))
			if err != nil {
				return Value{}, err
			}
			if err := machine.SetCarry(carry); err != nil {
				return Value{}, err
			}
			result, _, err := machine.Adc(value, machine.Const(Width8, 0x7E))
			if err != nil {
				return Value{}, err
			}
			assertConcrete(t, result, uint64(uint8(int(input)+0x7E+int(input&1))))
			return result, nil
		})
	})
}

func TestShiftAndPartialWriteBitFlipExhaustion(t *testing.T) {
	t.Run("shl immediate", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			result := machine.ShiftLeftImmediate(value, 2)
			assertConcreteAndSources(t, result, (uint64(input)<<2)&0xFF, func(bit int) SourceSet {
				if bit >= 2 {
					return Sources(Source{Byte: 0, Bit: uint8(bit - 2)})
				}
				return Sources()
			})
			return result, nil
		})
	})

	t.Run("shr immediate", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			result := machine.ShiftRightImmediate(value, 2)
			assertConcreteAndSources(t, result, uint64(input)>>2, func(bit int) SourceSet {
				if bit >= 6 {
					return Sources()
				}
				return Sources(Source{Byte: 0, Bit: uint8(bit + 2)})
			})
			return result, nil
		})
	})

	t.Run("sar immediate", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			result := machine.ShiftArithmeticRightImmediate(value, 2)
			assertConcreteAndSources(t, result, uint64(uint8(int8(input)>>2)), func(bit int) SourceSet {
				if bit < 6 {
					return Sources(Source{Byte: 0, Bit: uint8(bit + 2)})
				}
				return Sources(Source{Byte: 0, Bit: 7})
			})
			return result, nil
		})
	})

	t.Run("partial 8-bit register write", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			if err := machine.WriteRegister("r0", machine.Const(Width32, 0x11223344)); err != nil {
				return Value{}, err
			}
			if err := machine.WriteRegister("r0", value); err != nil {
				return Value{}, err
			}
			result, err := machine.ReadRegister("r0", Width32)
			if err != nil {
				return Value{}, err
			}
			assertConcreteAndSources(t, result, 0x11223300|uint64(input), func(bit int) SourceSet {
				if bit < 8 {
					return Sources(Source{Byte: 0, Bit: uint8(bit)})
				}
				return Sources()
			})
			return result, nil
		})
	})

	t.Run("partial 16-bit register write", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width16, uint64(input))
			if err != nil {
				return Value{}, err
			}
			if err := machine.WriteRegister("r0", machine.Const(Width32, 0x11223344)); err != nil {
				return Value{}, err
			}
			if err := machine.WriteRegister("r0", value); err != nil {
				return Value{}, err
			}
			result, err := machine.ReadRegister("r0", Width32)
			if err != nil {
				return Value{}, err
			}
			assertConcreteAndSources(t, result, 0x11220000|uint64(input), func(bit int) SourceSet {
				if bit < 16 {
					return Sources(Source{Byte: 0, Bit: uint8(bit)})
				}
				return Sources()
			})
			return result, nil
		})
	})

	t.Run("memory byte round trip", func(t *testing.T) {
		assertNoMissedSingleByteDependencies(t, func(machine *Machine, input uint8) (Value, error) {
			value, err := machine.InputValue(0, Width8, uint64(input))
			if err != nil {
				return Value{}, err
			}
			if err := machine.WriteMemory(0x10, value); err != nil {
				return Value{}, err
			}
			result, err := machine.ReadMemory(0x10, Width8)
			if err != nil {
				return Value{}, err
			}
			assertConcreteAndSources(t, result, uint64(input), func(bit int) SourceSet {
				return Sources(Source{Byte: 0, Bit: uint8(bit)})
			})
			return result, nil
		})
	})
}

func assertConcrete(t *testing.T, value Value, expected uint64) {
	t.Helper()
	if value.Uint64() != expected {
		t.Fatalf("concrete value %#x, want %#x", value.Uint64(), expected)
	}
}

func assertConcreteAndSources(t *testing.T, value Value, expected uint64, expectedSources func(int) SourceSet) {
	t.Helper()
	assertConcrete(t, value, expected)
	for bit := 0; bit < int(value.Width()); bit++ {
		sources := mustSources(t, value, bit)
		if !sameSourceSet(sources, expectedSources(bit)) {
			t.Fatalf("bit %d sources %v, want %v", bit, sources, expectedSources(bit))
		}
	}
}
