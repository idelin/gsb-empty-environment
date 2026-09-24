package enumkit

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestCodecRoundTripKnownAndUnknown(t *testing.T) {
	r := newColorEnum(t)
	for _, token := range []string{"CRIMSON", "GREEN", "99", "weird-token"} {
		v, err := r.Decode("v3", token)
		must(t, err, "decode "+token)
		wire, err := v.MarshalBinary()
		must(t, err, "marshal "+token)
		back, err := UnmarshalValue(wire)
		must(t, err, "unmarshal "+token)
		if back.Known() != v.Known() {
			t.Fatalf("known-ness changed for %q: %+v vs %+v", token, v, back)
		}
		if v.Known() && back.ID != v.ID {
			t.Fatalf("stable ID changed for %q: %+v vs %+v", token, v, back)
		}
		if !v.Known() && back.Raw != v.Raw {
			t.Fatalf("unknown token %q lost lexical form: %+v", token, back)
		}
	}
}

func TestCodecTruncated(t *testing.T) {
	cases := [][]byte{
		nil,
		{tagID},       // no varint
		{tagID, 0x80}, // truncated varint
		{tagRaw},      // no length
	}
	for i, c := range cases {
		if _, err := UnmarshalValue(c); !errors.Is(err, ErrTruncated) {
			t.Fatalf("case %d: err = %v, want ErrTruncated", i, err)
		}
	}
}

func TestCodecBadTag(t *testing.T) {
	if _, err := UnmarshalValue([]byte{0x7e}); !errors.Is(err, ErrBadTag) {
		t.Fatalf("err = %v, want ErrBadTag", err)
	}
}

func TestCodecLengthCheckedBeforeAllocation(t *testing.T) {
	// Declare a huge length with no payload: must fail on the bounds
	// check without allocating a multi-gigabyte slice.
	buf := []byte{tagRaw}
	buf = binary.AppendUvarint(buf, uint64(1<<40))
	if _, err := UnmarshalValue(buf); !errors.Is(err, ErrTokenTooLong) {
		t.Fatalf("huge declared length: err = %v, want ErrTokenTooLong", err)
	}
	// Declare 100 bytes, provide 3: truncation, not short allocation.
	buf = append([]byte{tagRaw}, 100)
	buf = append(buf, 'a', 'b', 'c')
	if _, err := UnmarshalValue(buf); !errors.Is(err, ErrTruncated) {
		t.Fatalf("short payload: err = %v, want ErrTruncated", err)
	}
}

func TestCodecRangeLimit(t *testing.T) {
	too := Value{Kind: KnownNumber, ID: MaxID + 1, Raw: "x"}
	if _, err := too.MarshalBinary(); !errors.Is(err, ErrRange) {
		t.Fatalf("marshal over-range: err = %v, want ErrRange", err)
	}
	buf := []byte{tagID}
	buf = binary.AppendUvarint(buf, uint64(MaxID+1))
	if _, err := UnmarshalValue(buf); !errors.Is(err, ErrRange) {
		t.Fatalf("unmarshal over-range: err = %v, want ErrRange", err)
	}
	big := strings.Repeat("x", MaxTokenLen+1)
	if _, err := (Value{Kind: UnknownName, Raw: big}).MarshalBinary(); !errors.Is(err, ErrTokenTooLong) {
		t.Fatalf("marshal oversized token: err not ErrTokenTooLong")
	}
}

func TestValueJSON(t *testing.T) {
	v := Value{Kind: UnknownName, Raw: "GREEN"}
	data, err := v.MarshalJSON()
	must(t, err, "marshal json")
	if string(data) != `"GREEN"` {
		t.Fatalf("json = %s", data)
	}
	var back Value
	must(t, back.UnmarshalJSON(data), "unmarshal json")
	if back.Kind != UnknownName || back.Raw != "GREEN" {
		t.Fatalf("back = %+v", back)
	}
	var num Value
	must(t, num.UnmarshalJSON([]byte(`42`)), "unmarshal number")
	if num.Kind != UnknownNumber || num.ID != 42 {
		t.Fatalf("num = %+v", num)
	}
}
