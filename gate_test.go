package netsec

import (
	"bytes"
	"errors"
	"testing"
)

// fx encodes a small non-negative fixnum in the single-byte marshal form.
func fx(n int) byte { return byte(n + 5) }

// sym encodes a literal symbol record.
func sym(name string) []byte {
	return append([]byte{':', fx(len(name))}, name...)
}

// str encodes a raw string record (no ivar wrapper).
func str(s string) []byte {
	return append([]byte{'"', fx(len(s))}, s...)
}

// istr encodes a string carrying the encoding ivar, as Ruby 1.9+ emits it.
func istr(s string) []byte {
	b := []byte{'I'}
	b = append(b, str(s)...)
	b = append(b, fx(1))
	b = append(b, sym("E")...)
	b = append(b, 'T')
	return b
}

func stream(parts ...[]byte) []byte {
	return bytes.Join(append([][]byte{{0x04, 0x08}}, parts...), nil)
}

func requireErr(t *testing.T, err, sentinel error) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("want error matching %v, got %v", sentinel, err)
	}
}

func TestValidNestedData(t *testing.T) {
	// Equivalent of Marshal.dump({"k" => ["a", "a", 42, nil, true, :s]})
	// with the second "a" stored as an object link to the first.
	hash := []byte{'{', fx(1)}
	hash = append(hash, istr("k")...)
	arr := []byte{'[', fx(7)}
	arr = append(arr, istr("a")...)
	arr = append(arr, '@', fx(1)) // object link to the string above
	arr = append(arr, 'i', fx(42))
	arr = append(arr, '0', 'T')
	arr = append(arr, sym("s")...)
	arr = append(arr, ';', fx(0)) // symbol link to :E
	hash = append(hash, arr...)

	if err := ValidateMarshal48(stream(hash), Options{}); err != nil {
		t.Fatalf("valid nested stream rejected: %v", err)
	}
}

func TestValidWhitelistedObject(t *testing.T) {
	// o:Foo with one ivar @a = 2, class name later referenced via symlink.
	obj := []byte{'o'}
	obj = append(obj, sym("Foo")...)
	obj = append(obj, fx(1))
	obj = append(obj, sym("a")...)
	obj = append(obj, 'i', fx(2))

	err := ValidateMarshal48(stream(obj), Options{AllowedClasses: []string{"Foo"}})
	if err != nil {
		t.Fatalf("whitelisted object rejected: %v", err)
	}

	// Same class referenced through a symbol link inside an array.
	arr := []byte{'[', fx(2)}
	arr = append(arr, obj...)
	second := []byte{'o', ';', fx(0), fx(0)} // class via link, no ivars
	arr = append(arr, second...)
	err = ValidateMarshal48(stream(arr), Options{AllowedClasses: []string{"Foo"}})
	if err != nil {
		t.Fatalf("whitelisted object via symbol link rejected: %v", err)
	}
}

func TestRejectUserDefinedTypeCode(t *testing.T) {
	// 'u' (user-defined _dump payload) must never reach the deserializer.
	u := []byte{'u'}
	u = append(u, sym("Evil")...)
	u = append(u, fx(3), 'b', 'o', 'm')
	requireErr(t, ValidateMarshal48(stream(u), Options{}), ErrBadType)
}

func TestRejectOtherDangerousTypeCodes(t *testing.T) {
	for _, code := range []byte{'U', 'e', 'C', 'd', 'S', 'M', 'c', 'm', 'f', 'l', '/', '}', 0x7f} {
		err := ValidateMarshal48(stream([]byte{code}), Options{})
		if !errors.Is(err, ErrBadType) {
			t.Errorf("type code 0x%02x: want ErrBadType, got %v", code, err)
		}
	}
}

func TestRejectNonWhitelistedClass(t *testing.T) {
	obj := []byte{'o'}
	obj = append(obj, sym("Gem::Requirement")...)
	obj = append(obj, fx(0))

	requireErr(t,
		ValidateMarshal48(stream(obj), Options{AllowedClasses: []string{"Foo"}}),
		ErrClassDenied)
	requireErr(t,
		ValidateMarshal48(stream(obj), Options{}),
		ErrClassDenied)
}

func TestRejectForgedLinks(t *testing.T) {
	// Object link before any object exists.
	requireErr(t,
		ValidateMarshal48(stream([]byte{'@', fx(0)}), Options{}),
		ErrBadLink)
	// Symbol link before any symbol exists.
	requireErr(t,
		ValidateMarshal48(stream([]byte{';', fx(0)}), Options{}),
		ErrBadLink)
	// Forward object link inside an array: index 1 is not registered yet.
	arr := []byte{'[', fx(2), '@', fx(1), '0'}
	requireErr(t,
		ValidateMarshal48(stream(arr), Options{}),
		ErrBadLink)
	// Forward symbol link: only one symbol (:a) exists, link points to 1.
	fwd := []byte{'[', fx(2)}
	fwd = append(fwd, sym("a")...)
	fwd = append(fwd, ';', fx(1))
	requireErr(t,
		ValidateMarshal48(stream(fwd), Options{}),
		ErrBadLink)
}

func TestSelfReferentialArrayIsLegal(t *testing.T) {
	// An array registered at index 0 containing a link to itself; this is
	// what Marshal.dump(a = []; a << a) produces and must be accepted.
	self := []byte{'[', fx(1), '@', fx(0)}
	if err := ValidateMarshal48(stream(self), Options{}); err != nil {
		t.Fatalf("self-referential array rejected: %v", err)
	}
}

func TestDepthBudget(t *testing.T) {
	deep := []byte{}
	for i := 0; i < 10; i++ {
		deep = append(deep, '[', fx(1))
	}
	deep = append(deep, '0')
	requireErr(t,
		ValidateMarshal48(stream(deep), Options{MaxDepth: 5}),
		ErrDepth)
	if err := ValidateMarshal48(stream(deep), Options{MaxDepth: 16}); err != nil {
		t.Fatalf("depth within budget rejected: %v", err)
	}
}

func TestElementBudget(t *testing.T) {
	wide := []byte{'[', fx(20)}
	for i := 0; i < 20; i++ {
		wide = append(wide, 'i', fx(i))
	}
	requireErr(t,
		ValidateMarshal48(stream(wide), Options{MaxElements: 10}),
		ErrElements)
	if err := ValidateMarshal48(stream(wide), Options{MaxElements: 64}); err != nil {
		t.Fatalf("element count within budget rejected: %v", err)
	}
}

func TestBadHeaderAndTruncation(t *testing.T) {
	requireErr(t, ValidateMarshal48(nil, Options{}), ErrBadHeader)
	requireErr(t, ValidateMarshal48([]byte{0x04, 0x09, '0'}, Options{}), ErrBadHeader)
	requireErr(t, ValidateMarshal48([]byte{0x04, 0x08}, Options{}), ErrTruncated)
	requireErr(t, ValidateMarshal48(stream([]byte{'"', fx(9), 'a'}), Options{}), ErrTruncated)
	requireErr(t, ValidateMarshal48(stream([]byte{'[', fx(2), '0'}), Options{}), ErrTruncated)
}

func TestTrailingBytes(t *testing.T) {
	requireErr(t,
		ValidateMarshal48(stream([]byte{'0', '0'}), Options{}),
		ErrTrailing)
}

func TestFixnumEncodings(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"zero", []byte{'i', 0}},
		{"small positive", []byte{'i', fx(123)}},
		{"small negative", []byte{'i', 0x80 + 5}}, // -123
		{"multibyte positive", []byte{'i', 2, 0x39, 0x30}},
		{"multibyte negative", []byte{'i', 0xFE, 0xC7, 0xCF}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateMarshal48(stream(tc.body), Options{}); err != nil {
				t.Fatalf("valid fixnum rejected: %v", err)
			}
		})
	}
}
