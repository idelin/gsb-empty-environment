package redirectplan_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"prescreen.local/ps-35e0932b88"
)

type authEntry struct {
	path string
	auth string
}

type authLog struct {
	mu      sync.Mutex
	entries []authEntry
}

func (l *authLog) record(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, authEntry{
		path: r.URL.Path,
		auth: r.Header.Get("Authorization"),
	})
}

func (l *authLog) snapshot() []authEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]authEntry(nil), l.entries...)
}

// siteTransport is an in-process pair (or more) of sites.  A real request is
// handed to the selected site's handler, so Authorization assertions observe
// exactly what the client would put on the wire for that origin.
type siteTransport struct {
	handlers map[string]http.Handler
}

func newSiteTransport(routes map[string]http.Handler) *siteTransport {
	handlers := make(map[string]http.Handler, len(routes))
	for rawURL, handler := range routes {
		origin, err := redirectplan.ParseOrigin(rawURL)
		if err != nil {
			panic(err)
		}
		handlers[origin.String()] = handler
	}
	return &siteTransport{handlers: handlers}
}

func (s *siteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	origin, err := redirectplan.ParseOrigin(req.URL.String())
	if err != nil {
		return nil, err
	}
	handler, ok := s.handlers[origin.String()]
	if !ok {
		return nil, fmt.Errorf("no site configured for %s", origin)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	resp.Request = req
	return resp, nil
}

func mustOrigin(t *testing.T, rawURL string) redirectplan.Origin {
	t.Helper()
	origin, err := redirectplan.ParseOrigin(rawURL)
	if err != nil {
		t.Fatalf("ParseOrigin(%q): %v", rawURL, err)
	}
	return origin
}

func mustBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func siteURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func TestCrossPortDoesNotShareCredential(t *testing.T) {
	const originBase = "http://127.0.0.1:19001"
	const mirrorBase = "http://127.0.0.1:19002"

	t.Run("public mirror gets no private token", func(t *testing.T) {
		var originLog, mirrorLog authLog
		transport := newSiteTransport(map[string]http.Handler{
			originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originLog.record(r)
				http.Redirect(w, r, siteURL(mirrorBase, "/artifact"), http.StatusFound)
			}),
			mirrorBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mirrorLog.record(r)
				_, _ = io.WriteString(w, "mirror")
			}),
		})

		originID := mustOrigin(t, originBase)
		mirrorID := mustOrigin(t, mirrorBase)
		if originID.Host != mirrorID.Host || originID.Port == mirrorID.Port {
			t.Fatalf("sites are not same-host different-port: %s and %s", originID, mirrorID)
		}

		client := redirectplan.New(redirectplan.Options{
			Transport: transport,
			Credentials: redirectplan.CredentialSet{
				{Origin: originID, Purpose: "artifact"}: redirectplan.BearerToken("private-token"),
			},
		})
		resp, err := client.Get(context.Background(), siteURL(originBase, "/start"), "artifact")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got := mustBody(t, resp); got != "mirror" {
			t.Fatalf("body = %q", got)
		}
		if got := originLog.snapshot(); !equalEntries(got, []authEntry{{path: "/start", auth: "Bearer private-token"}}) {
			t.Fatalf("origin auth = %#v", got)
		}
		if got := mirrorLog.snapshot(); !equalEntries(got, []authEntry{{path: "/artifact", auth: ""}}) {
			t.Fatalf("mirror received leaked auth: %#v", got)
		}
	})

	t.Run("mirror uses only its own credential", func(t *testing.T) {
		var originLog, mirrorLog authLog
		transport := newSiteTransport(map[string]http.Handler{
			originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originLog.record(r)
				http.Redirect(w, r, siteURL(mirrorBase, "/artifact"), http.StatusFound)
			}),
			mirrorBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mirrorLog.record(r)
			}),
		})

		originID := mustOrigin(t, originBase)
		mirrorID := mustOrigin(t, mirrorBase)
		client := redirectplan.New(redirectplan.Options{
			Transport: transport,
			Credentials: redirectplan.CredentialSet{
				{Origin: originID, Purpose: "artifact"}: redirectplan.BearerToken("private-token"),
				{Origin: mirrorID, Purpose: "artifact"}: redirectplan.BearerToken("mirror-token"),
			},
		})
		resp, err := client.Get(context.Background(), siteURL(originBase, "/start"), "artifact")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		resp.Body.Close()

		if got := originLog.snapshot(); !equalEntries(got, []authEntry{{path: "/start", auth: "Bearer private-token"}}) {
			t.Fatalf("origin auth = %#v", got)
		}
		if got := mirrorLog.snapshot(); !equalEntries(got, []authEntry{{path: "/artifact", auth: "Bearer mirror-token"}}) {
			t.Fatalf("mirror auth = %#v", got)
		}
	})
}

func TestCrossSchemeDoesNotShareCredential(t *testing.T) {
	const originBase = "http://127.0.0.1:19443"
	const mirrorBase = "https://127.0.0.1:19443"

	var originLog, mirrorLog authLog
	transport := newSiteTransport(map[string]http.Handler{
		originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originLog.record(r)
			http.Redirect(w, r, siteURL(mirrorBase, "/artifact"), http.StatusSeeOther)
		}),
		mirrorBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mirrorLog.record(r)
		}),
	})

	originID := mustOrigin(t, originBase)
	mirrorID := mustOrigin(t, mirrorBase)
	if originID.Host != mirrorID.Host || originID.Port != mirrorID.Port || originID.Scheme == mirrorID.Scheme {
		t.Fatalf("sites are not same-host same-port cross-scheme: %s and %s", originID, mirrorID)
	}

	client := redirectplan.New(redirectplan.Options{
		Transport: transport,
		Credentials: redirectplan.CredentialSet{
			{Origin: originID, Purpose: "artifact"}: redirectplan.BearerToken("private-token"),
		},
	})
	resp, err := client.Get(context.Background(), siteURL(originBase, "/start"), "artifact")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	if got := originLog.snapshot(); !equalEntries(got, []authEntry{{path: "/start", auth: "Bearer private-token"}}) {
		t.Fatalf("origin auth = %#v", got)
	}
	if got := mirrorLog.snapshot(); !equalEntries(got, []authEntry{{path: "/artifact", auth: ""}}) {
		t.Fatalf("mirror received leaked auth: %#v", got)
	}
}

func TestUserInfoNeverBecomesAuthentication(t *testing.T) {
	const originBase = "http://127.0.0.1:19011"
	const mirrorBase = "http://127.0.0.1:19012"

	var originLog, mirrorLog authLog
	transport := newSiteTransport(map[string]http.Handler{
		originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originLog.record(r)
			location := "http://attacker:response-secret@" + strings.TrimPrefix(mirrorBase, "http://") + "/artifact"
			http.Redirect(w, r, location, http.StatusTemporaryRedirect)
		}),
		mirrorBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mirrorLog.record(r)
		}),
	})

	originID := mustOrigin(t, originBase)
	client := redirectplan.New(redirectplan.Options{
		Transport: transport,
		Credentials: redirectplan.CredentialSet{
			{Origin: originID, Purpose: "artifact"}: redirectplan.BearerToken("private-token"),
		},
	})

	start, err := url.Parse(siteURL(originBase, "/start"))
	if err != nil {
		t.Fatal(err)
	}
	start.User = url.UserPassword("caller", "request-secret")

	resp, err := client.Get(context.Background(), start.String(), "artifact")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	if got := originLog.snapshot(); !equalEntries(got, []authEntry{{path: "/start", auth: "Bearer private-token"}}) {
		t.Fatalf("origin auth = %#v (URL user info must not create Basic auth)", got)
	}
	if got := mirrorLog.snapshot(); !equalEntries(got, []authEntry{{path: "/artifact", auth: ""}}) {
		t.Fatalf("mirror auth = %#v (Location user info must not create Basic auth)", got)
	}
}

func TestRedirectBackReappliesExactCredential(t *testing.T) {
	const originBase = "http://127.0.0.1:19021"
	const mirrorBase = "http://127.0.0.1:19022"

	var originLog, mirrorLog authLog
	transport := newSiteTransport(map[string]http.Handler{
		originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originLog.record(r)
			switch r.URL.Path {
			case "/start":
				http.Redirect(w, r, siteURL(mirrorBase, "/mirror"), http.StatusFound)
			case "/back":
				_, _ = io.WriteString(w, "done")
			default:
				http.NotFound(w, r)
			}
		}),
		mirrorBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mirrorLog.record(r)
			http.Redirect(w, r, siteURL(originBase, "/back"), http.StatusFound)
		}),
	})

	originID := mustOrigin(t, originBase)
	mirrorID := mustOrigin(t, mirrorBase)
	client := redirectplan.New(redirectplan.Options{
		Transport: transport,
		Credentials: redirectplan.CredentialSet{
			{Origin: originID, Purpose: "artifact"}: redirectplan.BearerToken("private-token"),
			{Origin: mirrorID, Purpose: "artifact"}: redirectplan.BearerToken("mirror-token"),
		},
	})

	resp, err := client.Get(context.Background(), siteURL(originBase, "/start"), "artifact")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := mustBody(t, resp); got != "done" {
		t.Fatalf("body = %q", got)
	}
	wantOrigin := []authEntry{
		{path: "/start", auth: "Bearer private-token"},
		{path: "/back", auth: "Bearer private-token"},
	}
	if got := originLog.snapshot(); !equalEntries(got, wantOrigin) {
		t.Fatalf("origin log = %#v, want %#v", got, wantOrigin)
	}
	if got := mirrorLog.snapshot(); !equalEntries(got, []authEntry{{path: "/mirror", auth: "Bearer mirror-token"}}) {
		t.Fatalf("mirror log = %#v", got)
	}
}

func TestPurposeIsPartOfIdentity(t *testing.T) {
	const originBase = "http://127.0.0.1:19031"
	const mirrorBase = "http://127.0.0.1:19032"

	var mirrorLog authLog
	transport := newSiteTransport(map[string]http.Handler{
		originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, siteURL(mirrorBase, "/artifact"), http.StatusFound)
		}),
		mirrorBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mirrorLog.record(r)
		}),
	})

	originID := mustOrigin(t, originBase)
	mirrorID := mustOrigin(t, mirrorBase)
	client := redirectplan.New(redirectplan.Options{
		Transport: transport,
		Credentials: redirectplan.CredentialSet{
			{Origin: originID, Purpose: "artifact"}: redirectplan.BearerToken("private-token"),
			{Origin: mirrorID, Purpose: "metadata"}: redirectplan.BearerToken("wrong-purpose"),
		},
	})
	resp, err := client.Get(context.Background(), siteURL(originBase, "/start"), "artifact")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()

	if got := mirrorLog.snapshot(); !equalEntries(got, []authEntry{{path: "/artifact", auth: ""}}) {
		t.Fatalf("mirror received credential for a different purpose: %#v", got)
	}
}

func TestSourceResponseCannotSupplyCredentials(t *testing.T) {
	const originBase = "http://127.0.0.1:19041"
	const mirrorBase = "http://127.0.0.1:19042"

	var originLog, mirrorLog authLog
	transport := newSiteTransport(map[string]http.Handler{
		originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			originLog.record(r)
			w.Header().Set("Authorization", "Bearer response-injected")
			w.Header().Set("Proxy-Authorization", "Basic response-injected")
			w.Header().Set("Cookie", "response=injected")
			w.Header().Set("X-Injected-Credential", "response-injected")
			w.Header().Set("Location", siteURL(mirrorBase, "/artifact"))
			w.WriteHeader(http.StatusFound)
		}),
		mirrorBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mirrorLog.record(r)
		}),
	})

	client := redirectplan.New(redirectplan.Options{Transport: transport})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, siteURL(originBase, "/start"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer request-injected")
	req.Header.Set("Proxy-Authorization", "Basic request-injected")
	req.Header.Set("Cookie", "request=injected")

	resp, err := client.Do(req, "artifact")
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if got := originLog.snapshot(); !equalEntries(got, []authEntry{{path: "/start", auth: ""}}) {
		t.Fatalf("origin request headers were not sanitized: %#v", got)
	}
	if got := mirrorLog.snapshot(); !equalEntries(got, []authEntry{{path: "/artifact", auth: ""}}) {
		t.Fatalf("source response influenced mirror credentials: %#v", got)
	}
}

func TestUnknownCredentialHeaderDoesNotCrossOrigin(t *testing.T) {
	const originBase = "http://127.0.0.1:19043"
	const mirrorBase = "http://127.0.0.1:19044"

	t.Run("unknown header is dropped", func(t *testing.T) {
		var originKey, mirrorKey, mirrorTrace string
		transport := newSiteTransport(map[string]http.Handler{
			originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originKey = r.Header.Get("X-Api-Key")
				http.Redirect(w, r, siteURL(mirrorBase, "/artifact"), http.StatusFound)
			}),
			mirrorBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mirrorKey = r.Header.Get("X-Api-Key")
				mirrorTrace = r.Header.Get("X-Trace")
			}),
		})

		client := redirectplan.New(redirectplan.Options{
			Transport:      transport,
			ForwardHeaders: []string{"X-Trace"},
		})
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, siteURL(originBase, "/start"), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Api-Key", "private-token")
		req.Header.Set("X-Trace", "trace-1")
		resp, err := client.Do(req, "artifact")
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		resp.Body.Close()

		if originKey != "private-token" {
			t.Fatalf("origin key = %q", originKey)
		}
		if mirrorKey != "" {
			t.Fatalf("unknown credential header crossed origin: %q", mirrorKey)
		}
		if mirrorTrace != "trace-1" {
			t.Fatalf("explicitly forwarded header = %q", mirrorTrace)
		}
	})

	t.Run("target configured credential is applied", func(t *testing.T) {
		var mirrorKey string
		transport := newSiteTransport(map[string]http.Handler{
			originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, siteURL(mirrorBase, "/artifact"), http.StatusFound)
			}),
			mirrorBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mirrorKey = r.Header.Get("X-Api-Key")
			}),
		})

		mirrorID := mustOrigin(t, mirrorBase)
		client := redirectplan.New(redirectplan.Options{
			Transport: transport,
			Credentials: redirectplan.CredentialSet{
				{Origin: mirrorID, Purpose: "artifact"}: redirectplan.HeaderCredential("X-Api-Key", "mirror-token"),
			},
		})
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, siteURL(originBase, "/start"), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Api-Key", "private-token")
		resp, err := client.Do(req, "artifact")
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		resp.Body.Close()
		if mirrorKey != "mirror-token" {
			t.Fatalf("mirror key = %q", mirrorKey)
		}
	})
}

func TestRelativeAndSameOriginRedirectKeepsCredential(t *testing.T) {
	const originBase = "http://127.0.0.1:19051"

	var log authLog
	transport := newSiteTransport(map[string]http.Handler{
		originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.record(r)
			switch r.URL.Path {
			case "/start":
				http.Redirect(w, r, "/next", http.StatusFound)
			case "/next":
				http.Redirect(w, r, "//127.0.0.1:19051/final?ok=1", http.StatusFound)
			case "/final":
				_, _ = io.WriteString(w, r.URL.Query().Get("ok"))
			default:
				http.NotFound(w, r)
			}
		}),
	})

	originID := mustOrigin(t, originBase)
	client := redirectplan.New(redirectplan.Options{
		Transport: transport,
		Credentials: redirectplan.CredentialSet{
			{Origin: originID, Purpose: "artifact"}: redirectplan.BearerToken("private-token"),
		},
	})
	resp, err := client.Get(context.Background(), siteURL(originBase, "/start"), "artifact")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := mustBody(t, resp); got != "1" {
		t.Fatalf("body = %q", got)
	}

	entries := log.snapshot()
	if len(entries) != 3 {
		t.Fatalf("same-origin hops = %#v", entries)
	}
	for _, entry := range entries {
		if entry.auth != "Bearer private-token" {
			t.Fatalf("same-origin hop %s auth = %q", entry.path, entry.auth)
		}
	}
}

func TestRedirectPolicyRunsBeforeTargetRequest(t *testing.T) {
	const originBase = "http://127.0.0.1:19061"
	const mirrorBase = "http://127.0.0.1:19062"

	mirrorHits := 0
	transport := newSiteTransport(map[string]http.Handler{
		originBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, siteURL(mirrorBase, "/artifact"), http.StatusFound)
		}),
		mirrorBase: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mirrorHits++
		}),
	})

	originID := mustOrigin(t, originBase)
	client := redirectplan.New(redirectplan.Options{
		Transport: transport,
		Redirects: redirectplan.RedirectRules{},
		Credentials: redirectplan.CredentialSet{
			{Origin: originID, Purpose: "artifact"}: redirectplan.BearerToken("private-token"),
		},
	})
	_, err := client.Get(context.Background(), siteURL(originBase, "/start"), "artifact")
	if !errors.Is(err, redirectplan.ErrRedirectDenied) {
		t.Fatalf("error = %v, want ErrRedirectDenied", err)
	}
	if mirrorHits != 0 {
		t.Fatalf("mirror was contacted despite denied policy")
	}
}

func TestPlanUsesNormalizedTargetsAndExactIdentity(t *testing.T) {
	private := mustOrigin(t, "https://PRIVATE.example.:443/")
	mirror := mustOrigin(t, "http://mirror.example:8080/")
	planner := redirectplan.NewPlanner(redirectplan.CredentialSet{
		{Origin: private, Purpose: "artifact"}: redirectplan.BearerToken("private-token"),
		{Origin: mirror, Purpose: "artifact"}:  redirectplan.BearerToken("mirror-token"),
	}, nil)

	plan, err := planner.BuildPlan(
		"https://user:secret@PRIVATE.example:443/start",
		"artifact",
		[]string{
			"https://private.example/next",
			"http://MIRROR.example:8080/artifact",
			"https://private.example/back",
		},
	)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(plan.Hops) != 4 {
		t.Fatalf("hops = %d", len(plan.Hops))
	}
	want := []struct {
		origin redirectplan.Origin
		auth   string
	}{
		{private, "Bearer private-token"},
		{private, "Bearer private-token"},
		{mirror, "Bearer mirror-token"},
		{private, "Bearer private-token"},
	}
	for i, w := range want {
		hop := plan.Hops[i]
		if hop.Origin != w.origin {
			t.Fatalf("hop %d origin = %s, want %s", i, hop.Origin, w.origin)
		}
		header := make(http.Header)
		hop.Credential.Apply(header)
		if got := header.Get("Authorization"); got != w.auth {
			t.Fatalf("hop %d auth = %q, want %q", i, got, w.auth)
		}
		if hop.URL.User != nil {
			t.Fatalf("hop %d retained URL user info", i)
		}
	}
}

func TestOriginNormalization(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"HTTP://Example.COM.:80/path", "http://example.com"},
		{"https://Example.COM/path", "https://example.com"},
		{"http://example.com:443/path", "http://example.com:443"},
		{"http://example.com:080/path", "http://example.com"},
		{"https://[0:0::1]:443/path", "https://[::1]"},
		{"https://[fe80::1%25eth0]/path", "https://[fe80::1%25eth0]"},
		{"https://user:pass@example.com/path", "https://example.com"},
	}
	for _, test := range tests {
		origin, err := redirectplan.ParseOrigin(test.raw)
		if err != nil {
			t.Fatalf("ParseOrigin(%q): %v", test.raw, err)
		}
		if got := origin.String(); got != test.want {
			t.Fatalf("ParseOrigin(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
	if _, err := redirectplan.ParseOrigin("ftp://example.com/file"); !errors.Is(err, redirectplan.ErrUnsupportedScheme) {
		t.Fatalf("ftp error = %v", err)
	}
	if _, err := redirectplan.ParseOrigin("http://"); !errors.Is(err, redirectplan.ErrInvalidOrigin) {
		t.Fatalf("empty host error = %v", err)
	}
}

func TestCrossPortOriginsDifferOnlyByPort(t *testing.T) {
	a := mustOrigin(t, "https://example.com/a")
	b := mustOrigin(t, "https://example.com:8443/b")
	c := mustOrigin(t, "http://example.com/b")
	if a == b || a == c || b == c {
		t.Fatalf("origins unexpectedly equal: %s %s %s", a, b, c)
	}
}

func equalEntries(a, b []authEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
