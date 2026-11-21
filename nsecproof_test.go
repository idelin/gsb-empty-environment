package nsecproof

import (
	"errors"
	"fmt"
	"testing"
)

func TestCanonicalOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{".", "example.", -1},
		{"a.example.", "b.example.", -1},
		{"example.", "a.example.", -1},
		{"a.example.", "aa.example.", -1},
		{"aa.example.", "b.example.", 1}, // label length dominates octets
		{"A.Example.", "a.example.", 0},  // lowercased
		{"b.example.", "b.example.", 0},
	}
	for _, c := range cases {
		got := compareNames(MustParseName(c.a), MustParseName(c.b))
		if got != c.want {
			t.Errorf("compareNames(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNSECValidateRejectsReversedInterval(t *testing.T) {
	r := NSEC{
		Owner:      MustParseName("z.example."),
		NextDomain: MustParseName("a.example."),
	}
	err := r.Validate(MustParseName("example."))
	if !errors.Is(err, ErrReversedInterval) {
		t.Fatalf("Validate() err = %v, want ErrReversedInterval", err)
	}

	equal := NSEC{
		Owner:      MustParseName("a.example."),
		NextDomain: MustParseName("a.example."),
	}
	if err := equal.Validate(MustParseName("example.")); !errors.Is(err, ErrReversedInterval) {
		t.Fatalf("equal-endpoint interval err = %v, want ErrReversedInterval", err)
	}
}

func TestNSECValidateAcceptsWrapRecord(t *testing.T) {
	r := NSEC{
		Owner:      MustParseName("ns.example."),
		NextDomain: MustParseName("example."),
	}
	if err := r.Validate(MustParseName("example.")); err != nil {
		t.Fatalf("wrap record Validate() err = %v, want nil", err)
	}
}

func TestVerifyRejectsReversedProof(t *testing.T) {
	p := Proof{
		Zone:  MustParseName("example."),
		Qname: MustParseName("b.example."),
		Qtype: "A",
		Rcode: RcodeNXDomain,
		NSECs: []NSEC{
			{Owner: MustParseName("m.example."), NextDomain: MustParseName("a.example.")},
		},
	}
	if err := p.Verify(); !errors.Is(err, ErrReversedInterval) {
		t.Fatalf("Verify() err = %v, want ErrReversedInterval", err)
	}
}

func TestNXDOMAINProofAccepted(t *testing.T) {
	p := Proof{
		Zone:  MustParseName("example."),
		Qname: MustParseName("a.b.example."),
		Qtype: "A",
		Rcode: RcodeNXDomain,
		NSECs: []NSEC{
			{Owner: MustParseName("example."), NextDomain: MustParseName("a.example."), Types: []string{"SOA", "NS"}},
			{Owner: MustParseName("a.example."), NextDomain: MustParseName("c.example.")},
			{Owner: MustParseName("c.example."), NextDomain: MustParseName("ns.example.")},
			{Owner: MustParseName("ns.example."), NextDomain: MustParseName("example.")},
		},
	}
	if err := p.Verify(); err != nil {
		t.Fatalf("Verify() err = %v, want nil", err)
	}
}

func TestWildcardNODATAAccepted(t *testing.T) {
	p := Proof{
		Zone:  MustParseName("example."),
		Qname: MustParseName("host.example."),
		Qtype: "A",
		Rcode: RcodeNoData,
		NSECs: []NSEC{
			// Owner "*" proves the wildcard exists; its interval also denies
			// the next closer name host.example.
			{Owner: MustParseName("*.example."), NextDomain: MustParseName("zzzzz.example."), Types: []string{"RRSIG", "NSEC"}},
		},
	}
	if err := p.Verify(); err != nil {
		t.Fatalf("Verify() err = %v, want nil", err)
	}
}

func TestWildcardExpansionOrderMismatch(t *testing.T) {
	// Closest encloser is example. (sub.example. does not exist), so only
	// *.example. may expand; a *.sub.example. wildcard is one level too deep.
	p := Proof{
		Zone:  MustParseName("example."),
		Qname: MustParseName("x.sub.example."),
		Qtype: "A",
		Rcode: RcodeNoData,
		NSECs: []NSEC{
			{Owner: MustParseName("example."), NextDomain: MustParseName("a.example.")},
			{Owner: MustParseName("a.example."), NextDomain: MustParseName("zzzzz.example.")},
			{Owner: MustParseName("*.sub.example."), NextDomain: MustParseName("x.sub.example.")},
		},
	}
	err := p.Verify()
	if !errors.Is(err, ErrWildcardMismatch) {
		t.Fatalf("Verify() err = %v, want ErrWildcardMismatch", err)
	}
}

func TestNXDOMAINContradictedByWildcard(t *testing.T) {
	p := Proof{
		Zone:  MustParseName("example."),
		Qname: MustParseName("ghost.example."),
		Qtype: "A",
		Rcode: RcodeNXDomain,
		NSECs: []NSEC{
			{Owner: MustParseName("*.example."), NextDomain: MustParseName("zzzzz.example."), Types: []string{"RRSIG", "NSEC"}},
		},
	}
	if err := p.Verify(); !errors.Is(err, ErrWildcardExists) {
		t.Fatalf("Verify() err = %v, want ErrWildcardExists", err)
	}
}

func TestNODATARejectedWhenQtypePresent(t *testing.T) {
	p := Proof{
		Zone:  MustParseName("example."),
		Qname: MustParseName("host.example."),
		Qtype: "A",
		Rcode: RcodeNoData,
		NSECs: []NSEC{
			{Owner: MustParseName("c.example."), NextDomain: MustParseName("zzzzzz.example.")},
			{Owner: MustParseName("host.example."), NextDomain: MustParseName("zzzzzz.example."), Types: []string{"A", "RRSIG", "NSEC"}},
		},
	}
	if err := p.Verify(); !errors.Is(err, ErrQtypePresent) {
		t.Fatalf("Verify() err = %v, want ErrQtypePresent", err)
	}
}

// TestForgedGapRejected feeds a denial whose NSEC intervals deliberately stop
// short of the queried name: the validator must refuse it and name the
// uncovered interval.
func TestForgedGapRejected(t *testing.T) {
	p := Proof{
		Zone:  MustParseName("example."),
		Qname: MustParseName("bbb.example."),
		Qtype: "A",
		Rcode: RcodeNXDomain,
		NSECs: []NSEC{
			{Owner: MustParseName("example."), NextDomain: MustParseName("a.example.")},
			{Owner: MustParseName("a.example."), NextDomain: MustParseName("c.example.")},
			// Forged record: claims the gap ends at m.example., but the
			// 3-label-long name bbb.example. sorts after every 1-label name.
			{Owner: MustParseName("c.example."), NextDomain: MustParseName("m.example.")},
		},
	}

	err := p.Verify()
	if !errors.Is(err, ErrUncoveredTarget) {
		t.Fatalf("Verify() err = %v, want ErrUncoveredTarget", err)
	}

	var gap *CoverageError
	if !errors.As(err, &gap) {
		t.Fatalf("error %v is not a *CoverageError", err)
	}
	if !gap.Target.Equal(MustParseName("bbb.example.")) {
		t.Errorf("gap.Target = %s", gap.Target)
	}
	if got, want := gap.GapStart.String(), "m.example."; got != want {
		t.Errorf("GapStart = %s, want %s", got, want)
	}
	if got, want := gap.GapEnd.String(), "bbb.example."; got != want {
		t.Errorf("GapEnd = %s, want %s", got, want)
	}
	if gap.Wrapped {
		t.Errorf("gap should not be a wrap gap")
	}
	t.Logf("forged response rejected: %s", err)
}

func ExampleProof_Verify_forgedGap() {
	p := Proof{
		Zone:  MustParseName("example."),
		Qname: MustParseName("bbb.example."),
		Qtype: "A",
		Rcode: RcodeNXDomain,
		NSECs: []NSEC{
			{Owner: MustParseName("example."), NextDomain: MustParseName("a.example.")},
			{Owner: MustParseName("a.example."), NextDomain: MustParseName("c.example.")},
			{Owner: MustParseName("c.example."), NextDomain: MustParseName("m.example.")},
		},
	}
	err := p.Verify()
	var gap *CoverageError
	if errors.As(err, &gap) {
		fmt.Println("rejected:", err.Error())
		fmt.Println("uncovered interval:", gap.GapStart.String(), "->", gap.GapEnd.String())
	}
	// Output:
	// rejected: nsec: no NSEC interval covers the required target name: target bbb.example. is not covered; interval (c.example., m.example.) stops before it, leaving uncovered interval (m.example., bbb.example.)
	// uncovered interval: m.example. -> bbb.example.
}
