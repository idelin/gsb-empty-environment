package format

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func mustEncode(t *testing.T, entries []Entry) []byte {
	t.Helper()
	data, err := Encode(entries)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return data
}

func TestRoundTripFixed(t *testing.T) {
	cases := [][]Entry{
		{},
		{{Key: "a", Value: []byte{1, 2, 3}}},
		{{Key: "k", Value: []byte{}}, {Key: "k", Value: []byte("dup keys ok")}},
		{{Key: "Alpha-1_2.3:+", Value: bytes.Repeat([]byte{0xff}, 512)}},
	}
	for _, entries := range cases {
		data := mustEncode(t, entries)
		got, err := Decode(data)
		if err != nil {
			t.Fatalf("Decode(%v): %v", entries, err)
		}
		if len(got) != len(entries) {
			t.Fatalf("Decode(%v): got %d entries", entries, len(got))
		}
		for i := range got {
			if got[i].Key != entries[i].Key || !bytes.Equal(got[i].Value, entries[i].Value) {
				t.Fatalf("entry %d: got %+v want %+v", i, got[i], entries[i])
			}
		}
		// Determinism: re-encoding the decode output is byte-identical.
		again := mustEncode(t, got)
		if !bytes.Equal(again, data) {
			t.Fatalf("non-deterministic encoding for %v", entries)
		}
	}
}

func TestEncodeRejectsOutOfScope(t *testing.T) {
	if _, err := Encode([]Entry{{Key: "", Value: nil}}); ClassOf(err) != ClassKeyLen {
		t.Fatalf("empty key: %v", err)
	}
	if _, err := Encode([]Entry{{Key: "a=b", Value: nil}}); ClassOf(err) != ClassKeyChar {
		t.Fatalf("key with '=': %v", err)
	}
	if _, err := Encode([]Entry{{Key: "k", Value: make([]byte, MaxValLen+1)}}); ClassOf(err) != ClassValLen {
		t.Fatalf("oversize value: %v", err)
	}
}

func TestDecodeRejectClasses(t *testing.T) {
	base := mustEncode(t, []Entry{{Key: "key", Value: []byte("value")}})

	corrupt := func(edit func(b []byte)) []byte {
		b := append([]byte(nil), base...)
		edit(b)
		return b
	}
	fixCRC := func(b []byte) {
		binary.LittleEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[:len(b)-4]))
	}

	cases := []struct {
		name string
		data []byte
		want Class
	}{
		{"magic", corrupt(func(b []byte) { b[0] ^= 1 }), ClassMagic},
		{"count", corrupt(func(b []byte) { binary.LittleEndian.PutUint16(b[4:6], MaxEntries+1); fixCRC(b) }), ClassCount},
		{"keylen", corrupt(func(b []byte) { b[6] = 0; fixCRC(b) }), ClassKeyLen},
		{"keychar", corrupt(func(b []byte) { b[7] = '\n'; fixCRC(b) }), ClassKeyChar},
		{"vallen", corrupt(func(b []byte) { binary.LittleEndian.PutUint16(b[10:12], MaxValLen+1); fixCRC(b) }), ClassValLen},
		{"crc", corrupt(func(b []byte) { b[len(b)-1] ^= 0x80 }), ClassCRC},
		{"truncated", base[:len(base)-2], ClassTruncated},
		{"trailing", append(append([]byte(nil), base...), 0), ClassTrailing},
		{"budget", make([]byte, Budget+1), ClassBudget},
	}
	for _, tc := range cases {
		_, err := Decode(tc.data)
		if got := ClassOf(err); got != tc.want {
			t.Errorf("%s: got class %q want %q (err=%v)", tc.name, got, tc.want, err)
		}
	}
}

// Lengths must be validated before any allocation or copy; these
// inputs claim large counts/lengths but are tiny, and must fail fast
// without panicking.
func TestLengthCheckedBeforeAllocation(t *testing.T) {
	claims := [][]byte{
		[]byte("KVS1\xff\xff"),                  // count = 65535
		[]byte("KVS1\x01\x00\x20"),              // klen = 32, no key bytes
		[]byte("KVS1\x01\x00\x01a\xff\xff\x00"), // vlen = 65535, no value
	}
	for _, data := range claims {
		if _, err := Decode(data); err == nil {
			t.Errorf("Decode(%x) unexpectedly succeeded", data)
		}
	}
}
