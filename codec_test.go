package chronos

import (
	"bytes"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	v1 := SyntheticIslandV1()
	in, _ := NewInstant(-62135596800, 0, PrecSecond)
	dur, _ := NewDuration(-123_456_789, PrecNanosecond)
	off, _ := NewOffsetChoice(7200, 1)
	wall, err := NewWallClock(2021, 10, 31, 2, 30, 0, 987_654_321, PrecNanosecond, v1, 1)
	if err != nil {
		t.Fatal(err)
	}
	vals := []interface{}{in, dur, off, wall}
	for _, v := range vals {
		b, err := Encode(v)
		if err != nil {
			t.Fatalf("encode %T: %v", v, err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("decode %T: %v", v, err)
		}
		switch want := v.(type) {
		case Instant:
			if !got.(Instant).Equal(want) {
				t.Fatalf("instant round trip: %v", got)
			}
		case Duration:
			if got.(Duration) != want {
				t.Fatalf("duration round trip: %v", got)
			}
		case OffsetChoice:
			if got.(OffsetChoice) != want {
				t.Fatalf("offset round trip: %v", got)
			}
		case WallClock:
			if !got.(WallClock).Equal(want) {
				t.Fatalf("wall round trip:\n got %v\nwant %v", got, want)
			}
		}
	}
}

func TestDecodeRejectsOutOfRange(t *testing.T) {
	in, _ := NewInstant(0, 0, PrecSecond)
	b, _ := Encode(in)
	// Seconds far beyond year 9999.
	for i := 0; i < 8; i++ {
		b[headerLen+i] = 0x7f
	}
	if _, err := Decode(b); err == nil {
		t.Fatal("expected range rejection for absurd instant seconds")
	}
	// Duration beyond the supported serialization range.
	d, _ := NewDuration(1_000_000_000, PrecNanosecond)
	bd, _ := Encode(d)
	for i := 0; i < 8; i++ {
		bd[headerLen+i] = 0x7f
	}
	if _, err := Decode(bd); err == nil {
		t.Fatal("expected range rejection for absurd duration")
	}
}

func TestDecodeRejectsIllegalLabels(t *testing.T) {
	in, _ := NewInstant(0, 0, PrecSecond)
	b, _ := Encode(in)

	bad := append([]byte(nil), b...)
	bad[6] = 9 // illegal precision label
	if _, err := Decode(bad); err == nil {
		t.Fatal("expected illegal precision label rejection")
	}

	bad = append([]byte(nil), b...)
	bad[5] = 99 // illegal type tag
	if _, err := Decode(bad); err == nil {
		t.Fatal("expected illegal type tag rejection")
	}

	bad = append([]byte(nil), b...)
	bad[4] = 7 // unknown format version
	if _, err := Decode(bad); err != ErrBadMagic {
		t.Fatalf("expected ErrBadMagic, got %v", err)
	}

	// Wall clock with an illegal zone label character.
	v1 := SyntheticIslandV1()
	w, _ := NewWallClock(2021, 1, 1, 0, 0, 0, 0, PrecSecond, v1, 0)
	bw, _ := Encode(w)
	bad = append([]byte(nil), bw...)
	bad[len(bad)-1] = '!' // last char of "X/Island"
	if _, err := Decode(bad); err == nil {
		t.Fatal("expected illegal zone label rejection")
	}
}

func TestDecodeValidatesLengthBeforeAllocation(t *testing.T) {
	v1 := SyntheticIslandV1()
	w, _ := NewWallClock(2021, 1, 1, 0, 0, 0, 0, PrecSecond, v1, 0)
	good, _ := Encode(w)

	// Header claims a 32-byte version but the buffer is much shorter.
	bad := append([]byte(nil), good[:headerLen]...)
	bad[7] = 32
	if _, err := Decode(bad); err != ErrTruncated {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
	// Version length beyond the label limit is an illegal label, and must
	// be rejected before any allocation of that size.
	bad = append([]byte(nil), good...)
	bad[7] = 200
	if _, err := Decode(bad); err == nil {
		t.Fatal("expected illegal tz version length rejection")
	}
	// Truncated body.
	if _, err := Decode(good[:len(good)-3]); err != ErrTruncated {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
	// Zone length field larger than the remaining buffer.
	bad = append([]byte(nil), good...)
	bad[len(bad)-len("X/Island")-1] = 60
	if _, err := Decode(bad); err == nil {
		t.Fatal("expected zone length rejection")
	}
	// Trailing garbage.
	if _, err := Decode(append(append([]byte(nil), good...), 0x00)); err == nil {
		t.Fatal("expected trailing garbage rejection")
	}
}

func TestDecodeUnknownTZVersionMarkedUnresolvable(t *testing.T) {
	v1 := SyntheticIslandV1()
	w, _ := NewWallClock(2021, 1, 15, 12, 0, 0, 0, PrecSecond, v1, 0)
	good, _ := Encode(w)
	// Same-length but unregistered version label.
	bad := bytes.Replace(good, []byte("2024a"), []byte("1999z"), 1)
	got, err := Decode(bad)
	if err != nil {
		t.Fatalf("unknown version must still decode as data: %v", err)
	}
	w2 := got.(WallClock)
	reg := SyntheticRegistry()
	if w2.Resolvable(reg) {
		t.Fatal("unknown tz version must be marked unresolvable")
	}
	if _, err := w2.InstantFrom(reg); err == nil {
		t.Fatal("expected unresolvable error")
	}
}
