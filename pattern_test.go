package pathpat

import (
	"reflect"
	"testing"
)

func TestSplitPath(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},            // empty path: zero segments
		{"/", []string{""}},  // root path: one empty segment
		{"a", []string{"a"}}, //
		{"a/b", []string{"a", "b"}},
		{"/a", []string{"", "a"}},
		{"a/", []string{"a", ""}},
		{"//", []string{"", "", ""}},
	}
	for _, c := range cases {
		if got := SplitPath(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRenderPathRoundTrip(t *testing.T) {
	for _, p := range []string{"", "/", "a", "a/b", "/a", "a/", "a//b"} {
		if got := RenderPath(SplitPath(p)); got != p {
			t.Errorf("RenderPath(SplitPath(%q)) = %q", p, got)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		pat  string
		path []string
		want bool
	}{
		// Empty pattern matches only the empty path; root pattern only root.
		{"", nil, true},
		{"", []string{""}, false},
		{"/", []string{""}, true},
		{"/", nil, false},
		{"/", []string{"", ""}, false},
		// Literals.
		{"a/b", []string{"a", "b"}, true},
		{"a/b", []string{"a", "c"}, false},
		{"a/b", []string{"a"}, false},
		{"a/b", []string{"a", "b", "c"}, false},
		// Single-segment wildcard: exactly one segment, never a separator.
		{"*", []string{"x"}, true},
		{"*", []string{""}, true},
		{"*", nil, false},
		{"*", []string{"a", "b"}, false},
		{"a/*", []string{"a", "b"}, true},
		{"a/*", []string{"a"}, false},
		{"a/*", []string{"a", "b", "c"}, false},
		{"a/*", []string{"b", "c"}, false},
		// Multi-segment wildcard: zero or more segments.
		{"**", nil, true},
		{"**", []string{""}, true},
		{"**", []string{"a", "b", "c"}, true},
		{"a/**", []string{"a"}, true},
		{"a/**", []string{"a", "b", "c"}, true},
		{"a/**", []string{"b", "a"}, false},
		{"**/b", []string{"b"}, true},
		{"**/b", []string{"a", "b"}, true},
		{"**/b", []string{"b", "a"}, false},
		{"a/**/b", []string{"a", "b"}, true},
		{"a/**/b", []string{"a", "x", "y", "b"}, true},
		{"a/**/b", []string{"a", "x", "y"}, false},
		{"a/**/b", []string{"x", "a", "b"}, false},
		// Mixed wildcards.
		{"*/**/*", []string{"a", "b"}, true},
		{"*/**/*", []string{"a"}, false},
		{"*/**/*", []string{"a", "b", "c", "d"}, true},
		{"**/*/**", []string{"a"}, true},
		{"**/*/**", nil, false},
	}
	for _, c := range cases {
		if got := ParsePattern(c.pat).Match(c.path); got != c.want {
			t.Errorf("pattern %q match %v = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
}
