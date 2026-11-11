package sitemapdisc

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func indexXML(locs ...string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, loc := range locs {
		b.WriteString("<sitemap><loc>" + loc + "</loc></sitemap>")
	}
	b.WriteString("</sitemapindex>")
	return []byte(b.String())
}

type urlSpec struct {
	loc     string
	lastMod string
}

func urlsetXML(specs ...urlSpec) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, spec := range specs {
		b.WriteString("<url><loc>" + spec.loc + "</loc>")
		if spec.lastMod != "" {
			b.WriteString("<lastmod>" + spec.lastMod + "</lastmod>")
		}
		b.WriteString("</url>")
	}
	b.WriteString("</urlset>")
	return []byte(b.String())
}

const testHost = "sitemapdisc.test"

// pipeListener is an in-memory net.Listener backed by net.Pipe. The sandbox
// forbids TCP loopback binds, so tests serve HTTP over pipes instead.
type pipeListener struct {
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		ch:     make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ch:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

// dial returns the client end of a fresh in-memory connection.
func (l *pipeListener) dial() net.Conn {
	server, client := net.Pipe()
	l.ch <- server
	return client
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return testHost + ":80" }

// testServer bundles an in-memory HTTP server with a client that dials it.
type testServer struct {
	client *http.Client
	base   *url.URL
}

// newTestServer serves the mux over an in-memory listener. Requests still run
// through the full net/http stack; no real socket or port is used.
func newTestServer(t *testing.T, register func(mux *http.ServeMux, base *url.URL)) *testServer {
	t.Helper()
	mux := http.NewServeMux()
	base, _ := url.Parse("http://" + testHost)
	register(mux, base)

	listener := newPipeListener()
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return listener.dial(), nil
			},
		},
	}
	return &testServer{client: client, base: base}
}

func serveContent(content []byte, gzipped bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if gzipped {
			w.Header().Set("Content-Encoding", "gzip")
		}
		_, _ = w.Write(content)
	}
}

func TestDiscoverNestedIndexGzipAndDedup(t *testing.T) {
	ts := newTestServer(t, func(mux *http.ServeMux, base *url.URL) {
		mux.HandleFunc("/sitemaps/root.xml", serveContent(indexXML(
			base.String()+"/sitemaps/inner.xml",
			base.String()+"/sitemaps/inner2.xml.gz",
			"sub/deep.xml",
		), false))

		mux.HandleFunc("/sitemaps/inner.xml", serveContent(indexXML(
			"root.xml",
			base.String()+"/sitemaps/pages-a.xml",
		), false))

		mux.HandleFunc("/sitemaps/inner2.xml.gz", serveContent(gzipBytes(t, indexXML(
			base.String()+"/sitemaps/pages-b.xml.gz",
		)), true))

		mux.HandleFunc("/sitemaps/pages-a.xml", serveContent(urlsetXML(
			urlSpec{loc: base.String() + "/sitemaps/p/1", lastMod: "2026-11-11T08:30:00+08:00"},
			urlSpec{loc: base.String() + "/sitemaps/p/2", lastMod: "2026-11-10"},
			urlSpec{loc: base.String() + "/sitemaps/p/dup", lastMod: "2026"},
			urlSpec{loc: "p/3", lastMod: "2026-11"},
		), false))

		mux.HandleFunc("/sitemaps/pages-b.xml.gz", serveContent(gzipBytes(t, urlsetXML(
			urlSpec{loc: base.String() + "/sitemaps/p/dup"},
			urlSpec{loc: base.String() + "/sitemaps/p/4", lastMod: "2026-11-09T12:00:00Z"},
		)), true))

		mux.HandleFunc("/sitemaps/sub/deep.xml", serveContent(urlsetXML(
			urlSpec{loc: base.String() + "/sitemaps/sub/deep-page"},
		), false))
	})

	d := New()
	d.Client = ts.client
	entries, err := d.Discover(context.Background(), ts.base.String()+"/sitemaps/root.xml")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	got := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		if _, ok := got[entry.Loc]; ok {
			t.Fatalf("duplicate entry returned: %s", entry.Loc)
		}
		got[entry.Loc] = entry
	}

	wantLocs := []string{
		ts.base.String() + "/sitemaps/p/1",
		ts.base.String() + "/sitemaps/p/2",
		ts.base.String() + "/sitemaps/p/dup",
		ts.base.String() + "/sitemaps/p/3",
		ts.base.String() + "/sitemaps/p/4",
		ts.base.String() + "/sitemaps/sub/deep-page",
	}
	if len(got) != len(wantLocs) {
		t.Fatalf("got %d entries %v, want %d", len(got), got, len(wantLocs))
	}
	for _, loc := range wantLocs {
		if _, ok := got[loc]; !ok {
			t.Errorf("missing entry %q", loc)
		}
	}

	if e := got[ts.base.String()+"/sitemaps/p/3"]; e.LastModRaw != "2026-11" {
		t.Errorf("relative loc entry missing or wrong lastmod: %+v", e)
	}

	wantTime, _ := time.Parse(time.RFC3339, "2026-11-11T08:30:00+08:00")
	if e := got[ts.base.String()+"/sitemaps/p/1"]; !e.LastMod.Equal(wantTime) {
		t.Errorf("RFC3339 lastmod = %v, want %v", e.LastMod, wantTime)
	}
	if e := got[ts.base.String()+"/sitemaps/p/4"]; e.LastMod.UTC() != time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) {
		t.Errorf("UTC lastmod = %v", e.LastMod)
	}
	if e := got[ts.base.String()+"/sitemaps/p/2"]; e.LastMod.IsZero() {
		t.Errorf("date-only lastmod was not parsed")
	}
}

func TestDiscoverCycleIsSkipped(t *testing.T) {
	var hitsMu sync.Mutex
	hits := make(map[string]int)

	ts := newTestServer(t, func(mux *http.ServeMux, base *url.URL) {
		count := func(name, next string) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				hitsMu.Lock()
				hits[name]++
				hitsMu.Unlock()
				_, _ = w.Write(indexXML(base.String() + next))
			}
		}
		mux.HandleFunc("/a.xml", count("a", "/b.xml"))
		mux.HandleFunc("/b.xml", count("b", "/c.xml"))
		mux.HandleFunc("/c.xml", count("c", "/a.xml"))
	})

	d := New()
	d.Client = ts.client
	entries, err := d.Discover(context.Background(), ts.base.String()+"/a.xml")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("index-only cycle produced entries: %v", entries)
	}
	for name, n := range hits {
		if n != 1 {
			t.Errorf("sitemap %s fetched %d times, want exactly once", name, n)
		}
	}
}

func TestDiscoverTooManyEntries(t *testing.T) {
	ts := newTestServer(t, func(mux *http.ServeMux, base *url.URL) {
		var specs []urlSpec
		for i := 0; i < 3; i++ {
			specs = append(specs, urlSpec{loc: fmt.Sprintf("%s/p/%d", base.String(), i)})
		}
		mux.HandleFunc("/big.xml", serveContent(urlsetXML(specs...), false))
	})

	d := New()
	d.Client = ts.client
	d.MaxEntries = 2
	if _, err := d.Discover(context.Background(), ts.base.String()+"/big.xml"); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("err = %v, want ErrTooManyEntries", err)
	}

	d.MaxEntries = 3
	entries, err := d.Discover(context.Background(), ts.base.String()+"/big.xml")
	if err != nil {
		t.Fatalf("Discover within limit: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
}

func TestDiscoverDecompressedSizeLimit(t *testing.T) {
	ts := newTestServer(t, func(mux *http.ServeMux, base *url.URL) {
		body := strings.Repeat("<url><loc>"+base.String()+"/x</loc></url>", 4000)
		xmlDoc := []byte(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + body + `</urlset>`)
		mux.HandleFunc("/bomb.xml.gz", serveContent(gzipBytes(t, xmlDoc), true))
	})

	d := New()
	d.Client = ts.client
	d.MaxDecompressed = 1024
	if _, err := d.Discover(context.Background(), ts.base.String()+"/bomb.xml.gz"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}

	d.MaxDecompressed = DefaultMaxDecompressed
	entries, err := d.Discover(context.Background(), ts.base.String()+"/bomb.xml.gz")
	if err != nil {
		t.Fatalf("Discover with generous limit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (repeated locs deduplicated)", len(entries))
	}
}

func TestDiscoverRejectsOutOfScopeReferences(t *testing.T) {
	ts := newTestServer(t, func(mux *http.ServeMux, base *url.URL) {
		mux.HandleFunc("/sitemaps/root.xml", serveContent(indexXML(
			"http://foreign.example/foreign.xml",
			"http://127.0.0.1:"+base.Port()+"/sitemaps/skipped.xml",
			base.String()+"/elsewhere/escaped.xml",
			base.String()+"/sitemaps/allowed.xml",
		), false))

		mux.HandleFunc("/sitemaps/allowed.xml", serveContent(urlsetXML(
			urlSpec{loc: base.String() + "/sitemaps/ok"},
			urlSpec{loc: base.String() + "/sitemaps/../elsewhere/blocked"},
			urlSpec{loc: "ftp://" + base.Host + "/sitemaps/nope"},
		), false))

		mux.HandleFunc("/elsewhere/escaped.xml", serveContent(urlsetXML(
			urlSpec{loc: base.String() + "/elsewhere/should-not-appear"},
		), false))
	})

	d := New()
	d.Client = ts.client
	entries, err := d.Discover(context.Background(), ts.base.String()+"/sitemaps/root.xml")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	var locs []string
	for _, entry := range entries {
		locs = append(locs, entry.Loc)
	}
	want := []string{ts.base.String() + "/sitemaps/ok"}
	if len(locs) != 1 || locs[0] != want[0] {
		t.Fatalf("got %v, want only %v", locs, want)
	}
}

func TestDiscoverInvalidSeedAndFetchFailure(t *testing.T) {
	d := New()

	if _, err := d.Discover(context.Background(), "not a url"); !errors.Is(err, ErrInvalidSeed) {
		t.Errorf("relative seed: err = %v, want ErrInvalidSeed", err)
	}
	if _, err := d.Discover(context.Background(), "ftp://example.test/a.xml"); !errors.Is(err, ErrInvalidSeed) {
		t.Errorf("non-http seed: err = %v, want ErrInvalidSeed", err)
	}

	ts := newTestServer(t, func(mux *http.ServeMux, base *url.URL) {
		mux.HandleFunc("/missing.xml", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
	})
	d.Client = ts.client

	if _, err := d.Discover(context.Background(), ts.base.String()+"/missing.xml"); err == nil {
		t.Error("404 response must return an error")
	}
}

func TestParseLastModFormats(t *testing.T) {
	cases := map[string]bool{
		"2026":                           true,
		"2026-11":                        true,
		"2026-11-11":                     true,
		"2026-11-11T08:30Z":              true,
		"2026-11-11T08:30:00Z":           true,
		"2026-11-11T08:30:00+08:00":      true,
		"2026-11-11T08:30:00.123456789Z": true,
		"2026-11-11 08:30:00":            true,
		"2026-11-11T08:30:00":            true,
		"":                               false,
		"not-a-date":                     false,
		"11/11/2026":                     false,
	}
	for raw, shouldParse := range cases {
		got := parseLastMod(raw)
		if shouldParse && got.IsZero() {
			t.Errorf("parseLastMod(%q) returned zero time", raw)
		}
		if !shouldParse && !got.IsZero() {
			t.Errorf("parseLastMod(%q) = %v, want zero time", raw, got)
		}
	}
}

func TestResolveChild(t *testing.T) {
	parent, _ := url.Parse("https://example.com:8443/sitemaps/a/index.xml")

	ok := []string{
		"https://example.com:8443/sitemaps/a/inner.xml",
		"https://example.com:8443/sitemaps/a/nested/deep.xml",
		"relative/child.xml",
		"/sitemaps/a/rooted.xml",
	}
	for _, loc := range ok {
		if _, good := resolveChild(parent, loc); !good {
			t.Errorf("resolveChild(%q) rejected, want accepted", loc)
		}
	}

	bad := []string{
		"https://other.example/sitemaps/a/inner.xml",
		"https://example.com/sitemaps/a/inner.xml",
		"https://example.com:9000/sitemaps/a/inner.xml",
		"/sitemaps/escaped.xml",
		"/sitemaps/ab/../a/../../etc.xml",
		"ftp://example.com:8443/sitemaps/a/x",
	}
	for _, loc := range bad {
		if _, good := resolveChild(parent, loc); good {
			t.Errorf("resolveChild(%q) accepted, want rejected", loc)
		}
	}
}

func TestIsGzip(t *testing.T) {
	if !isGzip([]byte{0x1f, 0x8b, 0x08, 0x00}) {
		t.Error("gzip magic not detected")
	}
	if isGzip([]byte(`<?xml version`)) {
		t.Error("plain XML misdetected as gzip")
	}
	if isGzip(nil) {
		t.Error("empty body misdetected as gzip")
	}
}

func TestDiscoverTransportGunzipAlsoBounded(t *testing.T) {
	ts := newTestServer(t, func(mux *http.ServeMux, base *url.URL) {
		xmlDoc := urlsetXML(
			urlSpec{loc: base.String() + "/a"},
			urlSpec{loc: base.String() + "/b"},
		)
		// The handler advertises Content-Encoding: gzip. A client without an
		// explicit Accept-Encoding lets http.Transport transparently inflate
		// the body, so the decompressed stream must still be size-bounded.
		mux.HandleFunc("/auto.xml.gz", serveContent(gzipBytes(t, xmlDoc), true))
	})

	d := New()
	d.Client = ts.client
	entries, err := d.Discover(context.Background(), ts.base.String()+"/auto.xml.gz")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	// Tight limit must reject the transparently inflated stream too.
	d.MaxDecompressed = 8
	if _, err := d.Discover(context.Background(), ts.base.String()+"/auto.xml.gz"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}
