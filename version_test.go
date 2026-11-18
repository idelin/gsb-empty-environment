package netsec

import "testing"

func TestParseVersionBuildMetadataIgnored(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		isPre bool
	}{
		{"1.2.3", "1.2.3", false},
		{"v1.2.3", "1.2.3", false},
		{"1.2.3+build.42", "1.2.3", false},
		{"1.2.3-rc.1", "1.2.3-rc.1", true},
		{"1.2.3-rc.1+exp.sha.5114f85", "1.2.3-rc.1", true},
		{"1.0.0-alpha+001", "1.0.0-alpha", true},
		{"1.0.0-x.7.z.92", "1.0.0-x.7.z.92", true},
		{"2.0.0-1.0.0", "2.0.0-1.0.0", true},
	}
	for _, tc := range cases {
		v, err := ParseVersion(tc.in)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", tc.in, err)
		}
		if got := v.String(); got != tc.want {
			t.Errorf("ParseVersion(%q).String() = %q, want %q", tc.in, got, tc.want)
		}
		if v.IsPrerelease() != tc.isPre {
			t.Errorf("ParseVersion(%q).IsPrerelease() = %v, want %v", tc.in, v.IsPrerelease(), tc.isPre)
		}
	}
}

func TestParseVersionInvalid(t *testing.T) {
	bad := []string{
		"", "v", "1.2", "1.2.x", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
		"1.2.3-", "1.2.3-01", "1.2.3-alpha..1", "1.2.3+", "1.2.3-rc_1",
		"1.2.3-beta+", "99999999999999999999999.0.0",
	}
	for _, in := range bad {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) expected error, got nil", in)
		}
	}
}

func TestCompareSemverOrdering(t *testing.T) {
	// semver.org clause 11 example, plus build metadata cases.
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
	}
	for i := 0; i < len(ordered)-1; i++ {
		a := mustVersion(t, ordered[i])
		b := mustVersion(t, ordered[i+1])
		if got := a.Compare(b); got >= 0 {
			t.Errorf("%s.Compare(%s) = %d, want < 0", ordered[i], ordered[i+1], got)
		}
		if got := b.Compare(a); got <= 0 {
			t.Errorf("%s.Compare(%s) = %d, want > 0", ordered[i+1], ordered[i], got)
		}
	}

	// Numeric vs alphanumeric identifier: 123 < alpha.
	if c := mustVersion(t, "1.0.0-123").Compare(mustVersion(t, "1.0.0-alpha")); c >= 0 {
		t.Errorf("123 identifier should sort before alpha: got %d", c)
	}
	// Numeric identifier compares numerically, not lexically.
	if c := mustVersion(t, "1.0.0-beta.2").Compare(mustVersion(t, "1.0.0-beta.11")); c >= 0 {
		t.Errorf("beta.2 should sort before beta.11: got %d", c)
	}
	// Prerelease always below its release.
	if c := mustVersion(t, "1.0.0-rc.99").Compare(mustVersion(t, "1.0.0")); c >= 0 {
		t.Errorf("prerelease should sort below release: got %d", c)
	}
	// Build metadata is ignored: differing only by metadata means equal.
	pairs := [][2]string{
		{"1.0.0+foo", "1.0.0+bar"},
		{"1.0.0-alpha+001", "1.0.0-alpha+002"},
		{"1.2.3", "1.2.3+build.7"},
	}
	for _, p := range pairs {
		a := mustVersion(t, p[0])
		b := mustVersion(t, p[1])
		if a.Compare(b) != 0 || b.Compare(a) != 0 {
			t.Errorf("build metadata must be ignored: %s vs %s", p[0], p[1])
		}
		if a.String() != b.String() {
			t.Errorf("canonical forms should match: %q vs %q", a.String(), b.String())
		}
	}
}

func TestVersionRangeBoundaries(t *testing.T) {
	cases := []struct {
		interval string
		in       []string
		out      []string
	}{
		{
			interval: "[1.0.0,2.0.0)",
			in:       []string{"1.0.0", "1.5.0", "1.99.99", "2.0.0-rc.1"},
			out:      []string{"0.9.9", "2.0.0", "2.0.1", "1.0.0-rc.1"},
		},
		{
			interval: "(1.0.0,2.0.0]",
			in:       []string{"1.0.1", "2.0.0", "2.0.0+meta"},
			out:      []string{"1.0.0", "1.0.0+meta", "2.0.1", "1.0.0-alpha"},
		},
		{
			interval: "[1.2.3,1.2.3]",
			in:       []string{"1.2.3", "1.2.3+anything"},
			out:      []string{"1.2.2", "1.2.4", "1.2.3-rc.1"},
		},
		{
			interval: "[1.0.0,)",
			in:       []string{"1.0.0", "9.9.9"},
			out:      []string{"0.9.9", "1.0.0-alpha"},
		},
		{
			interval: "(,1.0.0)",
			in:       []string{"0.9.9", "0.0.1", "1.0.0-alpha", "1.0.0-rc.1"},
			out:      []string{"1.0.0", "1.0.1", "1.0.0+build"},
		},
		{
			// Prerelease-only interval: release 1.0.0 sits above the bound.
			interval: "[1.0.0-alpha,1.0.0-rc.1]",
			in:       []string{"1.0.0-alpha", "1.0.0-beta", "1.0.0-rc.1"},
			out:      []string{"1.0.0", "1.0.0-rc.2", "0.9.9"},
		},
	}
	for _, tc := range cases {
		r, err := ParseVersionRange(tc.interval)
		if err != nil {
			t.Fatalf("ParseVersionRange(%q): %v", tc.interval, err)
		}
		for _, in := range tc.in {
			if !r.Contains(mustVersion(t, in)) {
				t.Errorf("range %s should contain %s", tc.interval, in)
			}
		}
		for _, out := range tc.out {
			if r.Contains(mustVersion(t, out)) {
				t.Errorf("range %s should not contain %s", tc.interval, out)
			}
		}
	}
}

func TestParseVersionRangeInvalid(t *testing.T) {
	bad := []string{
		"", "[1.0.0]", "1.0.0,2.0.0", "[1.0.0,2.0.0", "[1.0.0 2.0.0)",
		"{1.0.0,2.0.0}", "[2.0.0,1.0.0]", "[x,1.0.0]",
	}
	for _, in := range bad {
		if _, err := ParseVersionRange(in); err == nil {
			t.Errorf("ParseVersionRange(%q) expected error, got nil", in)
		}
	}
}

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", s, err)
	}
	return v
}
