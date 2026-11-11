package ctxescape

import (
	"bytes"
	"strings"
	"testing"
)

// renderBytesInChunks feeds template bytes in fixed-size chunks and returns
// the assembled output.
func renderBytesInChunks(t *testing.T, template string, size int) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	for i := 0; i < len(template); i += size {
		end := i + size
		if end > len(template) {
			end = len(template)
		}
		if _, err := w.Write([]byte(template[i:end])); err != nil {
			return buf.String(), err
		}
	}
	return buf.String(), w.Close()
}

func TestRawTextEndTagAcrossChunks(t *testing.T) {
	cases := []string{
		"<textarea>hello</textarea>",
		"<textarea>hello</textarea >",
		"<textarea>hello</TEXTAREA>",
		"<title>hi</title >",
		"<style>a{}</style >",
	}
	for _, tmpl := range cases {
		for size := 1; size <= len(tmpl); size++ {
			got, err := renderBytesInChunks(t, tmpl, size)
			if err != nil {
				t.Fatalf("%q size %d: %v", tmpl, size, err)
			}
			if got != tmpl {
				t.Fatalf("%q size %d:\n got %q\nwant %q", tmpl, size, got, tmpl)
			}
		}
	}
}

func TestCommentAndCDATAEndAcrossChunks(t *testing.T) {
	cases := []string{
		"<!-- a-b --><p>x</p>",
		"<a><![CDATA[ a ]]b]]></a>",
		"<a><![CDATA[x]]></a>",
		"<!---->",
	}
	for _, tmpl := range cases {
		for size := 1; size <= len(tmpl); size++ {
			got, err := renderBytesInChunks(t, tmpl, size)
			if err != nil {
				t.Fatalf("%q size %d: %v", tmpl, size, err)
			}
			if got != tmpl {
				t.Fatalf("%q size %d:\n got %q\nwant %q", tmpl, size, got, tmpl)
			}
		}
	}
}

func TestMultibyteRuneAcrossChunks(t *testing.T) {
	tmpl := "<p>héllo 世界 &#128512;</p>"
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

func TestInvalidUTF8Escaped(t *testing.T) {
	out, err := render(t, "<p>", data{"a\xffb\x00c"}, "</p>")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\xff") || strings.Contains(out, "\x00") {
		t.Fatalf("raw invalid bytes survived: %q", out)
	}
	if !strings.Contains(out, "\uFFFD") {
		t.Fatalf("expected replacement characters in %q", out)
	}
}

func TestDoctypeAcrossChunks(t *testing.T) {
	tmpl := `<!DOCTYPE html><p>ok</p>`
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

func TestDataURLEncodedBypass(t *testing.T) {
	attacks := []string{
		"javascript:alert(1)",
		"  javascript:alert(1)",
		"\tj\tavascript:alert(1)",
		"JAVASCRIPT:alert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"vbscript:msgbox(1)",
		"java\nscript:alert(1)",
	}
	for _, u := range attacks {
		if _, err := render(t, `<a href="`, data{u}, `">`); err == nil {
			t.Fatalf("attack %q was not rejected", u)
		}
	}
}
