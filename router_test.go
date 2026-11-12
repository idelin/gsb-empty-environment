package netsec

import (
	"errors"
	"testing"
)

func newTestRouter(t *testing.T) *Router {
	t.Helper()

	router, err := NewRouter(
		Rule{Kind: Exact, Path: "/admin", Target: "admin-exact"},
		Rule{Kind: Prefix, Path: "/admin", Target: "admin-prefix"},
		Rule{Kind: Exact, Path: "/public", Target: "public-exact"},
		Rule{Kind: Prefix, Path: "/foo", Target: "foo"},
		Rule{Kind: Prefix, Path: "/foo/bar", Target: "foo-bar"},
		Rule{Kind: Prefix, Path: "/foobar", Target: "foobar"},
		Rule{Kind: Prefix, Path: "/", Target: "public"},
	)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func TestRouterMatchAndForwardPath(t *testing.T) {
	router := newTestRouter(t)

	tests := []struct {
		name   string
		raw    string
		target string
		path   string
	}{
		{name: "exact wins over prefix", raw: "/admin", target: "admin-exact", path: "/admin"},
		{name: "prefix below admin", raw: "/admin/users", target: "admin-prefix", path: "/admin/users"},
		{name: "dot segments removed before match", raw: "/admin/../public", target: "public-exact", path: "/public"},
		{name: "double slash canonicalized", raw: "//admin", target: "admin-exact", path: "/admin"},
		{name: "double slash with suffix", raw: "//admin/users", target: "admin-prefix", path: "/admin/users"},
		{name: "case sensitive", raw: "/ADMIN", target: "public", path: "/ADMIN"},
		{name: "prefix matches own path", raw: "/foo", target: "foo", path: "/foo"},
		{name: "prefix matches child element", raw: "/foo/bar", target: "foo-bar", path: "/foo/bar"},
		{name: "longest prefix wins", raw: "/foo/bar/baz", target: "foo-bar", path: "/foo/bar/baz"},
		{name: "prefix does not match partial element", raw: "/foobarbaz", target: "public", path: "/foobarbaz"},
		{name: "sibling prefix matches", raw: "/foobar/baz", target: "foobar", path: "/foobar/baz"},
		{name: "dot segment changes prefix selection", raw: "/foo/bar/../baz", target: "foo", path: "/foo/baz"},
		{name: "encoded dots removed", raw: "/foo/%2e%2e/admin", target: "admin-exact", path: "/admin"},
		{name: "root path", raw: "/", target: "public", path: "/"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			match, err := router.Match(test.raw)
			if err != nil {
				t.Fatalf("Match(%q) error: %v", test.raw, err)
			}
			if !match.Matched {
				t.Fatalf("Match(%q) did not match", test.raw)
			}
			if match.Rule.Target != test.target {
				t.Fatalf("Match(%q) target = %q, want %q", test.raw, match.Rule.Target, test.target)
			}
			if match.Path != test.path {
				t.Fatalf("Match(%q) path = %q, want %q", test.raw, match.Path, test.path)
			}

			// A proxy must forward Match.Path, never the raw request path.
			canonical, err := CanonicalPath(test.raw)
			if err != nil {
				t.Fatalf("CanonicalPath(%q) error: %v", test.raw, err)
			}
			if match.Path != canonical {
				t.Fatalf("match path %q != forwarding path %q for %q", match.Path, canonical, test.raw)
			}
		})
	}
}

func TestRouterRejectsEncodedSeparators(t *testing.T) {
	router := newTestRouter(t)

	for _, raw := range []string{
		"/admin%2Fx",
		"/admin%2fx",
		"/admin%5Cx",
		"/admin%5cx",
		"/admin%00x",
	} {
		t.Run(raw, func(t *testing.T) {
			match, err := router.Match(raw)
			if err == nil {
				t.Fatalf("Match(%q) = %+v, want rejection", raw, match)
			}
			if !errors.Is(err, ErrPathRejected) {
				t.Fatalf("Match(%q) error = %v, want ErrPathRejected", raw, err)
			}
		})
	}
}

func TestRouterNoMatchStillReturnsCanonicalPath(t *testing.T) {
	router, err := NewRouter(Rule{Kind: Prefix, Path: "/private", Target: "private"})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	match, err := router.Match("/public/../admin")
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if match.Matched {
		t.Fatalf("unexpected match: %+v", match)
	}
	if match.Path != "/admin" {
		t.Fatalf("path = %q, want /admin", match.Path)
	}
}

func TestRouterUsesCanonicalRulePathsForDuplicates(t *testing.T) {
	router := &Router{}
	if err := router.AddPrefix("/foo/../bar", "first"); err != nil {
		t.Fatalf("AddPrefix: %v", err)
	}
	err := router.AddPrefix("//bar", "second")
	if !errors.Is(err, ErrDuplicateRule) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateRule", err)
	}
}

func TestRouterRejectsInvalidRules(t *testing.T) {
	tests := []Rule{
		{Kind: 0, Path: "/admin", Target: "bad-kind"},
		{Kind: Prefix, Path: "/admin%2Fx", Target: "encoded-slash"},
		{Kind: Exact, Path: "relative", Target: "relative-path"},
	}

	for _, rule := range tests {
		if err := (&Router{}).Add(rule); !errors.Is(err, ErrInvalidRule) {
			t.Fatalf("Add(%+v) error = %v, want ErrInvalidRule", rule, err)
		}
	}
}
