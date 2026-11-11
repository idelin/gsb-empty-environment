package ctxescape

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRegexpLiteralContext(t *testing.T) {
	// After "=" the '/' starts a regexp literal.
	tmpl := `<script>var re = /`
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if _, err := w.WriteString(tmpl); err != nil {
		t.Fatal(err)
	}
	if got := w.State(); got != "JavaScript regexp literal" {
		t.Fatalf("expected regexp context, got %s", got)
	}
	// Interpolation inside a regexp is not supported (fail closed).
	if err := w.WriteText("x"); !errors.Is(err, ErrAmbiguousContext) {
		t.Fatalf("regexp interpolation must be rejected, got %v", err)
	}
}

func TestScriptSubcontextsParseAcrossChunks(t *testing.T) {
	tmpl := "<script>" +
		`var a = "dq", b = 'sq', c = ` + "`tl`;" +
		"/* block */\n" +
		"// line\n" +
		`var r = /a-z/gi;` +
		"var t = `${1}`;" +
		`</script>`
	for size := 1; size <= len(tmpl); size++ {
		got, err := renderBytesInChunks(t, tmpl, size)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if got != tmpl {
			t.Fatalf("size %d:\n got %q\nwant %q", size, got, tmpl)
		}
	}
}

func TestTemplateLiteralExpressionNesting(t *testing.T) {
	tmpl := "<script>var x = `a ${ foo(`nested`) } b`;</script>"
	got, err := renderInChunks(tmpl, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != tmpl {
		t.Fatalf("nested template literal mismatch:\n got %q\nwant %q", got, tmpl)
	}
}

func TestJSStringSpecialEscapes(t *testing.T) {
	out, err := render(t, `<script>var s = "`,
		data{"a\\b\nc\r\td\u2028e\u2029f</g"}, `";</script>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"\n", "\r", "\t", "\u2028", "\u2029"} {
		if strings.Contains(out, raw) {
			t.Fatalf("raw control char survived in %q", out)
		}
	}
	if !strings.Contains(out, `\u003c\/g`) {
		t.Fatalf("script-close angle not escaped: %q", out)
	}
}

func TestNonCharacterRuneEscaped(t *testing.T) {
	// Non-character runes are legal in HTML text but must be entity-encoded
	// in an unquoted attribute where they could confuse legacy parsers.
	out, err := render(t, "<a title=", data{"a\ufdd0b\ufffec"}, ">")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\ufdd0") || strings.Contains(out, "\ufffe") {
		t.Fatalf("non-character rune not encoded in unquoted attr: %q", out)
	}
	if !strings.Contains(out, "&#x") {
		t.Fatalf("expected hex entity encoding in %q", out)
	}
}

func TestPoisonedWriterStops(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	// Force an unsafe URL error.
	_, _ = w.WriteString(`<a href="`)
	err1 := w.WriteText("javascript:alert(1)")
	if !errors.Is(err1, ErrUnsafeURL) {
		t.Fatalf("first error = %v", err1)
	}
	if _, err := w.WriteString("x"); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("writer must stay poisoned, got %v", err)
	}
	if err := w.Close(); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("close must return poisoning error, got %v", err)
	}
}

func TestSelfClosingAndVoidTags(t *testing.T) {
	cases := []string{
		`<br/>`,
		`<br />`,
		`<img src="x.png" alt="a"/>`,
		`<input checked>`,
	}
	for _, tmpl := range cases {
		got, err := renderInChunks(tmpl, 1)
		if err != nil {
			t.Fatalf("%q: %v", tmpl, err)
		}
		if got != tmpl {
			t.Fatalf("%q:\n got %q\nwant %q", tmpl, got, tmpl)
		}
	}
}
