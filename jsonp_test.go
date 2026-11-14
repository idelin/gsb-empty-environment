package netsec_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.org/netsec/q0303"
)

const (
	q0303SameOrigin = "http://example.com"
	q0303Trusted    = "https://trusted.example"
	q0303Evil       = "https://evil.example"
)

func q0303Request(target, query, origin string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if query != "" {
		req.URL.RawQuery = query
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func q0303Run(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func q0303AssertRejected(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%q", rec.Code, wantStatus, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "alert(") || strings.Contains(body, "handle(") {
		t.Fatalf("rejected response reflected a wrapper: %q", body)
	}
}

func TestQ0303ValidSameOriginWrapper(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{Data: map[string]any{"ok": true}})
	rec := q0303Run(t, h, q0303Request("/jsonp?callback=handleIt", "", q0303SameOrigin))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != netsec.JSONPContentType {
		t.Fatalf("Content-Type = %q, want %q", got, netsec.JSONPContentType)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got, want := rec.Body.String(), `handleIt({"ok":true});`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestQ0303ValidWhitelistedOrigin(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{
		AllowedOrigins: []string{q0303Trusted},
		Data:           map[string]any{"ok": true},
	})
	rec := q0303Run(t, h, q0303Request("/jsonp?callback=cb", "", q0303Trusted))
	if rec.Code != http.StatusOK || rec.Body.String() != `cb({"ok":true});` {
		t.Fatalf("status = %d, body=%q", rec.Code, rec.Body.String())
	}
}

func TestQ0303HostOnlyWhitelist(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{
		AllowedOrigins: []string{"trusted.example"},
		Data:           true,
	})
	rec := q0303Run(t, h, q0303Request("/jsonp?callback=cb", "", q0303Trusted))
	if rec.Code != http.StatusOK || rec.Body.String() != `cb(true);` {
		t.Fatalf("status = %d, body=%q", rec.Code, rec.Body.String())
	}
}

func TestQ0303CallbackInjectionRejected(t *testing.T) {
	cases := []string{
		"alert(1)",
		"cb);alert(1);//",
		"cb%3Balert(1)",
		"cb%2529%3Balert(1)",
		"cb%2Ealert",
		"cb%5B0%5D",
		"cb%60",
		"cb%00",
		"cb%20",
		"cb+",
		"1cb",
		"a-b",
		"constructor%2Econstructor",
		"%5Cu0063b",
		"%255Cu0063b",
		"%E2%80%A8cb",
		"%EF%BC%83%EF%BC%A2",
		"%25",
		"%ZZ",
	}

	h := netsec.NewHandler(netsec.Options{Data: map[string]any{"ok": true}})
	for _, callback := range cases {
		t.Run(callback, func(t *testing.T) {
			rec := q0303Run(t, h, q0303Request("/jsonp", "callback="+callback, q0303SameOrigin))
			q0303AssertRejected(t, rec, http.StatusBadRequest)
		})
	}
}

func TestQ0303OverlongAndMaximumCallback(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{Data: true})
	tooLong := strings.Repeat("a", netsec.DefaultMaxCallbackLength+1)
	q0303AssertRejected(t, q0303Run(t, h, q0303Request("/jsonp", "callback="+tooLong, q0303SameOrigin)),
		http.StatusBadRequest)

	atLimit := strings.Repeat("a", netsec.DefaultMaxCallbackLength)
	rec := q0303Run(t, h, q0303Request("/jsonp", "callback="+atLimit, q0303SameOrigin))
	if rec.Code != http.StatusOK {
		t.Fatalf("at-limit callback status = %d, body=%q", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), atLimit+"(true);"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestQ0303EncodedLegalCallbackIsCanonicalized(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{Data: true})
	rec := q0303Run(t, h, q0303Request("/jsonp", "callback=%2563b", q0303SameOrigin))
	if rec.Code != http.StatusOK || rec.Body.String() != `cb(true);` {
		t.Fatalf("status = %d, body=%q", rec.Code, rec.Body.String())
	}
}

func TestQ0303DuplicateCallbackRejected(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{Data: true})
	rec := q0303Run(t, h, q0303Request("/jsonp", "callback=cb&callback=alert", q0303SameOrigin))
	q0303AssertRejected(t, rec, http.StatusBadRequest)

	rec = q0303Run(t, h, q0303Request("/jsonp", "callback=cb&%63allback=alert", q0303SameOrigin))
	q0303AssertRejected(t, rec, http.StatusBadRequest)
}

func TestQ0303OriginPolicy(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{
		AllowedOrigins: []string{q0303Trusted},
		Data:           true,
	})

	tests := []struct {
		name   string
		origin string
		want   int
	}{
		{name: "same origin", origin: q0303SameOrigin, want: http.StatusOK},
		{name: "whitelisted", origin: q0303Trusted, want: http.StatusOK},
		{name: "evil", origin: q0303Evil, want: http.StatusForbidden},
		{name: "null", origin: "null", want: http.StatusForbidden},
		{name: "wrong scheme", origin: "http://trusted.example", want: http.StatusForbidden},
		{name: "suffix confusion", origin: "https://trusted.example.evil", want: http.StatusForbidden},
		{name: "missing", origin: "", want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := q0303Run(t, h, q0303Request("/jsonp?callback=cb", "", tt.origin))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%q", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestQ0303RefererFallback(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{
		AllowedOrigins: []string{q0303Trusted},
		Data:           true,
	})

	allowed := q0303Request("/jsonp?callback=cb", "", "")
	allowed.Header.Set("Referer", q0303Trusted+"/page")
	if rec := q0303Run(t, h, allowed); rec.Code != http.StatusOK {
		t.Fatalf("allowed referer status = %d", rec.Code)
	}

	denied := q0303Request("/jsonp?callback=cb", "", "")
	denied.Header.Set("Referer", q0303Evil+"/page")
	q0303AssertRejected(t, q0303Run(t, h, denied), http.StatusForbidden)
}

func TestQ0303SensitiveEndpointDisablesJSONP(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{Sensitive: true, Data: true})
	q0303AssertRejected(t, q0303Run(t, h,
		q0303Request("/jsonp?callback=cb", "", q0303SameOrigin)),
		http.StatusForbidden)

	byPath := netsec.NewHandler(netsec.Options{
		SensitivePaths: []string{"/private"},
		Data:           true,
	})
	q0303AssertRejected(t, q0303Run(t, byPath,
		q0303Request("/private/account?callback=cb", "", q0303SameOrigin)),
		http.StatusForbidden)
}

func TestQ0303CredentialsRejectedAndNotEmitted(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{Data: true})

	withCookie := q0303Request("/jsonp?callback=cb", "", q0303SameOrigin)
	withCookie.Header.Set("Cookie", "session=secret")
	q0303AssertRejected(t, q0303Run(t, h, withCookie), http.StatusForbidden)

	withAuth := q0303Request("/jsonp?callback=cb", "", q0303SameOrigin)
	withAuth.Header.Set("Authorization", "Bearer secret")
	q0303AssertRejected(t, q0303Run(t, h, withAuth), http.StatusForbidden)

	rec := httptest.NewRecorder()
	rec.Header().Set("Set-Cookie", "session=secret")
	h.ServeHTTP(rec, q0303Request("/jsonp?callback=cb", "", q0303SameOrigin))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("response emitted Set-Cookie: %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("response allowed credentials: %q", got)
	}
}

func TestQ0303PayloadCannotCloseScriptOrStatement(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{
		Data: `</script><script>alert(1)</script>` + "\u2028" + `);alert(2);//`,
	})
	rec := q0303Run(t, h, q0303Request("/jsonp?callback=cb", "", q0303SameOrigin))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, bad := range []string{"</script", "<script", "\u2028", "\u2029"} {
		if strings.Contains(body, bad) {
			t.Fatalf("body contains unsafe %q: %q", bad, body)
		}
	}
	if !strings.HasPrefix(body, `cb("`) || !strings.HasSuffix(body, `);`) {
		t.Fatalf("body is not a valid wrapper: %q", body)
	}
}

func TestQ0303MethodAndMissingCallback(t *testing.T) {
	h := netsec.NewHandler(netsec.Options{Data: true})

	post := httptest.NewRequest(http.MethodPost, "/jsonp?callback=cb", nil)
	post.Header.Set("Origin", q0303SameOrigin)
	rec := q0303Run(t, h, post)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", rec.Code)
	}

	q0303AssertRejected(t, q0303Run(t, h, q0303Request("/jsonp", "", q0303SameOrigin)),
		http.StatusBadRequest)
}

func TestQ0303TopLevelWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	err := netsec.WriteJSONP(rec,
		q0303Request("/jsonp?callback=cb", "", q0303SameOrigin),
		map[string]any{"ok": true},
		netsec.Options{AllowedOrigins: []string{q0303Trusted}},
	)
	if err != nil {
		t.Fatalf("WriteJSONP: %v", err)
	}
	if rec.Body.String() != `cb({"ok":true});` {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestQ0303PureWrapperValidation(t *testing.T) {
	body, err := netsec.MarshalJSONP("cb", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("MarshalJSONP: %v", err)
	}
	if string(body) != `cb({"ok":true});` {
		t.Fatalf("body = %q", body)
	}

	if _, err := netsec.MarshalJSONP("cb);alert(1);//", nil); err == nil {
		t.Fatal("MarshalJSONP accepted a closing-delimiter injection")
	}
	if _, err := netsec.MarshalJSONP("%5Cu0063b", nil); err == nil {
		t.Fatal("MarshalJSONP accepted a Unicode escape")
	}

	if _, err := netsec.NormalizeCallback("%2563b"); err != nil {
		t.Fatalf("double-encoded legal callback: %v", err)
	}
	if _, err := netsec.NormalizeCallback("cb%2529"); err == nil {
		t.Fatal("double-encoded closing delimiter was accepted")
	}
}
