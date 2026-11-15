package netsec

import (
	"errors"
	"testing"
)

func TestCanonicalCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		// Case and trailing dot do not affect identity.
		{"Example.", "example", 0},
		// Shorter names sort first when shared suffixes match.
		{"example.", "a.example.", -1},
		{"a.example.", "example.", 1},
		// Rightmost labels are most significant.
		{"a.example.", "b.example.", -1},
		{"z.example.", "a.test.", -1},
		// Different branch names ordered by the first differing label.
		{"a.z.example.", "b.a.example.", 1},
	}
	for _, tc := range tests {
		got, err := canonicalCompare(tc.a, tc.b)
		if err != nil {
			t.Fatalf("canonicalCompare(%q,%q) error %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Fatalf("canonicalCompare(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestNSECCovers(t *testing.T) {
	// Ordinary interval (example., ns1.example.) covers names strictly
	// between them in canonical order.
	nsec := NSECRR{OwnerName: "example.", NextName: "ns1.example."}

	covered := []string{"a.example.", "host.example.", "m.example."}
	for _, name := range covered {
		ok, err := nsec.Covers(name)
		if err != nil {
			t.Fatalf("Covers(%q) error %v", name, err)
		}
		if !ok {
			t.Fatalf("Covers(%q) = false, want true", name)
		}
	}

	notCovered := []string{
		"example.",     // owner itself is outside the open interval
		"ns1.example.", // next name is outside the open interval
		"z.example.",   // sorts after the next name
		"other.test.",  // outside the zone ordering
	}
	for _, name := range notCovered {
		ok, err := nsec.Covers(name)
		if err != nil {
			t.Fatalf("Covers(%q) error %v", name, err)
		}
		if ok {
			t.Fatalf("Covers(%q) = true, want false", name)
		}
	}

	// Wrap-around NSEC covers everything strictly after its owner.
	wrap := NSECRR{OwnerName: "z.example.", NextName: "a.example."}
	ok, err := wrap.Covers("zz.example.")
	if err != nil || !ok {
		t.Fatalf("wrap Covers(zz.example.) = %v, %v", ok, err)
	}
	ok, err = wrap.Covers("z.example.")
	if err != nil || ok {
		t.Fatalf("wrap Covers(owner) = %v, %v", ok, err)
	}

	// Malformed names surface as errors rather than false results.
	if _, err := nsec.Covers("a..example."); !errors.Is(err, ErrMalformedName) {
		t.Fatalf("Covers malformed name error = %v, want ErrMalformedName", err)
	}
	bad := NSECRR{OwnerName: "a.example.", NextName: "a..example."}
	if _, err := bad.Covers("b.example."); !errors.Is(err, ErrMalformedName) {
		t.Fatalf("bad next name error = %v, want ErrMalformedName", err)
	}
	if err := VerifyNSECProof("example.", "b.example.", []NSECRR{bad}); !errors.Is(err, ErrMalformedName) {
		t.Fatalf("VerifyNSECProof malformed error = %v, want ErrMalformedName", err)
	}
}

func TestVerifyNSECProof(t *testing.T) {
	const encloser = "example."
	const nextCloser = "b.example."

	// One NSEC owned by the encloser whose interval also covers the next
	// closer: both facts from a single record.
	good := []NSECRR{{OwnerName: "example.", NextName: "c.example."}}
	if err := VerifyNSECProof(encloser, nextCloser, good); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}

	// Two records split across the two facts are also acceptable.
	split := []NSECRR{
		{OwnerName: "example.", NextName: "a.example."},   // existence of encloser
		{OwnerName: "a.example.", NextName: "c.example."}, // covers b.example.
	}
	if err := VerifyNSECProof(encloser, nextCloser, split); err != nil {
		t.Fatalf("split proof rejected: %v", err)
	}

	// Missing proof entirely.
	if err := VerifyNSECProof(encloser, nextCloser, nil); !errors.Is(err, ErrNoClosestEncloserProof) {
		t.Fatalf("nil proof error = %v, want ErrNoClosestEncloserProof", err)
	}

	// Encloser exists but nothing covers the next closer.
	onlyEncloser := []NSECRR{{OwnerName: "example.", NextName: "a.example."}}
	if err := VerifyNSECProof(encloser, nextCloser, onlyEncloser); !errors.Is(err, ErrNoNextCloserProof) {
		t.Fatalf("missing closer error = %v, want ErrNoNextCloserProof", err)
	}

	// An interval covers the next closer but the encloser owner is absent.
	onlyCloser := []NSECRR{{OwnerName: "a.example.", NextName: "c.example."}}
	if err := VerifyNSECProof(encloser, nextCloser, onlyCloser); !errors.Is(err, ErrNoClosestEncloserProof) {
		t.Fatalf("missing encloser error = %v, want ErrNoClosestEncloserProof", err)
	}
}

func TestAnalyzeAndVerifyWildcard(t *testing.T) {
	// a.b.example. synthesized from *.example. with a complete NSEC proof.
	rr := RRSIG{OwnerName: "a.b.example.", Labels: 2}
	proof := []NSECRR{{OwnerName: "b.example.", NextName: "z.b.example."}}

	analysis, err := AnalyzeAndVerifyWildcard(rr, proof)
	if err != nil {
		t.Fatalf("valid wildcard answer rejected: %v", err)
	}
	if !analysis.WildcardExpanded || analysis.SignedName != "*.b.example." {
		t.Fatalf("unexpected analysis: %+v", analysis)
	}
	if analysis.ClosestEncloser != "b.example." || analysis.NextCloserName != "a.b.example." {
		t.Fatalf("unexpected encloser/closer: %+v", analysis)
	}

	// Same expansion but with no NSEC proof: must be refused.
	if _, err := AnalyzeAndVerifyWildcard(rr, nil); !errors.Is(err, ErrNoClosestEncloserProof) {
		t.Fatalf("missing proof error = %v, want ErrNoClosestEncloserProof", err)
	}

	// Encloser NSEC present but its interval stops before the next closer.
	incomplete := []NSECRR{{OwnerName: "b.example.", NextName: "a.b.example."}}
	if _, err := AnalyzeAndVerifyWildcard(rr, incomplete); !errors.Is(err, ErrNoNextCloserProof) {
		t.Fatalf("incomplete proof error = %v, want ErrNoNextCloserProof", err)
	}

	// Forged labels are refused before the proof is even consulted.
	forged := RRSIG{OwnerName: "a.b.example.", Labels: 9}
	if _, err := AnalyzeAndVerifyWildcard(forged, proof); !errors.Is(err, ErrLabelsOverflow) {
		t.Fatalf("forgery error = %v, want ErrLabelsOverflow", err)
	}

	// A concrete owner needs no NSEC records and verifies as non-wildcard.
	concrete := RRSIG{OwnerName: "a.b.example.", Labels: 3}
	analysis, err = AnalyzeAndVerifyWildcard(concrete, nil)
	if err != nil {
		t.Fatalf("concrete answer rejected: %v", err)
	}
	if analysis.WildcardExpanded || analysis.SignedName != "a.b.example." {
		t.Fatalf("unexpected concrete analysis: %+v", analysis)
	}
}
