package psl

import (
	"strings"
	"testing"
)

func TestOrganizationalDomains(t *testing.T) {
	cases := []struct {
		domain string
		suffix string
		org    string
	}{
		{"www.example.com", "com", "example.com"},
		{"a.b.example.com", "com", "example.com"},
		{"foo.example.co.uk", "co.uk", "example.co.uk"},
		{"example.com", "com", "example.com"},
		{"com", "com", ""},
		{"a.b.ck", "b.ck", "a.b.ck"},
		{"www.ck", "ck", "www.ck"},
		{"a.www.ck", "ck", "www.ck"},
		{"foo.city.kawasaki.jp", "kawasaki.jp", "city.kawasaki.jp"},
		{"a.b.kawasaki.jp", "b.kawasaki.jp", "a.b.kawasaki.jp"},
		{"user.github.io", "github.io", "user.github.io"},
	}
	for _, test := range cases {
		t.Run(test.domain, func(t *testing.T) {
			if got := PublicSuffix(test.domain); got != test.suffix {
				t.Fatalf("PublicSuffix(%q) = %q, want %q", test.domain, got, test.suffix)
			}
			if got := OrganizationalDomain(test.domain); got != test.org {
				t.Fatalf("OrganizationalDomain(%q) = %q, want %q", test.domain, got, test.org)
			}
		})
	}
}

func TestCustomRules(t *testing.T) {
	list, err := Parse(strings.NewReader("// comment\ncom\n*.wild.test\n!www.wild.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := list.PublicSuffix("a.b.wild.test"); got != "b.wild.test" {
		t.Fatalf("wildcard suffix = %q", got)
	}
	if got := list.OrganizationalDomain("www.wild.test"); got != "www.wild.test" {
		t.Fatalf("exception org = %q", got)
	}
	if !list.IsPublicSuffix("com") {
		t.Fatal("com should be a public suffix")
	}
}
