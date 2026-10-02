package tagunion

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

const (
	tagTransfer = 1
	tagPing     = 2
	tagProfile  = 3
)

// testRegistry 构造三标签、多版本的 schema 注册表与显式演进链。
func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(r.Register(Schema{Tag: tagTransfer, Version: 1, Fields: []FieldDef{
		{Name: "amount", Type: FUint},
	}}))
	must(r.Register(Schema{Tag: tagTransfer, Version: 2, Fields: []FieldDef{
		{Name: "amount", Type: FUint},
		{Name: "currency", Type: FString},
	}}))
	must(r.RegisterMigration(tagTransfer, 1, func(f map[string]any) (map[string]any, error) {
		f["currency"] = "USD"
		return f, nil
	}))
	must(r.Register(Schema{Tag: tagPing, Version: 1})) // 空分支
	must(r.Register(Schema{Tag: tagProfile, Version: 1, Fields: []FieldDef{
		{Name: "name", Type: FString},
		{Name: "age", Type: FUint},
		{Name: "score", Type: FInt},
		{Name: "admin", Type: FBool},
		{Name: "avatar", Type: FBytes},
	}}))
	return r
}

func TestEncodeRejectsMismatchedPayload(t *testing.T) {
	r := testRegistry(t)
	cases := []struct {
		name    string
		tag     uint64
		version uint64
		fields  map[string]any
		wantSub string
	}{
		{"标签A搭B字段-多字段", tagTransfer, 2, map[string]any{"amount": uint64(1), "currency": "USD", "op": "x"}, "schema has 2 fields, got 3"},
		{"标签A搭B字段-缺字段", tagTransfer, 2, map[string]any{"amount": uint64(1)}, "schema has 2 fields, got 1"},
		{"字段名不属于该标签", tagTransfer, 2, map[string]any{"amount": uint64(1), "name": "x"}, "missing field"},
		{"空分支塞字段", tagPing, 1, map[string]any{"amount": uint64(1)}, "schema has 0 fields, got 1"},
		{"字段类型不符", tagTransfer, 1, map[string]any{"amount": "4200"}, "want uint64"},
		{"未注册版本", tagTransfer, 7, map[string]any{}, "version not registered"},
		{"未知标签须显式透传", 99, 1, map[string]any{}, "unknown tag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Encode(tc.tag, tc.version, tc.fields)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("want error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
}

func TestEmptyBranchRoundTrip(t *testing.T) {
	r := testRegistry(t)
	f, err := r.Encode(tagPing, 1, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	v, n, err := r.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(f) || v.Kind != KindKnown || v.Tag != tagPing || len(v.Fields) != 0 {
		t.Fatalf("bad empty-branch round trip: %+v n=%d", v, n)
	}
}

func TestAllFieldTypesRoundTrip(t *testing.T) {
	r := testRegistry(t)
	in := map[string]any{
		"name":   "ada",
		"age":    uint64(36),
		"score":  int64(-7),
		"admin":  true,
		"avatar": []byte{0xDE, 0xAD, 0xBE, 0xEF},
	}
	f, err := r.Encode(tagProfile, 1, in)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := r.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Fields, in) {
		t.Fatalf("fields mismatch: got %v want %v", v.Fields, in)
	}
}

func TestMigrationIsExplicit(t *testing.T) {
	r := testRegistry(t)
	f, err := r.Encode(tagTransfer, 1, map[string]any{"amount": uint64(5)})
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := r.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != 2 || v.Fields["currency"] != "USD" || v.Fields["amount"] != uint64(5) {
		t.Fatalf("migration failed: %+v", v)
	}

	// 删除演进映射后，旧版本数据必须报错而非猜测。
	r2 := NewRegistry()
	_ = r2.Register(Schema{Tag: tagTransfer, Version: 1, Fields: []FieldDef{{Name: "amount", Type: FUint}}})
	_ = r2.Register(Schema{Tag: tagTransfer, Version: 2, Fields: []FieldDef{
		{Name: "amount", Type: FUint}, {Name: "currency", Type: FString}}})
	if _, _, err := r2.Decode(f); err == nil || !strings.Contains(err.Error(), "no explicit migration") {
		t.Fatalf("want explicit-migration error, got %v", err)
	}
}

func TestUnknownTagPassThrough(t *testing.T) {
	r := testRegistry(t)
	// 未知标签 99 的载荷“巧合地”是一段合法 transfer v1 载荷：
	// 解码器不得按已知 schema 解读它。
	inner, err := r.Encode(tagTransfer, 1, map[string]any{"amount": uint64(7)})
	if err != nil {
		t.Fatal(err)
	}
	payload := inner[3 : len(inner)-4]
	fu, err := EncodeUnknown(99, 5, payload)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := r.Decode(fu)
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != KindUnknown {
		t.Fatalf("unknown tag must not be decoded as known, got %+v", v)
	}
	if v.Version != 5 || !bytes.Equal(v.Raw, payload) {
		t.Fatalf("version/raw not preserved: %+v", v)
	}
	re, err := r.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(re, fu) {
		t.Fatalf("re-encode not byte-identical:\n got %x\nwant %x", re, fu)
	}
}

func TestUnknownVersionOfKnownTagPassThrough(t *testing.T) {
	r := testRegistry(t)
	// tagTransfer 的 v9 未注册：必须透传，不得用 v1/v2 schema 巧合解读。
	fu, err := EncodeUnknown(tagTransfer, 9, []byte{0x2A, 0x03, 'U', 'S', 'D'})
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := r.Decode(fu)
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != KindUnknown || v.Version != 9 {
		t.Fatalf("unregistered version must pass through, got %+v", v)
	}
	re, _ := r.Marshal(v)
	if !bytes.Equal(re, fu) {
		t.Fatalf("re-encode not byte-identical")
	}
}

func TestChecksumProtectsUnknownFrame(t *testing.T) {
	r := testRegistry(t)
	fu, err := EncodeUnknown(77, 1, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	fu[len(fu)-3] ^= 0xFF // 破坏校验区
	if _, _, err := r.Decode(fu); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum error, got %v", err)
	}
}

func TestLengthValidatedBeforeAllocation(t *testing.T) {
	r := testRegistry(t)
	huge := []byte{0x01, 0x01, 0x80, 0x80, 0x80, 0x10} // length = 2^25 > MaxPayloadLen
	if _, _, err := r.Decode(huge); err == nil || !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("want length-limit error, got %v", err)
	}
	truncated := []byte{0x01, 0x01, 0x05, 0x01} // length=5 但数据不足
	if _, _, err := r.Decode(truncated); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("want truncation error, got %v", err)
	}
}

func TestTrailingBytesRejected(t *testing.T) {
	r := testRegistry(t)
	// 载荷多出一个字节：不得按巧合字段解读，必须拒绝。
	reFramed, err := EncodeUnknown(tagTransfer, 1, []byte{0x01, 0x00})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Decode(reFramed); err == nil || !strings.Contains(err.Error(), "trailing bytes") {
		t.Fatalf("want trailing-bytes error, got %v", err)
	}
}
