package earlyhints_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"strings"

	"example.org/netsec/q0610/earlyhints"
)

// exampleTransport simulates an upstream that emits one attacker-laden 103
// before the final 200 response.
type exampleTransport struct{}

func (exampleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.Got1xxResponse != nil {
		poisoned := http.Header{}
		poisoned.Set("Link", `<https://cdn.example.com/app.js>; rel=preload, <https://evil.example.org/x.js>; rel=preload`)
		poisoned.Set("Set-Cookie", "session=attacker")
		if err := trace.Got1xxResponse(http.StatusEarlyHints, textproto.MIMEHeader(poisoned)); err != nil {
			return nil, err
		}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(strings.NewReader("hello")),
		Request:    req,
	}, nil
}

type noopCache struct{}

func (noopCache) Lookup(string) (earlyhints.CachedResponse, bool) {
	return earlyhints.CachedResponse{}, false
}
func (noopCache) Store(string, earlyhints.CachedResponse) {}

func ExampleHandler() {
	allowed := map[string]bool{"cdn.example.com": true}
	handler := earlyhints.NewHandler(noopCache{}, allowed, 0).
		WithTransport(exampleTransport{})

	req, _ := http.NewRequest(http.MethodGet, "https://example.test/", nil)
	req.ProtoMajor, req.ProtoMinor = 1, 1

	rec := newRecordingWriter()
	handler.ServeHTTP(rec, req)

	fmt.Printf("hints: %d\n", len(rec.hints))
	fmt.Printf("link: %s\n", rec.hints[0].Get("Link"))
	fmt.Printf("set-cookie leaked: %v\n", rec.hints[0].Get("Set-Cookie") != "")
	fmt.Printf("final: %d %s\n", rec.status, rec.body.String())
	// Output:
	// hints: 1
	// link: <https://cdn.example.com/app.js>; rel=preload
	// set-cookie leaked: false
	// final: 200 hello
}

type recordingWriter struct {
	header http.Header
	hints  []http.Header
	status int
	body   strings.Builder
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{header: http.Header{}, status: http.StatusOK}
}

func (w *recordingWriter) Header() http.Header { return w.header }

func (w *recordingWriter) WriteHeader(status int) {
	if status == http.StatusEarlyHints {
		captured := http.Header{}
		for name, values := range w.header {
			captured[name] = append([]string(nil), values...)
		}
		w.hints = append(w.hints, captured)
		return
	}
	w.status = status
}

func (w *recordingWriter) Flush() {}

func (w *recordingWriter) Write(p []byte) (int, error) {
	return w.body.Write(p)
}
