package wire

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

type Geo struct {
	Lat int64 `pb:"1,zigzag"`
	Lng int64 `pb:"2,zigzag"`
}

type Address struct {
	City string `pb:"1,string"`
	Zip  uint32 `pb:"2,fixed32"`
	Geo  *Geo   `pb:"3,message"`
}

type Person struct {
	ID      uint64   `pb:"1,varint"`
	Name    string   `pb:"2,string"`
	Active  bool     `pb:"3,varint"`
	Balance int64    `pb:"4,zigzag"`
	Stamp   uint64   `pb:"5,fixed64"`
	Addr    *Address `pb:"6,message"`
	Scores  []int64  `pb:"7,packed"`
	Tags    []string `pb:"8,string"`
	Blob    []byte   `pb:"9,bytes"`
}

func samplePerson() *Person {
	return &Person{
		ID:      1<<63 + 7,
		Name:    "Ada Lovelace",
		Active:  true,
		Balance: -9876543210,
		Stamp:   0xdeadbeefcafebabe,
		Addr: &Address{
			City: "Shanghai",
			Zip:  200001,
			Geo:  &Geo{Lat: 31120000, Lng: -121470000},
		},
		Scores: []int64{0, -1, 1, -2, 2, math.MinInt64, math.MaxInt64},
		Tags:   []string{"go", "protobuf", "lock-free"},
		Blob:   []byte{0xde, 0xad, 0xbe, 0xef},
	}
}

func TestReflectRoundTripNested(t *testing.T) {
	want := samplePerson()
	data, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Person
	if err := Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, &got) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, *want)
	}
}

func TestReflectTruncationSafety(t *testing.T) {
	data, err := Marshal(samplePerson())
	if err != nil {
		t.Fatal(err)
	}
	// Every strict prefix must either decode cleanly (when it happens to
	// end exactly on a field boundary) or fail with a truncation error —
	// never panic, never read out of bounds, never another error kind.
	truncations := 0
	for i := 1; i < len(data); i++ {
		var p Person
		err := Unmarshal(data[:i], &p)
		if err == nil { // landed exactly on a field boundary: valid
			continue
		}
		if !IsTruncated(err) {
			t.Fatalf("prefix %d/%d: want TruncatedError, got %v", i, len(data), err)
		}
		truncations++
	}
	if truncations == 0 {
		t.Fatal("expected at least one truncated prefix")
	}
}

func TestReflectWireTypeMismatch(t *testing.T) {
	// Field 1 of Person is varint; send it as fixed64 instead.
	buf := NewEncoder().Fixed64(1, 42).Bytes()
	var p Person
	err := Unmarshal(buf, &p)
	if !errors.Is(err, ErrBadWireType) {
		t.Fatalf("want ErrBadWireType, got %v", err)
	}
}

func TestReflectUnknownFieldsSkipped(t *testing.T) {
	data, err := Marshal(samplePerson())
	if err != nil {
		t.Fatal(err)
	}
	// Append unknown fields of every wire type.
	e := NewEncoder()
	e.buf = append(e.buf, data...)
	e.Varint(100, 12345).Fixed32(101, 7).Fixed64(102, 8).String(103, "ghost")
	var p Person
	if err := Unmarshal(e.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(samplePerson(), &p) {
		t.Fatal("unknown fields corrupted decode")
	}
}

func TestReflectRepeatedMessage(t *testing.T) {
	type Item struct {
		SKU uint64 `pb:"1,varint"`
	}
	type Cart struct {
		Items  []*Item `pb:"1,message"`
		Counts []int32 `pb:"2,packed"`
	}
	want := &Cart{
		Items:  []*Item{{SKU: 1}, {SKU: 2}, {SKU: math.MaxUint64}},
		Counts: []int32{-3, 0, 42},
	}
	data, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Cart
	if err := Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, &got) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestReflectBadTarget(t *testing.T) {
	var notStruct int
	if err := Unmarshal(nil, &notStruct); err == nil {
		t.Fatal("want error for non-struct target")
	}
	if err := Unmarshal(nil, nil); err == nil {
		t.Fatal("want error for nil target")
	}
}

func TestReflectOverflowGuard(t *testing.T) {
	type Small struct {
		V uint32 `pb:"1,varint"`
	}
	// 2^32 does not fit in uint32.
	buf := NewEncoder().Varint(1, 1<<32).Bytes()
	var s Small
	if err := Unmarshal(buf, &s); err == nil {
		t.Fatal("want overflow error")
	}
}

func TestReflectZeroValuesOmitted(t *testing.T) {
	data, err := Marshal(&Person{})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("zero struct should encode to 0 bytes, got % x", data)
	}
	var p Person
	if err := Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
}
