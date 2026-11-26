package seccomp

import (
	"bytes"
	"strings"
	"testing"
)

func TestDecodeGolden8ByteRecords(t *testing.T) {
	// Real little-endian struct sock_filter bytes:
	//   ld  [nr]            : 20 00 00 00 00 00 00 00
	//   jeq 59, +0, +1      : 15 00 00 01 3b 00 00 00
	raw := []byte{
		0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x15, 0x00, 0x00, 0x01, 0x3b, 0x00, 0x00, 0x00,
	}
	insns, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(insns) != 2 {
		t.Fatalf("got %d insns", len(insns))
	}
	want0 := Instruction{Code: 0x0020, JT: 0, JF: 0, K: 0}
	want1 := Instruction{Code: 0x0015, JT: 0, JF: 1, K: 59}
	if insns[0] != want0 || insns[1] != want1 {
		t.Fatalf("decode mismatch: %+v %+v", insns[0], insns[1])
	}
	if !bytes.Equal(EncodeAll(insns), raw) {
		t.Fatal("re-encoding did not reproduce raw bytes")
	}
}

func TestDecodeErrors(t *testing.T) {
	if _, err := Decode(nil); err == nil {
		t.Fatal("empty decode should fail")
	}
	if _, err := Decode(make([]byte, 7)); err == nil {
		t.Fatal("non-multiple-of-8 decode should fail")
	}
	if _, err := DecodeOne(make([]byte, 4)); err == nil {
		t.Fatal("short record should fail")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []Instruction{
		{Code: 0x00, K: 0x12345678},       // ld imm
		{Code: 0x20, K: 4},                // ld abs
		{Code: 0xb1, K: 0},                // ldx b msh
		{Code: 0x02, K: 15},               // st
		{Code: 0x03, K: 0},                // stx
		{Code: 0x54, K: 0x40000000},       // and k
		{Code: 0x1c},                      // sub x
		{Code: 0x9c},                      // mod x
		{Code: 0x15, JT: 2, JF: 3, K: 59}, // jeq k
		{Code: 0x4d, JT: 1, JF: 0},        // jset x
		{Code: 0x05, K: 7},                // ja
		{Code: 0x06, K: RetAllow},         // ret allow
		{Code: 0x16},                      // ret a
		{Code: 0x07},                      // tax
		{Code: 0x87},                      // txa
	}
	raw := EncodeAll(cases)
	got, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(cases) {
		t.Fatalf("len %d != %d", len(got), len(cases))
	}
	for i := range cases {
		if got[i] != cases[i] {
			t.Fatalf("insn %d roundtrip %+v != %+v", i, got[i], cases[i])
		}
	}
}

func TestEveryKnownOpcodeDisassembles(t *testing.T) {
	for code := range knownOpcodes {
		insn := Instruction{Code: code, JT: 1, JF: 2, K: 0x2a}
		s := insn.String()
		if s == "" || strings.Contains(s, "???") {
			t.Fatalf("opcode %#06x did not disassemble: %q", code, s)
		}
	}
}
