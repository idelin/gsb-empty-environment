package netsec

import (
	"errors"
	"testing"
)

func TestCanonicalCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"example.com.", "example.com.", 0},
		{"a.example.com.", "example.com.", 1},
		{"example.com.", "b.example.com.", -1},
		{"a.example.com.", "b.example.com.", -1},
		{"z.example.com.", "a.example.org.", -1},         // "com" < "org" dominates
		{"*.sub.example.com.", "a.sub.example.com.", -1}, // "*" (0x2a) < "a"
	}
	for _, tc := range cases {
		aw, _ := MustParseName(tc.a).WireEncode()
		bw, _ := MustParseName(tc.b).WireEncode()
		if got := canonicalCompare(aw, bw); got != tc.want {
			t.Errorf("canonicalCompare(%s,%s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIntervalContains(t *testing.T) {
	owner := MustParseName("sub.example.com.")
	next := MustParseName("zzz.example.com.")
	rr := NSEC{OwnerName: owner, NextDomainName: next}

	if intervalContains(rr, owner) {
		t.Fatal("interval must not contain its own owner")
	}
	if !intervalContains(rr, MustParseName("*.sub.example.com.")) {
		t.Fatal("interval should contain wildcard next-closer")
	}
	// A name beyond the interval's right edge is outside (non-wrapping).
	early := NSEC{
		OwnerName:      MustParseName("a.example.com."),
		NextDomainName: MustParseName("m.example.com."),
	}
	if intervalContains(early, MustParseName("z.example.com.")) {
		t.Fatal("z.example.com must be outside the (a, m] interval")
	}
	if !intervalContains(early, MustParseName("g.example.com.")) {
		t.Fatal("g.example.com must be inside the (a, m] interval")
	}

	wrapping := NSEC{
		OwnerName:      MustParseName("zzz.example.com."),
		NextDomainName: MustParseName("example.com."),
	}
	if !intervalContains(wrapping, MustParseName("zzzz.example.com.")) {
		t.Fatal("wrapping interval should contain names after owner")
	}
	// The wrap reaches the apex itself; the apex is the right edge and the
	// root "." sorts below it, so only root (not a normal subdomain) lies in
	// the wrapped portion for an apex-terminating interval.
	if !intervalContains(wrapping, MustParseName(".")) {
		t.Fatal("wrapping interval should contain the root below the apex edge")
	}
	if intervalContains(wrapping, MustParseName("a.example.com.")) {
		t.Fatal("a.example.com sorts above apex, outside wrapped interval")
	}
}

func TestVerifyNSECProofRequirements(t *testing.T) {
	encloser := MustParseName("sub.example.com.")
	qname := MustParseName("a.b.sub.example.com.")

	if err := VerifyNSECProof(nil, qname, encloser); !errors.Is(err, ErrProofMissing) {
		t.Fatalf("nil proof: want ErrProofMissing, got %v", err)
	}

	// Records present but no NSEC owned by the closest encloser; the only
	// interval here belongs to a different branch and cannot deny the
	// next-closer wildcard either.
	noEncloser := &NSECProof{Records: []NSEC{{
		OwnerName:      MustParseName("other.example.com."),
		NextDomainName: MustParseName("zzz.example.com."),
	}}}
	if err := VerifyNSECProof(noEncloser, qname, encloser); !errors.Is(err, ErrNoClosestEncloserNSEC) {
		t.Fatalf("want ErrNoClosestEncloserNSEC, got %v", err)
	}

	// SOA at the closest encloser is contradictory for an empty non-terminal.
	soa := &NSECProof{Records: []NSEC{{
		OwnerName:      encloser,
		NextDomainName: MustParseName("zzz.example.com."),
		Types:          []uint16{TypeSOA, TypeRRSIG, TypeNSEC},
	}}}
	if err := VerifyNSECProof(soa, qname, encloser); !errors.Is(err, ErrClosestEncloserHasSOA) {
		t.Fatalf("want ErrClosestEncloserHasSOA, got %v", err)
	}

	// A conforming proof passes.
	good := &NSECProof{Records: []NSEC{{
		OwnerName:      encloser,
		NextDomainName: MustParseName("zzz.example.com."),
		Types:          []uint16{TypeRRSIG, TypeNSEC},
	}}}
	if err := VerifyNSECProof(good, qname, encloser); err != nil {
		t.Fatalf("valid proof: %v", err)
	}
}
