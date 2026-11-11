package csp

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustPolicy(t *testing.T, b *Builder) *Policy {
	t.Helper()
	p, err := b.NewPolicy()
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	return p
}

// TestCrossRequestIsolation proves a nonce or approved script from one
// request can never authorize a script emitted by another request.
func TestCrossRequestIsolation(t *testing.T) {
	b := NewBuilder("default-src 'none'")

	p1, err := b.NewPolicy()
	if err != nil {
		t.Fatalf("first policy: %v", err)
	}
	s1, err := p1.Root().(*rootNode).DeriveScript("window.a = 1")
	if err != nil {
		t.Fatalf("derive on request 1: %v", err)
	}

	p2, err := b.NewPolicy()
	if err != nil {
		t.Fatalf("second policy: %v", err)
	}

	if p1.Nonce() == p2.Nonce() {
		t.Fatalf("nonces leaked across requests: %q", p1.Nonce())
	}
	if p2.isApproved(p1.Nonce()) {
		t.Fatal("request 2 accepted request 1 nonce")
	}
	if p2.isApproved(hashScript("window.a = 1")) {
		t.Fatal("request 2 accepted request 1 script hash")
	}

	// A script element carrying request 1's nonce must be blocked for request 2.
	var out bytes.Buffer
	sw := NewSanitizingWriter(&out, p2)
	foreign := []byte(`<script nonce="` + p1.Nonce() + `">window.a = 1</script>`)
	if _, err := sw.Write(foreign); err == nil {
		err = sw.Close()
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized for foreign nonce, got %v", err)
		}
	} else if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}

	// The same element is valid inside request 1: enforcement is per-policy.
	out.Reset()
	sw1 := NewSanitizingWriter(&out, p1)
	if _, err := sw1.Write([]byte(s1.HTML())); err != nil {
		t.Fatalf("own-policy script rejected: %v", err)
	}
	if err := sw1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !strings.Contains(out.String(), p1.Nonce()) {
		t.Fatalf("approved script missing from output: %q", out.String())
	}

	// Many concurrent requests must keep fully independent state.
	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := b.NewPolicy()
			if err != nil {
				t.Errorf("policy: %v", err)
				return
			}
			defer p.Release()
			root := p.Root().(*rootNode)
			if _, err := root.DeriveScript("var x = 1"); err != nil {
				t.Errorf("derive: %v", err)
			}
			var buf bytes.Buffer
			w := NewSanitizingWriter(&buf, p)
			if _, err := w.Write([]byte(`<p>hi</p>`)); err != nil {
				t.Errorf("write: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestPolicyPoolResetsState verifies a recycled policy object carries no
// residue (nonce, directives, derived set) into its next request.
func TestPolicyPoolResetsState(t *testing.T) {
	b := NewBuilder("default-src 'none'")
	p, err := b.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	oldNonce := p.Nonce()
	if err := p.AddDirective("img-src 'self'"); err != nil {
		t.Fatal(err)
	}
	root := p.Root().(*rootNode)
	if _, err := root.DeriveScript("var y = 2"); err != nil {
		t.Fatal(err)
	}
	if err := p.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	next, err := b.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	defer next.Release()
	if next.Nonce() == oldNonce {
		t.Fatal("recycled policy reused the old nonce")
	}
	if next.isApproved(oldNonce) || next.isApproved(hashScript("var y = 2")) {
		t.Fatal("recycled policy retained approved tokens")
	}
	if strings.Contains(next.HeaderValue(), "img-src") {
		t.Fatalf("recycled policy retained request directive: %q", next.HeaderValue())
	}

	stats := b.Stats()
	if stats.Alive != 1 || stats.Released != 1 || stats.Created != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

// TestFreezeAfterHeaders checks the freeze contract: directive edits are
// refused while trusted derivation remains available for streaming.
func TestFreezeAfterHeaders(t *testing.T) {
	b := NewBuilder("default-src 'none'")
	p, err := b.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release()

	if err := p.Freeze(); err != nil {
		t.Fatal(err)
	}
	if err := p.AddDirective("object-src 'none'"); !errors.Is(err, ErrPolicyFrozen) {
		t.Fatalf("expected ErrPolicyFrozen, got %v", err)
	}

	// Strict-dynamic style propagation still works while streaming the body.
	root := p.Root().(*rootNode)
	first, err := root.DeriveScript("var chain = 1")
	if err != nil {
		t.Fatalf("derivation after freeze: %v", err)
	}
	second, err := first.DeriveScript("var child = 2")
	if err != nil {
		t.Fatalf("child derivation: %v", err)
	}
	if !p.isNodeTrusted(second.NodeID()) {
		t.Fatal("derived child node was not registered as trusted")
	}

	header := p.HeaderValue()
	if !strings.Contains(header, "nonce-"+p.Nonce()) ||
		!strings.Contains(header, "strict-dynamic") {
		t.Fatalf("header missing nonce/strict-dynamic: %q", header)
	}

	// A released policy refuses every further operation.
	if err := p.Release(); err != nil {
		t.Fatal(err)
	}
	if err := p.Freeze(); !errors.Is(err, ErrPolicyReleased) {
		t.Fatalf("expected ErrPolicyReleased, got %v", err)
	}
	if _, err := root.DeriveScript("x"); !errors.Is(err, ErrPolicyReleased) {
		t.Fatalf("derive on released policy: %v", err)
	}
}

// TestOnlyTrustedParentDerives confirms nodes outside the trusted tree can
// never mint an eligible script.
func TestOnlyTrustedParentDerives(t *testing.T) {
	b := NewBuilder()
	p, err := b.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release()

	if _, err := deriveScript(p, untrustedNode{}, "evil()"); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("expected ErrUntrusted, got %v", err)
	}
	if _, err := deriveScript(p, nil, "evil()"); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("nil parent: %v", err)
	}

	// A derived script cannot smuggle a raw closing tag into the element.
	if _, err := p.Root().(*rootNode).DeriveScript(
		`</script><img src=x>`); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for closing-tag injection")
	}
}

// TestSanitizerEnforcement drives the streaming writer, including tags split
// across writes, raw inline scripts, wrong nonces, and external scripts.
func TestSanitizerEnforcement(t *testing.T) {
	b := NewBuilder()
	p, err := b.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release()
	root := p.Root().(*rootNode)
	script, err := root.DeriveScript("console.log('ok')")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("approved script across split writes", func(t *testing.T) {
		var out bytes.Buffer
		w := NewSanitizingWriter(&out, p)
		markup := string(script.HTML())
		mid := len(markup) / 3
		if _, err := w.Write([]byte(markup[:mid])); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(markup[mid:])); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		if out.String() != markup {
			t.Fatalf("output mismatch:\nwant %q\n got %q", markup, out.String())
		}
	})

	t.Run("raw inline script blocked", func(t *testing.T) {
		var out bytes.Buffer
		w := NewSanitizingWriter(&out, p)
		_, err := w.Write([]byte(`<script>stealCookies()</script>`))
		if err == nil {
			err = w.Close()
		}
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got %v", err)
		}
		if strings.Contains(out.String(), "stealCookies") {
			t.Fatalf("unapproved body leaked: %q", out.String())
		}
	})

	t.Run("wrong nonce blocked", func(t *testing.T) {
		var out bytes.Buffer
		w := NewSanitizingWriter(&out, p)
		_, err := w.Write([]byte(`<script nonce="someone-else">x()</script>`))
		if err == nil {
			err = w.Close()
		}
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got %v", err)
		}
	})

	t.Run("external script requires approved nonce", func(t *testing.T) {
		var out bytes.Buffer
		w := NewSanitizingWriter(&out, p)
		good := `<script nonce="` + p.Nonce() + `" src="/app.js"></script>`
		if _, err := w.Write([]byte(good)); err != nil {
			t.Fatalf("approved external script: %v", err)
		}

		bad := `<script nonce="stale" src="/evil.js"></script>`
		if _, err := w.Write([]byte(bad)); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("external script with bad nonce: %v", err)
		}
	})

	t.Run("unterminated script blocked", func(t *testing.T) {
		var out bytes.Buffer
		w := NewSanitizingWriter(&out, p)
		if _, err := w.Write([]byte(
			`<script nonce="` + p.Nonce() + `">never closed`)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got %v", err)
		}
	})

	t.Run("plain markup passes unchanged", func(t *testing.T) {
		var out bytes.Buffer
		w := NewSanitizingWriter(&out, p)
		page := `<div><span>a < b && b > c</span></div>`
		if _, err := w.Write([]byte(page)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if out.String() != page {
			t.Fatalf("markup altered: %q", out.String())
		}
	})

	t.Run("close-tag lookalike stays in body", func(t *testing.T) {
		var out bytes.Buffer
		w := NewSanitizingWriter(&out, p)
		// No nonce: authorization must come from the body hash instead.
		body := "a</scripz b"
		if err := p.approve(hashScript(body)); err != nil {
			t.Fatal(err)
		}
		markup := `<script>` + body + `</script>`
		if _, err := w.Write([]byte(markup)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if out.String() != markup {
			t.Fatalf("lookalike altered output: %q", out.String())
		}
	})

	t.Run("hash authorized script without attributes", func(t *testing.T) {
		var out bytes.Buffer
		w := NewSanitizingWriter(&out, p)
		body := "plain()"
		if err := p.approve(hashScript(body)); err != nil {
			t.Fatal(err)
		}
		markup := `<script>` + body + `</script>`
		if _, err := w.Write([]byte(markup)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if out.String() != markup {
			t.Fatalf("output mismatch: %q", out.String())
		}
	})
}

// blockingWriter cancels the render context after the first flushed chunk.
type blockingWriter struct {
	ctx    context.Context
	cancel context.CancelFunc
	fired  chan struct{}
	once   sync.Once
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	b.once.Do(func() {
		b.cancel()
		close(b.fired)
	})
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return len(p), nil
}

// TestCancelReclaimsState renders a large streaming template, cancels it, and
// verifies the policy is released and no goroutine is retained.
func TestCancelReclaimsState(t *testing.T) {
	tmpl := template.Must(template.New("stream").Funcs(template.FuncMap{
		"cspNonce":      func() string { return "" },
		"trustedScript": func(string) (template.HTML, error) { return "", nil },
		"deriveScript":  func(ScriptDeriver, string) (template.HTML, error) { return "", nil },
	}).Parse(`{{range .}}<p>{{.}}</p>{{trustedScript "payload()"}}{{end}}`))

	b := NewBuilder("default-src 'none'")
	renderer := NewRenderer(b, tmpl)

	items := make([]int, 500)
	for i := range items {
		items[i] = i
	}

	ctx, cancel := context.WithCancel(context.Background())
	bw := &blockingWriter{ctx: ctx, cancel: cancel, fired: make(chan struct{})}

	before := b.Stats()
	err := renderer.Render(WithPolicy(ctx, mustPolicy(t, b)), bw, items)
	if err == nil {
		t.Fatal("expected canceled render to fail")
	}

	after := b.Stats()
	if after.Alive != before.Alive {
		t.Fatalf("policy not reclaimed after cancel: alive before=%d after=%d",
			before.Alive, after.Alive)
	}
	if after.Released-before.Released != 1 {
		t.Fatalf("expected one release, got %d", after.Released-before.Released)
	}

	// The pool must hand out a clean policy afterward (no canceled state).
	next, nerr := b.NewPolicy()
	if nerr != nil {
		t.Fatal(nerr)
	}
	frozen, released := next.status()
	if frozen || released {
		t.Fatalf("recycled policy dirty: frozen=%v released=%v", frozen, released)
	}
	if err := next.Release(); err != nil {
		t.Fatal(err)
	}

	// No render goroutine should survive a canceled request. The renderer
	// itself runs inline, so the count must be stable across GC cycles.
	runtime.GC()
	baseline := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		time.Sleep(10 * time.Millisecond)
		runtime.GC()
		if runtime.NumGoroutine() > baseline {
			t.Fatalf("goroutines retained after cancel: baseline=%d now=%d",
				baseline, runtime.NumGoroutine())
		}
	}
}

// TestHTTPEndToEnd exercises real requests through the renderer, proving
// per-request headers, trusted emission, and rejection of a raw script.
func TestHTTPEndToEnd(t *testing.T) {
	tmpl := template.Must(template.New("page").Funcs(template.FuncMap{
		"cspNonce":      func() string { return "" },
		"trustedScript": func(string) (template.HTML, error) { return "", nil },
		"deriveScript":  func(ScriptDeriver, string) (template.HTML, error) { return "", nil },
		"rawHTML":       func(s string) template.HTML { return template.HTML(s) },
	}).Parse(
		`<html><head></head><body>{{trustedScript "boot()"}}` +
			`{{rawHTML "<script>rogue()</script>"}}</body></html>`))

	renderer := NewRenderer(NewBuilder("default-src 'none'"), tmpl)

	// First request: the raw inline script must be blocked even though the
	// trusted script before it already committed the response status.
	rec := httptest.NewRecorder()
	renderer.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	header := rec.Header().Get("Content-Security-Policy")
	if header == "" || !strings.Contains(header, "nonce-") {
		t.Fatalf("missing per-request CSP header: %q", header)
	}
	if strings.Contains(rec.Body.String(), "rogue()") {
		t.Fatalf("unauthorized script reached the body: %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "boot()") {
		t.Fatalf("trusted prefix missing before abort: %q", rec.Body.String())
	}

	// Second request must receive a distinct nonce in its header.
	rec2 := httptest.NewRecorder()
	renderer.ServeHTTP(rec2, httptest.NewRequest("GET", "/", nil))
	if header == rec2.Header().Get("Content-Security-Policy") {
		t.Fatalf("CSP headers reused across requests: %q", header)
	}

	// When the violation happens before any body byte, a 500 is observable.
	early := template.Must(template.New("early").Funcs(template.FuncMap{
		"rawHTML": func(s string) template.HTML { return template.HTML(s) },
	}).Parse(`{{rawHTML "<script>rogue()</script>"}}`))
	earlyRenderer := NewRenderer(NewBuilder(), early)
	earlyRec := httptest.NewRecorder()
	earlyRenderer.ServeHTTP(earlyRec, httptest.NewRequest("GET", "/", nil))
	if earlyRec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 before commit, got %d", earlyRec.Code)
	}
	if strings.Contains(earlyRec.Body.String(), "rogue()") {
		t.Fatalf("unauthorized body leaked: %q", earlyRec.Body.String())
	}

	// A template emitting only trusted scripts succeeds and renders the nonce.
	clean := template.Must(template.New("clean").Funcs(template.FuncMap{
		"cspNonce":      func() string { return "" },
		"trustedScript": func(string) (template.HTML, error) { return "", nil },
		"deriveScript":  func(ScriptDeriver, string) (template.HTML, error) { return "", nil },
	}).Parse(`<html><body>{{trustedScript "boot()"}}</body></html>`))
	okRenderer := NewRenderer(NewBuilder(), clean)

	ok := httptest.NewRecorder()
	okRenderer.ServeHTTP(ok, httptest.NewRequest("GET", "/", nil))
	if ok.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", ok.Code, ok.Body.String())
	}
	nonce := strings.TrimSuffix(
		strings.TrimPrefix(ok.Header().Get("Content-Security-Policy"),
			"script-src 'nonce-"),
		"' 'strict-dynamic'")
	if !strings.Contains(ok.Body.String(), `nonce="`+nonce+`"`) {
		t.Fatalf("trusted script missing request nonce: %q", ok.Body.String())
	}
	if !strings.Contains(ok.Body.String(), "boot()") {
		t.Fatalf("trusted script body missing: %q", ok.Body.String())
	}
}
