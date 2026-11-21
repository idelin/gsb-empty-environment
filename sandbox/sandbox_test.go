package sandbox

import (
	"reflect"
	"testing"
)

func TestDefaultDeny(t *testing.T) {
	p := &Policy{}
	d := p.Decide("file-read-data", "/etc/hosts")
	if d.Allowed || d.Rule != -1 {
		t.Fatalf("empty policy must default to deny, got %+v", d)
	}

	p = &Policy{Rules: []Rule{
		{Allow, "file-read*", SubpathFilter("/Library")},
	}}
	if d := p.Decide("file-write-data", "/Library/x"); d.Allowed || d.Rule != -1 {
		t.Fatalf("unmatched operation must default to deny, got %+v", d)
	}
}

func TestLastMatchWinsAndOrderFlip(t *testing.T) {
	denyFirst := &Policy{Rules: []Rule{
		{Deny, "file-read*", SubpathFilter("/secret")},
		{Allow, "file-read*", SubpathFilter("/secret")},
	}}
	if d := denyFirst.Decide("file-read-data", "/secret/key"); !d.Allowed || d.Rule != 1 {
		t.Fatalf("later allow must win, got %+v", d)
	}

	allowFirst := &Policy{Rules: []Rule{
		{Allow, "file-read*", SubpathFilter("/secret")},
		{Deny, "file-read*", SubpathFilter("/secret")},
	}}
	if d := allowFirst.Decide("file-read-data", "/secret/key"); d.Allowed || d.Rule != 1 {
		t.Fatalf("flipped order: later deny must win, got %+v", d)
	}
}

func TestSubpathBoundary(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Allow, "file-read*", SubpathFilter("/a/b")},
	}}
	cases := []struct {
		path    string
		allowed bool
	}{
		{"/a/b", true},     // the root itself
		{"/a/b/c", true},   // beneath the root
		{"/a/bc", false},   // sibling prefix is not a subpath
		{"/a/bc/d", false}, // nor anything beneath it
		{"/a", false},
		{"/a/bcdef", false},
	}
	for _, c := range cases {
		if d := p.Decide("file-read-data", c.path); d.Allowed != c.allowed {
			t.Errorf("Decide(%q) allowed=%v, want %v", c.path, d.Allowed, c.allowed)
		}
	}
}

func TestSubpathRootAndTrailingSlash(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Allow, "file-read*", SubpathFilter("/")},
	}}
	if d := p.Decide("file-read-data", "/anything/at/all"); !d.Allowed {
		t.Fatalf("subpath / must match every absolute path, got %+v", d)
	}

	q := &Policy{Rules: []Rule{
		{Allow, "file-read*", SubpathFilter("/a/b/")},
	}}
	if d := q.Decide("file-read-data", "/a/b/c"); !d.Allowed {
		t.Fatalf("trailing slash on subpath root must be ignored, got %+v", d)
	}
	if d := q.Decide("file-read-data", "/a/bc"); d.Allowed {
		t.Fatalf("trailing slash normalization must keep the boundary, got %+v", d)
	}
}

func TestWildcardOperation(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Allow, "file-read*", SubpathFilter("/data")},
		{Allow, "process-exec", LiteralFilter("/bin/ls")},
	}}
	cases := []struct {
		op      string
		path    string
		allowed bool
	}{
		{"file-read-data", "/data/x", true},
		{"file-read-metadata", "/data/x", true},
		{"file-read", "/data/x", true}, // prefix itself also matches
		{"file-write-data", "/data/x", false},
		{"file-read-extended", "/other", false},
		{"process-exec", "/bin/ls", true},
		{"process-exec", "/bin/sh", false},  // literal filter is exact
		{"process-exec-fork", "/bin/ls", false}, // exact op, not a wildcard
	}
	for _, c := range cases {
		if d := p.Decide(c.op, c.path); d.Allowed != c.allowed {
			t.Errorf("Decide(%q, %q) allowed=%v, want %v", c.op, c.path, d.Allowed, c.allowed)
		}
	}
}

func TestRegexFullStringMatch(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Allow, "file-read*", MustRegex(`/Users/[^/]+/Library/.*`)},
		{Deny, "file-read*", MustRegex(`/private/secret`)},
	}}
	if d := p.Decide("file-read-data", "/Users/alice/Library/Preferences/x.plist"); !d.Allowed {
		t.Errorf("regex should match full path, got %+v", d)
	}
	if d := p.Decide("file-read-data", "/etc/Users/alice/Library/x"); d.Allowed {
		t.Errorf("regex must be anchored at the start, got %+v", d)
	}
	if d := p.Decide("file-read-data", "/private/secret/even/deeper"); !d.Allowed {
		// The deny pattern is not "/private/secret.*", so it must not match
		// a longer path; the default deny decides instead.
		if d.Rule != -1 {
			t.Errorf("regex must be anchored at the end, got %+v", d)
		}
	}
	if _, err := RegexFilter("(unclosed"); err == nil {
		t.Error("invalid regex pattern must be rejected")
	}
}

func TestShadowedDenies(t *testing.T) {
	// The attack from the brief: a broad later allow re-opens a private
	// directory that an earlier deny meant to close.
	p := &Policy{Rules: []Rule{
		{Allow, "file-read*", SubpathFilter("/System")},                    // 0: unrelated
		{Deny, "file-read*", SubpathFilter("/Users/alice/Private")},        // 1: shadowed by 3
		{Deny, "file-write*", SubpathFilter("/Users/alice/Private")},       // 2: not shadowed
		{Allow, "file-read*", SubpathFilter("/Users")},                     // 3
	}}
	if got := p.ShadowedDenies(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("ShadowedDenies=%v, want [1]", got)
	}
	// The shadowed deny indeed never decides.
	if d := p.Decide("file-read-data", "/Users/alice/Private/key"); !d.Allowed || d.Rule != 3 {
		t.Fatalf("shadowed deny must not decide, got %+v", d)
	}
}

func TestShadowedDeniesCoverageShapes(t *testing.T) {
	cases := []struct {
		name  string
		deny  Rule
		later Rule
		want  bool
	}{
		{"literal covered by subpath",
			Rule{Deny, "file-read*", LiteralFilter("/a/b/c")},
			Rule{Allow, "file-read*", SubpathFilter("/a/b")}, true},
		{"subpath covered by wider subpath",
			Rule{Deny, "file-read*", SubpathFilter("/a/b")},
			Rule{Allow, "file-read*", SubpathFilter("/a")}, true},
		{"subpath not covered by narrower subpath",
			Rule{Deny, "file-read*", SubpathFilter("/a")},
			Rule{Allow, "file-read*", SubpathFilter("/a/b")}, false},
		{"subpath not covered by literal",
			Rule{Deny, "file-read*", SubpathFilter("/a/b")},
			Rule{Allow, "file-read*", LiteralFilter("/a/b")}, false},
		{"boundary: /a/b does not cover /a/bc",
			Rule{Deny, "file-read*", SubpathFilter("/a/bc")},
			Rule{Allow, "file-read*", SubpathFilter("/a/b")}, false},
		{"wildcard op covered by wider wildcard",
			Rule{Deny, "file-read*", SubpathFilter("/a")},
			Rule{Allow, "file-*", SubpathFilter("/a")}, true},
		{"exact op covers exact op",
			Rule{Deny, "process-exec", SubpathFilter("/a")},
			Rule{Allow, "process-exec", SubpathFilter("/a")}, true},
		{"wildcard not covered by exact op",
			Rule{Deny, "file-read*", SubpathFilter("/a")},
			Rule{Allow, "file-read-data", SubpathFilter("/a")}, false},
		{"different operation does not shadow",
			Rule{Deny, "file-read*", SubpathFilter("/a")},
			Rule{Allow, "file-write*", SubpathFilter("/a")}, false},
		{"literal covered by regex",
			Rule{Deny, "file-read*", LiteralFilter("/a/b/c")},
			Rule{Allow, "file-read*", MustRegex(`/a/.*`)}, true},
		{"identical regex shadows",
			Rule{Deny, "file-read*", MustRegex(`/a/[0-9]+`)},
			Rule{Allow, "file-read*", MustRegex(`/a/[0-9]+`)}, true},
		{"different regex does not shadow",
			Rule{Deny, "file-read*", MustRegex(`/a/[0-9]+`)},
			Rule{Allow, "file-read*", MustRegex(`/a/[0-9]*`)}, false},
		{"later deny also shadows",
			Rule{Deny, "file-read*", SubpathFilter("/a/b")},
			Rule{Deny, "file-read*", SubpathFilter("/a")}, true},
	}
	for _, c := range cases {
		p := &Policy{Rules: []Rule{c.deny, c.later}}
		got := len(p.ShadowedDenies()) == 1
		if got != c.want {
			t.Errorf("%s: shadowed=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestShadowedDeniesOrderMatters(t *testing.T) {
	// A deny is only shadowed by rules that come after it.
	p := &Policy{Rules: []Rule{
		{Allow, "file-read*", SubpathFilter("/Users")},
		{Deny, "file-read*", SubpathFilter("/Users/alice/Private")},
	}}
	if got := p.ShadowedDenies(); len(got) != 0 {
		t.Fatalf("earlier allow must not shadow a later deny, got %v", got)
	}
	if d := p.Decide("file-read-data", "/Users/alice/Private/key"); d.Allowed || d.Rule != 1 {
		t.Fatalf("later deny must decide, got %+v", d)
	}
}
