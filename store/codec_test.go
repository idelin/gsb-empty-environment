package store

import (
	"bytes"
	"errors"
	"testing"
)

func sampleEntries() []Entry {
	big := make([]byte, MaxCommandSize)
	for i := range big {
		big[i] = byte(i)
	}
	return []Entry{
		{Slot: 0, Term: 0, NodeID: 0, Flags: 0, Command: nil},
		{Slot: 1, Term: 7, NodeID: 3, Flags: FlagTest, Command: []byte{}},
		{Slot: 1<<63 + 5, Term: 1<<60 + 9, NodeID: 1<<31 + 1, Flags: 1 << 31, Command: []byte{0x00, 0xff, 0x7f, 0x80}},
		{Slot: 42, Term: 2, NodeID: 5, Flags: 0, Command: bytes.Repeat([]byte("paxos"), 1000)},
		{Slot: 99, Term: 4, NodeID: 2, Flags: 0, Command: big},
	}
}

// FlagTest is a test-only flag bit.
const FlagTest uint32 = 0x8000_0000

func TestCodecRoundTrip(t *testing.T) {
	for _, version := range []uint16{FormatV1, FormatV2} {
		for _, want := range sampleEntries() {
			if version == FormatV1 {
				want.Flags = 0 // V1 has no flags field
			}
			got, err := DecodeEntry(Encode(want, version))
			if err != nil {
				t.Fatalf("v%d decode: %v", version, err)
			}
			if got.Slot != want.Slot || got.Term != want.Term ||
				got.NodeID != want.NodeID || got.Flags != want.Flags ||
				!bytes.Equal(got.Command, want.Command) {
				t.Fatalf("v%d round trip mismatch:\n got %+v\nwant %+v", version, got, want)
			}
		}
	}
}

func TestZeroCopyAlias(t *testing.T) {
	payload := Encode(Entry{Slot: 9, Term: 1, NodeID: 2, Flags: 3, Command: []byte("hello")}, CurrentFormat)
	e, err := DecodeEntry(payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(e.Command) != "hello" {
		t.Fatalf("got %q", e.Command)
	}
	// Mutating the source buffer must be visible through the decoded entry:
	// proof that Command aliases the input instead of copying it.
	payload[len(payload)-1] = 'p'
	if string(e.Command) != "hellp" {
		t.Fatalf("decode copied the command; want zero-copy alias, got %q", e.Command)
	}
}

func TestDecodeTruncatedEveryPrefix(t *testing.T) {
	full := Encode(Entry{Slot: 123, Term: 45, NodeID: 6, Flags: 7, Command: []byte("some-command")}, CurrentFormat)
	for n := 0; n < len(full); n++ {
		if _, err := DecodeEntry(full[:n]); err == nil {
			t.Fatalf("prefix len %d: expected error", n)
		}
	}
}

func TestDecodeEmptyAndZeroVersion(t *testing.T) {
	if _, err := DecodeEntry(nil); !errors.Is(err, ErrTruncated) {
		t.Fatalf("nil payload: %v", err)
	}
	if _, err := DecodeEntry([]byte{0}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("version 0: %v", err)
	}
}

func TestDecodeCommandLengthOverflow(t *testing.T) {
	good := Encode(Entry{Slot: 1, Term: 1, NodeID: 1, Command: []byte("ab")}, CurrentFormat)

	// Declared length larger than the remaining bytes -> ErrTruncated.
	bad := append([]byte(nil), good...)
	bad[len(bad)-3] = 0x7f // cmdLen varint: 127 > 2 remaining
	if _, err := DecodeEntry(bad); !errors.Is(err, ErrTruncated) {
		t.Fatalf("oversized declared length: %v", err)
	}

	// Declared length beyond the hard cap -> ErrTooLarge.
	huge := []byte{byte(CurrentFormat)}
	huge = append(huge, 1, 1, 1, 0)          // slot, term, node, flags
	huge = append(huge, 0x80, 0x80, 0x40)    // uvarint 1<<20+... = 0x100000? see below
	huge = append(huge, make([]byte, 64)...) // nowhere near enough data
	if _, err := DecodeEntry(huge); !errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrTruncated) {
		t.Fatalf("huge length: %v", err)
	}

	// Exactly MaxCommandSize+1 declared -> ErrTooLarge even with data present.
	over := []byte{byte(CurrentFormat), 1, 1, 1, 0}
	over = appendUvarintManual(over, MaxCommandSize+1)
	over = append(over, make([]byte, MaxCommandSize+1)...)
	if _, err := DecodeEntry(over); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("cap overflow: %v", err)
	}
}

func appendUvarintManual(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func TestDecodeVarintOverflow(t *testing.T) {
	// 11 continuation bytes can never be a valid uvarint.
	payload := append([]byte{byte(CurrentFormat)}, bytes.Repeat([]byte{0xff}, 11)...)
	if _, err := DecodeEntry(payload); !errors.Is(err, ErrOverflow) {
		t.Fatalf("varint overflow: %v", err)
	}
}

func TestForwardCompatNewerPayload(t *testing.T) {
	// A "future" v3 payload: v2 fields plus an extra trailing field the
	// current decoder does not know. It must decode the known prefix and
	// ignore the rest.
	payload := Encode(Entry{Slot: 5, Term: 6, NodeID: 7, Flags: 8, Command: []byte("cmd")}, FormatV2)
	payload[0] = byte(FormatV2 + 1) // pretend to be v3
	payload = append(payload, 0x2a, 0x01, 0x02, 0x03)
	e, err := DecodeEntry(payload)
	if err != nil {
		t.Fatalf("forward compat decode: %v", err)
	}
	if e.Slot != 5 || e.Term != 6 || e.NodeID != 7 || e.Flags != 8 || string(e.Command) != "cmd" {
		t.Fatalf("forward compat mismatch: %+v", e)
	}
}

func TestBackwardCompatV1Payload(t *testing.T) {
	payload := Encode(Entry{Slot: 5, Term: 6, NodeID: 7, Command: []byte("cmd")}, FormatV1)
	e, err := DecodeEntry(payload)
	if err != nil {
		t.Fatalf("backward compat decode: %v", err)
	}
	if e.Flags != 0 {
		t.Fatalf("v1 payload must decode Flags=0, got %d", e.Flags)
	}
	if e.Slot != 5 || e.Term != 6 || e.NodeID != 7 || string(e.Command) != "cmd" {
		t.Fatalf("backward compat mismatch: %+v", e)
	}
}

func TestEncodePanicsOnOversizedCommand(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for oversized command")
		}
	}()
	Encode(Entry{Command: make([]byte, MaxCommandSize+1)}, CurrentFormat)
}
