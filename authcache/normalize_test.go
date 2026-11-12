package authcache

import (
	"errors"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
		err  error
	}{
		{name: "plain", in: "/a/b/c", want: "/a/b/c"},
		{name: "dot", in: "/a/./b", want: "/a/b"},
		{name: "dotdot", in: "/a/b/../c", want: "/a/c"},
		{name: "leading dotdot", in: "/../a", want: "/a"},
		{name: "trailing dot", in: "/a/.", want: "/a/"},
		{name: "trailing dotdot", in: "/a/b/..", want: "/a/"},
		{name: "encoded dot", in: "/a/%2e/b", want: "/a/b"},
		{name: "encoded dot uppercase", in: "/a/%2E/b", want: "/a/b"},
		{name: "encoded dotdot", in: "/a/%2e%2e/b", want: "/b"},
		{name: "mixed encoding dotdot", in: "/a/%2e./b", want: "/b"},
		{name: "encoded slash lower", in: "/a/%2fb", err: ErrEncodedSlash},
		{name: "encoded slash upper", in: "/a%2Fb", err: ErrEncodedSlash},
		{name: "encoded other escape kept after decode", in: "/a%20b", want: "/a b"},
		{name: "root", in: "/", want: "/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizePath(tc.in)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("NormalizePath(%q) error = %v, want %v", tc.in, err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizePath(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizePath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
