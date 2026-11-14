package jsonp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"example.org/netsec/q0303/jsonp"
)

func TestQ0303FacadeUsesSecureHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/jsonp?callback=cb", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()

	h := jsonp.NewHandler(jsonp.Options{Data: true})
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "cb(true);" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != jsonp.JSONPContentType {
		t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
	}
}
