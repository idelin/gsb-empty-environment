package nbf

import (
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

var orders = []struct {
	name  string
	order binary.ByteOrder
}{
	{"big", binary.BigEndian},
	{"little", binary.LittleEndian},
}

func sampleFile() *File {
	return &File{
		Version: 3,
		Records: []Record{
			{
				Fields:    1<<tagID | 1<<tagName | 1<<tagChildren,
				ID:        42,
				Name:      "root",
				Timestamp: 0,
				Children: []Record{
					{
						Fields:    1<<tagID | 1<<tagTimestamp | 1<<tagChildren,
						ID:        7,
						Timestamp: 1234567890123,
						Children: []Record{
							{Fields: 1 << tagFlags, Flags: 0xBEEF},
						},
					},
					{Fields: 1 << tagName, Name: "leaf"},
				},
			},
			{
				Fields: 1<<tagFlags | 1<<tagName,
				Flags:  9,
				Name:   "second",
			},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	for _, o := range orders {
		want := sampleFile()
		data := Encode(want, o.order)
		got, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: Parse: %v", o.name, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s: round trip mismatch\nwant %+v\ngot  %+v", o.name, want, got)
		}
	}
}

func TestMagicSelectsByteOrder(t *testing.T) {
	for _, o := range orders {
		data := Encode(sampleFile(), o.order)
		if o.order == binary.BigEndian && string(data[:4]) != MagicBigEndian {
			t.Fatalf("big-endian encode wrote magic %q", data[:4])
		}
		if o.order == binary.LittleEndian && string(data[:4]) != MagicLittleEndian {
			t.Fatalf("little-endian encode wrote magic %q", data[:4])
		}
		if _, err := Parse(data); err != nil {
			t.Fatalf("%s: %v", o.name, err)
		}
	}
}

func TestBadMagic(t *testing.T) {
	data := Encode(sampleFile(), binary.BigEndian)
	copy(data[:4], "XXXX")
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for bad magic")
	}
}

// buildFile hand-crafts a file: header plus one record with the given
// bitmap and raw body bytes.
func buildFile(order binary.ByteOrder, bitmap uint16, body []byte) []byte {
	w := &writer{order: order}
	if order == binary.BigEndian {
		w.bytes([]byte(MagicBigEndian))
	} else {
		w.bytes([]byte(MagicLittleEndian))
	}
	w.u16(1) // version
	w.u16(1) // record count
	w.u16(bitmap)
	w.u32(uint32(len(body)))
	w.bytes(body)
	return w.buf
}

func TestUnknownFieldSkipped(t *testing.T) {
	for _, o := range orders {
		w := &writer{order: o.order}
		w.u8(200) // unknown tag
		w.u16(3)
		w.bytes([]byte("abc"))
		w.u8(tagID)
		w.u16(4)
		w.u32(77)
		data := buildFile(o.order, 1<<tagID, w.buf)

		f, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", o.name, err)
		}
		if len(f.Records) != 1 || f.Records[0].ID != 77 || f.Records[0].Fields != 1<<tagID {
			t.Fatalf("%s: unexpected record %+v", o.name, f.Records[0])
		}
	}
}

func TestKnownFieldLengthMismatch(t *testing.T) {
	for _, o := range orders {
		w := &writer{order: o.order}
		w.u8(tagID) // known fixed-length field with wrong length
		w.u16(3)
		w.bytes([]byte("abc"))
		data := buildFile(o.order, 1<<tagID, w.buf)

		_, err := Parse(data)
		if err == nil {
			t.Fatalf("%s: expected length mismatch error", o.name)
		}
		pe, ok := err.(*ParseError)
		if !ok {
			t.Fatalf("%s: error is %T, want *ParseError", o.name, err)
		}
		if !strings.Contains(pe.Msg, "length 3, want 4") {
			t.Fatalf("%s: unexpected message %q", o.name, pe.Msg)
		}
	}
}

func TestBitmapMismatch(t *testing.T) {
	for _, o := range orders {
		// Bitmap declares ID but the body is empty.
		data := buildFile(o.order, 1<<tagID, nil)
		if _, err := Parse(data); err == nil {
			t.Fatalf("%s: expected bitmap mismatch error", o.name)
		}
	}
}

func TestNestedBodyCannotExceedParent(t *testing.T) {
	for _, o := range orders {
		// Nested value: count=1, then a child record whose body length
		// (100) overruns the parent field's value range.
		nested := &writer{order: o.order}
		nested.u16(1)    // child count
		nested.u16(0)    // child bitmap
		nested.u32(100)  // child body length: far beyond parent range
		nested.u16(0xAA) // only 2 bytes actually present

		body := &writer{order: o.order}
		body.u8(tagChildren)
		body.u16(uint16(len(nested.buf)))
		body.bytes(nested.buf)

		data := buildFile(o.order, 1<<tagChildren, body.buf)
		_, err := Parse(data)
		if err == nil {
			t.Fatalf("%s: expected overrun error", o.name)
		}
		pe, ok := err.(*ParseError)
		if !ok {
			t.Fatalf("%s: error is %T, want *ParseError", o.name, err)
		}
		if pe.Offset < 0 || pe.Offset > len(data) {
			t.Fatalf("%s: error offset %d outside file of %d bytes", o.name, pe.Offset, len(data))
		}
	}
}

func TestNestedTrailingBytes(t *testing.T) {
	for _, o := range orders {
		nested := &writer{order: o.order}
		nested.u16(0)    // child count: zero
		nested.u16(0xAA) // garbage after the nested list

		body := &writer{order: o.order}
		body.u8(tagChildren)
		body.u16(uint16(len(nested.buf)))
		body.bytes(nested.buf)

		data := buildFile(o.order, 1<<tagChildren, body.buf)
		if _, err := Parse(data); err == nil {
			t.Fatalf("%s: expected trailing-bytes error", o.name)
		}
	}
}

// TestTruncation parses every proper prefix of a validly encoded file.
// Each prefix must fail cleanly with a *ParseError whose offset stays
// within the truncated input — never a panic, never a silent success.
func TestTruncation(t *testing.T) {
	for _, o := range orders {
		full := Encode(sampleFile(), o.order)
		if _, err := Parse(full); err != nil {
			t.Fatalf("%s: full input does not parse: %v", o.name, err)
		}
		for n := 0; n < len(full); n++ {
			_, err := Parse(full[:n])
			if err == nil {
				t.Fatalf("%s: truncated to %d/%d bytes: parse succeeded", o.name, n, len(full))
			}
			pe, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("%s: truncated to %d bytes: error is %T, want *ParseError", o.name, n, err)
			}
			if pe.Offset < 0 || pe.Offset > n {
				t.Fatalf("%s: truncated to %d bytes: error offset %d out of range", o.name, n, pe.Offset)
			}
		}
	}
}

// TestTruncationOffsetPinsNestedError verifies that an error deep inside
// a nested body reports the absolute file offset, not a relative one.
func TestTruncationOffsetPinsNestedError(t *testing.T) {
	full := Encode(sampleFile(), binary.BigEndian)
	// Find where the first "leaf" name lives; truncating just past the
	// nested header must report an offset beyond the top-level header.
	var lastErr error
	for n := len(full) - 1; n >= headerLen; n-- {
		if _, err := Parse(full[:n]); err != nil {
			lastErr = err
		}
	}
	pe, ok := lastErr.(*ParseError)
	if !ok {
		t.Fatalf("error is %T, want *ParseError", lastErr)
	}
	if pe.Offset < headerLen {
		t.Fatalf("offset %d points into the header, want a body offset", pe.Offset)
	}
}
