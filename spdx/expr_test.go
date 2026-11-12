package spdx

import (
	"strings"
	"testing"
)

func set(ids ...string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[strings.ToLower(strings.Join(strings.Fields(id), " "))] = true
	}
	return m
}

func TestParseAndEvaluate(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		allowed map[string]bool
		want    bool
	}{
		{"single allowed", "MIT", set("mit"), true},
		{"single denied", "GPL-3.0-only", set("mit"), false},
		{"or first branch", "GPL-3.0-only OR MIT", set("mit"), true},
		{"or second branch", "GPL-3.0-only OR MIT", set("gpl-3.0-only"), true},
		{"or neither", "GPL-3.0-only OR AGPL-3.0-only", set("mit"), false},
		{"and both", "MIT AND Apache-2.0", set("mit", "apache-2.0"), true},
		{"and one missing", "MIT AND Apache-2.0", set("mit"), false},
		{"with base grant", "GPL-2.0-only WITH Classpath-exception-2.0",
			set("gpl-2.0-only"), true},
		{"with exact grant", "GPL-2.0-only WITH Classpath-exception-2.0",
			set("gpl-2.0-only with classpath-exception-2.0"), true},
		{"with denied", "GPL-2.0-only WITH Classpath-exception-2.0", set("mit"), false},
		{"and binds tighter than or", "MIT OR GPL-3.0-only AND Apache-2.0",
			set("mit"), true},
		{"parentheses override precedence", "(MIT OR GPL-3.0-only) AND Apache-2.0",
			set("mit", "apache-2.0"), true},
		{"parenthesized denied branch", "(MIT OR GPL-3.0-only) AND Apache-2.0",
			set("mit"), false},
		{"case insensitive keywords", "mit or apache-2.0", set("Apache-2.0"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("Parse(%q) failed: %v", tc.input, err)
			}
			if got := expr.Allowed(tc.allowed); got != tc.want {
				t.Errorf("Allowed(%q) = %v, want %v (expr=%s)",
					tc.input, got, tc.want, expr.String())
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"",
		"MIT AND",
		"OR MIT",
		"MIT WITH",
		"(MIT",
		"MIT)",
		"MIT OR OR Apache-2.0",
		"MIT Apache-2.0",
	}
	for _, input := range bad {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) expected error", input)
		}
	}
}

func TestStringRoundTrip(t *testing.T) {
	expr, err := Parse("MIT AND (Apache-2.0 OR GPL-3.0-only)")
	if err != nil {
		t.Fatal(err)
	}
	want := "(MIT AND (Apache-2.0 OR GPL-3.0-only))"
	if got := expr.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
