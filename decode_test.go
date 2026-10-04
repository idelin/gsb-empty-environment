package imgcodec

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
)

// u16le encodes a length field.
func u16le(n int) []byte { return []byte{byte(n), byte(n >> 8)} }

func lit(payload ...byte) []byte {
	out := []byte{byte(OpLiteral)}
	out = append(out, u16le(len(payload))...)
	return append(out, payload...)
}

func rep(n int, v byte) []byte {
	return append(append([]byte{byte(OpRepeat)}, u16le(n)...), v)
}

func ref(n int) []byte {
	return append([]byte{byte(OpRef)}, u16le(n)...)
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestDecodeValid(t *testing.T) {
	p := DefaultParams(3, 2)
	data := cat(lit(1, 2, 3), ref(2), rep(1, 9))
	got, err := Decode(data, p)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 2, 3, 1, 2, 9}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pixel %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	p := DefaultParams(3, 2) // 6 pixels, MaxRefLen 64
	p.MaxRefLen = 2
	tests := []struct {
		name       string
		data       []byte
		wantInstr  int
		wantOffset int
		wantReason string
	}{
		{"empty stream", nil, 0, 0, "incomplete"},
		{"unknown opcode", []byte{0x7f, 1, 0}, 0, 0, "unknown opcode"},
		{"zero length literal", []byte{0, 0, 0}, 0, 0, "zero-length"},
		{"zero length ref", cat(lit(1, 2, 3), ref(0)), 1, 6, "zero-length"},
		{"truncated header", []byte{2, 1}, 0, 0, "truncated instruction header"},
		{"truncated literal payload", []byte{0, 4, 0, 1, 2}, 0, 0, "truncated literal"},
		{"truncated repeat", []byte{1, 2, 0}, 0, 0, "truncated repeat"},
		{"ref in first row", ref(1), 0, 0, "first row"},
		{"ref too long", cat(lit(1, 2, 3), ref(5)), 1, 6, "exceeds MaxRefLen"},
		{"ref crosses row", cat(lit(1, 2, 3, 4, 5), ref(2)), 1, 8, "crosses row"},
		{"literal overruns image", lit(1, 2, 3, 4, 5, 6, 7), 0, 0, "overruns"},
		{"trailing instruction overruns", cat(lit(1, 2, 3, 4, 5, 6), rep(1, 42)), 1, 9, "overruns"},
		{"incomplete image", lit(1, 2, 3), 1, 6, "incomplete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pix, err := Decode(tt.data, p)
			if err == nil {
				t.Fatalf("expected error, got pixels %v", pix)
			}
			if pix != nil {
				t.Fatalf("corrupt input must not yield pixels, got %v", pix)
			}
			var de *DecodeError
			if !errors.As(err, &de) {
				t.Fatalf("error is %T, want *DecodeError", err)
			}
			if de.Instr != tt.wantInstr || de.Offset != tt.wantOffset {
				t.Fatalf("located instruction %d @%d, want %d @%d", de.Instr, de.Offset, tt.wantInstr, tt.wantOffset)
			}
			if !strings.Contains(de.Reason, tt.wantReason) {
				t.Fatalf("reason %q missing %q", de.Reason, tt.wantReason)
			}
		})
	}
}

func TestDecodeErrorMessage(t *testing.T) {
	p := DefaultParams(3, 2)
	_, err := Decode(cat(lit(1, 2, 3), ref(1)), p) // valid ref, then EOF: 4 of 6 pixels
	var de *DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("error is %T", err)
	}
	if !strings.Contains(err.Error(), "instruction 2") {
		t.Fatalf("message %q does not name the instruction", err)
	}
}

func TestDecodeRandomInputNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for trial := 0; trial < 5000; trial++ {
		p := DefaultParams(1+rng.Intn(5), 1+rng.Intn(4))
		p.MaxRefLen = 1 + rng.Intn(8)
		data := make([]byte, rng.Intn(40))
		rng.Read(data)
		_, _ = Decode(data, p) // must not panic
	}
}
