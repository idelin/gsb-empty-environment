package netsec_test

import (
	"bytes"
	"errors"
	"net/url"
	"testing"

	netsec "example.org/netsec/q0488"
	"example.org/netsec/q0488/canonical"
)

func TestCanonicalGoldenForm(t *testing.T) {
	fields := []netsec.Field{
		{Key: "unicode", Value: "中"},
		{Key: "slash", Value: "/"},
		{Key: "plus", Value: "+"},
		{Key: "pct", Value: "%20"},
		{Key: "null", Null: true},
		{Key: "eq", Value: "a=b"},
		{Key: "empty", Value: ""},
		{Key: "b", Value: "2"},
		{Key: "amp", Value: "a&b"},
		{Key: "a", Value: "1 2"},
	}

	want := "a=1%202&amp=a%26b&b=2&empty=&eq=a%3Db&null&pct=%2520&plus=%2B&slash=%2F&unicode=%E4%B8%AD"
	got, err := netsec.Canonicalize(fields)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if string(got) != want {
		t.Fatalf("canonical form mismatch\n got: %q\nwant: %q", got, want)
	}

	asString, err := netsec.CanonicalString(fields)
	if err != nil {
		t.Fatalf("CanonicalString: %v", err)
	}
	if asString != want {
		t.Fatalf("CanonicalString = %q, want %q", asString, want)
	}

	if err := netsec.ValidateCanonical(got); err != nil {
		t.Fatalf("ValidateCanonical(canonical): %v", err)
	}
	if err := netsec.ValidateCanonicalQuery(want); err != nil {
		t.Fatalf("ValidateCanonicalQuery(canonical): %v", err)
	}

	again, err := netsec.CanonicalizeQuery(want)
	if err != nil {
		t.Fatalf("CanonicalizeQuery(canonical): %v", err)
	}
	if !bytes.Equal(again, got) {
		t.Fatalf("canonicalization is not idempotent: %q -> %q", got, again)
	}

	escaped, err := netsec.EscapeComponent("a b")
	if err != nil {
		t.Fatalf("EscapeComponent: %v", err)
	}
	if escaped != "a%20b" {
		t.Fatalf("EscapeComponent(a b) = %q", escaped)
	}
}

func TestKeyOrderPermutation(t *testing.T) {
	one, err := netsec.Canonicalize([]netsec.Field{
		{Key: "b", Value: "2"},
		{Key: "a", Value: "1"},
		{Key: "c", Value: "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	two, err := netsec.Canonicalize([]netsec.Field{
		{Key: "c", Value: "3"},
		{Key: "b", Value: "2"},
		{Key: "a", Value: "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Fatalf("field order changed canonical bytes: %q vs %q", one, two)
	}
	if string(one) != "a=1&b=2&c=3" {
		t.Fatalf("canonical = %q", one)
	}

	fromMap, err := netsec.Canonicalize(map[string]string{"c": "3", "a": "1", "b": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, fromMap) {
		t.Fatalf("map order changed canonical bytes: %q vs %q", one, fromMap)
	}

	fromValues, err := netsec.CanonicalizeValues(url.Values{"c": {"3"}, "a": {"1"}, "b": {"2"}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, fromValues) {
		t.Fatalf("url.Values order changed canonical bytes: %q vs %q", one, fromValues)
	}

	q1, err := netsec.CanonicalizeQuery("c=3&a=1&b=2")
	if err != nil {
		t.Fatal(err)
	}
	q2, err := netsec.CanonicalizeQuery("b=2&c=3&a=1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(q1, q2) {
		t.Fatalf("query order changed canonical bytes: %q vs %q", q1, q2)
	}
}

func TestSpaceDifferencesStayDistinct(t *testing.T) {
	space, err := netsec.Canonicalize([]netsec.Field{{Key: "v", Value: "a b"}})
	if err != nil {
		t.Fatal(err)
	}
	plus, err := netsec.Canonicalize([]netsec.Field{{Key: "v", Value: "a+b"}})
	if err != nil {
		t.Fatal(err)
	}
	literalEscape, err := netsec.Canonicalize([]netsec.Field{{Key: "v", Value: "a%20b"}})
	if err != nil {
		t.Fatal(err)
	}

	if string(space) != "v=a%20b" {
		t.Fatalf("space canonical = %q", space)
	}
	if string(plus) != "v=a%2Bb" {
		t.Fatalf("plus canonical = %q", plus)
	}
	if string(literalEscape) != "v=a%2520b" {
		t.Fatalf("literal escape canonical = %q", literalEscape)
	}
	if bytes.Equal(space, plus) || bytes.Equal(space, literalEscape) || bytes.Equal(plus, literalEscape) {
		t.Fatal("space, plus, and literal percent escape must not collide")
	}

	qSpace, err := netsec.CanonicalizeQuery("v=a%20b")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(qSpace, space) {
		t.Fatalf("query space = %q, want %q", qSpace, space)
	}
	qLiteral, err := netsec.CanonicalizeQuery("v=a%2520b")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(qLiteral, literalEscape) {
		t.Fatalf("double-encoded space = %q, want %q", qLiteral, literalEscape)
	}
	if bytes.Equal(qLiteral, qSpace) {
		t.Fatal("double-encoded space and space must be distinct content")
	}

	if _, err := netsec.CanonicalizeQuery("v=a+b"); !errors.Is(err, netsec.ErrAmbiguousPlus) {
		t.Fatalf("raw plus error = %v, want ErrAmbiguousPlus", err)
	}
	if _, err := netsec.CanonicalizeQuery("v=a b"); !errors.Is(err, netsec.ErrIllegalCharacter) {
		t.Fatalf("raw space error = %v, want ErrIllegalCharacter", err)
	}
	if _, err := netsec.CanonicalizeQuery("v=a\tb"); !errors.Is(err, netsec.ErrIllegalCharacter) {
		t.Fatalf("raw tab error = %v, want ErrIllegalCharacter", err)
	}
	if _, err := netsec.CanonicalizeQuery("v=a%09b"); !errors.Is(err, netsec.ErrIllegalCharacter) {
		t.Fatalf("escaped tab error = %v, want ErrIllegalCharacter", err)
	}
}

func TestEncodingSubstitutionCollapsesOrRejects(t *testing.T) {
	for _, raw := range []string{"v=%2f", "v=%2F", "v=/"} {
		got, err := netsec.CanonicalizeQuery(raw)
		if err != nil {
			t.Fatalf("CanonicalizeQuery(%q): %v", raw, err)
		}
		if string(got) != "v=%2F" {
			t.Fatalf("CanonicalizeQuery(%q) = %q, want v=%%2F", raw, got)
		}
	}
	for _, raw := range []string{"v=%41", "v=A"} {
		got, err := netsec.CanonicalizeQuery(raw)
		if err != nil {
			t.Fatalf("CanonicalizeQuery(%q): %v", raw, err)
		}
		if string(got) != "v=A" {
			t.Fatalf("CanonicalizeQuery(%q) = %q, want v=A", raw, got)
		}
	}
	for _, raw := range []string{"v=%7e", "v=~"} {
		got, err := netsec.CanonicalizeQuery(raw)
		if err != nil {
			t.Fatalf("CanonicalizeQuery(%q): %v", raw, err)
		}
		if string(got) != "v=~" {
			t.Fatalf("CanonicalizeQuery(%q) = %q, want v=~", raw, got)
		}
	}

	if err := netsec.ValidateCanonicalQuery("v=%2F"); err != nil {
		t.Fatalf("uppercase escape must be canonical: %v", err)
	}
	for _, raw := range []string{"v=%2f", "v=/", "v=%41", "v=%7e"} {
		if err := netsec.ValidateCanonicalQuery(raw); !errors.Is(err, netsec.ErrNonCanonical) {
			t.Fatalf("ValidateCanonicalQuery(%q) = %v, want ErrNonCanonical", raw, err)
		}
	}
	if err := netsec.ValidateCanonicalQuery("v=A"); err != nil {
		t.Fatalf("raw unreserved value must be canonical: %v", err)
	}
	if _, err := netsec.CanonicalizeStrict("v=%2F"); err != nil {
		t.Fatalf("CanonicalizeStrict(canonical): %v", err)
	}
	if _, err := netsec.CanonicalizeStrict("v=%2f"); !errors.Is(err, netsec.ErrNonCanonical) {
		t.Fatalf("CanonicalizeStrict(re-encoded) error = %v, want ErrNonCanonical", err)
	}

	for _, raw := range []string{"v=%", "v=%2", "v=%GG"} {
		if _, err := netsec.CanonicalizeQuery(raw); !errors.Is(err, netsec.ErrMalformedEscape) {
			t.Fatalf("CanonicalizeQuery(%q) error = %v, want ErrMalformedEscape", raw, err)
		}
	}
	if _, err := netsec.CanonicalizeQuery("v=%C3%28"); !errors.Is(err, netsec.ErrInvalidUTF8) {
		t.Fatalf("invalid UTF-8 error = %v, want ErrInvalidUTF8", err)
	}
	if _, err := netsec.CanonicalizeQuery("v=%00"); !errors.Is(err, netsec.ErrIllegalCharacter) {
		t.Fatalf("NUL error = %v, want ErrIllegalCharacter", err)
	}
	if _, err := netsec.CanonicalizeQuery("v=%EF%BB%BF"); !errors.Is(err, netsec.ErrIllegalCharacter) {
		t.Fatalf("BOM error = %v, want ErrIllegalCharacter", err)
	}

	space, err := netsec.CanonicalizeQuery("v=%20")
	if err != nil {
		t.Fatal(err)
	}
	doubleSpace, err := netsec.CanonicalizeQuery("v=%2520")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(space, doubleSpace) {
		t.Fatal("one-pass and double-encoded space must stay distinct")
	}

	nfc, err := netsec.CanonicalizeQuery("v=caf%C3%A9")
	if err != nil {
		t.Fatal(err)
	}
	nfd, err := netsec.CanonicalizeQuery("v=cafe%CC%81")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(nfc, nfd) {
		t.Fatal("Unicode normalization must not silently unify different byte sequences")
	}

	rawUnicode, err := netsec.CanonicalizeQuery("v=中")
	if err != nil {
		t.Fatal(err)
	}
	escapedUnicode, err := netsec.CanonicalizeQuery("v=%E4%B8%AD")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawUnicode, escapedUnicode) {
		t.Fatalf("UTF-8 spelling difference changed content: %q vs %q", rawUnicode, escapedUnicode)
	}
}

func TestDuplicateKeysAndIllegalCharacters(t *testing.T) {
	if _, err := netsec.Canonicalize([]netsec.Field{
		{Key: "a", Value: "1"},
		{Key: "a", Value: "2"},
	}); !errors.Is(err, netsec.ErrDuplicateKey) {
		t.Fatalf("duplicate fields error = %v, want ErrDuplicateKey", err)
	}

	for _, raw := range []string{"a=1&a=2", "a=1&%61=2"} {
		if _, err := netsec.CanonicalizeQuery(raw); !errors.Is(err, netsec.ErrDuplicateKey) {
			t.Fatalf("duplicate query %q error = %v, want ErrDuplicateKey", raw, err)
		}
	}

	if _, err := netsec.CanonicalizeValues(url.Values{"a": {"1", "2"}}); !errors.Is(err, netsec.ErrDuplicateKey) {
		t.Fatalf("url.Values duplicate error = %v, want ErrDuplicateKey", err)
	}

	if _, err := netsec.Canonicalize([]netsec.Field{{Key: "", Value: "1"}}); !errors.Is(err, netsec.ErrEmptyKey) {
		t.Fatalf("empty key error = %v, want ErrEmptyKey", err)
	}
	if _, err := netsec.CanonicalizeQuery("=v"); !errors.Is(err, netsec.ErrEmptyKey) {
		t.Fatalf("empty query key error = %v, want ErrEmptyKey", err)
	}

	for _, raw := range []string{"a=1&&b=2", "a=1&", "&a=1"} {
		if _, err := netsec.CanonicalizeQuery(raw); !errors.Is(err, netsec.ErrEmptyComponent) {
			t.Fatalf("empty segment %q error = %v, want ErrEmptyComponent", raw, err)
		}
	}
	if _, err := netsec.CanonicalizeQuery("a=b=c"); !errors.Is(err, netsec.ErrAmbiguousSeparator) {
		t.Fatalf("raw equals error = %v, want ErrAmbiguousSeparator", err)
	}
	if _, err := netsec.CanonicalizeQuery("a=b#c"); !errors.Is(err, netsec.ErrAmbiguousSeparator) {
		t.Fatalf("raw hash error = %v, want ErrAmbiguousSeparator", err)
	}

	for _, field := range []netsec.Field{
		{Key: "a", Value: "\xff"},
		{Key: "a", Value: "\x00"},
		{Key: "a", Value: "\n"},
		{Key: "a", Value: "\uFEFF"},
		{Key: "a", Value: "\uFFFF"},
		{Key: "a", Value: "\u200B"},
		{Key: "a", Value: "\u202E"},
		{Key: "a", Value: "\u2028"},
	} {
		if _, err := netsec.Canonicalize([]netsec.Field{field}); err == nil {
			t.Fatalf("illegal field %#v was accepted", field)
		}
	}
	if _, err := netsec.Canonicalize([]netsec.Field{
		{Key: "a", Value: "x", Null: true},
	}); !errors.Is(err, netsec.ErrNullWithValue) {
		t.Fatalf("null with value error = %v, want ErrNullWithValue", err)
	}
}

func TestNullAndEmptyAreDistinct(t *testing.T) {
	nullValue, err := netsec.Canonicalize([]netsec.Field{{Key: "a", Null: true}})
	if err != nil {
		t.Fatal(err)
	}
	emptyValue, err := netsec.Canonicalize([]netsec.Field{{Key: "a", Value: ""}})
	if err != nil {
		t.Fatal(err)
	}
	if string(nullValue) != "a" {
		t.Fatalf("null canonical = %q", nullValue)
	}
	if string(emptyValue) != "a=" {
		t.Fatalf("empty canonical = %q", emptyValue)
	}
	if bytes.Equal(nullValue, emptyValue) {
		t.Fatal("null and empty must be distinct")
	}
	if err := netsec.ValidateCanonical(nullValue); err != nil {
		t.Fatalf("null is not canonical: %v", err)
	}
	if err := netsec.ValidateCanonical(emptyValue); err != nil {
		t.Fatalf("empty is not canonical: %v", err)
	}

	queryNull, err := netsec.CanonicalizeQuery("a")
	if err != nil {
		t.Fatal(err)
	}
	queryEmpty, err := netsec.CanonicalizeQuery("a=")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(queryNull, nullValue) || !bytes.Equal(queryEmpty, emptyValue) {
		t.Fatalf("query null/empty mismatch: %q %q", queryNull, queryEmpty)
	}

	empty := ""
	withPointers, err := netsec.Canonicalize(map[string]*string{
		"a": nil,
		"b": &empty,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(withPointers) != "a&b=" {
		t.Fatalf("pointer null/empty canonical = %q", withPointers)
	}

	nullValues, err := netsec.CanonicalizeValues(url.Values{"a": {}})
	if err != nil {
		t.Fatal(err)
	}
	if string(nullValues) != "a" {
		t.Fatalf("url.Values null canonical = %q", nullValues)
	}
}

func TestSignatureRoundTripAndReencoding(t *testing.T) {
	secret := []byte("correct horse battery staple")
	fields := []netsec.Field{
		{Key: "b", Value: "2"},
		{Key: "a", Value: "1 2"},
	}
	canonicalBytes := []byte("a=1%202&b=2")

	signature := netsec.HMACSign(secret, canonicalBytes)
	if !netsec.HMACVerify(secret, canonicalBytes, signature) {
		t.Fatal("valid HMAC did not verify")
	}
	if netsec.HMACVerify(secret, []byte("a=1+2&b=2"), signature) {
		t.Fatal("HMAC verified different bytes")
	}

	signer := netsec.NewHMACSigner(secret)
	gotSignature, err := signer.Sign(fields)
	if err != nil {
		t.Fatalf("Sign(fields): %v", err)
	}
	if !bytes.Equal(gotSignature, signature) {
		t.Fatalf("Sign(fields) = %x, want %x", gotSignature, signature)
	}
	ok, err := signer.Verify(fields, gotSignature)
	if err != nil || !ok {
		t.Fatalf("Verify(fields) = %v, %v", ok, err)
	}
	ok, err = signer.Verify([]netsec.Field{
		{Key: "b", Value: "2"},
		{Key: "a", Value: "1  2"},
	}, gotSignature)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("tampered field value verified")
	}

	ok, err = signer.Verify("a=1%202&b=2", gotSignature)
	if err != nil || !ok {
		t.Fatalf("Verify(canonical string) = %v, %v", ok, err)
	}
	if _, err := signer.Sign("b=2&a=1%202"); !errors.Is(err, netsec.ErrNonCanonical) {
		t.Fatalf("Sign(noncanonical query) error = %v, want ErrNonCanonical", err)
	}

	querySignature, err := signer.SignQuery("b=2&a=1%202")
	if err != nil {
		t.Fatalf("SignQuery: %v", err)
	}
	ok, err = signer.VerifyCanonicalQuery("a=1%202&b=2", querySignature)
	if err != nil || !ok {
		t.Fatalf("VerifyCanonicalQuery(canonical) = %v, %v", ok, err)
	}

	lowerHexSignature, err := signer.SignQuery("v=%2F")
	if err != nil {
		t.Fatal(err)
	}
	ok, err = signer.VerifyQuery("v=%2f", lowerHexSignature)
	if err != nil || !ok {
		t.Fatalf("VerifyQuery(equivalent spelling) = %v, %v", ok, err)
	}
	for _, recoded := range []string{"v=%2f", "v=/"} {
		ok, err = signer.VerifyCanonicalQuery(recoded, lowerHexSignature)
		if err == nil || ok {
			t.Fatalf("VerifyCanonicalQuery(%q) = %v, %v; want error", recoded, ok, err)
		}
	}
	ok, err = signer.VerifyCanonicalQuery("v=%2F", lowerHexSignature)
	if err != nil || !ok {
		t.Fatalf("VerifyCanonicalQuery(canonical) = %v, %v", ok, err)
	}
	if !netsec.VerifySignature(secret, "v=%2F", lowerHexSignature) {
		t.Fatal("VerifySignature rejected canonical bytes")
	}
	if netsec.VerifySignature(secret, "v=%2f", lowerHexSignature) {
		t.Fatal("VerifySignature accepted re-encoded bytes")
	}

	if netsec.HMACVerify(secret, []byte("v=%2520"), lowerHexSignature) {
		t.Fatal("re-encoded content verified against the original signature")
	}

	hexSignature, err := netsec.SignHex(secret, fields)
	if err != nil {
		t.Fatalf("SignHex: %v", err)
	}
	ok, err = netsec.VerifyHex(secret, fields, hexSignature)
	if err != nil || !ok {
		t.Fatalf("VerifyHex = %v, %v", ok, err)
	}
	if _, err := netsec.VerifyHex(secret, fields, "not-hex"); err == nil {
		t.Fatal("invalid hex signature was accepted")
	}
}

func TestCanonicalInputIsUniqueForEquivalentMaps(t *testing.T) {
	first, err := netsec.Canonicalize(map[string]string{
		"space": "a b",
		"plus":  "a+b",
		"pct":   "a%20b",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := netsec.Canonicalize(map[string]string{
		"pct":   "a%20b",
		"space": "a b",
		"plus":  "a+b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("same map content produced different bytes: %q vs %q", first, second)
	}
	if string(first) != "pct=a%2520b&plus=a%2Bb&space=a%20b" {
		t.Fatalf("canonical = %q", first)
	}
}

func TestSubpackageIsImportable(t *testing.T) {
	got, err := canonical.Canonicalize(map[string]string{"b": "2", "a": "1"})
	if err != nil {
		t.Fatalf("canonical.Canonicalize: %v", err)
	}
	if string(got) != "a=1&b=2" {
		t.Fatalf("canonical.Canonicalize = %q", got)
	}
	if err := canonical.ValidateCanonical(got); err != nil {
		t.Fatalf("canonical.ValidateCanonical: %v", err)
	}
	var field canonical.Field = netsec.Field{Key: "a", Value: "1"}
	if field.Key != "a" {
		t.Fatalf("alias field = %#v", field)
	}
}

func TestUnsupportedInput(t *testing.T) {
	if _, err := netsec.Canonicalize(123); !errors.Is(err, netsec.ErrUnsupportedInput) {
		t.Fatalf("unsupported input error = %v", err)
	}
	if _, err := netsec.Canonicalize([]string{"a"}); !errors.Is(err, netsec.ErrUnsupportedInput) {
		t.Fatalf("unsupported slice error = %v", err)
	}
	if err := netsec.ValidateCanonical(123); !errors.Is(err, netsec.ErrUnsupportedInput) {
		t.Fatalf("unsupported validation error = %v", err)
	}
	if _, err := netsec.Canonicalize(map[string]any{"a": 1}); !errors.Is(err, netsec.ErrUnsupportedInput) {
		t.Fatalf("unsupported map value error = %v", err)
	}
}

func TestGenericMapInput(t *testing.T) {
	got, err := netsec.Canonicalize(map[string]any{
		"b": "2",
		"a": nil,
	})
	if err != nil {
		t.Fatalf("Canonicalize(map[string]any): %v", err)
	}
	if string(got) != "a&b=2" {
		t.Fatalf("generic map canonical = %q", got)
	}
}

func FuzzCanonicalQueryIdempotent(f *testing.F) {
	for _, seed := range []string{
		"",
		"a=1&b=2",
		"a=%2f",
		"a=%2520",
		"a",
		"a=",
		"a=%E4%B8%AD",
		"a=+",
		"a=%",
		"a=1&a=2",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		canonicalBytes, err := netsec.CanonicalizeQuery(raw)
		if err != nil {
			return
		}
		if err := netsec.ValidateCanonical(canonicalBytes); err != nil {
			t.Fatalf("CanonicalizeQuery(%q) produced invalid canonical bytes %q: %v", raw, canonicalBytes, err)
		}
		again, err := netsec.CanonicalizeQuery(string(canonicalBytes))
		if err != nil {
			t.Fatalf("re-canonicalizing %q: %v", canonicalBytes, err)
		}
		if !bytes.Equal(canonicalBytes, again) {
			t.Fatalf("non-idempotent: %q -> %q -> %q", raw, canonicalBytes, again)
		}
	})
}
