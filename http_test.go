package symbolcache

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

// roundTripFunc lets tests exercise HTTPMirror without binding a socket, which
// the sandbox forbids.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newHTTPMirror(status int, body []byte, gotURL *string) *HTTPMirror {
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if gotURL != nil {
				*gotURL = req.URL.Path
			}
			return &http.Response{
				StatusCode: status,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(body)),
			}, nil
		}),
	}
	return &HTTPMirror{BaseURL: "https://symbols.example.com", Client: client}
}

func TestHTTPMirrorHappyPath(t *testing.T) {
	key := testKey(testGUID, 7)
	pdb := buildPDB(testGUID, 7, 512, nil)
	idx, _ := key.index()

	var gotPath string
	cache, _ := newTestCache(t, newHTTPMirror(http.StatusOK, pdb, &gotPath))
	h, err := cache.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get over HTTP: %v", err)
	}
	_ = h.Close()
	if gotPath != "/"+idx {
		t.Fatalf("request path = %q, want /%s", gotPath, idx)
	}
}

func TestHTTPMirrorFallsBackOn404(t *testing.T) {
	key := testKey(testGUID, 7)
	pdb := buildPDB(testGUID, 7, 512, nil)

	cache, _ := newTestCache(t,
		newHTTPMirror(http.StatusNotFound, nil, nil),
		newHTTPMirror(http.StatusOK, pdb, nil),
	)
	h, err := cache.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get with fallback: %v", err)
	}
	_ = h.Close()
}

func TestHTTPMirrorTruncatedBodyRejected(t *testing.T) {
	key := testKey(testGUID, 7)
	full := buildPDB(testGUID, 7, 512, nil)

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(full[:len(full)/2])),
			}, nil
		}),
	}
	cache, dir := newTestCache(t, &HTTPMirror{
		BaseURL: "https://symbols.example.com",
		Client:  client,
	})
	if _, err := cache.Get(context.Background(), key); err == nil {
		t.Fatal("expected rejection of truncated HTTP body")
	}
	if n := countObjects(t, dir); n != 0 {
		t.Fatal("truncated HTTP body was cached")
	}
}

func TestKeyIndex(t *testing.T) {
	key := testKey(testGUID, 7)
	idx, err := key.index()
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	want := "app.pdb/0102030405060708090A0B0C0D0E0F107/app.pdb"
	if idx != want {
		t.Fatalf("index = %q, want %q", idx, want)
	}
}

func TestKeyRejectsUnsafeNames(t *testing.T) {
	bad := []string{
		"",
		".",
		"../escape.pdb",
		"dir/app.pdb",
		`dir\app.pdb`,
		"app.txt",
	}
	for _, name := range bad {
		key := Key{FileName: name, GUID: testGUID, Age: 1}
		if _, err := key.index(); err == nil {
			t.Fatalf("name %q should be rejected", name)
		}
	}
}

func TestNewRequiresMirror(t *testing.T) {
	if _, err := New(t.TempDir()); err == nil {
		t.Fatal("New without mirrors should fail")
	}
}
