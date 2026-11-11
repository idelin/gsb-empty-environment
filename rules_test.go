package robotstxt

import (
	"net/url"
	"testing"
)

func TestParseDecision(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		userAgent string
		path      string
		want      bool
	}{
		// RFC 9309 section 2.4.2: prefix-based Disallow.
		{
			name:      "rfc example: disallow prefix",
			body:      "User-agent: *\nDisallow: /\n",
			userAgent: "foobot",
			path:      "/any",
			want:      false,
		},
		{
			name:      "rfc example: root disallowed",
			body:      "User-agent: *\nDisallow: /\n",
			userAgent: "foobot",
			path:      "/",
			want:      false,
		},
		{
			name:      "rfc example: empty disallow allows",
			body:      "User-agent: *\nDisallow:\n",
			userAgent: "foobot",
			path:      "/any",
			want:      true,
		},
		{
			name:      "rfc example: longest match allow wins",
			body:      "User-agent: *\nDisallow: /folder/\nAllow: /folder/page\n",
			userAgent: "foobot",
			path:      "/folder/page",
			want:      true,
		},
		{
			name:      "rfc example: longer disallow still blocks sibling",
			body:      "User-agent: *\nDisallow: /folder/\nAllow: /folder/page\n",
			userAgent: "foobot",
			path:      "/folder/other",
			want:      false,
		},
		// RFC 9309 section 2.4.3: end-of-path anchor "$".
		{
			name:      "dollar anchor blocks exact file",
			body:      "User-agent: *\nDisallow: /example.html$\n",
			userAgent: "foobot",
			path:      "/example.html",
			want:      false,
		},
		{
			name:      "dollar anchor does not match with extra suffix",
			body:      "User-agent: *\nDisallow: /example.html$\n",
			userAgent: "foobot",
			path:      "/example.htmlx",
			want:      true,
		},
		{
			name:      "dollar anchor allows directory form",
			body:      "User-agent: *\nDisallow: /example.html$\n",
			userAgent: "foobot",
			path:      "/example.html/info",
			want:      true,
		},
		{
			name:      "dollar sign in the middle is a literal character",
			body:      "User-agent: *\nDisallow: /example$.html\n",
			userAgent: "foobot",
			path:      "/example$.html",
			want:      false,
		},
		{
			name:      "literal dollar does not match without dollar",
			body:      "User-agent: *\nDisallow: /example$.html\n",
			userAgent: "foobot",
			path:      "/example.html",
			want:      true,
		},
		// RFC 9309 section 2.4.4: "*" wildcard.
		{
			name:      "star wildcard blocks extension",
			body:      "User-agent: *\nDisallow: /example/*.html\n",
			userAgent: "foobot",
			path:      "/example/a.html",
			want:      false,
		},
		{
			name:      "star wildcard nested path",
			body:      "User-agent: *\nDisallow: /example/*.html\n",
			userAgent: "foobot",
			path:      "/example/sub/a.html",
			want:      false,
		},
		{
			name:      "star wildcard different extension allowed",
			body:      "User-agent: *\nDisallow: /example/*.html\n",
			userAgent: "foobot",
			path:      "/example/a.txt",
			want:      true,
		},
		// RFC 9309 section 2.4.5: most specific user-agent group wins.
		{
			name: "specific group beats wildcard",
			body: "User-agent: foobot\nDisallow: /specific/\n\n" +
				"User-agent: *\nDisallow: /general/\n",
			userAgent: "foobot",
			path:      "/specific/x",
			want:      false,
		},
		{
			name: "specific group does not inherit wildcard rules",
			body: "User-agent: foobot\nDisallow: /specific/\n\n" +
				"User-agent: *\nDisallow: /general/\n",
			userAgent: "foobot",
			path:      "/general/x",
			want:      true,
		},
		{
			name: "other crawler falls back to wildcard",
			body: "User-agent: foobot\nDisallow: /specific/\n\n" +
				"User-agent: *\nDisallow: /general/\n",
			userAgent: "barbot",
			path:      "/general/x",
			want:      false,
		},
		{
			name: "longest user-agent token wins",
			body: "User-agent: foo\nDisallow: /a/\n\n" +
				"User-agent: foobot\nDisallow: /b/\n",
			userAgent: "foobot/1.0",
			path:      "/b/x",
			want:      false,
		},
		{
			name: "longest user-agent token wins, short group avoided",
			body: "User-agent: foo\nDisallow: /a/\n\n" +
				"User-agent: foobot\nDisallow: /b/\n",
			userAgent: "foobot/1.0",
			path:      "/a/x",
			want:      true,
		},
		// RFC 9309 section 2.4.1: groups with the same name are merged.
		{
			name: "same-name groups are merged",
			body: "User-agent: foobot\nDisallow: /a/\n\n" +
				"User-agent: foobot\nDisallow: /b/\n",
			userAgent: "foobot",
			path:      "/b/x",
			want:      false,
		},
		{
			name: "merged group keeps earlier rules",
			body: "User-agent: foobot\nDisallow: /a/\n\n" +
				"User-agent: foobot\nDisallow: /b/\n",
			userAgent: "foobot",
			path:      "/a/x",
			want:      false,
		},
		// Equal-length rules: Allow wins (RFC 9309 section 2.3.2.2).
		{
			name:      "equal length allow wins",
			body:      "User-agent: *\nDisallow: /folder/file\nAllow: /folder/file\n",
			userAgent: "foobot",
			path:      "/folder/file",
			want:      true,
		},
		{
			name:      "comments blank lines and crlf ignored",
			body:      "# a comment\r\n\r\nUser-agent: * # trailing\r\nDisallow: /x\r\n",
			userAgent: "foobot",
			path:      "/x",
			want:      false,
		},
		{
			name:      "sitemap and unknown fields ignored",
			body:      "User-agent: *\nSitemap: https://example.com/s.xml\nDisallow: /x\n",
			userAgent: "foobot",
			path:      "/x",
			want:      false,
		},
		{
			name:      "bom stripped",
			body:      "\ufeffUser-agent: *\nDisallow: /bom\n",
			userAgent: "foobot",
			path:      "/bom",
			want:      false,
		},
		// RFC 9309 section 2.3.2.3: percent-encoded paths.
		{
			name:      "percent encoded rule matches raw request",
			body:      "User-agent: *\nDisallow: /h%C3%A4\n",
			userAgent: "foobot",
			path:      "/h%C3%A4",
			want:      false,
		},
		{
			name:      "percent encoded rule matches decoded request",
			body:      "User-agent: *\nDisallow: /h%C3%A4\n",
			userAgent: "foobot",
			path:      "/h\u00e4",
			want:      false,
		},
		{
			name:      "unencoded rule only matches raw path, not encoded request",
			body:      "User-agent: *\nDisallow: /h\u00e4\n",
			userAgent: "foobot",
			path:      "/h%C3%A4",
			want:      true,
		},
		{
			name:      "unencoded rule matches unencoded request directly",
			body:      "User-agent: *\nDisallow: /h\u00e4\n",
			userAgent: "foobot",
			path:      "/h\u00e4",
			want:      false,
		},
		{
			name:      "encoded slash is one decoded character, not separator",
			body:      "User-agent: *\nDisallow: /a%2Fb\n",
			userAgent: "foobot",
			path:      "/a%2Fb",
			want:      false,
		},
		{
			name:      "literal slash rule does not match encoded slash",
			body:      "User-agent: *\nDisallow: /a/b\n",
			userAgent: "foobot",
			path:      "/a%2Fb",
			want:      true,
		},
		{
			name:      "percent triplet at end of rule path",
			body:      "User-agent: *\nDisallow: /a/b%2F\n",
			userAgent: "foobot",
			path:      "/a/b%2F",
			want:      false,
		},
		{
			name:      "consecutive user agents share rules",
			body:      "User-agent: foo\nUser-agent: bar\nDisallow: /shared/\n",
			userAgent: "bar",
			path:      "/shared/x",
			want:      false,
		},
		{
			name: "rule after user-agent block starts separate group",
			body: "User-agent: foo\nDisallow: /a/\n\n" +
				"User-agent: bar\nDisallow: /b/\n",
			userAgent: "foo",
			path:      "/b/x",
			want:      true,
		},
		{
			name:      "sitemap between groups does not merge them",
			body:      "User-agent: foo\nDisallow: /a/\nSitemap: https://e/s.xml\nUser-agent: bar\nDisallow: /b/\n",
			userAgent: "foo",
			path:      "/b/x",
			want:      true,
		},
		{
			name:      "star matches empty segment",
			body:      "User-agent: *\nDisallow: /a*/b\n",
			userAgent: "foobot",
			path:      "/a/b",
			want:      false,
		},
		{
			name:      "multiple stars in pattern",
			body:      "User-agent: *\nDisallow: /a/*/c/*.html\n",
			userAgent: "foobot",
			path:      "/a/x/c/y/z.html",
			want:      false,
		},
		{
			name:      "case insensitive user-agent token",
			body:      "User-agent: FooBot\nDisallow: /x\n",
			userAgent: "foobot/2.0 (http://example.com)",
			path:      "/x",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parse([]byte(tt.body)).allowed(tt.userAgent, tt.path, mustUnescape(t, tt.path))
			if got != tt.want {
				t.Fatalf("allowed(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestAllowedURLStripsQuery(t *testing.T) {
	r := parse([]byte("User-agent: *\nDisallow: /search\n"))
	u, err := url.Parse("http://example.com/search?q=1")
	if err != nil {
		t.Fatal(err)
	}
	if r.allowed("foobot", u.EscapedPath(), u.Path) {
		t.Fatal("query must not be part of the matched path; /search?q=1 should be disallowed")
	}
}

func TestEmptyGroupAllows(t *testing.T) {
	r := parse([]byte("User-agent: *\nDisallow:\n"))
	if !r.allowed("foobot", "/anything", "/anything") {
		t.Fatal("group with no rules must allow all paths")
	}
}

func mustUnescape(t *testing.T, path string) string {
	t.Helper()
	decoded, err := url.PathUnescape(path)
	if err != nil {
		t.Fatalf("PathUnescape(%q): %v", path, err)
	}
	return decoded
}
