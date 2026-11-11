package ctxescape

import (
	"bytes"
	"testing"
)

// fullTemplate exercises every modeled context in one document.
const fullTemplate = `<html><head><title>Hi</title>` +
	`<style>body{}</style></head><body>` +
	`<!-- c --><div class="a" id=b data-x-url='/x' onclick="f()">` +
	`<a href="/go">l</a><textarea>t</textarea>` +
	"<script>var a = \"s\", b = 'q', c = `t`;" +
	`/* x */ // y
` +
	`var r = /re/g;` +
	`</script><![CDATA[d]]></div></body></html>`

func renderInChunks(template string, size int) (string, error) {
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

func TestSplitChunkEquivalence(t *testing.T) {
	for _, chunk := range []int{1, 2, 3, 5, 7, 64, 1024} {
		got, err := renderInChunks(fullTemplate, chunk)
		if err != nil {
			t.Fatalf("chunk size %d: %v", chunk, err)
		}
		if got != fullTemplate {
			t.Fatalf("chunk size %d changed output:\n got %q\nwant %q",
				chunk, got, fullTemplate)
		}
	}
}

func TestSplitInterpolationEquivalence(t *testing.T) {
	prefix := `<p title="`
	suffix := `">`
	payload := `" onmouseover=alert(1) & <b>`

	ref, err := func() (string, error) {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		if _, err := w.WriteString(prefix); err != nil {
			return "", err
		}
		if err := w.WriteText(payload); err != nil {
			return "", err
		}
		if _, err := w.WriteString(suffix); err != nil {
			return "", err
		}
		return buf.String(), w.Close()
	}()
	if err != nil {
		t.Fatal(err)
	}

	for cut := 1; cut <= len(prefix); cut++ {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		if _, err := w.Write([]byte(prefix[:cut])); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if _, err := w.Write([]byte(prefix[cut:])); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if err := w.WriteText(payload); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if _, err := w.WriteString(suffix); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if got := buf.String(); got != ref {
			t.Fatalf("cut %d changed escaped output:\n got %q\nwant %q", cut, got, ref)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("cut %d close: %v", cut, err)
		}
	}
}

func TestSplitScriptStringInterpolation(t *testing.T) {
	prefix := `<script>var x = "`
	suffix := `";var y = 1;</script>`
	payload := `</script><img src=x onerror=alert(1)>`

	var ref bytes.Buffer
	{
		w := NewWriter(&ref)
		_, _ = w.WriteString(prefix)
		if err := w.WriteText(payload); err != nil {
			t.Fatal(err)
		}
		_, _ = w.WriteString(suffix)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}

	// Split the trusted prefix at every byte boundary. The interpolation is
	// always issued after the complete prefix has been reconstructed, so the
	// escaping context is the JS string in every iteration.
	for cut := 1; cut <= len(prefix); cut++ {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		if _, err := w.Write([]byte(prefix[:cut])); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if _, err := w.Write([]byte(prefix[cut:])); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if err := w.WriteText(payload); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if _, err := w.WriteString(suffix); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("cut %d close: %v", cut, err)
		}
		if buf.String() != ref.String() {
			t.Fatalf("cut %d changed output:\n got %q\nwant %q",
				cut, buf.String(), ref.String())
		}
	}
}

func FuzzWriterChunking(f *testing.F) {
	f.Add(fullTemplate)
	f.Add(`<a href="x" title='y' onclick="z()">`)
	f.Add(`<script>var s = "abc";</script>`)
	f.Add(`<!-- hi -->plain<![CDATA[ c ]]>`)
	f.Fuzz(func(t *testing.T, template string) {
		// Only fuzz templates that parse cleanly as a whole.
		var whole bytes.Buffer
		ref := NewWriter(&whole)
		if _, err := ref.WriteString(template); err != nil {
			return
		}
		if err := ref.Close(); err != nil {
			return
		}
		expected := whole.String()
		if expected != template {
			t.Fatalf("reference output differs from input:\n got %q\n in  %q",
				expected, template)
		}

		for size := 1; size <= len(template)+1; size++ {
			got, err := renderInChunks(template, size)
			if err != nil {
				t.Fatalf("size %d: unexpected error %v for %q", size, err, template)
			}
			if got != template {
				t.Fatalf("size %d:\n got %q\nwant %q", size, got, template)
			}
		}
	})
}

// FuzzEscapingStable verifies that splitting the trusted template around an
// interpolation point never changes the escaped output.
func FuzzEscapingStable(f *testing.F) {
	prefixes := []string{
		"<p>",
		`<a title="`,
		`<a title=`,
		`<a href="`,
		`<script>var x = "`,
		"<script>var x = `",
		`<textarea>`,
	}
	for _, p := range prefixes {
		f.Add(p, "payload\"><script>")
	}
	f.Fuzz(func(t *testing.T, prefix, payload string) {
		// Only run when the whole prefix parses cleanly to a modeled context.
		var ref bytes.Buffer
		rw := NewWriter(&ref)
		if _, err := rw.WriteString(prefix); err != nil {
			return
		}
		if err := rw.WriteText(payload); err != nil {
			return // context may legitimately reject the interpolation
		}
		expected := ref.String()

		for cut := 1; cut <= len(prefix); cut++ {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			if _, err := w.Write([]byte(prefix[:cut])); err != nil {
				t.Fatalf("cut %d: %v", cut, err)
			}
			if _, err := w.Write([]byte(prefix[cut:])); err != nil {
				t.Fatalf("cut %d: %v", cut, err)
			}
			if err := w.WriteText(payload); err != nil {
				t.Fatalf("cut %d: %v", cut, err)
			}
			if buf.String() != expected {
				t.Fatalf("cut %d:\n got %q\nwant %q", cut, buf.String(), expected)
			}
		}
	})
}
