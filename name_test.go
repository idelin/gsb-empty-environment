package netsec

import "testing"

func TestParseNameCanonicalWire(t *testing.T) {
	a := MustParseName("WWW.Example.COM.")
	b := MustParseName("www.example.com")
	if !a.Equal(b) {
		t.Fatalf("case/trailing-dot names differ: %s vs %s", a, b)
	}
	if got := a.String(); got != "www.example.com." {
		t.Fatalf("String() = %q", got)
	}
	if got := a.LabelCount(); got != 3 {
		t.Fatalf("LabelCount = %d", got)
	}
	parent, ok := a.Parent()
	if !ok || !parent.Equal(MustParseName("example.com.")) {
		t.Fatalf("Parent = %s, %v", parent, ok)
	}
}

func TestParseNameEscapes(t *testing.T) {
	n, err := ParseName(`a\.b.example.`)
	if err != nil {
		t.Fatal(err)
	}
	if n.LabelCount() != 2 {
		t.Fatalf("escaped dot produced %d labels", n.LabelCount())
	}
	if got := n.String(); got != `a\.b.example.` {
		t.Fatalf("String = %q", got)
	}
	if _, err := ParseName("bad..example."); err == nil {
		t.Fatal("accepted empty label")
	}
}

func TestCanonicalNameOrderAndNSECCoverage(t *testing.T) {
	apex := MustParseName("example.")
	a := MustParseName("a.example.")
	b := MustParseName("b.example.")
	if CompareNames(apex, a) >= 0 || CompareNames(a, b) >= 0 {
		t.Fatal("canonical name order is wrong")
	}
	if !NSECCoveredBy(a, b, MustParseName("a1.example.")) {
		t.Fatal("simple NSEC interval did not cover")
	}
	if NSECCoveredBy(a, b, MustParseName("b.example.")) {
		t.Fatal("NSEC interval incorrectly covered its next name")
	}
	if !NSECCoveredBy(b, a, MustParseName("z.example.")) {
		t.Fatal("wrapping NSEC interval did not cover")
	}
}

func TestNSEC3HashMatchesRFC5155Vector(t *testing.T) {
	apex := MustParseName("example.")
	p := ParamsNSEC3(12, []byte{0xaa, 0xbb, 0xcc, 0xdd})
	gotApex := EncodeBase32Hex(mustHash(t, p, apex))
	if gotApex != "0P9MHAVEQVM6T7VBL5LOP2U3T2RP3TOM" {
		t.Fatalf("apex NSEC3 hash = %s", gotApex)
	}
	decoded, err := DecodeBase32Hex("0p9mhaveqvm6t7vbl5lop2u3t2rp3tom")
	if err != nil {
		t.Fatal(err)
	}
	if EncodeBase32Hex(decoded) != gotApex {
		t.Fatal("base32hex decode/encode round trip failed")
	}
	p2 := ParamsNSEC3(11, []byte{0xaa, 0xbb, 0xcc, 0xdd})
	if EncodeBase32Hex(mustHash(t, p2, apex)) == gotApex {
		t.Fatal("iteration count is not bound into the NSEC3 hash")
	}
	p3 := ParamsNSEC3(12, []byte{0xaa, 0xbb, 0xcc})
	if EncodeBase32Hex(mustHash(t, p3, apex)) == gotApex {
		t.Fatal("salt is not bound into the NSEC3 hash")
	}
	if _, err := DecodeBase32Hex("1"); err == nil {
		t.Fatal("decoder accepted non-zero leftover bits")
	}
}

func TestTypeBitmapWireRoundTrip(t *testing.T) {
	in := MustTypeBitmap(TypeA, TypeNS, TypeDS, TypeRRSIG, TypeNSEC, 1234)
	wire := in.Wire()
	out, err := ParseTypeBitmap(wire)
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range in.Types {
		if !out.Has(typ) {
			t.Fatalf("round trip lost type %d", typ)
		}
	}
	if len(out.Types) != len(in.Types) {
		t.Fatalf("round trip type count = %d", len(out.Types))
	}
}
