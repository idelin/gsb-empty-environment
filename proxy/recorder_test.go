package proxy

import (
	"bytes"
	"io"
	"net/http"
	"time"
)

func timeBase() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

// responseRecorder is a minimal http.ResponseWriter whose output can be
// converted back into an *http.Response for a custom RoundTripper.
type responseRecorder struct {
	header     http.Header
	statusCode int
	body       bytes.Buffer
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: make(http.Header), statusCode: http.StatusOK}
}

func (r *responseRecorder) Header() http.Header { return r.header }

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	return r.body.Write(data)
}

func (r *responseRecorder) result(req *http.Request) *http.Response {
	return &http.Response{
		Status:        http.StatusText(r.statusCode),
		StatusCode:    r.statusCode,
		Header:        r.header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(r.body.Bytes())),
		ContentLength: int64(r.body.Len()),
		Request:       req,
		ProtoMajor:    1,
		ProtoMinor:    1,
	}
}
