package wirebuf

import (
	"reflect"
	"testing"
)

// buildTestPayload encodes a 3-level nested message with a packed
// repeated field (same layout as cmd/demo):
//
//	Envelope{ id=1 varint, delta=2 zigzag, stamp=3 fixed64,
//	          flags=4 fixed32, inner=5 msg, scores=6 packed }
//	Inner{ name=1 bytes, core=2 msg }
//	Core{ level=1 zigzag }
func buildTestPayload() []byte {
	core := AppendTag(nil, 1, WireVarint)
	core = AppendZigZag32(core, -7)

	inner := AppendTag(nil, 1, WireBytes)
	inner = AppendBytes(inner, []byte("nested-ok"))
	inner = AppendTag(inner, 2, WireBytes)
	inner = AppendBytes(inner, core)

	var packed []byte
	for _, v := range []uint64{3, 1, 4, 1, 5, 9, 2, 6} {
		packed = AppendVarint(packed, v)
	}

	buf := AppendTag(nil, 1, WireVarint)
	buf = AppendVarint(buf, 9001)
	buf = AppendTag(buf, 2, WireVarint)
	buf = AppendZigZag64(buf, -123456)
	buf = AppendTag(buf, 3, WireFixed64)
	buf = AppendFixed64(buf, 0xDEADBEEFCAFEBABE)
	buf = AppendTag(buf, 4, WireFixed32)
	buf = AppendFixed32(buf, 0x7F)
	buf = AppendTag(buf, 5, WireBytes)
	buf = AppendBytes(buf, inner)
	buf = AppendTag(buf, 6, WireBytes)
	buf = AppendBytes(buf, packed)
	return buf
}

func decodeTestPayload(t *testing.T) *Message {
	t.Helper()
	m := AcquireMessage()
	if err := Decode(buildTestPayload(), m); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return m
}

func TestParseNestedMessage(t *testing.T) {
	m := decodeTestPayload(t)
	defer m.Release()

	if got := len(m.Fields); got != 6 {
		t.Fatalf("top-level field count: got %d, want 6", got)
	}
	if v := m.Field(1).Varint; v != 9001 {
		t.Fatalf("id: got %d, want 9001", v)
	}
	if v := m.Field(2).ZigZag(); v != -123456 {
		t.Fatalf("delta: got %d, want -123456", v)
	}
	if v := m.Field(3).Fixed64; v != 0xDEADBEEFCAFEBABE {
		t.Fatalf("stamp: got %#x", v)
	}
	if v := m.Field(4).Fixed32; v != 0x7F {
		t.Fatalf("flags: got %#x", v)
	}

	inner, err := m.Field(5).DecodeNested()
	if err != nil {
		t.Fatalf("DecodeNested(inner): %v", err)
	}
	if got := string(inner.Field(1).Bytes); got != "nested-ok" {
		t.Fatalf("inner.name: got %q", got)
	}
	core, err := inner.Field(2).DecodeNested()
	if err != nil {
		t.Fatalf("DecodeNested(core): %v", err)
	}
	if v := core.Field(1).ZigZag(); v != -7 {
		t.Fatalf("core.level: got %d, want -7", v)
	}

	scores, err := m.Field(6).DecodePacked()
	if err != nil {
		t.Fatalf("DecodePacked: %v", err)
	}
	want := []uint64{3, 1, 4, 1, 5, 9, 2, 6}
	if !reflect.DeepEqual(scores, want) {
		t.Fatalf("scores: got %v, want %v", scores, want)
	}

	// Idempotent cached decode of nested/packed views.
	if again, _ := m.Field(5).DecodeNested(); again != inner {
		t.Fatal("DecodeNested not idempotent")
	}
	if again, _ := m.Field(6).DecodePacked(); &again[0] != &scores[0] {
		t.Fatal("DecodePacked not idempotent")
	}
}

func TestFieldAccessors(t *testing.T) {
	m := decodeTestPayload(t)
	defer m.Release()
	if m.Field(42) != nil {
		t.Fatal("Field(42) should be nil")
	}
	if got := len(m.FieldsByNumber(1)); got != 1 {
		t.Fatalf("FieldsByNumber(1): got %d, want 1", got)
	}
	if got := len(m.FieldsByNumber(42)); got != 0 {
		t.Fatalf("FieldsByNumber(42): got %d, want 0", got)
	}
}

func TestTruncationAtEveryOffset(t *testing.T) {
	payload := buildTestPayload()
	truncErrs := 0
	for n := 0; n < len(payload); n++ {
		m := AcquireMessage()
		err := Decode(payload[:n], m)
		m.Release()
		// A prefix ending exactly on a field boundary is itself a
		// valid message; any other prefix must fail with ErrTruncated
		// and must never panic or read out of bounds.
		if err == ErrTruncated {
			truncErrs++
		} else if err != nil {
			t.Fatalf("prefix %d: got %v, want nil or ErrTruncated", n, err)
		}
	}
	if truncErrs == 0 {
		t.Fatal("expected at least one truncated prefix")
	}
	// The full payload must decode cleanly.
	m := AcquireMessage()
	if err := Decode(payload, m); err != nil {
		t.Fatalf("full payload: %v", err)
	}
	m.Release()
	if !Balanced() {
		t.Fatal("pool imbalance after truncation sweep")
	}
}

func TestTruncatedNestedMessage(t *testing.T) {
	// Inner message payload chopped mid-field.
	inner := AppendTag(nil, 1, WireVarint)
	inner = AppendVarint(inner, 5)
	inner = inner[:len(inner)-1] // cut the varint value
	buf := AppendTag(nil, 1, WireBytes)
	buf = AppendBytes(buf, inner)

	m := AcquireMessage()
	defer m.Release()
	if err := Decode(buf, m); err != nil {
		t.Fatalf("outer decode: %v", err)
	}
	if _, err := m.Field(1).DecodeNested(); err != ErrTruncated {
		t.Fatalf("nested decode: got %v, want ErrTruncated", err)
	}
}

func TestTruncatedPackedField(t *testing.T) {
	// Packed payload whose final varint is cut off.
	packed := AppendVarint(nil, 1)
	packed = AppendVarint(packed, 2)
	packed = append(packed, 0x80) // dangling continuation byte
	buf := AppendTag(nil, 6, WireBytes)
	buf = AppendBytes(buf, packed)

	m := AcquireMessage()
	defer m.Release()
	if err := Decode(buf, m); err != nil {
		t.Fatalf("outer decode: %v", err)
	}
	if _, err := m.Field(6).DecodePacked(); err != ErrTruncated {
		t.Fatalf("packed decode: got %v, want ErrTruncated", err)
	}
}

func TestInvalidWireTypeInMessage(t *testing.T) {
	for _, wt := range []WireType{3, 4, 6, 7} {
		buf := AppendTag(nil, 1, wt)
		buf = append(buf, 0x00)
		m := AcquireMessage()
		err := Decode(buf, m)
		m.Release()
		if err != ErrInvalidWireType {
			t.Fatalf("wire type %d: got %v, want ErrInvalidWireType", wt, err)
		}
	}
}

func TestDecodeNestedWrongWireType(t *testing.T) {
	buf := AppendTag(nil, 1, WireVarint)
	buf = AppendVarint(buf, 1)
	m := AcquireMessage()
	defer m.Release()
	if err := Decode(buf, m); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Field(1).DecodeNested(); err != ErrInvalidWireType {
		t.Fatalf("got %v, want ErrInvalidWireType", err)
	}
	if _, err := m.Field(1).DecodePacked(); err != ErrInvalidWireType {
		t.Fatalf("got %v, want ErrInvalidWireType", err)
	}
}

func TestMessageReleaseResets(t *testing.T) {
	m := decodeTestPayload(t)
	if _, err := m.Field(5).DecodeNested(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Field(6).DecodePacked(); err != nil {
		t.Fatal(err)
	}
	m.Release()
	if len(m.Fields) != 0 {
		t.Fatalf("Fields not reset: %d", len(m.Fields))
	}
	// Re-acquire from the pool: must be empty and reusable.
	m2 := AcquireMessage()
	if len(m2.Fields) != 0 {
		t.Fatalf("pooled message not empty: %d fields", len(m2.Fields))
	}
	m2.Release()
	if !Balanced() {
		t.Fatal("pool imbalance after release cycle")
	}
}

func TestRepeatedFields(t *testing.T) {
	var buf []byte
	for _, v := range []uint64{10, 20, 30} {
		buf = AppendTag(buf, 7, WireVarint)
		buf = AppendVarint(buf, v)
	}
	m := AcquireMessage()
	defer m.Release()
	if err := Decode(buf, m); err != nil {
		t.Fatal(err)
	}
	fs := m.FieldsByNumber(7)
	if len(fs) != 3 {
		t.Fatalf("got %d repeated fields, want 3", len(fs))
	}
	for i, want := range []uint64{10, 20, 30} {
		if fs[i].Varint != want {
			t.Fatalf("field %d: got %d, want %d", i, fs[i].Varint, want)
		}
	}
}
