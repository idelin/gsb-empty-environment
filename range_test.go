package netsec

import "testing"

func TestRangeAcceptance(t *testing.T) {
	tests := []struct {
		expr    string
		inside  []string
		outside []string
	}{
		{
			expr:    "^1.2.3",
			inside:  []string{"1.2.3", "1.9.9"},
			outside: []string{"1.2.2", "2.0.0"},
		},
		{
			expr:    "~1.2.3",
			inside:  []string{"1.2.3", "1.2.99"},
			outside: []string{"1.3.0", "1.1.9"},
		},
		{
			expr:    "1.2.x",
			inside:  []string{"1.2.0", "1.2.99"},
			outside: []string{"1.3.0", "1.1.9"},
		},
		{
			expr:    "^1.x",
			inside:  []string{"1.0.0", "1.99.99"},
			outside: []string{"2.0.0", "0.9.9"},
		},
		{
			expr:    "~1.2.x",
			inside:  []string{"1.2.0", "1.2.99"},
			outside: []string{"1.3.0", "1.1.9"},
		},
		{
			expr:    ">=1.0.0 <2.0.0",
			inside:  []string{"1.0.0", "1.99.99"},
			outside: []string{"0.9.9", "2.0.0"},
		},
		{
			expr:    "[1.0.0,2.0.0)",
			inside:  []string{"1.0.0", "1.99.99"},
			outside: []string{"0.9.9", "2.0.0"},
		},
		{
			expr:    "1.2.3 - 2.3.4",
			inside:  []string{"1.2.3", "2.3.4"},
			outside: []string{"1.2.2", "2.3.5"},
		},
		{
			expr:    "vers:npm/1.2.3|>=2.0.0|<5.0.0",
			inside:  []string{"1.2.3", "2.0.0", "4.9.9"},
			outside: []string{"1.2.4", "5.0.0"},
		},
		{
			expr:    "^0.2.3",
			inside:  []string{"0.2.3", "0.2.99"},
			outside: []string{"0.3.0", "0.3.9"},
		},
		{
			expr:    ">=1.0.0 <2.0.0 || >=3.0.0",
			inside:  []string{"1.5.0", "3.0.0", "9.0.0"},
			outside: []string{"0.9.9", "2.5.0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			r, err := ParseVersionRange(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range tc.inside {
				if !r.Contains(v) {
					t.Errorf("%s should contain %s (canonical %s)", tc.expr, v, r)
				}
			}
			for _, v := range tc.outside {
				if r.Contains(v) {
					t.Errorf("%s should not contain %s (canonical %s)", tc.expr, v, r)
				}
			}
		})
	}
}

func TestRangeCanonicalMerging(t *testing.T) {
	a, err := ParseVersionRange(">=1.0.0 <2.0.0 || >=1.5.0 <3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseVersionRange(">=1.0.0 <3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("overlapping intervals did not merge:\n  %s\n  %s", a, b)
	}
}

func TestVersionParsing(t *testing.T) {
	tests := map[string]string{
		"v1.2.3":          "1.2.3",
		"01.002.0003":     "1.2.3",
		"1.2":             "1.2.0",
		"1":               "1.0.0",
		"1.0.0-rc.01":     "1.0.0-rc.1",
		"1.0.0+build.001": "1.0.0+build.001",
		"1:2.3.4-1":       "1:2.3.4-1",
	}
	for in, want := range tests {
		v, err := ParseVersion(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := v.String(); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestBuildMetadataIgnoredForPrecedence(t *testing.T) {
	if CompareVersions("1.2.3+linux", "1.2.3+darwin") != 0 {
		t.Fatalf("build metadata must not affect precedence")
	}
	if CompareVersions("1.2.3+build", "1.2.3") != 0 {
		t.Fatalf("build metadata must not affect precedence")
	}
}

func TestMatchesVersionArgumentOrder(t *testing.T) {
	for _, args := range [][2]string{
		{"1.5.0", "^1.0.0"},
		{"^1.0.0", "1.5.0"},
		{"1.5.0", "[1.0.0,2.0.0)"},
	} {
		ok, err := MatchesVersion(args[0], args[1])
		if err != nil || !ok {
			t.Fatalf("MatchesVersion(%q, %q) = %v, %v", args[0], args[1], ok, err)
		}
	}
}
