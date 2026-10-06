package speckey

import (
	"strings"
	"testing"
)

// adversarialFields returns field values designed to collide with the
// encoding's markers, separators, and length prefixes.
func adversarialFields() []Field {
	return []Field{
		Null(),
		Str(""),
		Str("N"),
		Str("E"),
		Str("V"),
		Str("V0:"),
		Str("V1:a"),
		Str("V3:abc"),
		Str("|"),
		Str("||"),
		Str(":"),
		Str("#c:"),
		Str("v1|0|"),
		Str("v1|1|N"),
		Str("0"),
		Str("00"),
		Str(" "),
		Str("\x00"),
		Str("a\x00b"),
		Str("中文键"),
		Str("🚀"),
		Str("N|E|V3:abc|"),
		Str(strings.Repeat("x", 4096)),
		Str(strings.Repeat("|V9:", 64)),
	}
}

func TestNullDistinctFromEmpty(t *testing.T) {
	if Null().Equal(Str("")) {
		t.Fatal("null field must differ from empty-string field")
	}
	if NewKey(Null()).String() == NewKey(Str("")).String() {
		t.Fatal("null and empty-string keys must encode differently")
	}
}

func TestFieldEncodingsAreInjective(t *testing.T) {
	fields := adversarialFields()
	seen := make(map[string]Field)
	for _, f := range fields {
		enc := NewKey(f).String()
		if prev, dup := seen[enc]; dup {
			t.Fatalf("encoding collision: %+v and %+v both -> %q", prev, f, enc)
		}
		seen[enc] = f
	}
}

func TestMultiFieldKeysAreInjective(t *testing.T) {
	fields := adversarialFields()
	seen := make(map[string]SpecKey)
	add := func(k SpecKey) {
		enc := k.String()
		if prev, dup := seen[enc]; dup {
			t.Fatalf("key collision: %v and %v both -> %q", prev.fields, k.fields, enc)
		}
		seen[enc] = k
	}
	// Singles, pairs (including self-pairs), and one triple must never
	// collide: separator injection across field boundaries is impossible
	// because values are length-prefixed.
	for _, a := range fields {
		add(NewKey(a))
		for _, b := range fields {
			add(NewKey(a, b))
		}
	}
	for _, f := range fields {
		add(NewKey(f, Null(), f))
	}
}

func TestParseKeyRoundTrip(t *testing.T) {
	fields := adversarialFields()
	keys := []SpecKey{NewKey()}
	for _, a := range fields {
		keys = append(keys, NewKey(a))
		for _, b := range fields {
			keys = append(keys, NewKey(a, b))
		}
	}
	for _, k := range keys {
		got, err := ParseKey(k.String())
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", k.String(), err)
		}
		if !got.Equal(k) {
			t.Fatalf("round trip mismatch: %q -> %v, want %v", k.String(), got.fields, k.fields)
		}
	}
}

func TestParseKeyRejectsMalformed(t *testing.T) {
	bad := []string{
		"",
		"v2|0|",
		"v1|",
		"v1|x|",
		"v1|-1|",
		"v1|1|",      // missing field
		"v1|1|N|",    // trailing separator
		"v1|0|N",     // trailing data
		"v1|1|X",     // unknown marker
		"v1|1|V",     // missing length
		"v1|1|V3:ab", // short payload
		"v1|1|V0:",   // zero length must use E
		"v1|1|V-1:x", // negative length
		"v1|2|N",     // count exceeds fields
	}
	for _, s := range bad {
		if _, err := ParseKey(s); err == nil {
			t.Fatalf("ParseKey(%q) unexpectedly succeeded", s)
		}
	}
}

func TestKeyEqualityAndStability(t *testing.T) {
	a := NewKey(Str("lib"), Null(), Str(""))
	b := NewKey(Str("lib"), Null(), Str(""))
	if !a.Equal(b) || a.String() != b.String() {
		t.Fatal("identical keys must be equal and encode identically")
	}
	c := NewKey(Str("lib"), Str(""), Null())
	if a.Equal(c) || a.String() == c.String() {
		t.Fatal("swapped null/empty fields must produce different keys")
	}
}
