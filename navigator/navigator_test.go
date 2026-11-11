package navigator_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"example.org/offlinecli/navigator"
)

// virtualTransport is an in-process RoundTripper that dispatches requests to
// named virtual sites without binding a socket (the sandbox forbids
// listen()). Handlers run through the real net/http server machinery via
// httptest.ResponseRecorder, so Set-Cookie, Location rewriting and the
// Cookie header are all exercised exactly as on the wire.
type virtualTransport struct {
	hosts map[string]http.Handler
	mu    sync.Mutex
	seen  []string
}

func (v *virtualTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	v.mu.Lock()
	v.seen = append(v.seen, req.Method+" "+req.Host+req.URL.Path+
		" | Cookie: "+req.Header.Get("Cookie"))
	v.mu.Unlock()

	handler, ok := v.hosts[req.URL.Hostname()]
	if !ok {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("unknown host")),
			Request:    req,
		}, nil
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

func (v *virtualTransport) snapshot() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.seen...)
}

func readBody(req *http.Request) []byte {
	if req.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(req.Body)
	return body
}

const (
	bankOrigin   = "https://bank.test"
	widgetOrigin = "https://widget.test"
)

// testWorld models two cross-site HTTPS origins behind one virtual
// transport. Site identity ignores ports, so distinct hosts suffice.
type testWorld struct {
	t         *testing.T
	transport *virtualTransport
}

func newTestWorld(t *testing.T) *testWorld {
	t.Helper()
	w := &testWorld{t: t, transport: &virtualTransport{hosts: map[string]http.Handler{}}}

	muxBank := http.NewServeMux()
	muxBank.HandleFunc("/set-login", func(rw http.ResponseWriter, req *http.Request) {
		http.SetCookie(rw, &http.Cookie{Name: "session", Value: "BANK-LOGIN",
			SameSite: http.SameSiteLaxMode, Secure: true, Path: "/"})
		http.SetCookie(rw, &http.Cookie{Name: "tracking", Value: "NONE-COOKIE",
			SameSite: http.SameSiteNoneMode, Secure: true, Path: "/"})
		rw.WriteHeader(http.StatusNoContent)
	})
	muxBank.HandleFunc("/pay", func(rw http.ResponseWriter, req *http.Request) {
		switch req.URL.Query().Get("via") {
		case "307":
			http.Redirect(rw, req, bankOrigin+"/confirm", http.StatusTemporaryRedirect)
		case "303":
			http.Redirect(rw, req, bankOrigin+"/done", http.StatusSeeOther)
		case "back":
			http.Redirect(rw, req, widgetOrigin+"/landed", http.StatusSeeOther)
		default:
			body := readBody(req)
			rw.Header().Set("Content-Type", "text/plain")
			_, _ = rw.Write([]byte(req.Method + " body=" + string(body)))
		}
	})
	muxBank.HandleFunc("/confirm", func(rw http.ResponseWriter, req *http.Request) {
		body := readBody(req)
		_, _ = rw.Write([]byte(req.Method + " body=" + string(body)))
	})
	muxBank.HandleFunc("/done", func(rw http.ResponseWriter, req *http.Request) {
		_, _ = rw.Write([]byte(req.Method))
	})

	muxWidget := http.NewServeMux()
	muxWidget.HandleFunc("/frame", func(rw http.ResponseWriter, req *http.Request) {
		_, _ = rw.Write([]byte("frame"))
	})
	muxWidget.HandleFunc("/bounce-to-bank", func(rw http.ResponseWriter, req *http.Request) {
		http.Redirect(rw, req, bankOrigin+"/pay", http.StatusTemporaryRedirect)
	})
	muxWidget.HandleFunc("/chain", func(rw http.ResponseWriter, req *http.Request) {
		// widget -> bank -> widget: returning to the first hop must not
		// restore the initiator identity.
		http.Redirect(rw, req, bankOrigin+"/pay?via=back", http.StatusSeeOther)
	})
	muxWidget.HandleFunc("/landed", func(rw http.ResponseWriter, req *http.Request) {
		_, _ = rw.Write([]byte("landed " + req.Method))
	})

	w.transport.hosts["bank.test"] = muxBank
	w.transport.hosts["widget.test"] = muxWidget

	bankURL, _ := url.Parse(bankOrigin)
	widgetURL, _ := url.Parse(widgetOrigin)
	if navigator.SiteOf(bankURL).SameSite(navigator.SiteOf(widgetURL)) {
		t.Fatalf("virtual hosts must be cross-site")
	}
	return w
}

func (w *testWorld) newNavigator() *navigator.Navigator {
	return navigator.New(navigator.NewJar(),
		navigator.WithTransport(w.transport),
		navigator.WithLaxPostWindow(0))
}

func (w *testWorld) seedLogin(t *testing.T, nav *navigator.Navigator) {
	t.Helper()
	bankURL, _ := url.Parse(bankOrigin)
	req, err := http.NewRequest(http.MethodGet, bankOrigin+"/set-login", nil)
	if err != nil {
		t.Fatal(err)
	}
	trace, err := nav.Navigate(context.Background(),
		navigator.NewTopLevelContext(bankURL), req)
	if err != nil || trace.Final == nil || trace.Final.StatusCode != http.StatusNoContent {
		t.Fatalf("login seed failed: err=%v status=%v", err, trace.Final)
	}
	w.transport.seen = nil
}

func TestTopLevel307PreservesPostAndCookies(t *testing.T) {
	w := newTestWorld(t)
	nav := w.newNavigator()
	w.seedLogin(t, nav)

	req, _ := http.NewRequest(http.MethodPost, bankOrigin+"/pay?via=307",
		strings.NewReader("amount=42"))
	bankURL, _ := url.Parse(bankOrigin)
	trace, err := nav.Navigate(context.Background(),
		navigator.NewTopLevelContext(bankURL), req)
	if err != nil {
		t.Fatal(err)
	}

	if len(trace.Hops) != 2 {
		t.Fatalf("hops = %d, want 2", len(trace.Hops))
	}
	for i, hop := range trace.Hops {
		if hop.Method != http.MethodPost {
			t.Errorf("hop %d method = %s, want POST preserved by 307", i, hop.Method)
		}
		if !strings.Contains(hop.CookieHeader, "session=BANK-LOGIN") {
			t.Errorf("hop %d missing login cookie: %q", i, hop.CookieHeader)
		}
	}
	final, _ := io.ReadAll(trace.Final.Body)
	if string(final) != "POST body=amount=42" {
		t.Fatalf("final = %q, want replayed POST body", final)
	}
	seen := w.transport.snapshot()
	if len(seen) != 2 || !strings.Contains(seen[0], "POST") || !strings.Contains(seen[1], "POST") {
		t.Fatalf("server saw %v, want two POSTs", seen)
	}
}

func TestTopLevel303DowngradesPostAndKeepsCookies(t *testing.T) {
	w := newTestWorld(t)
	nav := w.newNavigator()
	w.seedLogin(t, nav)

	req, _ := http.NewRequest(http.MethodPost, bankOrigin+"/pay?via=303",
		strings.NewReader("payload=1"))
	bankURL, _ := url.Parse(bankOrigin)
	trace, err := nav.Navigate(context.Background(),
		navigator.NewTopLevelContext(bankURL), req)
	if err != nil {
		t.Fatal(err)
	}

	if trace.Hops[0].Method != http.MethodPost {
		t.Fatalf("first hop = %s, want POST", trace.Hops[0].Method)
	}
	if trace.Hops[1].Method != http.MethodGet {
		t.Fatalf("second hop = %s, want GET after 303", trace.Hops[1].Method)
	}
	if !strings.Contains(trace.Hops[1].CookieHeader, "session=BANK-LOGIN") {
		t.Fatalf("downgraded GET lost login cookie: %q", trace.Hops[1].CookieHeader)
	}
	final, _ := io.ReadAll(trace.Final.Body)
	if string(final) != "GET" {
		t.Fatalf("final = %q, want GET", final)
	}
	seen := w.transport.snapshot()
	if !strings.Contains(seen[0], "POST") || !strings.Contains(seen[1], "GET") {
		t.Fatalf("server did not observe method downgrade: %v", seen)
	}
}

func TestCrossSiteEmbeddedDoesNotBorrowTopLevelLogin(t *testing.T) {
	w := newTestWorld(t)
	nav := w.newNavigator()
	w.seedLogin(t, nav)

	// Logged-in bank tab in the top level; an unrelated widget embed issues
	// a request that 307-redirects (POST preserved) into the bank.
	req, _ := http.NewRequest(http.MethodPost, widgetOrigin+"/bounce-to-bank",
		strings.NewReader("x=1"))
	widgetURL, _ := url.Parse(widgetOrigin)
	trace, err := nav.Navigate(context.Background(),
		navigator.NewEmbeddedContext(widgetURL), req)
	if err != nil {
		t.Fatal(err)
	}

	if len(trace.Hops) != 2 {
		t.Fatalf("hops = %d, want 2", len(trace.Hops))
	}
	if trace.Hops[1].Method != http.MethodPost {
		t.Fatalf("307 changed method to %s", trace.Hops[1].Method)
	}
	if !trace.Hops[1].CrossSite || trace.Hops[1].TopLevel {
		t.Fatal("bank hop must be a cross-site embedded request")
	}
	if strings.Contains(trace.Hops[1].CookieHeader, "session=BANK-LOGIN") {
		t.Fatalf("embedded cross-site hop borrowed Lax login cookie: %q",
			trace.Hops[1].CookieHeader)
	}
	if !strings.Contains(trace.Hops[1].CookieHeader, "tracking=NONE-COOKIE") {
		t.Fatalf("SameSite=None cookie should still be sent, got: %q",
			trace.Hops[1].CookieHeader)
	}
	for _, line := range w.transport.snapshot() {
		if strings.Contains(line, "/pay") && strings.Contains(line, "BANK-LOGIN") {
			t.Fatalf("bank server received borrowed login state: %s", line)
		}
	}
}

func TestCrossSiteRedirectPoisoningSurvivesReturnHop(t *testing.T) {
	w := newTestWorld(t)
	nav := w.newNavigator()
	w.seedLogin(t, nav)

	req, _ := http.NewRequest(http.MethodGet, widgetOrigin+"/chain", nil)
	widgetURL, _ := url.Parse(widgetOrigin)
	trace, err := nav.Navigate(context.Background(),
		navigator.NewEmbeddedContext(widgetURL), req)
	if err != nil {
		t.Fatal(err)
	}

	if len(trace.Hops) != 3 {
		t.Fatalf("hops = %d, want widget->bank->widget chain", len(trace.Hops))
	}
	// Hop 0 is same-site (the embed's own origin). The bank hop is
	// cross-site, and that poisoning must survive the return to widget.
	if trace.Hops[0].CrossSite {
		t.Error("hop 0 to the embed's own site must be same-site")
	}
	for i := 1; i < len(trace.Hops); i++ {
		hop := trace.Hops[i]
		if !hop.CrossSite {
			t.Errorf("hop %d lost cross-site classification after chain return", i)
		}
	}
	for i, hop := range trace.Hops {
		if strings.Contains(hop.CookieHeader, "BANK-LOGIN") {
			t.Errorf("hop %d borrowed login cookie: %q", i, hop.CookieHeader)
		}
	}
	final, _ := io.ReadAll(trace.Final.Body)
	if string(final) != "landed GET" {
		t.Fatalf("final = %q, want downgraded GET landing", final)
	}
}

func TestCrossSiteTopLevelNavigationLaxOnGetOnly(t *testing.T) {
	w := newTestWorld(t)
	nav := w.newNavigator()
	w.seedLogin(t, nav)
	widgetURL, _ := url.Parse(widgetOrigin)

	getReq, _ := http.NewRequest(http.MethodGet, bankOrigin+"/pay", nil)
	getTrace, err := nav.Navigate(context.Background(),
		navigator.NewTopLevelContext(widgetURL), getReq)
	if err != nil {
		t.Fatal(err)
	}
	if !getTrace.Hops[0].CrossSite {
		t.Fatal("widget -> bank top-level GET must be cross-site")
	}
	if !strings.Contains(getTrace.Hops[0].CookieHeader, "session=BANK-LOGIN") {
		t.Errorf("cross-site top-level GET should carry Lax cookie: %q",
			getTrace.Hops[0].CookieHeader)
	}

	postReq, _ := http.NewRequest(http.MethodPost, bankOrigin+"/pay",
		strings.NewReader("z=9"))
	postTrace, err := nav.Navigate(context.Background(),
		navigator.NewTopLevelContext(widgetURL), postReq)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(postTrace.Hops[0].CookieHeader, "session=BANK-LOGIN") {
		t.Fatalf("cross-site top-level POST leaked Lax cookie: %q",
			postTrace.Hops[0].CookieHeader)
	}
	if !strings.Contains(postTrace.Hops[0].CookieHeader, "tracking=NONE-COOKIE") {
		t.Fatalf("None cookie missing on cross-site POST: %q",
			postTrace.Hops[0].CookieHeader)
	}
}

func TestOpaqueEmbeddedContextIsAlwaysCrossSite(t *testing.T) {
	w := newTestWorld(t)
	nav := w.newNavigator()
	w.seedLogin(t, nav)

	req, _ := http.NewRequest(http.MethodGet, bankOrigin+"/pay", nil)
	trace, err := nav.Navigate(context.Background(),
		navigator.NewOpaqueEmbeddedContext(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !trace.Hops[0].CrossSite || trace.Hops[0].TopLevel {
		t.Fatal("opaque embed request must be cross-site and non-top-level")
	}
	if strings.Contains(trace.Hops[0].CookieHeader, "BANK-LOGIN") {
		t.Fatalf("opaque embed borrowed login cookie: %q", trace.Hops[0].CookieHeader)
	}
}
