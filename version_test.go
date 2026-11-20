package netsec

import "testing"

func TestRangeContains(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		resolved  string
		wantOK    bool
	}{
		{"pinned exact", "[1.0.0]", "1.0.0", true},
		{"pinned mismatch", "[1.0.0]", "1.0.1", false},
		{"inclusive interval", "[1.0.0,2.0.0]", "2.0.0", true},
		{"exclusive upper", "[1.0.0,2.0.0)", "2.0.0", false},
		{"exclusive lower", "(1.0.0,2.0.0)", "1.0.0", false},
		{"open upper", "[1.0.0, )", "99.0.0", true},
		{"open both lower", "(,2.0.0)", "1.5.0", true},
		{"bare minimum", "1.0.0", "1.0.0", true},
		{"bare minimum above", "1.0.0", "0.9.0", false},
		{"floating minor", "1.*", "1.99.0", true},
		{"floating patch", "1.2.*", "1.2.0", true},
		{"floating patch below", "1.2.*", "1.1.9", false},
		{"bare star", "*", "0.0.1", true},
		{"prerelease ordering", "[1.0.0,1.0.0]", "1.0.0-alpha", false},
		{"prerelease inside", "[1.0.0-alpha,1.0.0]", "1.0.0-alpha", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng, err := parseVersionRange(tc.requested)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.requested, err)
			}
			v, err := parseNugetVersion(tc.resolved)
			if err != nil {
				t.Fatalf("parse version %q: %v", tc.resolved, err)
			}
			if got := rng.contains(v); got != tc.wantOK {
				t.Fatalf("contains(%s in %s) = %v, want %v",
					tc.resolved, tc.requested, got, tc.wantOK)
			}
		})
	}
}

func TestDowngradeClassification(t *testing.T) {
	rng, err := parseVersionRange("[1.0.0,2.0.0)")
	if err != nil {
		t.Fatal(err)
	}
	low, _ := parseNugetVersion("0.9.9")
	high, _ := parseNugetVersion("3.0.0")
	if !rng.belowLowerBound(low) {
		t.Fatal("0.9.9 must be a downgrade against [1.0.0,2.0.0)")
	}
	if rng.belowLowerBound(high) {
		t.Fatal("3.0.0 is out of range but not a downgrade")
	}
}

func TestMalformedRangesRejected(t *testing.T) {
	for _, s := range []string{"", "[1.0.0", "1.0.0]", "[1.0.0,", "abc", "[1.*,2.0.0]"} {
		if _, err := parseVersionRange(s); err == nil {
			t.Fatalf("range %q should fail to parse", s)
		}
	}
}

func TestMalformedVersionsRejected(t *testing.T) {
	for _, s := range []string{"", "1.x", "1.2.3.4.5", "-", "1.-alpha"} {
		if _, err := parseNugetVersion(s); err == nil {
			t.Fatalf("version %q should fail to parse", s)
		}
	}
}
