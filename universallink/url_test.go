package universallink

import "testing"

func TestRemoveDotSegments(t *testing.T) {
	// Examples from RFC 3986 section 5.4 plus the attack shapes from the brief.
	tests := []struct {
		input string
		want  string
	}{
		{"/a/b/c/./../../g", "/a/g"},
		{"mid/content=5/../6", "mid/6"},
		{"/share/../account", "/account"},
		{"/share/./profile", "/share/profile"},
		{"/a/../../b", "/b"},
		{"/a/b/..", "/a/"},
		{"/a/b/../", "/a/"},
		{"/a/b/./", "/a/b/"},
		{"/static/../static/./x", "/static/x"},
		{"./relative", "relative"},
		{"../relative", "relative"},
		{"/", "/"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			if got := removeDotSegments(tc.input); got != tc.want {
				t.Fatalf("removeDotSegments(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestPrepareURLRejects(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		errIs error
	}{
		{"lowercase encoded slash", "https://example.com/share%2faccount", ErrEncodedSlash},
		{"uppercase encoded slash", "https://example.com/share%2Faccount", ErrEncodedSlash},
		{"encoded slash before dot segment", "https://example.com/a/b%2f../c", ErrEncodedSlash},
		{"duplicate slash after host", "https://example.com//evil", ErrDuplicateSlash},
		{"internal duplicate slash", "https://example.com/a//b", ErrDuplicateSlash},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := prepareURL(tc.raw); err != tc.errIs {
				t.Fatalf("prepareURL(%q) error = %v, want %v", tc.raw, err, tc.errIs)
			}
		})
	}
}

func TestPrepareURLCleansPath(t *testing.T) {
	tests := []struct {
		raw  string
		path string
	}{
		{"https://example.com/share/../account", "/account"},
		{"https://example.com/share/./profile", "/share/profile"},
		{"https://example.com", "/"},
		{"https://example.com?x=1", "/"},
		{"https://example.com/a%20b/c", "/a b/c"},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			prepared, err := prepareURL(tc.raw)
			if err != nil {
				t.Fatalf("prepareURL(%q) unexpected error: %v", tc.raw, err)
			}
			if prepared.path != tc.path {
				t.Fatalf("prepareURL(%q).path = %q, want %q", tc.raw, prepared.path, tc.path)
			}
		})
	}
}

func TestParseQuery(t *testing.T) {
	values, err := parseQuery("a=1&b=hello%20world&flag&a=2&semi=a%3Bb")
	if err != nil {
		t.Fatalf("parseQuery returned error: %v", err)
	}
	if got := values["a"]; len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("repeated key a = %v, want [1 2]", got)
	}
	if got := values.Get("b"); got != "hello world" {
		t.Fatalf("b = %q, want %q", got, "hello world")
	}
	if got := values.Get("flag"); got != "" {
		t.Fatalf("flag = %q, want empty string", got)
	}
	if got := values.Get("semi"); got != "a;b" {
		t.Fatalf("semi = %q, want %q", got, "a;b")
	}

	empty, err := parseQuery("")
	if err != nil || len(empty) != 0 {
		t.Fatalf("parseQuery(\"\") = %v, %v", empty, err)
	}
}

func TestParseQueryRejectsBadEscaping(t *testing.T) {
	if _, err := parseQuery("a=%zz"); err == nil {
		t.Fatal("parseQuery with invalid percent escape must return an error")
	}
	if _, err := parseQuery("%zz=1"); err == nil {
		t.Fatal("parseQuery with invalid key escaping must return an error")
	}
}

func TestEncodedDotSegmentsAreNormalized(t *testing.T) {
	// Encoded dot segments decode to ordinary dot segments and must be removed
	// exactly like their literal forms.
	prepared, err := prepareURL("https://example.com/share/%2e%2e/account")
	if err != nil {
		t.Fatalf("prepareURL unexpected error: %v", err)
	}
	if prepared.path != "/account" {
		t.Fatalf("path = %q, want /account", prepared.path)
	}
}

func TestPrepareURLRejectsMalformedAndOpaque(t *testing.T) {
	if _, err := prepareURL("http://[::1:bad"); err == nil {
		t.Fatal("malformed URL must return an error")
	}
	if _, err := prepareURL("mailto:user@example.com"); err == nil {
		t.Fatal("opaque URL must return an error")
	}
}

func TestRemoveDotSegmentsTrailingDots(t *testing.T) {
	if got := removeDotSegments("a/b/."); got != "a/b/" {
		t.Fatalf("removeDotSegments(\"a/b/.\") = %q, want %q", got, "a/b/")
	}
	if got := removeDotSegments("a/b/.."); got != "a/" {
		t.Fatalf("removeDotSegments(\"a/b/..\") = %q, want %q", got, "a/")
	}
	if got := removeDotSegments("a/.."); got != "/" {
		t.Fatalf("removeDotSegments(\"a/..\") = %q, want %q", got, "/")
	}
}
