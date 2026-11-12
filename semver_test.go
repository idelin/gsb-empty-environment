package netsec

import "testing"

func TestParseAndCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.1.0", "2.0.9", 1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
		{"1.0.0-alpha.beta", "1.0.0-beta", -1},
		{"1.0.0-beta", "1.0.0-beta.2", -1},
		{"1.0.0-beta.2", "1.0.0-beta.11", -1},
		{"1.0.0-beta.11", "1.0.0-rc.1", -1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0+build.1", "1.0.0+build.2", 0},
		{"1.0.0-alpha+exp.1", "1.0.0-alpha+exp.2", 0},
	}
	for _, tc := range tests {
		va, err := ParseVersion(tc.a)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", tc.a, err)
		}
		vb, err := ParseVersion(tc.b)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", tc.b, err)
		}
		if got := va.Compare(vb); got != tc.want {
			t.Errorf("Compare(%q,%q)=%d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCanonicalIgnoresBuildMetadata(t *testing.T) {
	v, err := ParseVersion("1.2.3-rc.1+sha.abc")
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Canonical(); got != "1.2.3-rc.1" {
		t.Errorf("Canonical()=%q, want 1.2.3-rc.1", got)
	}
	v2, _ := ParseVersion("1.2.3-rc.1+other")
	if v.Compare(v2) != 0 || v.Canonical() != v2.Canonical() {
		t.Error("build metadata must not affect precedence or identity")
	}
}

func TestParseVersionErrors(t *testing.T) {
	bad := []string{
		"1.0", "1.0.0.0", "v1.0.0", "01.0.0", "1.0.x",
		"1.0.0-", "1.0.0-01", "1.0.0+", "1.0.0-alpha..1", "1.0.0-alpha+001",
		"1.0.0-beta_1", "",
	}
	for _, s := range bad {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q) expected error", s)
		}
	}
}

func TestRangeContainsBoundaries(t *testing.T) {
	tests := []struct {
		expr string
		v    string
		want bool
	}{
		{"*", "99.99.99", true},
		{"1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.4", false},
		{">=1.0.0 <2.0.0", "1.0.0", true},
		{">=1.0.0 <2.0.0", "1.9.9", true},
		{">=1.0.0 <2.0.0", "2.0.0", false},
		{">=1.0.0 <2.0.0", "0.9.9", false},
		{">1.0.0 <=2.0.0", "1.0.0", false},
		{">1.0.0 <=2.0.0", "2.0.0", true},
		// Prereleases use plain semver ordering relative to the bounds.
		{">=1.0.0 <2.0.0", "1.0.0-alpha", false},
		{">=1.0.0-alpha <2.0.0", "1.0.0-alpha", true},
		{">=1.0.0-alpha <2.0.0", "1.5.0-rc.1", true},
		{">=1.0.0 <2.0.0", "2.0.0-rc.1", true},
		// Build metadata is ignored.
		{"=1.0.0", "1.0.0+deadbeef", true},
	}
	for _, tc := range tests {
		r, err := ParseRange(tc.expr)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", tc.expr, err)
		}
		v, err := ParseVersion(tc.v)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", tc.v, err)
		}
		if got := r.Contains(v); got != tc.want {
			t.Errorf("Range(%q).Contains(%q)=%v, want %v", tc.expr, tc.v, got, tc.want)
		}
	}
}

func TestParseRangeErrors(t *testing.T) {
	bad := []string{"", "   ", ">= ", "==1.0.0", "1.0", ">=1.0.0 <"}
	for _, s := range bad {
		if _, err := ParseRange(s); err == nil {
			t.Errorf("ParseRange(%q) expected error", s)
		}
	}
}
