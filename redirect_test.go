package netsec

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type wireHit struct {
	path   string
	header http.Header
}

type wireLog struct {
	mu   sync.Mutex
	hits []wireHit
}

func (l *wireLog) add(req *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits = append(l.hits, wireHit{
		path:   req.URL.Path,
		header: req.Header.Clone(),
	})
}

func (l *wireLog) snapshot() []wireHit {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]wireHit, len(l.hits))
	copy(out, l.hits)
	return out
}

func authOf(header http.Header) string {
	return header.Get("Authorization")
}

func mustBody(t *testing.T, resp *http.Response, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}
	if got := strings.TrimSpace(string(body)); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestRedirectRebindsCredentialsAcrossPortsAndBack(t *testing.T) {
	t.Parallel()

	network := newMemoryNetwork()
	var source *fakeSite
	var mirror *fakeSite
	sourceHits := &wireLog{}
	mirrorHits := &wireLog{}

	source = network.Add(t, "http", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sourceHits.add(req)
		switch req.URL.Path {
		case "/download":
			if got := authOf(req.Header); got != "Bearer source-token" {
				http.Error(w, "missing source token", http.StatusUnauthorized)
				return
			}
			next := source.URL + "/finish"
			http.Redirect(w, req, mirror.URL+"/mirror?next="+url.QueryEscape(next), http.StatusFound)
		case "/finish":
			if got := authOf(req.Header); got != "Bearer source-token" {
				http.Error(w, "missing source token", http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, "download complete")
		default:
			http.NotFound(w, req)
		}
	}))
	defer source.Close()

	mirror = network.Add(t, "http", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mirrorHits.add(req)
		if req.URL.Path != "/mirror" {
			http.NotFound(w, req)
			return
		}
		// A response must not be able to mint credentials for the next hop.
		w.Header().Set("WWW-Authenticate", `Bearer response-supplied-token`)
		w.Header().Set("X-Auth-Token", "response-supplied-token")
		w.Header().Set("Set-Cookie", "mirror_token=response-supplied-token; Path=/")
		http.Redirect(w, req, req.URL.Query().Get("next"), http.StatusTemporaryRedirect)
	}))
	defer mirror.Close()

	sourceOrigin := MustParseOrigin(source.URL)
	planner, err := NewPlanner(Config{
		Credentials: []Credential{{
			Identity: "source-download",
			Source:   sourceOrigin,
			Purpose:  PurposeDownload,
			Headers: http.Header{
				"Authorization": {"Bearer source-token"},
				"X-Api-Key":     {"source-api-key"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	client := planner.WrapClient(network.Client())
	req, err := planner.NewRequest(t.Context(), http.MethodGet, source.URL+"/download", PurposeDownload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	mustBody(t, resp, err, "download complete")

	sourceWire := sourceHits.snapshot()
	if len(sourceWire) != 2 {
		t.Fatalf("source hits = %d, want 2", len(sourceWire))
	}
	for _, hit := range sourceWire {
		if got := authOf(hit.header); got != "Bearer source-token" {
			t.Errorf("source %s Authorization = %q, want source token", hit.path, got)
		}
		if got := hit.header.Get("X-Api-Key"); got != "source-api-key" {
			t.Errorf("source %s X-Api-Key = %q, want configured key", hit.path, got)
		}
	}

	mirrorWire := mirrorHits.snapshot()
	if len(mirrorWire) != 1 {
		t.Fatalf("mirror hits = %d, want 1", len(mirrorWire))
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "X-Api-Key", "Cookie"} {
		if got := mirrorWire[0].header.Get(name); got != "" {
			t.Errorf("public mirror received %s = %q", name, got)
		}
	}
}

func TestEachHopSelectsItsOwnConfiguredCredential(t *testing.T) {
	t.Parallel()

	network := newMemoryNetwork()
	var source *fakeSite
	var mirror *fakeSite
	sourceHits := &wireLog{}
	mirrorHits := &wireLog{}

	source = network.Add(t, "http", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sourceHits.add(req)
		switch req.URL.Path {
		case "/start":
			http.Redirect(w, req, mirror.URL+"/artifact", http.StatusFound)
		case "/finish":
			fmt.Fprint(w, "finished")
		default:
			http.NotFound(w, req)
		}
	}))
	defer source.Close()

	mirror = network.Add(t, "http", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mirrorHits.add(req)
		http.Redirect(w, req, source.URL+"/finish", http.StatusTemporaryRedirect)
	}))
	defer mirror.Close()

	planner, err := NewPlanner(Config{
		Credentials: []Credential{
			{
				Source:  MustParseOrigin(source.URL),
				Purpose: PurposeDownload,
				Headers: http.Header{"Authorization": {"Bearer source-token"}},
			},
			{
				Source:  MustParseOrigin(mirror.URL),
				Purpose: PurposeDownload,
				Headers: http.Header{"Authorization": {"Bearer mirror-token"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req, err := planner.NewRequest(t.Context(), http.MethodGet, source.URL+"/start", PurposeDownload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := planner.WrapClient(network.Client()).Do(req)
	mustBody(t, resp, err, "finished")

	sourceWire := sourceHits.snapshot()
	if len(sourceWire) != 2 {
		t.Fatalf("source hits = %d, want 2", len(sourceWire))
	}
	for _, hit := range sourceWire {
		if got := authOf(hit.header); got != "Bearer source-token" {
			t.Errorf("source %s Authorization = %q, want source token", hit.path, got)
		}
	}

	mirrorWire := mirrorHits.snapshot()
	if len(mirrorWire) != 1 {
		t.Fatalf("mirror hits = %d, want 1", len(mirrorWire))
	}
	if got := authOf(mirrorWire[0].header); got != "Bearer mirror-token" {
		t.Errorf("mirror Authorization = %q, want mirror token", got)
	}
}

func TestRedirectAcrossSchemeAndUserinfoDoesNotLeak(t *testing.T) {
	t.Parallel()

	network := newMemoryNetwork()
	var source *fakeSite
	var mirror *fakeSite
	sourceHits := &wireLog{}
	mirrorHits := &wireLog{}

	source = network.Add(t, "https", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sourceHits.add(req)
		if got := authOf(req.Header); got != "Bearer private-token" {
			http.Error(w, "missing private token", http.StatusUnauthorized)
			return
		}
		switch req.URL.Path {
		case "/start":
			http.Redirect(w, req, mirror.URL+"/hop", http.StatusFound)
		case "/back":
			fmt.Fprint(w, "round trip complete")
		default:
			http.NotFound(w, req)
		}
	}))
	defer source.Close()

	mirror = network.Add(t, "http", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mirrorHits.add(req)
		switch req.URL.Path {
		case "/hop":
			http.Redirect(w, req, mirror.URL+"/userinfo", http.StatusFound)
		case "/userinfo":
			withUserinfo, err := url.Parse(mirror.URL + "/final")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			withUserinfo.User = url.UserPassword("mirror-user", "mirror-password")
			http.Redirect(w, req, withUserinfo.String(), http.StatusFound)
		case "/final":
			http.Redirect(w, req, source.URL+"/back", http.StatusTemporaryRedirect)
		default:
			http.NotFound(w, req)
		}
	}))
	defer mirror.Close()

	planner, err := NewPlanner(Config{
		DefaultPurpose: PurposeDownload,
		Credentials: []Credential{{
			Source:  MustParseOrigin(source.URL),
			Purpose: PurposeDownload,
			Headers: http.Header{"Authorization": {"Bearer private-token"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	client := planner.WrapClient(network.Client())
	req, err := planner.NewRequest(t.Context(), http.MethodGet, source.URL+"/start", PurposeDownload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	mustBody(t, resp, err, "round trip complete")

	sourceWire := sourceHits.snapshot()
	if len(sourceWire) != 2 {
		t.Fatalf("source hits = %d, want 2", len(sourceWire))
	}
	if got := authOf(sourceWire[0].header); got != "Bearer private-token" {
		t.Errorf("initial source Authorization = %q", got)
	}
	if got := authOf(sourceWire[1].header); got != "Bearer private-token" {
		t.Errorf("redirect-back source Authorization = %q", got)
	}

	mirrorWire := mirrorHits.snapshot()
	if len(mirrorWire) != 3 {
		t.Fatalf("mirror hits = %d, want 3", len(mirrorWire))
	}
	for _, hit := range mirrorWire {
		if got := authOf(hit.header); got != "" {
			t.Errorf("HTTP mirror %s received Authorization = %q", hit.path, got)
		}
		if strings.HasPrefix(authOf(hit.header), "Basic ") {
			t.Errorf("URL userinfo became Basic auth at %s", hit.path)
		}
	}
}

func TestCallerConfigurationIsOnlyAuthorizationSource(t *testing.T) {
	t.Parallel()

	network := newMemoryNetwork()
	var source *fakeSite
	var mirror *fakeSite
	sourceHits := &wireLog{}
	mirrorHits := &wireLog{}

	source = network.Add(t, "http", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sourceHits.add(req)
		http.Redirect(w, req, mirror.URL+"/artifact", http.StatusFound)
	}))
	defer source.Close()

	mirror = network.Add(t, "http", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mirrorHits.add(req)
		fmt.Fprint(w, "artifact")
	}))
	defer mirror.Close()

	planner, err := NewPlanner(Config{
		DefaultPurpose: PurposeDownload,
		Credentials: []Credential{{
			Source:  MustParseOrigin(source.URL),
			Purpose: PurposeDownload,
			Headers: http.Header{
				"Authorization": {"Bearer configured-token"},
				"X-Api-Key":     {"configured-key"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, source.URL+"/download", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ambient-token")
	req.Header.Set("Proxy-Authorization", "Basic ambient-proxy")
	req.Header.Set("Cookie", "ambient=token")
	req.Header.Set("X-Api-Key", "ambient-key")

	// Deliberately send the raw request. The wrapped transport must still strip
	// ambient credentials and apply only caller configuration.
	resp, err := planner.WrapClient(network.Client()).Do(req)
	mustBody(t, resp, err, "artifact")

	sourceWire := sourceHits.snapshot()
	if len(sourceWire) != 1 {
		t.Fatalf("source hits = %d, want 1", len(sourceWire))
	}
	if got := authOf(sourceWire[0].header); got != "Bearer configured-token" {
		t.Errorf("source Authorization = %q, want configured token", got)
	}
	if got := sourceWire[0].header.Get("X-Api-Key"); got != "configured-key" {
		t.Errorf("source X-Api-Key = %q, want configured key", got)
	}
	if got := sourceWire[0].header.Get("Cookie"); got != "" {
		t.Errorf("ambient cookie reached source: %q", got)
	}

	mirrorWire := mirrorHits.snapshot()
	if len(mirrorWire) != 1 {
		t.Fatalf("mirror hits = %d, want 1", len(mirrorWire))
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "X-Api-Key", "Cookie"} {
		if got := mirrorWire[0].header.Get(name); got != "" {
			t.Errorf("mirror received %s = %q", name, got)
		}
	}
}

func TestPurposeIsPreservedAndScoped(t *testing.T) {
	t.Parallel()

	hits := &wireLog{}
	network := newMemoryNetwork()
	server := network.Add(t, "http", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.add(req)
		switch req.URL.Path {
		case "/start-download", "/start-metadata":
			http.Redirect(w, req, "/next", http.StatusFound)
		case "/next":
			fmt.Fprint(w, "ok")
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	origin := MustParseOrigin(server.URL)
	planner, err := NewPlanner(Config{
		Credentials: []Credential{
			{
				Source:  origin,
				Purpose: PurposeDownload,
				Headers: http.Header{"Authorization": {"Bearer download-token"}},
			},
			{
				Source:  origin,
				Purpose: PurposeMetadata,
				Headers: http.Header{"Authorization": {"Bearer metadata-token"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := planner.WrapClient(network.Client())

	download, err := planner.NewRequest(t.Context(), http.MethodGet, server.URL+"/start-download", PurposeDownload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(download)
	mustBody(t, resp, err, "ok")

	metadata, err := planner.NewRequest(t.Context(), http.MethodGet, server.URL+"/start-metadata", PurposeMetadata)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(metadata)
	mustBody(t, resp, err, "ok")

	wire := hits.snapshot()
	if len(wire) != 4 {
		t.Fatalf("hits = %d, want 4", len(wire))
	}
	want := []string{
		"Bearer download-token",
		"Bearer download-token",
		"Bearer metadata-token",
		"Bearer metadata-token",
	}
	for i, hit := range wire {
		if got := authOf(hit.header); got != want[i] {
			t.Errorf("hit %d (%s) Authorization = %q, want %q", i, hit.path, got, want[i])
		}
	}
}

func TestUnspecifiedPurposeReceivesNoCredential(t *testing.T) {
	t.Parallel()

	hits := &wireLog{}
	network := newMemoryNetwork()
	server := network.Add(t, "http", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.add(req)
		if req.URL.Path == "/start" {
			http.Redirect(w, req, "/next", http.StatusFound)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	planner, err := NewPlanner(Config{
		Credentials: []Credential{{
			Source:  MustParseOrigin(server.URL),
			Purpose: PurposeDownload,
			Headers: http.Header{"Authorization": {"Bearer download-token"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := planner.Prepare(req, "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := planner.WrapClient(network.Client()).Do(prepared)
	mustBody(t, resp, err, "ok")

	for _, hit := range hits.snapshot() {
		if got := authOf(hit.header); got != "" {
			t.Errorf("unbound-purpose request %s received Authorization = %q", hit.path, got)
		}
	}
}

func TestRedirectLimit(t *testing.T) {
	t.Parallel()

	var hits int32
	network := newMemoryNetwork()
	server := network.Add(t, "http", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits++
		http.Redirect(w, req, "/loop", http.StatusFound)
	}))
	defer server.Close()

	planner, err := NewPlanner(Config{MaxRedirects: 2})
	if err != nil {
		t.Fatal(err)
	}
	req, err := planner.NewRequest(t.Context(), http.MethodGet, server.URL+"/loop", PurposeDownload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := planner.WrapClient(network.Client()).Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected redirect limit error")
	}
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("error = %v, want ErrTooManyRedirects", err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("server hits = %d, want initial request plus two followed redirects", got)
	}
}
