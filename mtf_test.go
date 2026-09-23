package mtf

import (
	"bytes"
	"errors"
	"hash/crc32"
	"testing"
)

func mustAlphabet(t *testing.T, symbols string) *Alphabet {
	t.Helper()
	a, err := NewAlphabet([]byte(symbols))
	if err != nil {
		t.Fatalf("NewAlphabet: %v", err)
	}
	return a
}

func TestRepeatEncodesZero(t *testing.T) {
	m := NewModel(mustAlphabet(t, "abcdef"))
	for _, want := range []struct {
		sym byte
		idx int
	}{{'d', 3}, {'d', 0}, {'c', 3}, {'c', 0}, {'a', 2}, {'b', 3}} {
		idx, err := m.EncodeIndex(want.sym)
		if err != nil {
			t.Fatalf("EncodeIndex(%q): %v", want.sym, err)
		}
		if idx != want.idx {
			t.Fatalf("EncodeIndex(%q) = %d, want %d", want.sym, idx, want.idx)
		}
	}
}

func TestDuplicateAlphabetRejected(t *testing.T) {
	if _, err := NewAlphabet([]byte("abca")); !errors.Is(err, ErrDuplicateSymbol) {
		t.Fatalf("got %v, want ErrDuplicateSymbol", err)
	}
	if _, err := NewAlphabet(nil); !errors.Is(err, ErrEmptyAlphabet) {
		t.Fatalf("got %v, want ErrEmptyAlphabet", err)
	}
}

func TestIndexOutOfRangeKeepsState(t *testing.T) {
	m := NewModel(mustAlphabet(t, "abcdef"))
	if _, err := m.EncodeIndex('c'); err != nil {
		t.Fatal(err)
	}
	before := m.Order()
	for _, bad := range []int{-1, 6, 7, 1 << 30} {
		if _, err := m.DecodeIndex(bad); !errors.Is(err, ErrIndexOutOfRange) {
			t.Fatalf("DecodeIndex(%d): got %v, want ErrIndexOutOfRange", bad, err)
		}
		if !bytes.Equal(m.Order(), before) {
			t.Fatalf("DecodeIndex(%d) mutated state: %q -> %q", bad, before, m.Order())
		}
	}
}

func TestEncodeUnknownRejectedKeepsState(t *testing.T) {
	m := NewModel(mustAlphabet(t, "abc"))
	before := m.Order()
	if _, err := m.EncodeIndex('z'); !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("got %v, want ErrUnknownSymbol", err)
	}
	if !bytes.Equal(m.Order(), before) {
		t.Fatal("state mutated by rejected encode")
	}
	enc := NewBlockEncoder(m, RejectUnknown)
	if err := enc.Write('z'); !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("got %v, want ErrUnknownSymbol", err)
	}
}

func TestEscapeProtocolRoundTrip(t *testing.T) {
	input := "abzcdz" // 'z' is unknown and escaped; second 'z' encodes as index
	encModel := NewModel(mustAlphabet(t, "abcd"))
	enc := NewBlockEncoder(encModel, EscapeUnknown)
	for i := 0; i < len(input); i++ {
		if err := enc.Write(input[i]); err != nil {
			t.Fatalf("Write(%q): %v", input[i], err)
		}
	}
	if encModel.Alphabet().Version() != 2 {
		t.Fatalf("alphabet version = %d, want 2 after one escape", encModel.Alphabet().Version())
	}
	dec := NewBlockDecoder(NewModel(mustAlphabet(t, "abcd")))
	out, err := dec.Decode(enc.Bytes(), 1<<20)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if string(out) != input {
		t.Fatalf("round trip = %q, want %q", out, input)
	}
	// Both sides must agree on the extended alphabet and final MTF order.
	if encModel.Alphabet().Version() != 2 {
		t.Fatal("encoder alphabet not extended")
	}
	if !bytes.Equal(encModel.Order(), dec.m.Order()) {
		t.Fatalf("encoder order %q != decoder order %q", encModel.Order(), dec.m.Order())
	}
}

func TestEscapeOfKnownSymbolRejected(t *testing.T) {
	// Hand-craft a stream: escape token (index == size) followed by a
	// symbol that is already in the alphabet.
	stream := append(binary4(3), 'a')
	dec := NewBlockDecoder(NewModel(mustAlphabet(t, "abc")))
	if _, err := dec.Decode(stream, 100); !errors.Is(err, ErrCorruptStream) {
		t.Fatalf("got %v, want ErrCorruptStream", err)
	}
}

func binary4(v uint64) []byte {
	var b [10]byte
	n := 0
	for {
		b[n] = byte(v) | 0x80
		v >>= 7
		if v == 0 {
			b[n] &^= 0x80
			n++
			break
		}
		n++
	}
	return b[:n]
}

func TestCheckpointRoundTrip(t *testing.T) {
	m := NewModel(mustAlphabet(t, "abcdef"))
	for _, s := range []byte("fadebed") {
		if _, err := m.EncodeIndex(s); err != nil {
			t.Fatal(err)
		}
	}
	cp := m.Checkpoint()
	m2, err := Restore(mustAlphabet(t, "abcdef"), cp)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !bytes.Equal(m.Order(), m2.Order()) {
		t.Fatalf("restored order %q != %q", m2.Order(), m.Order())
	}
	// Continuous processing on both must stay identical.
	for _, s := range []byte("decaf") {
		i1, _ := m.EncodeIndex(s)
		i2, _ := m2.EncodeIndex(s)
		if i1 != i2 {
			t.Fatalf("diverged after restore: %d != %d", i1, i2)
		}
	}
}

func TestCheckpointTamperRejected(t *testing.T) {
	m := NewModel(mustAlphabet(t, "abcd"))
	_, _ = m.EncodeIndex('c')
	good := m.Checkpoint()

	cases := map[string]func([]byte) []byte{
		"truncated":    func(b []byte) []byte { return b[:len(b)-3] },
		"bad magic":    func(b []byte) []byte { b[0] = 'X'; return b },
		"bad fmt ver":  func(b []byte) []byte { b[4] = 99; return b },
		"bad payload":  func(b []byte) []byte { b[checkpointHeaderLen] ^= 0xff; return b },
		"bad checksum": func(b []byte) []byte { b[len(b)-1] ^= 0xff; return b },
	}
	for name, mutate := range cases {
		cp := mutate(append([]byte(nil), good...))
		if _, err := Restore(mustAlphabet(t, "abcd"), cp); !errors.Is(err, ErrBadCheckpoint) {
			t.Fatalf("%s: got %v, want ErrBadCheckpoint", name, err)
		}
	}
	// Alphabet version mismatch: extend the alphabet after checkpointing.
	a := mustAlphabet(t, "abcd")
	if err := a.Extend('z'); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(a, good); !errors.Is(err, ErrBadCheckpoint) {
		t.Fatalf("version mismatch: got %v, want ErrBadCheckpoint", err)
	}
}

func TestCheckpointPermutationValidated(t *testing.T) {
	m := NewModel(mustAlphabet(t, "abcd"))
	cp := m.Checkpoint()
	// Corrupt payload into a non-permutation, then fix the checksum so only
	// the permutation check can catch it.
	cp[checkpointHeaderLen+1] = cp[checkpointHeaderLen]
	fixChecksum(cp)
	if _, err := Restore(mustAlphabet(t, "abcd"), cp); !errors.Is(err, ErrBadCheckpoint) {
		t.Fatalf("got %v, want ErrBadCheckpoint", err)
	}
}

func fixChecksum(cp []byte) {
	// Recompute CRC-32/IEEE over header+payload.
	crc := crc32.ChecksumIEEE(cp[:len(cp)-4])
	for i := 0; i < 4; i++ {
		cp[len(cp)-4+i] = byte(crc >> (8 * i))
	}
}

func TestReset(t *testing.T) {
	a := mustAlphabet(t, "abcdef")
	m := NewModel(a)
	for _, s := range []byte("fade") {
		if _, err := m.EncodeIndex(s); err != nil {
			t.Fatal(err)
		}
	}
	m.Reset()
	if !bytes.Equal(m.Order(), a.Symbols()) {
		t.Fatalf("after Reset order = %q, want %q", m.Order(), a.Symbols())
	}
	idx, err := m.EncodeIndex('f')
	if err != nil || idx != 5 {
		t.Fatalf("after Reset EncodeIndex('f') = %d, %v; want 5, nil", idx, err)
	}
}

func TestDecodeOutputBounded(t *testing.T) {
	input := "abcabcabc"
	enc := NewBlockEncoder(NewModel(mustAlphabet(t, "abc")), RejectUnknown)
	for i := 0; i < len(input); i++ {
		if err := enc.Write(input[i]); err != nil {
			t.Fatal(err)
		}
	}
	dec := NewBlockDecoder(NewModel(mustAlphabet(t, "abc")))
	if _, err := dec.Decode(enc.Bytes(), len(input)-1); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("got %v, want ErrOutputLimit", err)
	}
	out, err := NewBlockDecoder(NewModel(mustAlphabet(t, "abc"))).Decode(enc.Bytes(), len(input))
	if err != nil || string(out) != input {
		t.Fatalf("bounded decode = %q, %v", out, err)
	}
}

func TestTruncatedStreamRejected(t *testing.T) {
	dec := NewBlockDecoder(NewModel(mustAlphabet(t, "abc")))
	if _, err := dec.Decode([]byte{0x80}, 100); !errors.Is(err, ErrCorruptStream) {
		t.Fatalf("got %v, want ErrCorruptStream", err)
	}
	// Escape token without its literal byte.
	if _, err := dec.Decode([]byte{3}, 100); !errors.Is(err, ErrCorruptStream) {
		t.Fatalf("got %v, want ErrCorruptStream", err)
	}
}
