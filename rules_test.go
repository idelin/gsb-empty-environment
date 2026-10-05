package pkgincr

import (
	"strings"
	"testing"
)

func mustRule(t *testing.T, line string) *Rule {
	t.Helper()
	r, err := ParseRule(line, 1)
	if err != nil {
		t.Fatalf("ParseRule(%q): %v", line, err)
	}
	if r == nil {
		t.Fatalf("ParseRule(%q): got nil rule", line)
	}
	return r
}

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		isDir   bool
		want    bool
	}{
		// Base-name patterns match at any depth.
		{"- *.o", "a/b/c.o", false, true},
		{"- *.o", "a/b/c.c", false, false},
		{"- build", "x/build", true, true},
		{"- build", "x/build", false, true}, // no trailing slash: files too
		{"- build", "x/build.txt", false, false},

		// Directory-only patterns.
		{"- build/", "x/build", true, true},
		{"- build/", "x/build", false, false},
		{"- /build/", "build", true, true},
		{"- /build/", "x/build", true, false},

		// Anchored patterns.
		{"+ /a/b", "a/b", false, true},
		{"+ /a/b", "x/a/b", false, false},
		{"+ a/b/c", "a/b/c", false, true},
		{"+ a/b/c", "z/a/b/c", false, false},

		// Single-segment wildcards.
		{"- a?c", "abc", false, true},
		{"- a?c", "a/c", false, false},
		{"- [ab].txt", "a.txt", false, true},
		{"- [ab].txt", "c.txt", false, false},
		{"- */keep.txt", "a/keep.txt", false, true},
		{"- */keep.txt", "a/b/keep.txt", false, false},

		// Double wildcard: zero or more segments.
		{"- a/**/b", "a/b", false, true},
		{"- a/**/b", "a/x/b", false, true},
		{"- a/**/b", "a/x/y/b", false, true},
		{"- a/**/b", "a/x/y", false, false},
		{"- a/**/b", "z/a/b", false, false},
		{"- a/**", "a", true, true},
		{"- a/**", "a/b/c", false, true},
		{"- **/keep.txt", "keep.txt", false, true},
		{"- **/keep.txt", "a/b/keep.txt", false, true},
		{"- **", "anything/at/all", false, true},
		{"- /**", "anything/at/all", true, true},
		{"- a/**/b/**/c", "a/b/c", false, true},
		{"- a/**/b/**/c", "a/1/b/2/3/c", false, true},
	}
	for _, c := range cases {
		r := mustRule(t, c.pattern)
		segs := strings.Split(c.path, "/")
		if got := r.Matches(segs, c.isDir); got != c.want {
			t.Errorf("pattern %q vs path %q (dir=%v): got %v, want %v",
				c.pattern, c.path, c.isDir, got, c.want)
		}
	}
}

func TestNormalization(t *testing.T) {
	// All of these spellings name the same path and must behave alike.
	equivalent := []string{"+ a/b", "+ ./a/b", "+ a//b", "+ a/./b", "+ /a/b", "+ /./a/b"}
	var rules []*Rule
	for _, p := range equivalent {
		rules = append(rules, mustRule(t, p))
	}
	segs := []string{"a", "b"}
	for i, r := range rules {
		if !r.Matches(segs, false) {
			t.Errorf("spelling %q did not match a/b", equivalent[i])
		}
		if r.Matches([]string{"x", "a", "b"}, false) {
			t.Errorf("spelling %q unexpectedly matched x/a/b", equivalent[i])
		}
	}
	// Trailing slash means directory-only and must survive normalization.
	r := mustRule(t, "+ /a//b/")
	if !r.dirOnly {
		t.Error("expected dirOnly for '+ /a//b/'")
	}
	if r.Matches(segs, false) {
		t.Error("dir-only pattern matched a file")
	}
	if !r.Matches(segs, true) {
		t.Error("dir-only pattern did not match a directory")
	}
	// Unanchored base-name pattern keeps no slash.
	if mustRule(t, "- name").anchored {
		t.Error("base-name pattern should not be anchored")
	}
	if !mustRule(t, "- a/b").anchored {
		t.Error("pattern with middle slash should be anchored")
	}
}

func TestParseErrors(t *testing.T) {
	for _, line := range []string{"", "   ", "# comment", "  # comment"} {
		r, err := ParseRule(line, 1)
		if err != nil || r != nil {
			t.Errorf("ParseRule(%q) = %v, %v; want nil, nil", line, r, err)
		}
	}
	for _, line := range []string{"* bad", "+", "-  ", "+ /", "+ .", "+ a/../b", "+ [", "+ a[/b"} {
		if _, err := ParseRule(line, 1); err == nil {
			t.Errorf("ParseRule(%q): expected error", line)
		}
	}
}

func TestCanMatchDescendant(t *testing.T) {
	cases := []struct {
		pattern string
		dir     string
		want    bool
	}{
		{"+ /a/b/c", "a", true},
		{"+ /a/b/c", "a/b", true},
		{"+ /a/b/c", "a/b/c", false}, // matches the dir itself, nothing below
		{"+ /a/b/c", "a/b/c/d", false},
		{"+ /a/b/c", "x", false},
		{"+ /a/**/c", "a", true},
		{"+ /a/**/c", "a/x/y", true},
		{"+ /a/**/c", "b", false},
		{"+ /a/**", "a", true},
		{"+ /a/**", "a/b", true},
		{"+ /a/**", "ab", false},
		{"+ /**/keep.txt", "build", true},
		{"+ /**/keep.txt", "a/b/c", true},
		{"+ /a/b/", "a", true}, // dir-only pattern, descendant dir possible
		{"+ /a/b/", "a/b", false},
		// Base-name patterns are conservative: always possible.
		{"+ keep.txt", "anything", true},
		{"+ *.h", "a/b", true},
	}
	for _, c := range cases {
		r := mustRule(t, c.pattern)
		got := r.CanMatchDescendant(strings.Split(c.dir, "/"))
		if got != c.want {
			t.Errorf("pattern %q below dir %q: got %v, want %v",
				c.pattern, c.dir, got, c.want)
		}
	}
}

func TestLastMatchWins(t *testing.T) {
	rules, err := ParseRules(`
- *.o
+ keep.o
- /keep.o
`)
	if err != nil {
		t.Fatal(err)
	}
	segs := []string{"keep.o"}
	if got := LastMatch(rules, segs, false); got != 2 {
		t.Fatalf("LastMatch = %d, want 2 (later rule overrides)", got)
	}
	if rules[LastMatch(rules, segs, false)].Kind != Exclude {
		t.Fatal("expected the last matching rule to be the exclude")
	}
}
