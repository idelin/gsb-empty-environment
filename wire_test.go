package wire

import (
	"encoding/binary"
	"strings"
	"testing"
)

// personV1: 旧版本 schema，只认识编号 1、2。
func personV1() *Schema {
	s := NewSchema("person", 1)
	s.MustAdd(FieldDef{Num: 1, Type: TString, Required: true})
	s.MustAdd(FieldDef{Num: 2, Type: TUint64})
	return s
}

// personV2: 新增可选字段 3、4，并保留已删除编号 5。
func personV2() *Schema {
	s := NewSchema("person", 2)
	s.MustAdd(FieldDef{Num: 1, Type: TString, Required: true})
	s.MustAdd(FieldDef{Num: 2, Type: TUint64})
	s.MustAdd(FieldDef{Num: 3, Type: TBool})  // 新增可选
	s.MustAdd(FieldDef{Num: 4, Type: TBytes}) // 新增可选
	_ = s.Reserve(5, "legacy nickname, removed in v2")
	return s
}

func frame(version uint32, fields ...[]byte) []byte {
	out := append([]byte(nil), magic...)
	out = binary.AppendUvarint(out, uint64(version))
	for _, f := range fields {
		out = append(out, f...)
	}
	return out
}

func varintField(num int, v uint64) []byte {
	return binary.AppendUvarint(appendTag(nil, num, wtVarint), v)
}

func bytesField(num int, payload []byte) []byte {
	b := appendTag(nil, num, wtBytes)
	b = binary.AppendUvarint(b, uint64(len(payload)))
	return append(b, payload...)
}

func TestRoundTrip(t *testing.T) {
	s := NewSchema("all-types", 1)
	s.MustAdd(FieldDef{Num: 1, Type: TUint64, Required: true})
	s.MustAdd(FieldDef{Num: 2, Type: TInt64})
	s.MustAdd(FieldDef{Num: 3, Type: TBool})
	s.MustAdd(FieldDef{Num: 4, Type: TString})
	s.MustAdd(FieldDef{Num: 5, Type: TBytes})

	m := NewMessage(s)
	for _, kv := range [][2]any{
		{1, uint64(1<<60 + 7)}, {2, int64(-123456)}, {3, true},
		{4, "你好, stable wire"}, {5, []byte{0xde, 0xad, 0xbe, 0xef}},
	} {
		if err := m.Set(kv[0].(int), kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	data, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(s, data)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetUint64(1) != 1<<60+7 {
		t.Errorf("uint64 mismatch: %d", got.GetUint64(1))
	}
	if got.GetInt64(2) != -123456 {
		t.Errorf("int64 mismatch: %d", got.GetInt64(2))
	}
	if !got.GetBool(3) {
		t.Error("bool mismatch")
	}
	if got.GetString(4) != "你好, stable wire" {
		t.Errorf("string mismatch: %q", got.GetString(4))
	}
	if b, _ := got.Get(5); string(b.([]byte)) != string([]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Errorf("bytes mismatch: %x", b)
	}
}

// 旧程序(v1 schema)读取新数据后转存，新字段不能丢。
func TestUnknownFieldsSurviveResave(t *testing.T) {
	v2 := personV2()
	src := NewMessage(v2)
	src.Set(1, "alice")
	src.Set(2, uint64(30))
	src.Set(3, true)
	src.Set(4, []byte("avatar-png"))
	data, err := src.Encode()
	if err != nil {
		t.Fatal(err)
	}

	// 旧程序：v1 schema 解码，3、4 是未知字段。
	old, err := Decode(personV1(), data)
	if err != nil {
		t.Fatal(err)
	}
	if !old.Diag.NewerData {
		t.Error("expected NewerData diagnostic")
	}
	if len(old.Diag.UnknownFieldNums) != 2 {
		t.Errorf("expected 2 unknown fields, got %v", old.Diag.UnknownFieldNums)
	}
	resaved, err := old.Encode()
	if err != nil {
		t.Fatal(err)
	}

	// 新程序再读：新字段完整无损。
	back, err := Decode(v2, resaved)
	if err != nil {
		t.Fatal(err)
	}
	if !back.GetBool(3) {
		t.Error("field 3 lost after old-program resave")
	}
	if b, _ := back.Get(4); string(b.([]byte)) != "avatar-png" {
		t.Errorf("field 4 lost after old-program resave: %q", b)
	}
	if back.GetString(1) != "alice" || back.GetUint64(2) != 30 {
		t.Error("known fields corrupted")
	}
}

// 新增可选字段向后兼容：v1 写的数据，v2 正常解码。
func TestNewOptionalFieldCompatible(t *testing.T) {
	old := NewMessage(personV1())
	old.Set(1, "bob")
	data, _ := old.Encode()
	got, err := Decode(personV2(), data)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetString(1) != "bob" {
		t.Error("v1 data not readable by v2 schema")
	}
	if got.Diag.NewerData {
		t.Error("v1 data should not be flagged as newer")
	}
}

// 缺必填字段：报错并给出具体编号。
func TestMissingRequiredReportsNumber(t *testing.T) {
	s := NewSchema("req", 1)
	s.MustAdd(FieldDef{Num: 7, Type: TString, Required: true})
	s.MustAdd(FieldDef{Num: 9, Type: TUint64, Required: true})
	s.MustAdd(FieldDef{Num: 2, Type: TBool})

	data := frame(1, varintField(2, 1)) // 只有可选字段
	_, err := Decode(s, data)
	nums, ok := IsMissingRequired(err)
	if !ok {
		t.Fatalf("expected MissingRequiredError, got %v", err)
	}
	if len(nums) != 2 || nums[0] != 7 || nums[1] != 9 {
		t.Errorf("expected missing [7 9], got %v", nums)
	}
}

// 同一字段号在线上出现两种线类型：拒绝。
func TestConflictingWireTypesRejected(t *testing.T) {
	s := NewSchema("dup", 1)
	s.MustAdd(FieldDef{Num: 1, Type: TUint64})
	data := frame(1, varintField(1, 5), bytesField(1, []byte("x")))
	_, err := Decode(s, data)
	if err == nil || !strings.Contains(err.Error(), "conflicting wire types") {
		t.Fatalf("expected conflict rejection, got %v", err)
	}
}

// schema 内重复编号定义：拒绝。
func TestDuplicateFieldNumberRejected(t *testing.T) {
	s := NewSchema("dupdef", 1)
	s.MustAdd(FieldDef{Num: 1, Type: TString})
	if err := s.AddField(FieldDef{Num: 1, Type: TUint64}); err == nil {
		t.Fatal("expected duplicate field number rejection")
	}
}

// 删除的编号被保留，禁止复用为不相关类型。
func TestReservedNumberNotReusable(t *testing.T) {
	s := personV2()
	err := s.AddField(FieldDef{Num: 5, Type: TBytes})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved-number rejection, got %v", err)
	}
}

// 先验证长度再分配：声明长度超过剩余输入即报错。
func TestLengthCheckedBeforeAllocation(t *testing.T) {
	s := NewSchema("len", 1)
	s.MustAdd(FieldDef{Num: 1, Type: TBytes})

	// 声明 1000 字节，实际只剩 3 字节。
	bad := appendTag(nil, 1, wtBytes)
	bad = binary.AppendUvarint(bad, 1000)
	bad = append(bad, 1, 2, 3)
	_, err := Decode(s, frame(1, bad))
	if err == nil || !strings.Contains(err.Error(), "exceeds remaining input") {
		t.Fatalf("expected length-vs-remaining rejection, got %v", err)
	}

	// 声明长度超过 MaxFieldLen。
	huge := appendTag(nil, 1, wtBytes)
	huge = binary.AppendUvarint(huge, MaxFieldLen+1)
	_, err = Decode(s, frame(1, huge))
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected max-length rejection, got %v", err)
	}
}

// 版本诊断：新数据 + 未知字段应出现在诊断里。
func TestVersionDiagnostics(t *testing.T) {
	data := frame(3, bytesField(1, []byte("x")), varintField(42, 7))
	got, err := Decode(personV1(), data)
	if err != nil {
		t.Fatal(err)
	}
	d := got.Diag
	if d.DataVersion != 3 || d.SchemaVersion != 1 || !d.NewerData {
		t.Errorf("bad version diagnostics: %+v", d)
	}
	if len(d.UnknownFieldNums) != 1 || d.UnknownFieldNums[0] != 42 {
		t.Errorf("expected unknown field 42, got %v", d.UnknownFieldNums)
	}
	if len(d.Notes) == 0 {
		t.Error("expected human-readable note about newer data")
	}
}

func TestTruncatedAndCorruptInput(t *testing.T) {
	s := personV1()
	good := NewMessage(personV1())
	good.Set(1, "alice")
	data, _ := good.Encode()

	if _, err := Decode(s, data[:len(data)-2]); err == nil {
		t.Error("truncated input should fail")
	}
	if _, err := Decode(s, []byte("nope")); err == nil {
		t.Error("bad magic should fail")
	}
	// 未知线类型。
	if _, err := Decode(s, frame(1, append(appendTag(nil, 3, 7), 0))); err == nil {
		t.Error("unsupported wire type should fail")
	}
	// 字段号 0 非法。
	if _, err := Decode(s, frame(1, varintField(0, 1))); err == nil {
		t.Error("field number 0 should fail")
	}
}

func TestSetTypeMismatch(t *testing.T) {
	m := NewMessage(personV1())
	if err := m.Set(1, 42); err == nil {
		t.Error("Set with wrong Go type should fail")
	}
	if err := m.Set(99, "x"); err == nil {
		t.Error("Set on undefined field should fail")
	}
}
