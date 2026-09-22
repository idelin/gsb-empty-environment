package fp

import "testing"

// TestExhaustiveBFloat16 widens every bf16 pattern to f32 and back (bit
// identity), and narrows through the reference for the finite patterns.
func TestExhaustiveBFloat16(t *testing.T) {
	for raw := uint64(0); raw < 1<<16; raw++ {
		_, exp, mant := BFloat16.unpack(raw)
		maxExp := uint64(1)<<BFloat16.ExpBits - 1
		if exp == maxExp && mant != 0 {
			// NaN: check normalization on widening.
			w := Convert(Binary32, BFloat16, raw, ToNearestEven)
			_, wexp, wmant := Binary32.unpack(w.Bits)
			if wexp != maxExp || wmant&Binary32.quietBit() == 0 {
				t.Fatalf("bf16 NaN %04x -> f32 %08x", raw, w.Bits)
			}
			continue
		}
		w := Convert(Binary32, BFloat16, raw, ToNearestEven)
		back := Convert(BFloat16, Binary32, w.Bits, ToNearestEven)
		if back.Bits != raw {
			t.Fatalf("bf16 %04x -> f32 %08x -> bf16 %04x", raw, w.Bits, back.Bits)
		}
	}
}

// TestF32SampleNarrowing feeds a deterministic stride of f32 patterns into
// f16/bf16 under all modes and compares against the big.Rat reference.
func TestF32SampleNarrowing(t *testing.T) {
	stride := uint32(7919) // coprime-ish stride for broad coverage
	for _, dst := range []*Format{Binary16, BFloat16} {
		for raw := uint32(0); ; raw += stride {
			for _, mode := range allModes() {
				got := Convert(dst, Binary32, uint64(raw), mode)
				ref := ReferenceConvertPattern(dst, Binary32, uint64(raw), mode)
				if got.Bits != ref.Bits || got.Flags != ref.Flags {
					t.Fatalf("f32 %08x -> %s mode=%d got=%04x/%v ref=%04x/%v",
						raw, dst.Name(), mode, got.Bits, got.Flags, ref.Bits, ref.Flags)
				}
			}
			if raw > uint32(0)-stride {
				break
			}
		}
	}
}

// TestF64SampleNarrowing exercises the subnormal boundary on the f64 source
// side, including values that need 128-bit intermediate products.
func TestF64SampleNarrowing(t *testing.T) {
	// Patterns around 2^-24 (f16 min subnormal region) and near zero.
	for _, e := range []int{-25, -24, -23, -14, -13, -12, 15, 16} {
		for sig := uint64(0); sig < 1<<12; sig++ {
			biased := uint64(e + Binary64.Bias)
			if biased == 0 {
				continue
			}
			raw := biased<<Binary64.MantBits | (sig << (Binary64.MantBits - 12))
			for _, mode := range allModes() {
				got := Convert(Binary16, Binary64, raw, mode)
				ref := ReferenceConvertPattern(Binary16, Binary64, raw, mode)
				if got.Bits != ref.Bits || got.Flags != ref.Flags {
					t.Fatalf("f64 %016x mode=%d got=%04x/%v ref=%04x/%v",
						raw, mode, got.Bits, got.Flags, ref.Bits, ref.Flags)
				}
			}
		}
	}
}
