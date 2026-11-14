package netsec

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	return NewHandler(newTestResolver(t))
}

func doRequest(t *testing.T, h http.Handler, target string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerServesLegitFile(t *testing.T) {
	h := newTestHandler(t)
	rec := doRequest(t, h, "http://gw/static/public/hello.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "hello world" {
		t.Fatalf("body = %q, want %q", body, "hello world")
	}
}

func TestHandlerTraversalAndMissingAreIdentical(t *testing.T) {
	h := newTestHandler(t)
	targets := []string{
		"http://gw/static/%2e%2e/outside/secret.conf", // encoded traversal
		"http://gw/static/%2e%2e%2f%2e%2e%2foutside/secret.conf",
		"http://gw/static//etc/passwd",        // absolute-path trick
		"http://gw/static/public/escape.conf", // symlink out of root
		"http://gw/static/public/direscape/secret.conf",
		"http://gw/static/no-such-file.txt",    // plain missing file
		"http://gw/staticfoo/public/hello.txt", // wrong segment prefix
	}
	var wantCode int
	var wantBody string
	for i, target := range targets {
		rec := doRequest(t, h, target)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", target, rec.Code)
		}
		if i == 0 {
			wantCode, wantBody = rec.Code, rec.Body.String()
			continue
		}
		if rec.Code != wantCode || rec.Body.String() != wantBody {
			t.Errorf("%s: response differs from other 404s: %d %q vs %d %q",
				target, rec.Code, rec.Body.String(), wantCode, wantBody)
		}
		if strings.Contains(rec.Body.String(), "top secret") {
			t.Errorf("%s: leaked out-of-root content", target)
		}
	}
}

func TestHandlerRejectsMalformed(t *testing.T) {
	h := newTestHandler(t)
	// Note: net/http itself rejects unparseable escapes like "%zz" before
	// they reach a handler; those cases are covered at the Resolver level.
	for _, target := range []string{
		"http://gw/static/%00",
		"http://gw/static/%5c..%5c..%5csecret.conf",
	} {
		rec := doRequest(t, h, target)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", target, rec.Code)
		}
	}
}

func TestHandlerRangeRequest(t *testing.T) {
	h := newTestHandler(t)
	rec := doRequest(t, h, "http://gw/static/public/hello.txt", "Range", "bytes=0-4")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if body := rec.Body.String(); body != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 0-4/11" {
		t.Fatalf("Content-Range = %q, want %q", cr, "bytes 0-4/11")
	}
}

func TestHandlerConditionalRequest(t *testing.T) {
	h := newTestHandler(t)
	future := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	rec := doRequest(t, h, "http://gw/static/public/hello.txt", "If-Modified-Since", future)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("304 body not empty: %q", rec.Body.String())
	}
}

func TestHandlerDirectoryListing(t *testing.T) {
	h := newTestHandler(t)
	rec := doRequest(t, h, "http://gw/static/public/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"hello.txt", "sub/"} {
		if !strings.Contains(body, want) {
			t.Errorf("listing missing %q", want)
		}
	}
	// Listing of a directory outside the root via symlink is refused.
	rec = doRequest(t, h, "http://gw/static/public/direscape/")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("direscape listing: status = %d, want 404", rec.Code)
	}
}

func TestHandlerNeverLeaksOutsideContent(t *testing.T) {
	h := newTestHandler(t)
	// Belt and braces: no attack variant may return the outside secret.
	targets := []string{
		"http://gw/static/%2e%2e/outside/secret.conf",
		"http://gw/static/%2e%2e%2foutside/secret.conf",
		"http://gw/static/%252e%252e/outside/secret.conf",
		"http://gw/static//../outside/secret.conf",
		"http://gw/static/public/../../outside/secret.conf",
		"http://gw/static/public/escape.conf",
		"http://gw/static/public/direscape/secret.conf",
	}
	for _, target := range targets {
		rec := doRequest(t, h, target)
		body, _ := io.ReadAll(rec.Result().Body)
		if strings.Contains(string(body), "top secret") {
			t.Errorf("%s: leaked out-of-root content (status %d)", target, rec.Code)
		}
	}
}
