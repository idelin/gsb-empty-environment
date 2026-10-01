package wire

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// Deeply nested schema: Person -> Address (nested message), packed scores,
// repeated strings, zigzag signed, fixed fields, required fields.
type Address struct {
	City  string `protobuf:"1,req"`
	Zip   uint32 `protobuf:"2,varint"`
	Geo   *Geo   `protobuf:"3"`
	Extra []byte `protobuf:"4"`
}

type Geo struct {
	Lat float64 `protobuf:"1,fixed64"`
	Lng float64 `protobuf:"2,fixed64"`
}

type Person struct {
	Name    string   `protobuf:"1,req"`
	ID      int32    `protobuf:"2,zigzag,req"`
	Email   []string `protobuf:"3"`
	Scores  []int32  `protobuf:"4,zigzag,packed"`
	Lucky   uint64   `protobuf:"5,varint"`
	Addr    *Address `protobuf:"6,req"`
	Active  bool     `protobuf:"7,varint"`
	Ratio   float32  `protobuf:"8,fixed32"`
	Aliases []Alias  `protobuf:"9"`
}

type Alias struct {
	Nick string `protobuf:"1,req"`
}

// encodePerson hand-builds the wire stream for a Person, field by field.
func encodePerson() []byte {
	var e Encoder
	// 1: Name (required)
	e.Tag(1, WTBytes)
	e.AppendBytes([]byte("alice"))
	// 2: ID zigzag(-42)
	e.Tag(2, WTVarint)
	e.ZigZag(-42)
	// 3: repeated Email (unpacked)
	for _, m := range []string{"a@x.io", "b@y.io"} {
		e.Tag(3, WTBytes)
		e.AppendBytes([]byte(m))
	}
	// 4: packed sint32 Scores {7, -3, 100}
	var p Encoder
	for _, s := range []int32{7, -3, 100} {
		p.ZigZag(int64(s))
	}
	e.Tag(4, WTBytes)
	e.AppendBytes(p.Bytes())
	// 5: Lucky
	e.Tag(5, WTVarint)
	e.Varint(1<<60 + 5)
	// 6: nested Address (required), itself containing nested Geo
	var a Encoder
	a.Tag(1, WTBytes)
	a.AppendBytes([]byte("shanghai"))
	a.Tag(2, WTVarint)
	a.Varint(200000)
	var g Encoder
	g.Tag(1, WTFixed64)
	g.Fixed64(math.Float64bits(121.47))
	g.Tag(2, WTFixed64)
	g.Fixed64(math.Float64bits(30.32))
	a.Tag(3, WTBytes)
	a.AppendBytes(g.Bytes())
	a.Tag(4, WTBytes)
	a.AppendBytes([]byte{0xde, 0xad})
	e.Tag(6, WTBytes)
	e.AppendBytes(a.Bytes())
	// 7: Active
	e.Tag(7, WTVarint)
	e.Varint(1)
	// 8: Ratio
	e.Tag(8, WTFixed32)
	e.Fixed32(0x3fc00000) // 1.5
	// 9: repeated nested Alias messages
	for _, n := range []string{"al", "ali"} {
		var al Encoder
		al.Tag(1, WTBytes)
		al.AppendBytes([]byte(n))
		e.Tag(9, WTBytes)
		e.AppendBytes(al.Bytes())
	}
	// 99: unknown field, must be skipped
	e.Tag(99, WTVarint)
	e.Varint(12345)
	return e.Bytes()
}

func TestUnmarshalNestedPacked(t *testing.T) {
	var p Person
	if err := Unmarshal(encodePerson(), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Name != "alice" || p.ID != -42 || !p.Active {
		t.Fatalf("scalars: %+v", p)
	}
	if !reflect.DeepEqual(p.Email, []string{"a@x.io", "b@y.io"}) {
		t.Fatalf("emails: %v", p.Email)
	}
	if !reflect.DeepEqual(p.Scores, []int32{7, -3, 100}) {
		t.Fatalf("packed scores: %v", p.Scores)
	}
	if p.Lucky != 1<<60+5 {
		t.Fatalf("lucky: %d", p.Lucky)
	}
	if p.Addr == nil || p.Addr.City != "shanghai" || p.Addr.Zip != 200000 {
		t.Fatalf("addr: %+v", p.Addr)
	}
	if p.Addr.Geo == nil || p.Addr.Geo.Lat != 121.47 || p.Addr.Geo.Lng != 30.32 {
		t.Fatalf("geo: %+v", p.Addr.Geo)
	}
	if !reflect.DeepEqual(p.Addr.Extra, []byte{0xde, 0xad}) {
		t.Fatalf("extra: %x", p.Addr.Extra)
	}
	if p.Ratio != 1.5 {
		t.Fatalf("ratio: %v", p.Ratio)
	}
	if len(p.Aliases) != 2 || p.Aliases[0].Nick != "al" || p.Aliases[1].Nick != "ali" {
		t.Fatalf("aliases: %+v", p.Aliases)
	}
}

func TestUnmarshalTruncationSweep(t *testing.T) {
	// Cut the stream at every possible offset: decoding must never panic
	// and never read out of bounds. A prefix ending exactly on a field
	// boundary may legitimately decode; anything cut mid-field must error.
	full := encodePerson()
	for n := 0; n < len(full); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix %d/%d panicked: %v", n, len(full), r)
				}
			}()
			var p Person
			_ = Unmarshal(full[:n], &p)
		}()
	}
	// Cutting the final byte lands inside the trailing varint field and
	// must be reported as truncation.
	var p Person
	if err := Unmarshal(full[:len(full)-1], &p); err != ErrTruncated {
		t.Fatalf("expected ErrTruncated for mid-field cut, got %v", err)
	}
}

func TestValidationRequired(t *testing.T) {
	// Drop Name (field 1) and Addr (field 6) from the stream.
	var e Encoder
	e.Tag(2, WTVarint)
	e.ZigZag(7)
	var p Person
	err := Unmarshal(e.Bytes(), &p)
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	joined := strings.Join(ve.Missing, ",")
	if !strings.Contains(joined, "Name") || !strings.Contains(joined, "Addr") {
		t.Fatalf("missing fields not reported: %v", ve.Missing)
	}
}

func TestValidationNestedRequired(t *testing.T) {
	// Address is present but its required City is missing.
	var e Encoder
	e.Tag(1, WTBytes)
	e.AppendBytes([]byte("bob"))
	e.Tag(2, WTVarint)
	e.ZigZag(1)
	var a Encoder
	a.Tag(2, WTVarint)
	a.Varint(100)
	e.Tag(6, WTBytes)
	e.AppendBytes(a.Bytes())
	var p Person
	err := Unmarshal(e.Bytes(), &p)
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	found := false
	for _, m := range ve.Missing {
		if m == "Address.City" {
			found = true
		}
	}
	if !found {
		t.Fatalf("nested required field not reported: %v", ve.Missing)
	}
}

func TestUnpackedRepeatedAccepted(t *testing.T) {
	// Scores sent as unpacked varints must still decode into the slice.
	var e Encoder
	e.Tag(1, WTBytes)
	e.AppendBytes([]byte("x"))
	e.Tag(2, WTVarint)
	e.ZigZag(0)
	e.Tag(6, WTBytes)
	var a Encoder
	a.Tag(1, WTBytes)
	a.AppendBytes([]byte("c"))
	e.AppendBytes(a.Bytes())
	for _, s := range []int32{5, 6, 7} {
		e.Tag(4, WTVarint)
		e.ZigZag(int64(s))
	}
	var p Person
	if err := Unmarshal(e.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Scores, []int32{5, 6, 7}) {
		t.Fatalf("unpacked scores: %v", p.Scores)
	}
}

func TestWireTypeMismatch(t *testing.T) {
	var e Encoder
	e.Tag(1, WTVarint) // Name is a string (WTBytes expected)
	e.Varint(1)
	var p Person
	if err := Unmarshal(e.Bytes(), &p); err != ErrBadWireType {
		t.Fatalf("expected ErrBadWireType, got %v", err)
	}
}

func TestUnmarshalBadTarget(t *testing.T) {
	var notStruct int
	if err := Unmarshal(nil, &notStruct); err == nil {
		t.Fatal("expected error for non-struct target")
	}
	if err := Unmarshal(nil, Person{}); err == nil {
		t.Fatal("expected error for non-pointer target")
	}
}
