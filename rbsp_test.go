package h264bs

import (
	"bytes"
	"testing"
)

func TestEscapeUnescapeRoundTrip(t *testing.T) {
	cases := [][]byte{
		{0x00, 0x00, 0x00},
		{0x00, 0x00, 0x01},
		{0x00, 0x00, 0x02},
		{0x00, 0x00, 0x03},
		{0x00, 0x00, 0x00, 0x00, 0x01},
		{0x01, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00},
		{0xAB, 0x00, 0x00, 0x01, 0xCD, 0x00, 0x00, 0x00, 0xEF},
		{},
		{0x00},
		{0x00, 0x00},
	}
	for i, in := range cases {
		escaped := EscapeRBSP(in)
		got, err := UnescapeRBSP(escaped)
		if err != nil {
			t.Fatalf("case %d: unescape error: %v", i, err)
		}
		if !bytes.Equal(got, in) {
			t.Fatalf("case %d: round trip mismatch\nwant %x\n got %x", i, in, got)
		}
	}
}

func TestEscapeRBSPInsertsPreventionBytes(t *testing.T) {
	// Every 00 00 followed by 00/01/02/03 must gain a 0x03.
	in := []byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x02, 0x00, 0x00, 0x03}
	want := []byte{
		0x00, 0x00, 0x03, 0x00,
		0x00, 0x03, 0x01,
		0x00, 0x00, 0x03, 0x02,
		0x00, 0x00, 0x03, 0x03,
	}
	if got := EscapeRBSP(in); !bytes.Equal(got, want) {
		t.Fatalf("escape mismatch\nwant %x\n got %x", want, got)
	}
}

func TestUnescapeRBSPErrors(t *testing.T) {
	// Prevention byte with forbidden follower (>0x03) is invalid.
	bad := []byte{0x00, 0x00, 0x03, 0x04}
	if _, err := UnescapeRBSP(bad); err == nil {
		t.Fatal("expected error for invalid prevention sequence")
	}
	// A trailing prevention byte is legal and unescapes to 00 00.
	trailing := []byte{0x00, 0x00, 0x03}
	got, err := UnescapeRBSP(trailing)
	if err != nil {
		t.Fatalf("trailing prevention byte should be legal: %v", err)
	}
	if !bytes.Equal(got, []byte{0x00, 0x00}) {
		t.Fatalf("trailing prevention unescape = %x, want 0000", got)
	}
}

func TestExpGolombCodes(t *testing.T) {
	// ue(v) code table from H.264 Table 9-6.
	wantUE := map[uint]string{
		0: "1",
		1: "010",
		2: "011",
		3: "00100",
		4: "00101",
		5: "00110",
		6: "00111",
		7: "0001000",
		8: "0001001",
	}
	for v, bits := range wantUE {
		w := &bitWriter{}
		w.writeUE(v)
		if got := bitString(w.bits, w.n); got != bits {
			t.Errorf("ue(%d) = %s, want %s", v, got, bits)
		}
		br := newBitReader(w.bits)
		got, err := br.readUE()
		if err != nil || got != v {
			t.Errorf("readUE = %d, %v; want %d", got, err, v)
		}
	}

	// se(v) mapping from H.264 Table 9-7.
	wantSE := map[int]uint{0: 0, 1: 1, -1: 2, 2: 3, -2: 4, 3: 5, -3: 6}
	for signed, codeNum := range wantSE {
		w := &bitWriter{}
		w.writeSE(signed)
		br := newBitReader(w.bits)
		got, err := br.readSE()
		if err != nil || got != signed {
			t.Errorf("se round trip %d -> %d (%v), codeNum %d", signed, got, err, codeNum)
		}
	}
}

func bitString(data []byte, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		if data[i>>3]&(1<<(7-uint(i&7))) != 0 {
			out = append(out, '1')
		} else {
			out = append(out, '0')
		}
	}
	return string(out)
}
