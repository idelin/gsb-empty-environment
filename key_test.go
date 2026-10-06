package canonkey

import (
	"math"
	"strings"
	"testing"
)

// TestKeyAdversarialDistinct feeds field combinations specifically crafted
// to break naive delimiter-based encodings, and requires every distinct
// field list to produce a distinct key.
func TestKeyAdversarialDistinct(t *testing.T) {
	lists := [][]Value{
		{},                         // empty field list
		{Null()},                   // null
		{String("")},               // empty string != null
		{Bytes(nil)},               // empty bytes != null != empty string
		{String("1")},              // string "1"
		{Int(1)},                   // int 1
		{Float(1)},                 // float 1.0
		{Bool(true)},               // bool true
		{String("true")},           // string "true"
		{String("a:b")},            // payload containing the separator
		{String("a"), String("b")}, // two fields that concatenate to "ab"
		{String("ab")},             // one field "ab"
		{String("S3:abc")},         // payload that looks like an encoded field
		{String("N0:")},            // payload that looks like a null field
		{String("K1#0#")},          // payload that looks like a whole key
		{String("#")},              // framing character as payload
		{String("0:")},             // length-prefix-shaped payload
		{String("\x00")},           // NUL byte payload
		{Bytes([]byte{0})},         // NUL byte as bytes
		{String("héllo→世界")},       // multibyte unicode
		{Int(-1)},
		{Int(math.MinInt64)},
		{Int(math.MaxInt64)},
		{Float(0)},
		{Float(math.Copysign(0, -1))}, // -0.0 differs from +0.0 by bit pattern
		{Float(math.NaN())},
		{Float(math.Inf(1))},
		{Float(math.Inf(-1))},
		{Bool(false)},
		{Null(), Null()},                     // repeated nulls
		{Null(), String("")},                 // null then empty string
		{String(""), Null()},                 // empty string then null: order matters
		{String(strings.Repeat("x", 1<<16))}, // long payload
		{String("a"), Null(), Int(3), Bytes([]byte("z")), Bool(true), Float(2.5)},
	}

	seen := make(map[Key]int)
	for i, fields := range lists {
		k := EncodeKey(fields...)
		if j, dup := seen[k]; dup {
			t.Fatalf("key collision between case %d %v and case %d %v: %q",
				j, lists[j], i, fields, string(k))
		}
		seen[k] = i
	}
}

// TestKeyRoundTrip verifies decode(encode(fields)) == fields for adversarial
// inputs, including payloads containing the framing characters.
func TestKeyRoundTrip(t *testing.T) {
	lists := [][]Value{
		{},
		{Null()},
		{String("")},
		{Bytes(nil)},
		{String("S3:abc"), String("N0:"), String("K1#2#B1:1")},
		{String("a:b#c"), Int(-42), Float(math.NaN()), Bool(true), Bytes([]byte{0, 255, ':'})},
		{Float(math.Inf(-1)), Float(math.Copysign(0, -1))},
		{String("héllo→世界")},
		{String(strings.Repeat("y", 1<<15))},
	}
	for i, fields := range lists {
		k := EncodeKey(fields...)
		got, err := k.Fields()
		if err != nil {
			t.Fatalf("case %d: decode failed: %v", i, err)
		}
		if len(got) != len(fields) {
			t.Fatalf("case %d: decoded %d fields, want %d", i, len(got), len(fields))
		}
		for j := range fields {
			if !got[j].Equal(fields[j]) {
				t.Fatalf("case %d field %d: decoded %+v, want %+v", i, j, got[j], fields[j])
			}
		}
		// Re-encoding the decoded fields must be byte-identical.
		if re := EncodeKey(got...); re != k {
			t.Fatalf("case %d: re-encode mismatch: %q vs %q", i, string(re), string(k))
		}
	}
}

// TestKeyNullVsEmpty pins the core requirement: null and empty string are
// different keys, and both differ from empty bytes.
func TestKeyNullVsEmpty(t *testing.T) {
	null := EncodeKey(Null())
	emptyStr := EncodeKey(String(""))
	emptyBytes := EncodeKey(Bytes(nil))
	if null == emptyStr || null == emptyBytes || emptyStr == emptyBytes {
		t.Fatalf("null/empty-string/empty-bytes must encode distinctly: %q %q %q",
			null, emptyStr, emptyBytes)
	}
}

// TestKeyTypeDisambiguation pins that same-looking values of different
// types never share a key.
func TestKeyTypeDisambiguation(t *testing.T) {
	pairs := [][2]Value{
		{String("1"), Int(1)},
		{Int(1), Float(1)},
		{String("1"), Float(1)},
		{String("true"), Bool(true)},
		{String("0"), Bool(false)},
		{String("a"), Bytes([]byte("a"))},
	}
	for i, p := range pairs {
		if EncodeKey(p[0]) == EncodeKey(p[1]) {
			t.Fatalf("pair %d: type confusion, %+v and %+v share a key", i, p[0], p[1])
		}
	}
}

// TestKeyMalformedRejected ensures corrupted keys fail decoding instead of
// silently producing wrong fields.
func TestKeyMalformedRejected(t *testing.T) {
	bad := []string{
		"",
		"K2#0#",         // unknown version
		"K1#",           // missing count
		"K1#x#",         // non-numeric count
		"K1#1#",         // declared field missing
		"K1#0#S0:",      // trailing garbage after 0 fields
		"K1#1#S5:ab",    // payload shorter than declared length
		"K1#1#S:abc",    // missing length
		"K1#1#Q0:",      // unknown tag
		"K1#1#N1:x",     // null with payload
		"K1#1#B1:2",     // invalid bool payload
		"K1#1#I2:xy",    // invalid int payload
		"K1#1#F3:1.2.3", // invalid float payload
	}
	for _, s := range bad {
		if _, err := Key(s).Fields(); err == nil {
			t.Fatalf("malformed key %q decoded without error", s)
		}
	}
}

// TestEncodeKeyDeterministic ensures encoding is stable across calls.
func TestEncodeKeyDeterministic(t *testing.T) {
	fields := []Value{String("x"), Null(), Int(7), Bytes([]byte{1, 2})}
	if EncodeKey(fields...) != EncodeKey(fields...) {
		t.Fatal("encoding is not deterministic")
	}
}

// TestBytesNilEqualsEmpty pins that nil and empty byte slices are the same
// logical value and therefore share a key (both differ from null).
func TestBytesNilEqualsEmpty(t *testing.T) {
	if EncodeKey(Bytes(nil)) != EncodeKey(Bytes([]byte{})) {
		t.Fatal("nil and empty byte slices must encode identically")
	}
	if EncodeKey(Bytes(nil)) == EncodeKey(Null()) {
		t.Fatal("empty bytes must differ from null")
	}
}
