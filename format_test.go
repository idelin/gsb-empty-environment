package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestDictRoundTrip(t *testing.T) {
	for _, dict := range [][][]byte{
		nil,
		{[]byte("abc")},
		{[]byte("a"), []byte("bc"), []byte("defgh"), bytes.Repeat([]byte{0, 255}, 40)},
	} {
		got, err := unmarshalDict(marshalDict(dict))
		if err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(got) != len(dict) {
			t.Fatalf("entry count: got %d want %d", len(got), len(dict))
		}
		for i := range dict {
			if !bytes.Equal(got[i], dict[i]) {
				t.Fatalf("entry %d: got %q want %q", i, got[i], dict[i])
			}
		}
	}
}

func TestDictCorrupt(t *testing.T) {
	good := marshalDict([][]byte{[]byte("hello")})
	if _, err := unmarshalDict(good); err != nil {
		t.Fatalf("sanity: %v", err)
	}
	bad := append([]byte(nil), good...)
	bad[0] = 'X'
	if _, err := unmarshalDict(bad); !errors.Is(err, errBadMagic) {
		t.Fatalf("bad magic: got %v", err)
	}
	bad = append([]byte(nil), good...)
	bad[len(bad)-1] ^= 0xff
	if _, err := unmarshalDict(bad); !errors.Is(err, errBadCRC) {
		t.Fatalf("bad crc: got %v", err)
	}
	if _, err := unmarshalDict(good[:5]); !errors.Is(err, errTruncated) {
		t.Fatalf("severely truncated: got %v", err)
	}
	// A truncated body is caught by the checksum (truncation and corruption
	// are indistinguishable past the header, by design).
	if _, err := unmarshalDict(good[:len(good)-6]); err == nil {
		t.Fatalf("truncated body: want error")
	}
	if _, err := unmarshalDict(append(append([]byte(nil), good...), 0)); !errors.Is(err, errBadCRC) {
		t.Fatalf("trailing: got %v", err)
	}
}

func roundTrip(t *testing.T, dict [][]byte, src []byte) {
	t.Helper()
	di := buildDictIndex(dict)
	stream := encodeStream(dictFileCRC(marshalDict(dict)), src, fastParse(src, di))
	got, err := decodeStream(stream, dict, 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("roundtrip mismatch: got %d bytes, want %d", len(got), len(src))
	}
}

func TestStreamRoundTrip(t *testing.T) {
	dict := [][]byte{[]byte("abc"), []byte("hello, ")}
	cases := [][]byte{
		{},
		[]byte("a"),
		[]byte("hello, hello, hello, abc abc"),
		bytes.Repeat([]byte("x"), 500),   // long literal run, split needed
		bytes.Repeat([]byte("abc"), 200), // mostly matches
		func() []byte {
			b := make([]byte, 4096)
			for i := range b {
				b[i] = byte(i * 37)
			}
			return b
		}(),
	}
	for _, src := range cases {
		roundTrip(t, dict, src)
	}
	// Empty dictionary roundtrip.
	roundTrip(t, nil, []byte("no dictionary at all"))
}

func TestDecodeMaxOut(t *testing.T) {
	dict := [][]byte{[]byte("abc")}
	src := bytes.Repeat([]byte("abc"), 50)
	di := buildDictIndex(dict)
	stream := encodeStream(dictFileCRC(marshalDict(dict)), src, fastParse(src, di))
	if _, err := decodeStream(stream, dict, len(src)); err != nil {
		t.Fatalf("exact limit should pass: %v", err)
	}
	if _, err := decodeStream(stream, dict, len(src)-1); !errors.Is(err, errTooLarge) {
		t.Fatalf("limit-1: got %v", err)
	}
	if _, err := decodeStream(stream, dict, 0); !errors.Is(err, errTooLarge) {
		t.Fatalf("limit 0: got %v", err)
	}
}

func TestDecodeCorrupt(t *testing.T) {
	dict := [][]byte{[]byte("abc")}
	src := []byte("abc abc abc")
	di := buildDictIndex(dict)
	stream := encodeStream(dictFileCRC(marshalDict(dict)), src, fastParse(src, di))

	if _, err := decodeStream(stream, [][]byte{[]byte("xyz")}, 1<<20); !errors.Is(err, errWrongDict) {
		t.Fatalf("wrong dict: got %v", err)
	}
	bad := append([]byte(nil), stream...)
	bad[0] = 'X'
	if _, err := decodeStream(bad, dict, 1<<20); !errors.Is(err, errBadMagic) {
		t.Fatalf("bad magic: got %v", err)
	}
	if _, err := decodeStream(stream[:len(stream)-3], dict, 1<<20); err == nil {
		t.Fatalf("truncated: want error")
	}
	bad = append([]byte(nil), stream...)
	bad[len(bad)-1] ^= 0xff
	if _, err := decodeStream(bad, dict, 1<<20); !errors.Is(err, errBadCRC) {
		t.Fatalf("bad crc: got %v", err)
	}
	bad = append(append([]byte(nil), stream...), 0)
	if _, err := decodeStream(bad, dict, 1<<20); !errors.Is(err, errTrailing) {
		t.Fatalf("trailing: got %v", err)
	}
	// Corrupt a token tag.
	bad = append([]byte(nil), stream...)
	bad[12] = 99
	if _, err := decodeStream(bad, dict, 1<<20); !errors.Is(err, errBadToken) {
		t.Fatalf("bad tag: got %v", err)
	}
	// Match index out of range: hand-encode a stream referencing entry 5.
	bad = []byte(streamMagic)
	var crc4 [4]byte
	binary.BigEndian.PutUint32(crc4[:], dictFileCRC(marshalDict(dict)))
	bad = append(bad, crc4[:]...)
	bad = binary.AppendUvarint(bad, 3) // origLen
	bad = binary.AppendUvarint(bad, 1) // numTokens
	bad = append(bad, tagMatch, 5)     // dict has 1 entry
	bad = append(bad, 0, 0, 0, 0)      // crc (unchecked: token error comes first)
	if _, err := decodeStream(bad, dict, 1<<20); !errors.Is(err, errBadToken) {
		t.Fatalf("bad index: got %v", err)
	}
}
