package dnssec

import (
	"bytes"
	"testing"
)

func TestCanonicalCompare(t *testing.T) {
	// RFC 4034 section 6.1 example ordering.
	names := []string{
		"example.",
		"a.example.",
		"yljkjljk.a.example.",
		"Z.a.example.",
		"z.a.example.",
		"zABC.a.example.",
		"b.example.",
		"a.b.example.",
		"\x00.z.example.",
		"*.z.example.",
		"\xff.z.example.",
	}
	shuffled := []string{
		"b.example.",
		"*.z.example.",
		"a.example.",
		"zABC.a.EXAMPLE.", // case-insensitive
		"example.",
		"a.b.example.",
		"\x00.z.example.",
		"yljkjljk.a.example.",
		"\xff.z.example.",
		"Z.a.example.",
		"z.a.example.",
	}

	parsed := make([]Name, len(shuffled))
	for i, s := range shuffled {
		n, err := ParseName(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		parsed[i] = n
	}
	// Simple insertion sort using CanonicalCompare.
	for i := 1; i < len(parsed); i++ {
		for j := i; j > 0 && CanonicalCompare(parsed[j-1], parsed[j]) > 0; j-- {
			parsed[j-1], parsed[j] = parsed[j], parsed[j-1]
		}
	}
	for i, want := range names {
		if got := parsed[i].String(); !parsed[i].Equal(MustParseName(want)) {
			t.Errorf("position %d: got %q want %q", i, got, want)
		}
	}
}

func TestCanonicalRRSetDedupAndOrder(t *testing.T) {
	owner := MustParseName("a.example.")
	mk := func(ip byte) RR {
		return RR{Name: owner, Type: TypeA, Class: ClassIN, TTL: 100, Rdata: []byte{192, 0, 2, ip}}
	}
	rrset := []RR{mk(3), mk(1), mk(2), mk(1) /* duplicate */}
	out, err := CanonicalRRSet(rrset, 300)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}

	// Expect three canonical records, each 1 (root-less name is 2 labels)
	// owner-wire + 10 fixed + 4 RDATA = 16 octets.
	recLen := len(owner.LowerWire()) + 10 + 4
	if len(out) != 3*recLen {
		t.Fatalf("got %d bytes, want %d", len(out), 3*recLen)
	}
	for i, ip := range []byte{1, 2, 3} {
		rec := out[i*recLen : (i+1)*recLen]
		if !bytes.Equal(rec[len(rec)-4:], []byte{192, 0, 2, ip}) {
			t.Errorf("record %d not sorted by RDATA", i)
		}
		// Canonical TTL must be the RRSIG Original TTL (300), not 100.
		ttlOff := len(owner.LowerWire()) + 4
		gotTTL := uint32(rec[ttlOff])<<24 | uint32(rec[ttlOff+1])<<16 |
			uint32(rec[ttlOff+2])<<8 | uint32(rec[ttlOff+3])
		if gotTTL != 300 {
			t.Errorf("record %d TTL = %d, want 300", i, gotTTL)
		}
	}
}

func TestCanonicalRdataNameLowercased(t *testing.T) {
	rr := RR{
		Name:  MustParseName("WWW.Example."),
		Type:  TypeCNAME,
		Class: ClassIN,
		TTL:   50,
		Rdata: MustParseName("Target.Example.").Wire(),
	}
	got, err := CanonicalRdata(rr)
	if err != nil {
		t.Fatalf("canonical rdata: %v", err)
	}
	want := MustParseName("target.example.").LowerWire()
	if !bytes.Equal(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}
