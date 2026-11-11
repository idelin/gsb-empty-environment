package mailfrag_test

import (
	"errors"
	"strings"
	"testing"

	"example.org/offlinecli/mailfrag"
)

func mustRender(t *testing.T, n mailfrag.Node) string {
	t.Helper()
	got, err := mailfrag.Render(n)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	return got
}

func TestTextEscapesMarkup(t *testing.T) {
	n := mailfrag.Text(`<script>alert("x")</script> & 2 > 1`)
	want := `&lt;script&gt;alert("x")&lt;/script&gt; &amp; 2 &gt; 1`
	if got := mustRender(t, n); got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestAttributeQuoteEntities(t *testing.T) {
	n := mailfrag.El("span", []mailfrag.Attr{
		mailfrag.A("title", `He said "hi" & it's <here>`),
	}, mailfrag.Text("x"))
	want := `<span title="He said &quot;hi&quot; &amp; it&#39;s &lt;here&gt;">x</span>`
	got := mustRender(t, n)
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
	if strings.Contains(got, `"hi"`) || strings.Contains(got, "it's") {
		t.Errorf("Render() = %q, raw quotes leaked into attribute", got)
	}
}

func TestDoubleSerialization(t *testing.T) {
	n := mailfrag.El("p", nil, mailfrag.Text(`a<b>&"'`))
	first := mustRender(t, n)
	second := mustRender(t, n)
	if first != second {
		t.Errorf("Render not deterministic: %q vs %q", first, second)
	}

	// Feeding rendered output back as text must escape entities again,
	// never re-interpret them as markup.
	back := mustRender(t, mailfrag.Text(first))
	if strings.Contains(back, "<b>") {
		t.Errorf("re-serialized output contains live markup: %q", back)
	}
	if !strings.Contains(back, "&amp;lt;") {
		t.Errorf("re-serialized output missing double-escaped entity: %q", back)
	}

	// Literal entity text is escaped once more, not decoded.
	got := mustRender(t, mailfrag.Text("&lt;script&gt;"))
	if want := "&amp;lt;script&amp;gt;"; got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestNestedTags(t *testing.T) {
	u, err := mailfrag.ParseURL("https://example.com/?a=1&b=2")
	if err != nil {
		t.Fatalf("ParseURL() error = %v", err)
	}
	n := mailfrag.El("div", []mailfrag.Attr{mailfrag.A("class", "msg")},
		mailfrag.El("p", nil,
			mailfrag.Text("Hello "),
			mailfrag.El("strong", nil,
				mailfrag.El("em", nil, mailfrag.Text("w<o>rld"))),
			mailfrag.Text("!"),
		),
		mailfrag.El("ul", nil,
			mailfrag.El("li", nil,
				mailfrag.El("a", []mailfrag.Attr{mailfrag.Href(u)},
					mailfrag.Text("link"))),
		),
	)
	want := `<div class="msg"><p>Hello <strong><em>w&lt;o&gt;rld</em></strong>!</p>` +
		`<ul><li><a href="https://example.com/?a=1&amp;b=2">link</a></li></ul></div>`
	if got := mustRender(t, n); got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestConcatFragments(t *testing.T) {
	f := mailfrag.Concat(
		mailfrag.Frag(mailfrag.Text("a")),
		mailfrag.Frag(mailfrag.El("br", nil), mailfrag.Text("b")),
	)
	if got, want := mustRender(t, f), "a<br>b"; got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestURLConfusionRejected(t *testing.T) {
	bad := []string{
		"",
		"javascript:alert(1)",
		"JaVaScRiPt:alert(1)",
		"java\tscript:alert(1)",
		"java\nscript:alert(1)",
		" javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"//evil.example/phish",
		"https:///path-only",
		"http://",
		"http://user:pass@evil.example/",
		"http://trusted.example@evil.example/",
		"https://exa mple.com/",
		`https://evil.example\@trusted.example/`,
		"\x01https://example.com/",
		"https://example.com/\x7f",
	}
	for _, raw := range bad {
		if u, err := mailfrag.ParseURL(raw); err == nil {
			t.Errorf("ParseURL(%q) = %q, want error", raw, u)
		}
	}
}

func TestURLAccepted(t *testing.T) {
	good := []string{
		"https://example.com/path?q=1&r=2",
		"http://sub.example.co.uk:8080/x",
		"HTTPS://EXAMPLE.COM/KeepCase",
		"mailto:user@example.com",
		"mailto:user@example.com?subject=Hi%20there",
	}
	for _, raw := range good {
		u, err := mailfrag.ParseURL(raw)
		if err != nil {
			t.Errorf("ParseURL(%q) error = %v", raw, err)
			continue
		}
		if u.String() != raw {
			t.Errorf("ParseURL(%q).String() = %q", raw, u)
		}
	}

	// Ampersand in a query string is entity-escaped in href output.
	u, err := mailfrag.ParseURL("https://example.com/?a=1&b=2")
	if err != nil {
		t.Fatalf("ParseURL() error = %v", err)
	}
	n := mailfrag.El("a", []mailfrag.Attr{mailfrag.Href(u)}, mailfrag.Text("x"))
	want := `<a href="https://example.com/?a=1&amp;b=2">x</a>`
	if got := mustRender(t, n); got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestCrossContextRejected(t *testing.T) {
	cases := []struct {
		name string
		node mailfrag.Node
	}{
		{"script tag not allowlisted", mailfrag.El("script", nil, mailfrag.Text("alert(1)"))},
		{"tag with markup", mailfrag.El("b><script", nil)},
		{"event handler attr", mailfrag.El("span", []mailfrag.Attr{mailfrag.A("onclick", "x()")}, mailfrag.Text("x"))},
		{"raw href string", mailfrag.El("a", []mailfrag.Attr{mailfrag.A("href", "https://example.com")}, mailfrag.Text("x"))},
		{"raw src string", mailfrag.El("img", []mailfrag.Attr{mailfrag.A("src", "https://example.com/x.png")})},
		{"attr not in allowlist", mailfrag.El("span", []mailfrag.Attr{mailfrag.A("formaction", "x")}, mailfrag.Text("x"))},
		{"duplicate attr", mailfrag.El("span", []mailfrag.Attr{mailfrag.A("class", "a"), mailfrag.A("class", "b")}, mailfrag.Text("x"))},
		{"attr name with quote", mailfrag.El("span", []mailfrag.Attr{mailfrag.A(`x" onmouseover="`, "y")}, mailfrag.Text("x"))},
		{"void with children", mailfrag.El("br", nil, mailfrag.Text("x"))},
		{"img without src", mailfrag.El("img", []mailfrag.Attr{mailfrag.A("alt", "x")})},
		{"control char in text", mailfrag.Text("a\x00b")},
		{"control char in attr", mailfrag.El("span", []mailfrag.Attr{mailfrag.A("title", "a\x07b")}, mailfrag.Text("x"))},
		{"zero-value URL in href", mailfrag.El("a", []mailfrag.Attr{mailfrag.Href(mailfrag.URL{})}, mailfrag.Text("x"))},
		{"nil node", nil},
	}
	for _, tc := range cases {
		if got, err := mailfrag.Render(tc.node); err == nil {
			t.Errorf("%s: Render() = %q, want error", tc.name, got)
		}
	}
}

type spyQueue struct {
	bodies []string
	err    error
}

func (s *spyQueue) Enqueue(to, htmlBody string) error {
	if s.err != nil {
		return s.err
	}
	s.bodies = append(s.bodies, to+"|"+htmlBody)
	return nil
}

func TestEnqueueRenderedIsAtomic(t *testing.T) {
	q := &spyQueue{}
	bad := mailfrag.El("div", nil,
		mailfrag.Text("partial "),
		mailfrag.El("script", nil, mailfrag.Text("alert(1)")),
	)
	if err := mailfrag.EnqueueRendered(q, "victim@example.com", bad); err == nil {
		t.Fatal("EnqueueRendered() with invalid fragment, want error")
	}
	if len(q.bodies) != 0 {
		t.Fatalf("queue received %d messages after render failure, want 0: %q", len(q.bodies), q.bodies)
	}

	good := mailfrag.El("p", nil, mailfrag.Text("hi"))
	if err := mailfrag.EnqueueRendered(q, "a@example.com", good); err != nil {
		t.Fatalf("EnqueueRendered() error = %v", err)
	}
	if want := []string{"a@example.com|<p>hi</p>"}; strings.Join(q.bodies, ",") != strings.Join(want, ",") {
		t.Errorf("queue bodies = %q, want %q", q.bodies, want)
	}

	failing := &spyQueue{err: errors.New("smtp down")}
	if err := mailfrag.EnqueueRendered(failing, "a@example.com", good); err == nil {
		t.Fatal("EnqueueRendered() with failing queue, want error")
	}
}
