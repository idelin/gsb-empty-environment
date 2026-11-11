package ctxescape

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type data struct{ s string }

func render(t *testing.T, parts ...any) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	for _, part := range parts {
		switch p := part.(type) {
		case string:
			if _, err := w.WriteString(p); err != nil {
				return buf.String(), err
			}
		case data:
			if err := w.WriteText(p.s); err != nil {
				return buf.String(), err
			}
		}
	}
	return buf.String(), w.Close()
}

func TestHTMLTextEscaping(t *testing.T) {
	out, err := render(t, "<p>",
		data{`<script>alert("x") & co</script>`}, "</p>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `<p>&lt;script&gt;alert(&#34;x&#34;) &amp; co&lt;/script&gt;</p>`
	if out != want {
		t.Fatalf("text context:\n got %q\nwant %q", out, want)
	}
}

func TestQuotedAttributeEscaping(t *testing.T) {
	out, err := render(t, `<a title="`,
		data{`" onmouseover="alert(1)`}, `">x</a>`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, `onmouseover="alert`) {
		t.Fatalf("attribute breakout not neutralized: %q", out)
	}
	want := `<a title="&#34; onmouseover=&#34;alert(1)">x</a>`
	if out != want {
		t.Fatalf("quoted attr:\n got %q\nwant %q", out, want)
	}
}

func TestSingleQuotedAttributeEscaping(t *testing.T) {
	out, err := render(t, `<a title='`,
		data{`' onclick='alert(1)`}, `'>x</a>`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, `onclick='alert`) {
		t.Fatalf("single-quote breakout not neutralized: %q", out)
	}
}

func TestUnquotedAttributeEscaping(t *testing.T) {
	cases := []string{
		`x onmouseover=alert(1)`,
		"x\tonclick=alert(1)",
		"x\nonclick=alert(1)",
		"x`onclick=alert(1)",
		`x><script>alert(1)</script>`,
		"x=y",
	}
	for _, in := range cases {
		out, err := render(t, `<a title=`, data{in}, `>link</a>`)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", in, err)
		}
		payload := out[len("<a title=") : len(out)-len(">link</a>")]
		for _, bad := range []string{" ", "\t", "\n", "=", "`", ">", "<"} {
			if strings.Contains(payload, bad) {
				t.Fatalf("unquoted payload %q contains raw %q in %q", in, bad, out)
			}
		}
	}
}

func TestURLAttributeRejectsUnsafeSchemes(t *testing.T) {
	unsafe := []string{
		"javascript:alert(1)",
		"JaVaScRiPt:alert(1)",
		"  javascript:alert(1)",
		"\njavascript:alert(1)",
		"java\x00script:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
	}
	for _, u := range unsafe {
		if _, err := render(t, `<a href="`, data{u}, `">x</a>`); !errors.Is(err, ErrUnsafeURL) {
			t.Fatalf("expected ErrUnsafeURL for %q, got %v", u, err)
		}
	}

	safe := []string{
		"/path/to/page?q=1",
		"relative/path",
		"//cdn.example.com/app.js",
		"https://example.com/a?b=c&d=e",
		"http://example.com",
		"mailto:user@example.com",
		"#fragment",
		"?query=1",
	}
	for _, u := range safe {
		if _, err := render(t, `<a href="`, data{u}, `">x</a>`); err != nil {
			t.Fatalf("expected safe URL %q to pass, got %v", u, err)
		}
	}
}

func TestUnquotedURLAttributeRejectsUnsafeScheme(t *testing.T) {
	_, err := render(t, `<a href=`, data{"javascript:alert(1)"}, `>x</a>`)
	if !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("expected ErrUnsafeURL, got %v", err)
	}
}

func TestHTMLCommentRejectsInterpolation(t *testing.T) {
	_, err := render(t, "<!-- comment ",
		data{"--> <img src=x onerror=alert(1)>"}, " -->")
	if !errors.Is(err, ErrAmbiguousContext) {
		t.Fatalf("expected ErrAmbiguousContext in comment, got %v", err)
	}
}

func TestCDATARejectsInterpolation(t *testing.T) {
	_, err := render(t, "<svg><![CDATA[ hello ",
		data{"]]><script>alert(1)</script>"}, " ]]></svg>")
	if !errors.Is(err, ErrAmbiguousContext) {
		t.Fatalf("expected ErrAmbiguousContext in CDATA, got %v", err)
	}
}

func TestCommentAndCDATAParseAcrossChunks(t *testing.T) {
	template := "<!-- a --><b>ok</b><![CDATA[ raw ]]><p>done</p>"
	got, err := renderInChunks(template, 1)
	if err != nil {
		t.Fatalf("split parse failed: %v", err)
	}
	if got != template {
		t.Fatalf("comment/CDATA template not preserved:\n got %q\nwant %q", got, template)
	}
}

func TestScriptStringEscaping(t *testing.T) {
	out, err := render(t,
		`<script>var msg = "`,
		data{`";alert(1);//`},
		`";</script>`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, `";alert(1);`) {
		t.Fatalf("JS string breakout: %q", out)
	}
	if !strings.Contains(out, `\u0022`) {
		t.Fatalf("quote not JS-escaped: %q", out)
	}

	out, err = render(t,
		`<script>var msg = '`,
		data{`'</script><script>alert(1)</script>`},
		`';</script>`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "</script><script>") {
		t.Fatalf("raw script close not neutralized: %q", out)
	}
}

func TestScriptTemplateLiteralEscaping(t *testing.T) {
	out, err := render(t,
		"<script>var msg = `",
		data{"${alert(1)}`"},
		"`;</script>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "${alert(1)}") {
		t.Fatalf("template-literal interpolation not neutralized: %q", out)
	}
}

func TestScriptStatementPositionRejected(t *testing.T) {
	_, err := render(t, `<script>`, data{"alert(1)"}, `</script>`)
	if !errors.Is(err, ErrAmbiguousContext) {
		t.Fatalf("expected rejection in JS code position, got %v", err)
	}
}

func TestEventHandlerAttribute(t *testing.T) {
	out, err := render(t, `<button onclick="say('`,
		data{`');alert(1);//`}, `')">go</button>`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, `');alert(1);`) {
		t.Fatalf("event-handler breakout: %q", out)
	}
}

func TestStyleAttributeAndElementRejected(t *testing.T) {
	if _, err := render(t, `<p style="`,
		data{"x:expression(alert(1))"}, `">`); !errors.Is(err, ErrAmbiguousContext) {
		t.Fatalf("style attribute should reject interpolation, got %v", err)
	}
	if _, err := render(t, `<style>`,
		data{"body{color:red}"}, `</style>`); !errors.Is(err, ErrAmbiguousContext) {
		t.Fatalf("style element should reject interpolation, got %v", err)
	}
}

func TestAmbiguousPartialMarkupRejected(t *testing.T) {
	partials := []string{
		`<a hr`,       // mid attribute name
		`<div c`,      // mid tag attribute after name
		`<!-- x`,      // inside comment
		`<![CDATA[x`,  // inside CDATA
		`<script>var`, // JS statement position
	}
	for _, tmpl := range partials {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		if _, err := w.WriteString(tmpl); err != nil {
			t.Fatalf("write %q: %v", tmpl, err)
		}
		if err := w.WriteText("x"); !errors.Is(err, ErrAmbiguousContext) {
			t.Fatalf("%q must reject interpolation, got %v", tmpl, err)
		}
	}
}

func TestCloseDetectsIncompleteStream(t *testing.T) {
	cases := []string{
		`<div`,
		`<div title="unterminated`,
		`<!-- unterminated`,
		`<![CDATA[ unterminated`,
		`<script>var x = 1;`,
	}
	for _, tmpl := range cases {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		if _, err := w.WriteString(tmpl); err != nil {
			t.Fatalf("write %q: %v", tmpl, err)
		}
		if err := w.Close(); !errors.Is(err, ErrIncompleteStream) {
			t.Fatalf("expected ErrIncompleteStream for %q, got %v", tmpl, err)
		}
	}
}

func TestRCDATAEscaping(t *testing.T) {
	out, err := render(t, `<textarea>`,
		data{`</textarea><img src=x onerror=alert(1)>`}, `</textarea>`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, `</textarea><img`) {
		t.Fatalf("RCDATA breakout not neutralized: %q", out)
	}
}
