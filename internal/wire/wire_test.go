package wire

import (
	"bytes"
	"testing"
)

func sampleFile(t *testing.T) (dict [][]byte, tokens []Token, plain, enc []byte) {
	t.Helper()
	dict = [][]byte{[]byte("abab"), []byte("XYZ")}
	plain = []byte("abab--XYZ--abab!")
	tokens = []Token{
		{Ref: 0},
		{Ref: -1, Lit: []byte("--")},
		{Ref: 1},
		{Ref: -1, Lit: []byte("--")},
		{Ref: 0},
		{Ref: -1, Lit: []byte("!")},
	}
	enc, err := Encode(dict, tokens, plain)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return dict, tokens, plain, enc
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	wantDict, _, wantPlain, enc := sampleFile(t)
	dict, plain, err := Decode(enc, 1<<20)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(plain, wantPlain) {
		t.Fatalf("payload mismatch: got %q want %q", plain, wantPlain)
	}
	if len(dict) != len(wantDict) {
		t.Fatalf("dictionary size mismatch")
	}
	for i := range dict {
		if !bytes.Equal(dict[i], wantDict[i]) {
			t.Fatalf("entry %d mismatch: got %q want %q", i, dict[i], wantDict[i])
		}
	}
}

func TestEncodeEmptyPayload(t *testing.T) {
	enc, err := Encode(nil, nil, nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	_, plain, err := Decode(enc, 1<<20)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(plain) != 0 {
		t.Fatalf("expected empty payload, got %d bytes", len(plain))
	}
}

func TestEncodeRejectsMismatchedTokens(t *testing.T) {
	_, err := Encode(nil, []Token{{Ref: -1, Lit: []byte("ab")}}, []byte("xx"))
	if err == nil {
		t.Fatal("expected error for tokens not matching payload")
	}
}

func TestDecodeRejectsBadMagic(t *testing.T) {
	_, _, _, enc := sampleFile(t)
	enc[0] = 'X'
	if _, _, err := Decode(enc, 1<<20); err != ErrBadMagic {
		t.Fatalf("expected ErrBadMagic, got %v", err)
	}
}

func TestDecodeRejectsBadCRC(t *testing.T) {
	_, _, _, enc := sampleFile(t)
	enc[len(enc)-1] ^= 0xFF // corrupt a literal byte in the token stream
	if _, _, err := Decode(enc, 1<<20); err != ErrBadCRC {
		t.Fatalf("expected ErrBadCRC, got %v", err)
	}
}

func TestDecodeRejectsTruncation(t *testing.T) {
	_, _, _, enc := sampleFile(t)
	for _, n := range []int{0, 3, 7, 10, len(enc) / 2, len(enc) - 1} {
		if _, _, err := Decode(enc[:n], 1<<20); err == nil {
			t.Fatalf("expected error for truncation to %d bytes", n)
		}
	}
}

func TestDecodeRejectsTrailingGarbage(t *testing.T) {
	_, _, _, enc := sampleFile(t)
	enc = append(enc, 0x00)
	if _, _, err := Decode(enc, 1<<20); err == nil {
		t.Fatal("expected error for trailing garbage")
	}
}

func TestDecodeOutputBound(t *testing.T) {
	_, _, plain, enc := sampleFile(t)
	if _, _, err := Decode(enc, int64(len(plain)-1)); err != ErrTooLarge {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
	if _, _, err := Decode(enc, int64(len(plain))); err != nil {
		t.Fatalf("bound equal to payload size must succeed, got %v", err)
	}
}

func TestDecodeRejectsImpossibleDeclaredLength(t *testing.T) {
	// Hand-craft a header declaring a huge N with a tiny token stream:
	// Decode must reject it before allocating.
	var buf bytes.Buffer
	buf.WriteString(MagicFile)
	buf.Write([]byte{0, 0, 0, 0})                         // 0 dictionary entries
	buf.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0}) // N = 2^32-1
	buf.Write([]byte{0, 0, 0, 0})                         // CRC
	buf.Write([]byte{2, 0, 0, 0})                         // T = 2
	buf.Write([]byte{0x01, 'a'})                          // one literal byte
	if _, _, err := Decode(buf.Bytes(), 1<<40); err == nil {
		t.Fatal("expected error for impossible declared length")
	}
}

func TestDecodeRejectsInvalidControlByte(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(MagicFile)
	buf.Write([]byte{0, 0, 0, 0})
	buf.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0}) // N = 0
	buf.Write([]byte{0, 0, 0, 0})             // CRC
	buf.Write([]byte{1, 0, 0, 0})             // T = 1
	buf.Write([]byte{0x00})                   // invalid control byte
	if _, _, err := Decode(buf.Bytes(), 1<<20); err == nil {
		t.Fatal("expected error for control byte 0x00")
	}
}

func TestDictFileRoundTrip(t *testing.T) {
	dict := [][]byte{[]byte("hello "), []byte("world"), []byte{0x00, 0xFF}}
	data, err := WriteDict(dict)
	if err != nil {
		t.Fatalf("WriteDict: %v", err)
	}
	got, err := ReadDict(data)
	if err != nil {
		t.Fatalf("ReadDict: %v", err)
	}
	if len(got) != len(dict) {
		t.Fatalf("entry count mismatch")
	}
	for i := range got {
		if !bytes.Equal(got[i], dict[i]) {
			t.Fatalf("entry %d mismatch", i)
		}
	}
	data[len(data)-1] ^= 0xFF
	if _, err := ReadDict(data); err != ErrBadCRC {
		t.Fatalf("expected ErrBadCRC for corrupted dictionary, got %v", err)
	}
}
