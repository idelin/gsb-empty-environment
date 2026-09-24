package canonkey

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func canon(t *testing.T, v any) string {
	t.Helper()
	b, err := Canonical(v, Limits{})
	if err != nil {
		t.Fatalf("Canonical(%v): %v", v, err)
	}
	return string(b)
}

func TestObjectFieldOrderIrrelevant(t *testing.T) {
	a := map[string]any{"a": json.Number("1"), "b": json.Number("2"), "c": "x"}
	b := map[string]any{"c": "x", "b": json.Number("2"), "a": json.Number("1")}
	if canon(t, a) != canon(t, b) {
		t.Fatalf("field order leaked into key:\n%s\n%s", canon(t, a), canon(t, b))
	}
	// Byte-order field sorting is verifiable: "Z" (0x5A) sorts before "a" (0x61).
	got := canon(t, map[string]any{"a": json.Number("1"), "Z": json.Number("2")})
	want := `o2{s1:Zn2e0s1:an1e0}`
	if got != want {
		t.Fatalf("field sort not byte order: got %q want %q", got, want)
	}
}

func TestArrayOrderPreserved(t *testing.T) {
	a := []any{json.Number("1"), json.Number("2")}
	b := []any{json.Number("2"), json.Number("1")}
	if canon(t, a) == canon(t, b) {
		t.Fatal("array order must be significant")
	}
}

func TestNumberVsStringNeverCollides(t *testing.T) {
	if canon(t, json.Number("1")) == canon(t, "1") {
		t.Fatal("number 1 and string \"1\" collided")
	}
	if canon(t, json.Number("1")) != "n1e0" {
		t.Fatalf("number 1 canonical form: %q", canon(t, json.Number("1")))
	}
	if canon(t, "1") != "s1:1" {
		t.Fatalf("string \"1\" canonical form: %q", canon(t, "1"))
	}
}

func TestNumberNormalizationExact(t *testing.T) {
	same := []string{"1", "1.0", "1e0", "100e-2", "0.01e2", "1000e-3"}
	for _, lit := range same {
		if got := canon(t, json.Number(lit)); got != "n1e0" {
			t.Fatalf("%s normalized to %q, want n1e0", lit, got)
		}
	}
	pairs := [][2]string{
		{"1.5", "15e-1"}, {"0", "0"}, {"-0", "0"}, {"0.0", "0"},
		{"-2.50", "-25e-1"}, {"123456789012345678901234567890", "12345678901234567890123456789e1"},
	}
	for _, p := range pairs {
		if got := canon(t, json.Number(p[0])); got != "n"+p[1] {
			t.Fatalf("%s -> %q, want n%s", p[0], got, p[1])
		}
	}
	// Precision is preserved: these differ only far past float64 precision.
	big1 := canon(t, json.Number("1.0000000000000000000000001"))
	big2 := canon(t, json.Number("1"))
	if big1 == big2 {
		t.Fatal("exact decimal representation lost precision")
	}
	if _, err := Canonical(json.Number("1..2"), Limits{}); !errors.Is(err, ErrInvalidNumber) {
		t.Fatalf("malformed number: %v", err)
	}
}

func TestMissingVsNullDistinct(t *testing.T) {
	k := NewKeyer(Limits{})
	absent := k.MustBuildKey(Request{Tenant: "t", ConfigVersion: "v1", Params: nil})
	explicit := k.MustBuildKey(Request{Tenant: "t", ConfigVersion: "v1", Params: Null})
	if absent.Digest == explicit.Digest || bytes.Equal(absent.Canon, explicit.Canon) {
		t.Fatal("absent params and explicit null must not share a key")
	}
	// Same distinction inside objects: missing field vs explicit null field.
	withNull := canon(t, map[string]any{"a": nil})
	without := canon(t, map[string]any{})
	if withNull == without {
		t.Fatal("missing field and null field must differ")
	}
}

func TestUnicodeStrategyVerifiable(t *testing.T) {
	// Byte-exact UTF-8: composed vs. decomposed forms are distinct.
	composed := "café"    // é as U+00E9
	decomposed := "café" // e + combining accent U+0301
	if canon(t, composed) == canon(t, decomposed) {
		t.Fatal("byte-exact policy must distinguish composed/decomposed")
	}
	// Invalid UTF-8 is rejected, never silently rewritten.
	if _, err := Canonical(string([]byte{0xff, 0xfe}), Limits{}); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("invalid UTF-8: %v", err)
	}
	// Length-prefixed encoding removes delimiter ambiguity.
	if canon(t, []any{"1:", "x"}) == canon(t, []any{"1", ":x"}) {
		t.Fatal("string framing is ambiguous")
	}
}

func TestCycleRejectedSharedExpanded(t *testing.T) {
	// Explicit supported scope: cyclic structures are rejected.
	cyc := make([]any, 1)
	cyc[0] = cyc
	if _, err := Canonical(cyc, Limits{}); !errors.Is(err, ErrCycle) {
		t.Fatalf("cyclic slice: %v", err)
	}
	m := map[string]any{}
	m["self"] = m
	if _, err := Canonical(m, Limits{}); !errors.Is(err, ErrCycle) {
		t.Fatalf("cyclic map: %v", err)
	}
	// Shared (acyclic) substructures use value semantics: expanded.
	shared := []any{json.Number("1")}
	got := canon(t, []any{shared, shared})
	want := canon(t, []any{[]any{json.Number("1")}, []any{json.Number("1")}})
	if got != want {
		t.Fatal("shared substructure must expand by value")
	}
}

func TestBudgets(t *testing.T) {
	lim := Limits{MaxDepth: 5, MaxNodes: 20, MaxStringLen: 4, MaxInputBytes: 16}
	deep := any(json.Number("1"))
	for i := 0; i < 10; i++ {
		deep = []any{deep}
	}
	if _, err := Canonical(deep, lim); !errors.Is(err, ErrBudget) {
		t.Fatalf("deep nesting: %v", err)
	}
	wide := map[string]any{}
	for i := 0; i < 50; i++ {
		wide[string(rune('a'+i%26))+string(rune('a'+i/26))] = json.Number("1")
	}
	if _, err := Canonical(wide, lim); !errors.Is(err, ErrBudget) {
		t.Fatalf("too many nodes: %v", err)
	}
	if _, err := Canonical("12345", lim); !errors.Is(err, ErrBudget) {
		t.Fatalf("long string: %v", err)
	}
	if _, err := ParseParams(strings.NewReader(strings.Repeat("1", 100)), lim); !errors.Is(err, ErrBudget) {
		t.Fatalf("huge input: %v", err)
	}
}

func TestUnsupportedTypeRejected(t *testing.T) {
	if _, err := Canonical(struct{ X int }{1}, Limits{}); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("struct: %v", err)
	}
	if _, err := Canonical(map[int]any{1: "x"}, Limits{}); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("non-string map keys: %v", err)
	}
}
