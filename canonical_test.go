package dnssec

import (
	"bytes"
	"testing"
)

func TestCanonicalNameOrdering(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{".", "a.", -1},
		{"example.", "example.com.", 1},
		{"a.example.com.", "b.example.com.", -1},
		{"A.Example.COM.", "a.example.com.", 0},
		{"a.z.com.", "a.example.com.", 1},
		{"www.example.com.", "example.com.", 1},
		{"a.example.com.", "aa.example.com.", -1},
		{"z.example.com.", "a.example.net.", -1},
	}
	for _, tc := range tests {
		got, err := CompareNameStrings(tc.a, tc.b)
		if err != nil {
			t.Fatalf("CompareNameStrings(%q, %q): %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Errorf("compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCaseFoldedWireName(t *testing.T) {
	n := MustParseName("WwW.ExAmPle.CoM.")
	want := []byte{3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
	if got, err := CanonicalNameWire(n); err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(got, want) {
		t.Fatalf("wire name = %x, want %x", got, want)
	}
	decoded, off, err := ParseWireName(want, 0)
	if err != nil {
		t.Fatal(err)
	}
	if off != len(want) || !EqualNames(decoded, n) {
		t.Fatalf("decoded %s at %d", decoded, off)
	}
}

func TestCanonicalRRsetSorting(t *testing.T) {
	owner := MustParseName("example.com.")
	rrs := []RR{
		{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(0x0a000003)},
		{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(0x0a000001)},
		{Name: owner, Type: TypeA, Class: ClassINET, TTL: 300, Data: makeA(0x0a000002)},
	}
	got, err := CanonicalizeRRset(rrs)
	if err != nil {
		t.Fatal(err)
	}
	for i, data := range [][]byte{makeA(0x0a000001), makeA(0x0a000002), makeA(0x0a000003)} {
		if !bytes.Equal(got[i].Data, data) {
			t.Fatalf("record %d data = %x, want %x", i, got[i].Data, data)
		}
	}
}

func TestCanonicalRDATALowercasesOnlyOwnerNames(t *testing.T) {
	caseName := MustParseName("TaRgEt.ExAmPle.")
	data, err := CanonicalRData(TypeCNAME, caseName.Wire())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, MustParseName("target.example.").Wire()) {
		t.Fatalf("CNAME rdata = %x", data)
	}
}
