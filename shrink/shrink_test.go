package shrink_test

import (
	"errors"
	"math"
	"testing"

	"example.org/netsec/q0497/shrink"
)

func intField(name string, min, max int64) shrink.Field {
	return shrink.Field{Integer: &shrink.IntegerField{Name: name, Min: min, Max: max}}
}

func optIntField(name string, min, max int64) shrink.Field {
	f := intField(name, min, max)
	f.Optional = true
	return f
}

func strField(name string, maxLen int) shrink.Field {
	return shrink.Field{String: &shrink.StringField{Name: name, MaxLen: maxLen}}
}

func optStrField(name string, maxLen int) shrink.Field {
	f := strField(name, maxLen)
	f.Optional = true
	return f
}

func mustSchema(t *testing.T, name, assertion string, fields []shrink.Field) *shrink.Schema {
	t.Helper()
	s, err := shrink.NewSchema(name, assertion, fields)
	if err != nil {
		t.Fatalf("NewSchema returned error: %v", err)
	}
	return s
}

func allowAll(*shrink.Candidate) bool { return true }

func TestNewSchemaRejectsInvalid(t *testing.T) {
	bad := []struct {
		name   string
		fields []shrink.Field
	}{
		{"empty", nil},
		{"no domain", []shrink.Field{{Optional: true}}},
		{"both domains", []shrink.Field{{Integer: &shrink.IntegerField{Name: "i"}, String: &shrink.StringField{Name: "s"}}}},
		{"int empty name", []shrink.Field{intField("", 0, 1)}},
		{"int reversed", []shrink.Field{intField("i", 9, 1)}},
		{"int range too large", []shrink.Field{intField("i", 0, int64(shrink.MaxIntRange)+1)}},
		{"str negative", []shrink.Field{strField("s", -1)}},
		{"str too long", []shrink.Field{strField("s", shrink.MaxStringLen+1)}},
		{"too many optional", manyOptional(t, shrink.MaxOptionalFields+1)},
		{"too many fields", manyRequired(t, shrink.MaxTotalFields+1)},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := shrink.NewSchema("schema", "ASSERT-1", tc.fields); !errors.Is(err, shrink.ErrInvalidSchema) {
				t.Fatalf("expected ErrInvalidSchema, got %v", err)
			}
		})
	}

	if _, err := shrink.NewSchema("", "ASSERT-1", []shrink.Field{intField("i", 0, 1)}); !errors.Is(err, shrink.ErrInvalidSchema) {
		t.Fatalf("empty schema name: got %v", err)
	}
	if _, err := shrink.NewSchema("schema", "", []shrink.Field{intField("i", 0, 1)}); !errors.Is(err, shrink.ErrInvalidSchema) {
		t.Fatalf("empty assertion id: got %v", err)
	}
}

func manyOptional(t *testing.T, n int) []shrink.Field {
	t.Helper()
	fields := make([]shrink.Field, n)
	for i := range fields {
		fields[i] = optIntField("o", 0, 0)
	}
	return fields
}

func manyRequired(t *testing.T, n int) []shrink.Field {
	t.Helper()
	fields := make([]shrink.Field, n)
	for i := range fields {
		fields[i] = intField("r", 0, 0)
	}
	return fields
}

func TestEnumeratorOrderingAndAssertionID(t *testing.T) {
	// Required integer [1,2] (varint costs: 1,2 are both 1 byte), optional
	// string length [0,2], optional integer [1,1].
	s := mustSchema(t, "packet", "ASSERT-42", []shrink.Field{
		intField("code", 1, 2),
		optStrField("label", 2),
		optIntField("flag", 1, 1),
	})
	if got := s.OptionalCount(); got != 2 {
		t.Fatalf("OptionalCount = %d, want 2", got)
	}

	enum, err := shrink.NewEnumerator(s, allowAll)
	if err != nil {
		t.Fatalf("NewEnumerator: %v", err)
	}

	type step struct {
		present []bool
		values  []shrink.Value
		bytes   int
	}
	var got []step
	for {
		cand, ok := enum.Next()
		if !ok {
			break
		}
		if cand.AssertionID() != "ASSERT-42" {
			t.Fatalf("AssertionID = %q, want ASSERT-42", cand.AssertionID())
		}
		present, values := cand.Snapshot()
		got = append(got, step{present, values, cand.TotalBytes()})
	}

	// One optional-present tier must fully precede the two-present tier, and
	// within each tier total bytes must be non-decreasing.
	prevOptional := -1
	prevBytes := math.MinInt
	for _, st := range got {
		optional := 0
		for i, ok := range st.present {
			if ok && i > 0 {
				optional++
			}
		}
		if optional < prevOptional {
			t.Fatalf("optional count decreased: %d after %d", optional, prevOptional)
		}
		if optional > prevOptional {
			prevBytes = math.MinInt
			prevOptional = optional
		}
		if st.bytes < prevBytes {
			t.Fatalf("bytes decreased within tier: %d after %d", st.bytes, prevBytes)
		}
		prevBytes = st.bytes
	}

	// Domain cardinality: required 2 * (1 + 3) * (1 + 1) = 16.
	if len(got) != 16 {
		t.Fatalf("enumerated %d candidates, want 16", len(got))
	}
	if !enum.Exhausted() {
		t.Fatal("enumerator not exhausted after full domain")
	}

	first := got[0]
	// Fewest optional fields = 0; mask 1 byte, required int 1 byte => 2.
	if first.present[1] || first.present[2] {
		t.Fatalf("first candidate has optional fields present: %v", first.present)
	}
	if first.bytes != 2 {
		t.Fatalf("first TotalBytes = %d, want 2", first.bytes)
	}
}

func TestShrinkGlobalMinimumAndIDRetention(t *testing.T) {
	s := mustSchema(t, "packet", "ASSERT-7", []shrink.Field{
		intField("code", 1, 5),
		optStrField("label", 3),
		optIntField("flag", 1, 2),
	})

	// Public precondition: when label is present it must hold at least 2 bytes.
	pre := func(c *shrink.Candidate) bool {
		if v, ok := c.ValueAt(1); ok && len(v.Str) < 2 {
			return false
		}
		return true
	}
	// Failure needs flag present and >= 2, or label present with length 3.
	fail := func(c *shrink.Candidate) bool {
		if v, ok := c.ValueAt(2); ok && v.Int >= 2 {
			return true
		}
		if v, ok := c.ValueAt(1); ok && len(v.Str) == 3 {
			return true
		}
		return false
	}

	result, err := shrink.Shrink(shrink.Request{
		Schema:       s,
		Precondition: pre,
		Reproduce:    fail,
		Budget:       s.Size() + 1,
	})
	if err != nil {
		t.Fatalf("Shrink: %v", err)
	}
	if result.Best == nil {
		t.Fatal("expected a failing candidate")
	}
	if result.Best.AssertionID() != "ASSERT-7" {
		t.Fatalf("AssertionID = %q", result.Best.AssertionID())
	}
	if result.Incomplete {
		t.Fatalf("ordered enumeration proves the minimum without a budget overrun, got %+v", result)
	}

	// Smallest failing shape: one optional field. flag=2 (1 byte) beats
	// label length 3 (3 bytes); required code cheapest is 1 (1 byte) and the
	// mask costs 1 byte, giving 3 total bytes with flag present.
	if present, _ := result.Best.Snapshot(); present[1] || !present[2] {
		t.Fatalf("unexpected presence flags: %v", present)
	}
	if v, _ := result.Best.ValueAt(2); v.Int != 2 {
		t.Fatalf("flag = %d, want 2", v.Int)
	}
	if result.Best.TotalBytes() != 3 {
		t.Fatalf("TotalBytes = %d, want 3", result.Best.TotalBytes())
	}
}
