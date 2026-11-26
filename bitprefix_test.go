package kt

import "testing"

func TestBitPrefixBasics(t *testing.T) {
	p, err := PrefixFromString("010111")
	if err != nil {
		t.Fatal(err)
	}
	if p.Len() != 6 || p.String() != "010111" {
		t.Fatalf("bad prefix %q len %d", p.String(), p.Len())
	}
	parent, err := p.Parent()
	if err != nil || parent.String() != "01011" {
		t.Fatalf("parent = %q, err %v", parent, err)
	}
	sib, err := p.Sibling()
	if err != nil || sib.String() != "010110" {
		t.Fatalf("sibling = %q, err %v", sib, err)
	}
	if !parent.IsPrefixOf(p) || p.IsPrefixOf(parent) {
		t.Fatal("prefix relation wrong")
	}
	q := mustPrefix(t, "010110")
	if p.CommonPrefixLen(q) != 5 {
		t.Fatalf("common prefix len = %d", p.CommonPrefixLen(q))
	}
	enc, err := p.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var back BitPrefix
	if err := back.UnmarshalBinary(enc); err != nil || !back.Equal(p) {
		t.Fatalf("round trip failed: %v", err)
	}
}

func TestBitPrefix256Limit(t *testing.T) {
	p := EmptyPrefix()
	for i := 0; i < MaxLabelBits; i++ {
		p, _ = p.AppendBit(i % 2)
	}
	if _, err := p.AppendBit(0); err != ErrLabelTooLong {
		t.Fatalf("expected ErrLabelTooLong, got %v", err)
	}
	if p.Len() != 256 {
		t.Fatalf("len = %d", p.Len())
	}
}

func TestBitPrefixEncodingRejectsDirt(t *testing.T) {
	// 3 significant bits but a dirty low bit must be rejected.
	if _, err := NewBitPrefix([]byte{0x01}, 3); err != ErrPrefixEncoding {
		t.Fatalf("got %v", err)
	}
	if _, err := NewBitPrefix([]byte{0x20}, 3); err != nil {
		t.Fatalf("valid encoding rejected: %v", err)
	}
}

func TestDisjointAndOrder(t *testing.T) {
	a := mustPrefix(t, "00")
	b := mustPrefix(t, "01")
	c := mustPrefix(t, "0")
	if Disjoint([]BitPrefix{a, b}) != true {
		t.Fatal("00 and 01 should be disjoint")
	}
	if Disjoint([]BitPrefix{a, c}) {
		t.Fatal("0 is a prefix of 00: must not be disjoint")
	}
	ps := []BitPrefix{b, a, c}
	SortPrefixes(ps)
	if ps[0].String() != "0" || ps[1].String() != "00" || ps[2].String() != "01" {
		t.Fatalf("bad order: %v", ps)
	}
}
