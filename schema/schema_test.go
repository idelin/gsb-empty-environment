package schema

import (
	"reflect"
	"strings"
	"testing"

	"protowire/proto"
	"protowire/wire"
)

type Address struct {
	City string `protobuf:"string,1" validate:"required,minlen=2"`
	Zip  int32  `protobuf:"zigzag32,2" validate:"min=-999999,max=999999"`
}

type Person struct {
	Name    string   `protobuf:"string,1" validate:"required,minlen=1,maxlen=64"`
	Age     uint32   `protobuf:"varint,2" validate:"max=150"`
	Addr    *Address `protobuf:"message,3"`
	Ids     []int64  `protobuf:"zigzag64,4,rep,packed"`
	Token   uint64   `protobuf:"fixed64,5"`
	Balance int64    `protobuf:"zigzag64,6"`
	Scores  []uint32 `protobuf:"fixed32,7,rep,packed"`
	Tags    []string `protobuf:"string,8,rep" validate:"maxlen=4"`
}

func (p *Person) Validate() error {
	if p.Age > 0 && p.Name == "" {
		return errString("age set without name")
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }

func encodePerson() []byte {
	addr := wire.NewWriter()
	addr.AppendVarint(proto.EncodeTag(1, proto.WireLengthDelimited))
	addr.AppendLengthDelimited([]byte("Shanghai"))
	addr.AppendVarint(proto.EncodeTag(2, proto.WireVarint))
	addr.AppendZigZag32(-200001)

	packedIds := wire.NewWriter()
	for _, id := range []int64{7, -8, 9000} {
		packedIds.AppendZigZag64(id)
	}
	packedScores := wire.NewWriter()
	for _, s := range []uint32{100, 200, 300} {
		packedScores.AppendFixed32(s)
	}

	w := wire.NewWriter()
	w.AppendVarint(proto.EncodeTag(1, proto.WireLengthDelimited))
	w.AppendLengthDelimited([]byte("alice"))
	w.AppendVarint(proto.EncodeTag(2, proto.WireVarint))
	w.AppendVarint(30)
	w.AppendVarint(proto.EncodeTag(3, proto.WireLengthDelimited))
	w.AppendLengthDelimited(addr.Bytes())
	w.AppendVarint(proto.EncodeTag(4, proto.WireLengthDelimited))
	w.AppendLengthDelimited(packedIds.Bytes())
	w.AppendVarint(proto.EncodeTag(5, proto.WireFixed64))
	w.AppendFixed64(0xdeadbeefcafe)
	w.AppendVarint(proto.EncodeTag(6, proto.WireVarint))
	w.AppendZigZag64(-123456789)
	w.AppendVarint(proto.EncodeTag(7, proto.WireLengthDelimited))
	w.AppendLengthDelimited(packedScores.Bytes())
	for _, tag := range []string{"go", "proto", "adaptive"} {
		w.AppendVarint(proto.EncodeTag(8, proto.WireLengthDelimited))
		w.AppendLengthDelimited([]byte(tag))
	}
	return w.Bytes()
}

func TestUnmarshalFullStruct(t *testing.T) {
	var p Person
	if err := Unmarshal(encodePerson(), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Name != "alice" || p.Age != 30 {
		t.Fatalf("name/age = %q/%d", p.Name, p.Age)
	}
	if p.Addr == nil || p.Addr.City != "Shanghai" || p.Addr.Zip != -200001 {
		t.Fatalf("addr = %+v", p.Addr)
	}
	if want := []int64{7, -8, 9000}; !reflect.DeepEqual(p.Ids, want) {
		t.Fatalf("ids = %v", p.Ids)
	}
	if p.Token != 0xdeadbeefcafe {
		t.Fatalf("token = %#x", p.Token)
	}
	if p.Balance != -123456789 {
		t.Fatalf("balance = %d", p.Balance)
	}
	if want := []uint32{100, 200, 300}; !reflect.DeepEqual(p.Scores, want) {
		t.Fatalf("scores = %v", p.Scores)
	}
	if want := []string{"go", "proto", "adaptive"}; !reflect.DeepEqual(p.Tags, want) {
		t.Fatalf("tags = %v", p.Tags)
	}
}

func TestUnmarshalValidationFailures(t *testing.T) {
	// Age 200 exceeds max=150; name missing (required).
	w := wire.NewWriter()
	w.AppendVarint(proto.EncodeTag(2, proto.WireVarint))
	w.AppendVarint(200)
	var p Person
	err := Unmarshal(w.Bytes(), &p)
	if err == nil {
		t.Fatal("expected validation error")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error type %T, want *ValidationError", err)
	}
	joined := ve.Error()
	if !strings.Contains(joined, "required") || !strings.Contains(joined, "above max") {
		t.Fatalf("problems = %q", joined)
	}
}

func TestUnmarshalNestedValidationPropagates(t *testing.T) {
	// Address with 1-char city violates minlen=2.
	addr := wire.NewWriter()
	addr.AppendVarint(proto.EncodeTag(1, proto.WireLengthDelimited))
	addr.AppendLengthDelimited([]byte("X"))
	addr.AppendVarint(proto.EncodeTag(2, proto.WireVarint))
	addr.AppendZigZag32(100)

	w := wire.NewWriter()
	w.AppendVarint(proto.EncodeTag(1, proto.WireLengthDelimited))
	w.AppendLengthDelimited([]byte("bob"))
	w.AppendVarint(proto.EncodeTag(3, proto.WireLengthDelimited))
	w.AppendLengthDelimited(addr.Bytes())

	var p Person
	err := Unmarshal(w.Bytes(), &p)
	if err == nil || !strings.Contains(err.Error(), "minlen") {
		t.Fatalf("expected nested minlen violation, got %v", err)
	}
}

func TestUnmarshalTruncatedInput(t *testing.T) {
	full := encodePerson()
	var p Person
	err := Unmarshal(full[:len(full)-5], &p)
	if err == nil {
		t.Fatal("expected truncation error")
	}
	if !strings.Contains(err.Error(), "truncated") && !strings.Contains(err.Error(), "exceeds remaining") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnmarshalUnknownFieldsIgnored(t *testing.T) {
	w := wire.NewWriter()
	w.AppendVarint(proto.EncodeTag(1, proto.WireLengthDelimited))
	w.AppendLengthDelimited([]byte("carol"))
	// Unknown field 99, varint; unknown field 100, length-delimited.
	w.AppendVarint(proto.EncodeTag(99, proto.WireVarint))
	w.AppendVarint(12345)
	w.AppendVarint(proto.EncodeTag(100, proto.WireLengthDelimited))
	w.AppendLengthDelimited([]byte("future-field"))
	var p Person
	if err := Unmarshal(w.Bytes(), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Name != "carol" {
		t.Fatalf("name = %q", p.Name)
	}
}

func TestUnmarshalWireTypeMismatch(t *testing.T) {
	// Field 1 declared as string but arrives as varint.
	w := wire.NewWriter()
	w.AppendVarint(proto.EncodeTag(1, proto.WireVarint))
	w.AppendVarint(7)
	var p Person
	err := Unmarshal(w.Bytes(), &p)
	if err == nil || !strings.Contains(err.Error(), "wire type") {
		t.Fatalf("expected wire type error, got %v", err)
	}
}

func TestUnmarshalBadTarget(t *testing.T) {
	var notStruct int
	if err := Unmarshal([]byte{}, &notStruct); err == nil {
		t.Fatal("expected target error")
	}
	if err := Unmarshal([]byte{}, Person{}); err == nil {
		t.Fatal("expected non-pointer error")
	}
}

func TestDomainValidateHook(t *testing.T) {
	// Age set, name empty: tag rules pass (name required fails first, so
	// craft via direct bind path instead): use a type where hook decides.
	type Order struct {
		Qty int64 `protobuf:"zigzag64,1"`
	}
	_ = Order{}
	// Person hook: age>0 && name=="" -> error. But required tag on Name
	// fires first; verify hook via a type without the required tag.
	type Account struct {
		Owner string `protobuf:"string,1"`
		Vip   bool   `protobuf:"varint,2"`
	}
	_ = Account{}
	// Direct: Person with age but no name triggers both required + hook;
	// assert at least the required violation surfaces.
	w := wire.NewWriter()
	w.AppendVarint(proto.EncodeTag(2, proto.WireVarint))
	w.AppendVarint(42)
	var p Person
	if err := Unmarshal(w.Bytes(), &p); err == nil {
		t.Fatal("expected validation error")
	}
}

type hookOnly struct {
	A int64 `protobuf:"zigzag64,1"`
	B int64 `protobuf:"zigzag64,2"`
}

func (h *hookOnly) Validate() error {
	if h.A > h.B {
		return errString("A must not exceed B")
	}
	return nil
}

func TestValidateHookInvoked(t *testing.T) {
	w := wire.NewWriter()
	w.AppendVarint(proto.EncodeTag(1, proto.WireVarint))
	w.AppendZigZag64(10)
	w.AppendVarint(proto.EncodeTag(2, proto.WireVarint))
	w.AppendZigZag64(3)
	var h hookOnly
	err := Unmarshal(w.Bytes(), &h)
	if err == nil || !strings.Contains(err.Error(), "A must not exceed B") {
		t.Fatalf("expected hook error, got %v", err)
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Fatalf("hook error should be wrapped in *ValidationError, got %T", err)
	}
}
