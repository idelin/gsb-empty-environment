package fp

import (
	"testing"
)

func allModes() []Round {
	return []Round{ToNearestEven, ToZero, ToPositiveInf, ToNegativeInf}
}

// TestExhaustiveBinary16 walks every 16-bit pattern as both a source (to wider
// formats) and, for finite values, checks widen-then-narrow bit identity.
func TestExhaustiveBinary16(t *testing.T) {
	for raw := uint64(0); raw < 1<<16; raw++ {
		_, exp, mant := Binary16.unpack(raw)
		maxExp := uint64(1)<<Binary16.ExpBits - 1
		isNaN := exp == maxExp && mant != 0
		for _, mode := range allModes() {
			// NaNs are normalized on conversion, so bit-identity and
			// production/reference equality apply to all non-NaN patterns.
			if isNaN {
				continue
			}
			// Binary16 -> Binary32 -> Binary16 must be bit identical.
			w := Convert(Binary32, Binary16, raw, mode)
			back := Convert(Binary16, Binary32, w.Bits, mode)
			if back.Bits != raw {
				t.Fatalf("f16 %04x -> f32 %08x -> f16 %04x mode=%d", raw, w.Bits, back.Bits, mode)
			}
			if back.Flags != 0 {
				t.Fatalf("f16 %04x round trip raised flags %v", raw, back.Flags)
			}

			// Binary16 -> Binary64 -> Binary16 likewise.
			w64 := Convert(Binary64, Binary16, raw, mode)
			back64 := Convert(Binary16, Binary64, w64.Bits, mode)
			if back64.Bits != raw {
				t.Fatalf("f16 %04x -> f64 %016x -> f16 %04x mode=%d", raw, w64.Bits, back64.Bits, mode)
			}

			ref := ReferenceConvertPattern(Binary16, Binary32, w.Bits, mode)
			if ref.Bits != raw || ref.Flags != 0 {
				t.Fatalf("ref disagreement f32 %08x mode=%d got=%04x/%v ref=%04x/%v",
					w.Bits, mode, raw, 0, ref.Bits, ref.Flags)
			}
		}

		if isNaN {
			quiet := mant&Binary16.quietBit() != 0
			// NaN payload (lower 9 bits) survives to f32/f64 with quiet set.
			payload := mant & (Binary16.quietBit() - 1)
			w := Convert(Binary32, Binary16, raw, ToNearestEven)
			wexp, wmant := func() (uint64, uint64) { _, e, m := Binary32.unpack(w.Bits); return e, m }()
			if wexp != uint64(1)<<Binary32.ExpBits-1 {
				t.Fatalf("f16 NaN %04x widened to non-NaN f32 %08x", raw, w.Bits)
			}
			if wmant&Binary32.quietBit() == 0 {
				t.Fatalf("f16 NaN %04x widened to signaling f32 %08x", raw, w.Bits)
			}
			if wmant&(Binary32.quietBit()-1) != payload {
				t.Fatalf("f16 NaN %04x payload lost: f32=%08x want low=%x", raw, w.Bits, payload)
			}
			wantFlags := Flags(0)
			if !quiet {
				wantFlags = FlagInvalid
			}
			if w.Flags != wantFlags {
				t.Fatalf("f16 NaN %04x flags=%v want %v", raw, w.Flags, wantFlags)
			}
			// Narrowing a wide qNaN truncates payload to f16 width.
			widePayload := payload | uint64(0x123)<<9
			wideNaN := func() uint64 {
				m := widePayload | Binary32.quietBit()
				return Binary32.pack(false, uint64(1)<<Binary32.ExpBits-1, m)
			}()
			n := Convert(Binary16, Binary32, wideNaN, ToNearestEven)
			if n.Bits != Binary16.qnanBits(false, payload) {
				t.Fatalf("NaN truncation f32 %08x -> f16 %04x want %04x", wideNaN, n.Bits, Binary16.qnanBits(false, payload))
			}
		}
	}
}
