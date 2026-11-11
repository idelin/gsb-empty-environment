package shadow

import "testing"

func TestXorSameLineageClearsSources(t *testing.T) {
	machine := NewMachine("r0")
	input, err := machine.InputValue(0, Width8, 0xA5)
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.WriteRegister("r0", input); err != nil {
		t.Fatal(err)
	}

	register, err := machine.ReadRegister("r0", Width8)
	if err != nil {
		t.Fatal(err)
	}
	result, err := machine.Xor(register, register)
	if err != nil {
		t.Fatal(err)
	}
	if result.Uint64() != 0 {
		t.Fatalf("xor self = %d, want 0", result.Uint64())
	}
	for index := 0; index < int(result.Width()); index++ {
		if len(mustSources(t, result, index)) != 0 {
			t.Fatalf("result bit %d retained sources after self xor: %v", index, mustSources(t, result, index))
		}
	}
}

func changedBits(before, after uint64, width int) map[int]bool {
	diff := before ^ after
	result := make(map[int]bool, width)
	for bit := 0; bit < width; bit++ {
		result[bit] = diff&(uint64(1)<<bit) != 0
	}
	return result
}

func valuesDiffer(t *testing.T, before, after Value, bit int) bool {
	t.Helper()
	return mustBit(t, before, bit) != mustBit(t, after, bit)
}

func sameSourceSet(a, b SourceSet) bool {
	if len(a) != len(b) {
		return false
	}
	for source := range a {
		if !b.Contains(source) {
			return false
		}
	}
	return true
}

func mustBit(t *testing.T, value Value, index int) uint8 {
	t.Helper()
	bit, err := value.Bit(index)
	if err != nil {
		t.Fatal(err)
	}
	return bit
}

func mustSources(t *testing.T, value Value, index int) SourceSet {
	t.Helper()
	sources, err := value.Sources(index)
	if err != nil {
		t.Fatal(err)
	}
	return sources
}

func mustJumpSources(t *testing.T, target JumpTarget, index int) SourceSet {
	t.Helper()
	sources, err := target.Sources(index)
	if err != nil {
		t.Fatal(err)
	}
	return sources
}

func assertBitFlipCoverage(t *testing.T, byteIndex, bit int, baseline, mutated Value) {
	t.Helper()
	source := Source{Byte: byteIndex, Bit: uint8(bit)}
	for resultBit := 0; resultBit < int(baseline.Width()); resultBit++ {
		if valuesDiffer(t, baseline, mutated, resultBit) && !mustSources(t, baseline, resultBit).Contains(source) {
			t.Fatalf("byte %d bit %d changed result bit %d but source set was %v",
				byteIndex, bit, resultBit, mustSources(t, baseline, resultBit))
		}
	}
}

func assertNoMissedSingleByteDependencies(t *testing.T, run func(*Machine, uint8) (Value, error)) {
	t.Helper()
	for input := 0; input < 256; input++ {
		machine := NewMachine("r0", "r1")
		baseline, err := run(machine, uint8(input))
		if err != nil {
			t.Fatalf("input %02x: %v", input, err)
		}
		for bit := 0; bit < 8; bit++ {
			mutatedMachine := NewMachine("r0", "r1")
			mutated, err := run(mutatedMachine, uint8(input^(1<<bit)))
			if err != nil {
				t.Fatalf("mutated input %02x: %v", input^(1<<bit), err)
			}
			assertBitFlipCoverage(t, 0, bit, baseline, mutated)
		}
	}
}
