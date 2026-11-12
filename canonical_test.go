package netsec

import (
	"errors"
	"testing"
)

func TestCanonicalPath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "root", raw: "/", want: "/"},
		{name: "plain", raw: "/admin", want: "/admin"},
		{name: "dot segment", raw: "/admin/../public", want: "/public"},
		{name: "leading repeated slash", raw: "//admin", want: "/admin"},
		{name: "inner repeated slash", raw: "/foo//bar", want: "/foo/bar"},
		{name: "trailing slash", raw: "/foo/", want: "/foo"},
		{name: "encoded dot segments", raw: "/%2e%2e/admin", want: "/admin"},
		{name: "encoded trailing dot", raw: "/admin/%2E", want: "/admin"},
		{name: "dot inside element is not a dot segment", raw: "/foo%2Ebar", want: "/foo.bar"},
		{name: "case preserved", raw: "/ADMIN", want: "/ADMIN"},
		{name: "space escaped", raw: "/foo%20bar", want: "/foo%20bar"},
		{name: "unicode escaped", raw: "/caf\u00e9", want: "/caf%C3%A9"},
		{name: "plus is literal", raw: "/a+b", want: "/a+b"},
		{name: "encoded question mark", raw: "/a%3Fb", want: "/a%3Fb"},
		{name: "encoded fragment", raw: "/a%23b", want: "/a%23b"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CanonicalPath(test.raw)
			if err != nil {
				t.Fatalf("CanonicalPath(%q) error: %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("CanonicalPath(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestCanonicalPathRejectsAmbiguousInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "encoded slash", raw: "/admin%2Fx"},
		{name: "lowercase encoded slash", raw: "/admin%2fx"},
		{name: "encoded backslash", raw: "/admin%5Cx"},
		{name: "lowercase encoded backslash", raw: "/admin%5cx"},
		{name: "encoded NUL", raw: "/admin%00x"},
		{name: "raw NUL", raw: "/admin\x00x"},
		{name: "raw backslash", raw: `/admin\x`},
		{name: "double encoded slash", raw: "/admin%252Fx"},
		{name: "query delimiter", raw: "/admin?x=1"},
		{name: "fragment delimiter", raw: "/admin#top"},
		{name: "invalid escape", raw: "/admin%zz"},
		{name: "truncated escape", raw: "/admin%2"},
		{name: "relative path", raw: "admin"},
		{name: "empty path", raw: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CanonicalPath(test.raw)
			if err == nil {
				t.Fatalf("CanonicalPath(%q) = %q, want rejection", test.raw, got)
			}
			if !errors.Is(err, ErrPathRejected) {
				t.Fatalf("CanonicalPath(%q) error = %v, want ErrPathRejected", test.raw, err)
			}
		})
	}
}
