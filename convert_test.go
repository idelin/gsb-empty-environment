package typepipe

import "testing"

func conv(t *testing.T, f *Field, v any) (any, *Anomaly) {
	t.Helper()
	c, ok := converters[f.Converter]
	if !ok {
		t.Fatalf("unknown converter %q", f.Converter)
	}
	return c(f, v)
}

func TestTinyIntToBool(t *testing.T) {
	f := &Field{Name: "is_active", Converter: "bool", Kind: KindBool}
	cases := []struct {
		in      any
		want    any
		wantLog bool
	}{
		{int64(0), false, false},
		{int64(1), true, false},
		{true, true, false},
		{"true", true, false},
		{"0", false, false},
		{[]byte("1"), true, false},
		{int64(7), true, true}, // 越域值：告警 + 非零即真
		{"abc", false, true},   // 坏字段：安全默认 false
		{nil, false, true},     // NULL：安全默认 false
	}
	for _, c := range cases {
		got, anom := conv(t, f, c.in)
		if got != c.want {
			t.Errorf("bool(%v) = %v, want %v", c.in, got, c.want)
		}
		if (anom != nil) != c.wantLog {
			t.Errorf("bool(%v) anomaly = %v, wantLog=%v", c.in, anom, c.wantLog)
		}
	}
}

func TestDateTimeToISOWithUTCOffset(t *testing.T) {
	f := &Field{Name: "created_at", Converter: "datetime", Kind: KindTimestamp, TZOffset: "+08:00"}

	got, anom := conv(t, f, "2024-03-01 10:30:00")
	if anom != nil {
		t.Fatalf("unexpected anomaly: %+v", anom)
	}
	if got != "2024-03-01T10:30:00+08:00" {
		t.Errorf("got %v, want 2024-03-01T10:30:00+08:00", got)
	}

	// 已带时区的输入按原时区保留
	got, _ = conv(t, f, "2024-03-01T10:30:00+05:30")
	if got != "2024-03-01T10:30:00+05:30" {
		t.Errorf("got %v", got)
	}

	// Unix 秒按 UTC 输出
	got, _ = conv(t, f, int64(0))
	if got != "1970-01-01T00:00:00Z" {
		t.Errorf("got %v", got)
	}

	// 坏字段：安全默认 + 异常日志
	got, anom = conv(t, f, "not-a-date")
	if got != "1970-01-01T00:00:00Z" || anom == nil || anom.Reason != "invalid_format" {
		t.Errorf("bad input: got=%v anom=%+v", got, anom)
	}
}

func TestEnumToDictCode(t *testing.T) {
	f := &Field{
		Name: "status", Converter: "enum", Kind: KindInt,
		SourceType: "enum('active','pending','banned')",
	}
	cases := []struct {
		in   any
		want any
	}{
		{"active", int64(1)},
		{"pending", int64(2)},
		{"banned", int64(3)},
	}
	for _, c := range cases {
		got, anom := conv(t, f, c.in)
		if anom != nil || got != c.want {
			t.Errorf("enum(%v) = %v anom=%+v, want %v", c.in, got, anom, c.want)
		}
	}

	// 未登记枚举值 -> 默认码 0 + unknown_enum 日志
	got, anom := conv(t, f, "deleted")
	if got != int64(0) || anom == nil || anom.Reason != "unknown_enum" {
		t.Errorf("unknown enum: got=%v anom=%+v", got, anom)
	}

	// 显式字典映射优先
	f2 := &Field{Name: "s", Converter: "enum", Kind: KindInt, EnumMap: map[string]int64{"y": 10, "n": 20}}
	got, _ = conv(t, f2, "y")
	if got != int64(10) {
		t.Errorf("explicit map: got %v", got)
	}
}

func TestParseEnumDefEscapes(t *testing.T) {
	m := ParseEnumDef("enum('it''s','a\\'b','plain')")
	if m["it's"] != 1 || m["a'b"] != 2 || m["plain"] != 3 {
		t.Errorf("got %v", m)
	}
}

func TestUUIDStringInterop(t *testing.T) {
	f := &Field{Name: "uid", Converter: "uuid", Kind: KindUUID}

	// 大写无连字符 -> 规范化小写连字符
	got, anom := conv(t, f, "550E8400E29B41D4A716446655440000")
	if anom != nil || got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got=%v anom=%+v", got, anom)
	}

	// 非法 UUID -> 零 UUID 安全默认 + 日志
	got, anom = conv(t, f, "not-a-uuid")
	if got != "00000000-0000-0000-0000-000000000000" || anom == nil {
		t.Errorf("got=%v anom=%+v", got, anom)
	}
}

func TestIntOverflowClamped(t *testing.T) {
	min, max := int64(-32768), int64(32767)
	f := &Field{Name: "score", Converter: "int", Kind: KindInt, Min: &min, Max: &max}

	got, anom := conv(t, f, "70000")
	if got != int64(32767) || anom == nil || anom.Reason != "overflow" || anom.Action != "clamped" {
		t.Errorf("overflow high: got=%v anom=%+v", got, anom)
	}

	got, anom = conv(t, f, -40000)
	if got != int64(-32768) || anom == nil || anom.Reason != "overflow" {
		t.Errorf("overflow low: got=%v anom=%+v", got, anom)
	}

	got, anom = conv(t, f, 100)
	if got != int64(100) || anom != nil {
		t.Errorf("in range: got=%v anom=%+v", got, anom)
	}

	// 无法解析 -> 安全默认 0
	got, anom = conv(t, f, "abc")
	if got != int64(0) || anom == nil || anom.Reason != "invalid_format" {
		t.Errorf("bad number: got=%v anom=%+v", got, anom)
	}
}

func TestParseUTCOffset(t *testing.T) {
	for _, s := range []string{"+08:00", "-0530", "Z", "UTC"} {
		if _, err := parseUTCOffset(s); err != nil {
			t.Errorf("offset %q: %v", s, err)
		}
	}
	if _, err := parseUTCOffset("+25:00"); err == nil {
		t.Error("expected error for +25:00")
	}
}
