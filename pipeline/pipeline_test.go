package pipeline

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func shanghai() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		// fall back to a fixed +08:00 zone if tzdata is unavailable
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

func newEngine(rules []Rule) (*Engine, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return New(rules, buf), buf
}

func mysqlRules() []Rule {
	return []Rule{
		{Source: MySQL, Match: "tinyint", Target: TypeBool},
		{Source: MySQL, Match: "datetime", Target: TypeISO8601, Location: shanghai()},
		{Source: MySQL, Match: "enum", Target: TypeDictCode,
			Dict: map[string]int{"small": 1, "medium": 2, "large": 3}},
		{Source: MySQL, Match: "smallint", Target: TypeInt,
			Min: -128, Max: 127, Overflow: Clamp},
		{Source: MySQL, Match: "char", Target: TypeUUID},
	}
}

func TestTinyIntToBool(t *testing.T) {
	e, _ := newEngine(mysqlRules())
	field := Field{Name: "is_active", Type: FieldType{Name: "tinyint", Args: []string{"1"}}}

	cases := []struct {
		in    any
		want  any
		issue bool
	}{
		{int64(0), false, false},
		{int64(1), true, false},
		{true, true, false},
		{"true", true, false},
		{"0", false, false},
		{int64(7), false, true}, // out of 0/1: safe default + warning
		{"maybe", false, true},  // garbage: safe default + warning
		{nil, false, true},      // null: safe default + warning
	}
	for _, c := range cases {
		got := e.Convert(MySQL, field, c.in)
		if got != c.want {
			t.Errorf("Convert(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	if len(e.Issues()) != 3 {
		t.Fatalf("expected 3 issues, got %d: %v", len(e.Issues()), e.Issues())
	}
}

func TestDateTimeToISO8601UTCOffset(t *testing.T) {
	e, _ := newEngine(mysqlRules())
	field := Field{Name: "created_at", Type: FieldType{Name: "datetime"}}

	// Naive MySQL datetime is read as Asia/Shanghai (+08:00) and rendered
	// as a standard ISO string with an explicit UTC offset.
	got := e.Convert(MySQL, field, "2024-03-01 12:30:00")
	if got != "2024-03-01T04:30:00+00:00" {
		t.Errorf("datetime conversion = %v, want 2024-03-01T04:30:00+00:00", got)
	}

	// Fractional seconds and date-only forms are accepted too.
	if got := e.Convert(MySQL, field, "2024-03-01 12:30:00.5"); got != "2024-03-01T04:30:00+00:00" {
		t.Errorf("fractional datetime = %v", got)
	}
	if got := e.Convert(MySQL, field, "2024-03-01"); got != "2024-02-29T16:00:00+00:00" {
		t.Errorf("date-only datetime = %v", got)
	}

	// Zone-aware input is honored and shifted to UTC.
	if got := e.Convert(MySQL, field, "2024-03-01T12:30:00+05:00"); got != "2024-03-01T07:30:00+00:00" {
		t.Errorf("rfc3339 input = %v", got)
	}

	// Bad input falls back to the epoch default and raises an issue.
	if got := e.Convert(MySQL, field, "not-a-date"); got != "1970-01-01T00:00:00+00:00" {
		t.Errorf("bad datetime = %v, want epoch default", got)
	}
	if n := len(e.Issues()); n != 1 {
		t.Fatalf("expected 1 issue, got %d", n)
	}
}

func TestEnumToDictCode(t *testing.T) {
	e, _ := newEngine(mysqlRules())
	field := Field{Name: "size", Type: FieldType{Name: "enum",
		Args: []string{"small", "medium", "large"}}}

	if got := e.Convert(MySQL, field, "medium"); got != 2 {
		t.Errorf("enum medium = %v, want 2", got)
	}
	// Unknown label -> dictionary code 0 + warning.
	if got := e.Convert(MySQL, field, "huge"); got != 0 {
		t.Errorf("unknown enum = %v, want 0", got)
	}
	// Non-string input -> default + warning.
	if got := e.Convert(MySQL, field, 3.14); got != 0 {
		t.Errorf("non-string enum = %v, want 0", got)
	}
	if n := len(e.Issues()); n != 2 {
		t.Fatalf("expected 2 issues, got %d", n)
	}
}

func TestEnumDictDerivedFromColumnDefinition(t *testing.T) {
	// Without an explicit Dict, codes come 1-based from enum(...)'s members.
	rules := []Rule{{Source: MySQL, Match: "enum", Target: TypeDictCode}}
	e, _ := newEngine(rules)
	field := Field{Name: "level", Type: FieldType{Name: "enum",
		Args: []string{"low", "mid", "high"}}}

	if got := e.Convert(MySQL, field, "high"); got != 3 {
		t.Errorf("derived enum code = %v, want 3", got)
	}
}

func TestIntOverflowClampAndDefault(t *testing.T) {
	clampRules := []Rule{{Source: MySQL, Match: "smallint", Target: TypeInt,
		Min: -128, Max: 127, Overflow: Clamp}}
	e, _ := newEngine(clampRules)
	field := Field{Name: "score", Type: FieldType{Name: "smallint"}}

	if got := e.Convert(MySQL, field, int64(300)); got != int64(127) {
		t.Errorf("overflow high = %v, want clamped 127", got)
	}
	if got := e.Convert(MySQL, field, int64(-999)); got != int64(-128) {
		t.Errorf("overflow low = %v, want clamped -128", got)
	}
	if got := e.Convert(MySQL, field, int64(42)); got != int64(42) {
		t.Errorf("in-range = %v, want 42", got)
	}
	issues := e.Issues()
	if len(issues) != 2 || !strings.Contains(issues[0].Reason, "out of range") {
		t.Fatalf("expected 2 overflow issues, got %v", issues)
	}

	// UseDefault policy replaces the value instead of clamping.
	defRules := []Rule{{Source: JSONDoc, Match: "number", Target: TypeInt,
		Min: 0, Max: 255, Overflow: UseDefault, Default: int64(-1)}}
	e2, _ := newEngine(defRules)
	jf := Field{Name: "port", Type: FieldType{Name: "number"}}
	if got := e2.Convert(JSONDoc, jf, json.Number("70000")); got != int64(-1) {
		t.Errorf("UseDefault overflow = %v, want -1", got)
	}
	if got := e2.Convert(JSONDoc, jf, json.Number("80")); got != int64(80) {
		t.Errorf("json.Number in range = %v, want 80", got)
	}
	// Non-numeric garbage -> default + warning.
	if got := e2.Convert(JSONDoc, jf, "lots"); got != int64(-1) {
		t.Errorf("non-numeric = %v, want -1", got)
	}
}

func TestUUIDStringInterop(t *testing.T) {
	e, _ := newEngine(append(mysqlRules(),
		Rule{Source: Postgres, Match: "uuid", Target: TypeString}))

	charField := Field{Name: "api_key", Type: FieldType{Name: "char", Args: []string{"36"}}}
	// Mixed-case UUID normalizes to canonical lowercase.
	got := e.Convert(MySQL, charField, "A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11")
	if got != "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11" {
		t.Errorf("uuid normalize = %v", got)
	}
	// Invalid UUID -> nil UUID default + warning.
	if got := e.Convert(MySQL, charField, "zzz"); got != nilUUID {
		t.Errorf("bad uuid = %v, want nil UUID", got)
	}

	// PostgreSQL uuid -> plain string.
	uuidField := Field{Name: "id", Type: FieldType{Name: "uuid"}}
	if got := e.Convert(Postgres, uuidField, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"); got != "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11" {
		t.Errorf("uuid->string = %v", got)
	}
	if n := len(e.Issues()); n != 1 {
		t.Fatalf("expected 1 issue, got %d", n)
	}
}

func TestConvertRecordEndToEnd(t *testing.T) {
	e, logBuf := newEngine(mysqlRules())
	schema := []Field{
		{Name: "id", Type: FieldType{Name: "bigint"}}, // unmapped: passthrough
		{Name: "is_active", Type: FieldType{Name: "tinyint", Args: []string{"1"}}},
		{Name: "created_at", Type: FieldType{Name: "datetime"}},
		{Name: "size", Type: FieldType{Name: "enum", Args: []string{"small", "medium", "large"}}},
		{Name: "score", Type: FieldType{Name: "smallint"}},
		{Name: "api_key", Type: FieldType{Name: "char", Args: []string{"36"}}},
	}
	row := map[string]any{
		"id":         int64(42),
		"is_active":  int64(1),
		"created_at": "2024-03-01 12:30:00",
		"size":       "huge",     // unknown enum label
		"score":      int64(300), // overflows smallint bound
		"api_key":    "not-a-uuid",
	}
	out := e.ConvertRecord(MySQL, schema, row)

	want := map[string]any{
		"id":         int64(42),
		"is_active":  true,
		"created_at": "2024-03-01T04:30:00+00:00",
		"size":       0,
		"score":      int64(127),
		"api_key":    nilUUID,
	}
	for k, w := range want {
		if out[k] != w {
			t.Errorf("out[%s] = %v, want %v", k, out[k], w)
		}
	}

	issues := e.Issues()
	if len(issues) != 3 {
		t.Fatalf("expected 3 issues, got %d: %v", len(issues), issues)
	}
	// The cleaning exception log must mention every bad field.
	logText := logBuf.String()
	for _, name := range []string{"size", "score", "api_key"} {
		if !strings.Contains(logText, "field="+name) {
			t.Errorf("cleaning log missing field %s:\n%s", name, logText)
		}
	}
	if !strings.Contains(logText, "out of range [-128,127]") {
		t.Errorf("cleaning log missing overflow detail:\n%s", logText)
	}
}

func TestJSONDocSources(t *testing.T) {
	rules := []Rule{
		{Source: JSONDoc, Match: "string", Target: TypeBool},
		{Source: JSONDoc, Match: "number", Target: TypeISO8601},
	}
	e, _ := newEngine(rules)

	bf := Field{Name: "flag", Type: FieldType{Name: "string"}}
	if got := e.Convert(JSONDoc, bf, "true"); got != true {
		t.Errorf("json string bool = %v, want true", got)
	}
	// Unix epoch seconds -> ISO-8601 UTC.
	nf := Field{Name: "ts", Type: FieldType{Name: "number"}}
	if got := e.Convert(JSONDoc, nf, json.Number("1700000000")); got != "2023-11-14T22:13:20+00:00" {
		t.Errorf("epoch to iso8601 = %v", got)
	}
}

func TestUnmappedTypePassesThrough(t *testing.T) {
	e, _ := newEngine(mysqlRules())
	field := Field{Name: "bio", Type: FieldType{Name: "text"}}
	if got := e.Convert(MySQL, field, "hello"); got != "hello" {
		t.Errorf("unmapped passthrough = %v, want hello", got)
	}
	if len(e.Issues()) != 0 {
		t.Errorf("passthrough should not raise issues: %v", e.Issues())
	}
}
