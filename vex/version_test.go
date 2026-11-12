package vex

import "testing"

func TestRangeContains(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		version string
		want    bool
	}{
		{"star matches anything", "*", "9.9.9", true},
		{"empty matches anything", "", "1.0.0", true},
		{"exact bare version", "2.9.10", "2.9.10", true},
		{"bare version mismatch", "2.9.10", "2.9.11", false},
		{"equality operator", "=2.9.10", "2.9.10", true},
		{"not equal", "!=2.9.10", "2.9.11", true},
		{"greater than", ">2.9.9", "2.9.10", true},
		{"greater or equal boundary", ">=2.9.10", "2.9.10", true},
		{"less than excludes boundary", "<2.9.10", "2.9.10", false},
		{"comma range inside", ">=2.9.0, <3.0.0", "2.9.10", true},
		{"comma range outside low", ">=3.0.0, <4.0.0", "2.9.10", false},
		{"vers expression", "vers:semver/>=2.9.0|<3.0.0", "2.9.10", true},
		{"vers generic scheme", "vers:generic/>=1.0|<2.0", "1.5.2", true},
		{"closed interval inside", "[2.0.0,3.0.0]", "2.9.10", true},
		{"closed interval upper boundary", "[2.0.0,2.9.10]", "2.9.10", true},
		{"open interval upper boundary excluded", "[2.0.0,2.9.10)", "2.9.10", false},
		{"open interval lower boundary excluded", "(2.9.10,3.0.0)", "2.9.10", false},
		{"half interval", "(,2.0.0]", "1.9.9", true},
		{"v prefix tolerated", ">=v2.9.0", "v2.9.10", true},
		{"malformed range fails closed", ">=not-a-version", "2.9.10", false},
		{"malformed version fails closed", ">=1.0.0", "not-a-version", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rangeContains(tt.expr, tt.version); got != tt.want {
				t.Fatalf("rangeContains(%q, %q) = %v, want %v",
					tt.expr, tt.version, got, tt.want)
			}
		})
	}
}

func TestCompareSemVerPrecedence(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.1", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0", "1.0.0", 0},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-rc.1", "1.0.0-rc.2", -1},
		{"1.0.0-1", "1.0.0-alpha", -1},
		{"1.0.0+build.7", "1.0.0", 0},
	}
	for _, tt := range tests {
		t.Run(tt.a+" vs "+tt.b, func(t *testing.T) {
			va, err := parseSemVer(tt.a)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.a, err)
			}
			vb, err := parseSemVer(tt.b)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.b, err)
			}
			if got := compareSemVer(va, vb); got != tt.want {
				t.Fatalf("compareSemVer(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestParseSemVerInvalid(t *testing.T) {
	for _, raw := range []string{"", "v", "1.2.x", "1.2.3.4", "a.b.c"} {
		if _, err := parseSemVer(raw); err == nil {
			t.Fatalf("parseSemVer(%q) expected error", raw)
		}
	}
}
