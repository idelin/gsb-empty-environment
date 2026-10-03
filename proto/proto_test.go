package proto

import (
	"reflect"
	"strings"
	"testing"
)

// Three-level nested message tree with packed repeated fields.
type GrandChild struct {
	Score int64 `wire:"1,zigzag" check:"min=-100,max=100"`
}

type Child struct {
	Name  string      `wire:"1,bytes" check:"required,minlen=1,maxlen=16"`
	Fixed uint32      `wire:"2,fixed32"`
	GC    *GrandChild `wire:"3,bytes"`
}

type Parent struct {
	ID       uint64   `wire:"1,varint" check:"required,min=1"`
	Delta    int64    `wire:"2,zigzag"`
	Stamp    uint64   `wire:"3,fixed64"`
	Ratio    float64  `wire:"4,fixed64"`
	Active   bool     `wire:"5,varint"`
	Tags     []string `wire:"6,bytes" check:"maxlen=4"`
	Nums     []uint64 `wire:"7,varint,packed"`
	ZNums    []int64  `wire:"8,zigzag,packed"`
	Child    *Child   `wire:"9,bytes"`
	Children []*Child `wire:"10,bytes"`
	Raw      []byte   `wire:"11,bytes"`
}

func sample() *Parent {
	return &Parent{
		ID:     12345678901234,
		Delta:  -987654321,
		Stamp:  0xdeadbeefcafebabe,
		Ratio:  3.141592653589793,
		Active: true,
		Tags:   []string{"alpha", "beta", "gamma"},
		Nums:   []uint64{0, 1, 127, 128, 300, 1 << 40},
		ZNums:  []int64{0, -1, 1, -2, 2, -123456789},
		Child: &Child{
			Name:  "primary",
			Fixed: 42,
			GC:    &GrandChild{Score: -55},
		},
		Children: []*Child{
			{Name: "c1", Fixed: 1, GC: &GrandChild{Score: 10}},
			{Name: "c2", Fixed: 2, GC: &GrandChild{Score: -20}},
		},
		Raw: []byte{0x00, 0x01, 0xfe, 0xff},
	}
}

func TestNestedRoundTrip(t *testing.T) {
	want := sample()
	data, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var got Parent
	if err := Decode(data, &got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(want, &got) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", &got, want)
	}
}

func TestTruncationAtEveryByte(t *testing.T) {
	data, err := Encode(sample())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for cut := 0; cut < len(data); cut++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("panic decoding %d-byte truncation: %v", cut, rec)
				}
			}()
			// Safety property: no panic and no out-of-bounds read for any
			// truncation. A cut exactly on a field boundary is a legal
			// prefix and may decode successfully.
			var p Parent
			_ = Decode(data[:cut], &p)
		}()
	}
}

func TestMidFieldTruncationReportsError(t *testing.T) {
	data, err := Encode(sample())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Cutting the final byte of the packed []byte field (Raw, field 11,
	// the last field written) must be reported as an error, proving the
	// bounds guard fires inside nested length-delimited payloads.
	var p Parent
	if err := Decode(data[:len(data)-1], &p); err == nil {
		t.Fatal("expected error for mid-field truncation")
	}
}

func TestUnknownFieldsSkipped(t *testing.T) {
	data, err := Encode(sample())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Append unknown field 99 (varint, value 42) and field 100
	// (length-delimited, "xyz"). Tag 99<<3|0 = 792 -> 0x98 0x06;
	// tag 100<<3|2 = 802 -> 0xA2 0x0C.
	extra := []byte{0x98, 0x06, 0x2A, 0xA2, 0x0C, 0x03, 'x', 'y', 'z'}
	var p Parent
	if err := Decode(append(data, extra...), &p); err != nil {
		t.Fatalf("Decode with unknown fields: %v", err)
	}
	if p.ID != 12345678901234 || len(p.Nums) != 6 {
		t.Fatalf("known fields corrupted by unknown fields: %+v", p)
	}
}

func TestPackedVsUnpacked(t *testing.T) {
	// A packed field must also decode when sent as individual values and
	// vice versa is not required, but packed output must be length-delimited.
	data, err := Encode(sample())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// field 7 (Nums, packed) should appear as wire type 2
	found := false
	for i := 0; i+1 < len(data); i++ {
		if data[i] == 0x3A { // tag 7, wire type 2
			found = true
		}
	}
	if !found {
		t.Fatal("packed field not encoded as length-delimited")
	}
}

func TestValidateOK(t *testing.T) {
	if err := Validate(sample()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateViolations(t *testing.T) {
	bad := sample()
	bad.ID = 0                                   // required + min=1
	bad.Child.Name = ""                          // required + minlen=1
	bad.Child.GC.Score = 500                     // max=100
	bad.Tags = []string{"a", "b", "c", "d", "e"} // maxlen=4

	err := Validate(bad)
	if err == nil {
		t.Fatal("expected validation error")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	joined := ve.Error()
	for _, want := range []string{"Parent.ID", "Parent.Child.Name", "Parent.Child.GC.Score", "Parent.Tags"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("violation for %s missing in %q", want, joined)
		}
	}
}

func TestValidateNilNestedPointer(t *testing.T) {
	p := sample()
	p.Child = nil // optional, no required rule on Child itself
	if err := Validate(p); err != nil {
		t.Fatalf("Validate with nil optional child: %v", err)
	}
}

func TestDecodeRejectsNonPointer(t *testing.T) {
	var p Parent
	if err := Decode([]byte{0x08, 0x01}, p); err == nil {
		t.Fatal("expected error for non-pointer decode target")
	}
}
