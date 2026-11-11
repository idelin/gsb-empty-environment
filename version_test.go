package netsec

import "testing"

func TestParseSemVer(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1.2.3", "1.2.3"},
		{"v1.2.3", "1.2.3"},
		{" 1.2.3-rc.1 ", "1.2.3-rc.1"},
		{"10.20.30-x.y-z.9", "10.20.30-x.y-z.9"},
	}
	for _, tc := range cases {
		v, err := ParseSemVer(tc.in)
		if err != nil {
			t.Fatalf("ParseSemVer(%q) unexpected error: %v", tc.in, err)
		}
		if got := v.String(); got != tc.want {
			t.Fatalf("ParseSemVer(%q).String() = %q, want %q", tc.in, got, tc.want)
		}
	}

	bad := []string{"", "1.2", "1", "1.2.x", "1.2.3-", "1.2.3-rc..1", "1.2.3-01", "1.2.3+meta"}
	for _, in := range bad {
		if _, err := ParseSemVer(in); err == nil {
			t.Fatalf("ParseSemVer(%q) expected error, got nil", in)
		}
	}
}

func TestSemVerComparePrereleaseOrdering(t *testing.T) {
	order := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
	}
	for i := 1; i < len(order); i++ {
		prev := mustVersion(t, order[i-1])
		cur := mustVersion(t, order[i])
		if prev.Compare(cur) != -1 || cur.Compare(prev) != 1 {
			t.Fatalf("ordering violated: %s should be < %s", prev, cur)
		}
	}
	eq := mustVersion(t, "1.2.3")
	if eq.Compare(mustVersion(t, "v1.2.3")) != 0 {
		t.Fatal("v-prefixed version should compare equal")
	}
	if mustVersion(t, "2.0.0").Compare(mustVersion(t, "1.9.9")) != 1 {
		t.Fatal("major version ordering wrong")
	}
}

func TestVersionRangeBoundaries(t *testing.T) {
	r, err := ParseVersionRange(">=1.0.0 <2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"0.9.9":       false,
		"1.0.0-alpha": false,
		"1.0.0":       true,
		"1.5.7":       true,
		"1.9.9":       true,
		"2.0.0-alpha": true,
		"2.0.0":       false,
		"2.0.1":       false,
	}
	for raw, want := range cases {
		if got := r.Contains(mustVersion(t, raw)); got != want {
			t.Fatalf("range %s contains %s = %v, want %v", r, raw, got, want)
		}
	}

	open, err := ParseVersionRange(">1.0.0 <=1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if open.Contains(mustVersion(t, "1.0.0")) {
		t.Fatal("exclusive lower bound must reject its boundary version")
	}
	if !open.Contains(mustVersion(t, "1.2.0")) {
		t.Fatal("inclusive upper bound must accept its boundary version")
	}

	unbounded, err := ParseVersionRange(">=0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !unbounded.Contains(mustVersion(t, "99.0.0")) {
		t.Fatal("missing upper bound must be unbounded above")
	}
}

func TestParseVersionRangeErrors(t *testing.T) {
	bad := []string{
		"",
		"1.0.0",
		"^1.0.0",
		">=1.0.0 >=1.1.0",
		"<1.0.0 >2.0.0",
		">2.0.0 <2.0.0",
		">=2.0.0 <2.0.0",
	}
	for _, expr := range bad {
		if _, err := ParseVersionRange(expr); err == nil {
			t.Fatalf("ParseVersionRange(%q) expected error", expr)
		}
	}
}

func TestVersionRangeStringRoundTrip(t *testing.T) {
	r, err := ParseVersionRange(">=1.0.0-rc.1 <2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if r.String() != ">=1.0.0-rc.1 <2.0.0" {
		t.Fatalf("unexpected rendering: %s", r.String())
	}
	var unbounded VersionRange
	if unbounded.String() != "*" {
		t.Fatalf("empty range should render as *, got %s", unbounded.String())
	}
}

func mustVersion(t *testing.T, raw string) SemVer {
	t.Helper()
	v, err := ParseSemVer(raw)
	if err != nil {
		t.Fatalf("ParseSemVer(%q): %v", raw, err)
	}
	return v
}
