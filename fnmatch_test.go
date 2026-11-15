package netsec

import "testing"

func TestFnmatch(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		// Literal, case-sensitive matching.
		{"sensor/temp", "sensor/temp", true},
		{"sensor/temp", "sensor/TEMP", false},
		{"sensor/temp", "sensor/tem", false},
		// '*' matches across separators and the empty string.
		{"sensor/*", "sensor/temp/raw", true},
		{"sensor/*", "sensor/", true},
		{"*", "anything/at/all", true},
		{"*.data", "telemetry.data", true},
		{"*.data", "telemetry.meta", false},
		// '?' matches exactly one character.
		{"cmd/?", "cmd/a", true},
		{"cmd/?", "cmd/ab", false},
		{"cmd/??", "cmd/ab", true},
		// Character classes, negation and ranges.
		{"cmd/[ab]", "cmd/a", true},
		{"cmd/[ab]", "cmd/c", false},
		{"cmd/[!ab]", "cmd/c", true},
		{"cmd/[!ab]", "cmd/a", false},
		{"cmd/[a-c]", "cmd/b", true},
		{"cmd/[a-c]", "cmd/d", false},
		// A leading ']' inside a class is literal.
		{"cmd/[]a]", "cmd/]", true},
		{"cmd/[]a]", "cmd/a", true},
		// Backslash escapes wildcards.
		{`cmd/\*`, "cmd/*", true},
		{`cmd/\*`, "cmd/a", false},
		{`cmd/\[x\]`, "cmd/[x]", true},
		// Consecutive stars collapse.
		{"a/**/b", "a/x/y/b", true},
	}
	for _, tc := range cases {
		got, err := Fnmatch(tc.pattern, tc.name)
		if err != nil {
			t.Errorf("Fnmatch(%q, %q) returned error %v", tc.pattern, tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Fnmatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestFnmatchMalformed(t *testing.T) {
	bad := []string{
		"cmd/[ab",
		"cmd/[",
		"cmd/[!",
		`cmd/\`,
	}
	for _, pattern := range bad {
		if _, err := Fnmatch(pattern, "cmd/a"); err != ErrBadPattern {
			t.Errorf("Fnmatch(%q, ...) error = %v, want ErrBadPattern", pattern, err)
		}
	}
}
