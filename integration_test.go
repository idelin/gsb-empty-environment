package netsec

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

type downstreamRecorder struct {
	mu     sync.Mutex
	calls  int
	header http.Header
	body   string
}

func (r *downstreamRecorder) serve(header http.Header, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.header = header
	r.body = body
}

func (r *downstreamRecorder) snapshot() (int, http.Header, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.header, r.body
}

func TestGatewayDoesNotDeliverUnauthenticatedTrailers(t *testing.T) {
	digest := hmacDigest([]byte("gateway-test-key"))
	downstream := &downstreamRecorder{}
	gateway := trailerGateway(digest, downstream)

	validHeader := http.Header{
		"Content-Type": {"text/plain"},
		"Trailer":      {"Digest"},
	}
	validTrailers := http.Header{
		"Digest": {"sha-256=:valid:"},
	}

	tests := []struct {
		name          string
		header        http.Header
		trailers      http.Header
		wireTrailers  http.Header
		signPolicy    bool
		wantStatus    int
		wantError     string
		wantCalls     int
		wantBody      string
		wantMergedKey string
	}{
		{
			name:          "authenticated trailer reaches downstream",
			header:        validHeader,
			trailers:      validTrailers,
			wireTrailers:  validTrailers,
			signPolicy:    true,
			wantStatus:    http.StatusNoContent,
			wantCalls:     1,
			wantBody:      "payload",
			wantMergedKey: "sha-256=:valid:",
		},
		{
			name: "header and trailer with the same name are rejected",
			header: http.Header{
				"Content-Type": {"text/plain"},
				"Trailer":      {"Digest"},
				"Digest":       {"authenticated-header-value"},
			},
			trailers:     http.Header{"Digest": {"trailer-value"}},
			wireTrailers: http.Header{"Digest": {"trailer-value"}},
			signPolicy:   true,
			wantStatus:   http.StatusBadRequest,
			wantError:    "collides with authenticated header",
			wantCalls:    0,
		},
		{
			name:         "undeclared trailer is rejected",
			header:       validHeader,
			trailers:     http.Header{"Digest": {"sha-256=:valid:"}, "X-Extra": {"no"}},
			wireTrailers: http.Header{"Digest": {"sha-256=:valid:"}, "X-Extra": {"no"}},
			signPolicy:   true,
			wantStatus:   http.StatusBadRequest,
			wantError:    "undeclared trailer",
			wantCalls:    0,
		},
		{
			name:         "truncated trailer section is rejected",
			header:       validHeader,
			trailers:     nil,
			wireTrailers: nil,
			signPolicy:   true,
			wantStatus:   http.StatusBadRequest,
			wantError:    "declared trailer field missing",
			wantCalls:    0,
		},
		{
			name:         "tampered trailer is rejected",
			header:       validHeader,
			trailers:     validTrailers,
			wireTrailers: http.Header{"Digest": {"sha-256=:tampered:"}},
			signPolicy:   true,
			wantStatus:   http.StatusBadRequest,
			wantError:    "signature mismatch",
			wantCalls:    0,
		},
		{
			name:         "forbidden trailer is rejected",
			header:       http.Header{"Content-Type": {"text/plain"}, "Trailer": {"Authorization"}},
			trailers:     http.Header{"Authorization": {"Bearer secret"}},
			wireTrailers: http.Header{"Authorization": {"Bearer secret"}},
			signPolicy:   false,
			wantStatus:   http.StatusBadRequest,
			wantError:    "forbidden trailer",
			wantCalls:    0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before, _, _ := downstream.snapshot()

			signedHeader := test.header.Clone()
			var signature []byte
			var err error
			if test.signPolicy {
				signature, err = SignHTTP(signedHeader, test.trailers, digest)
			} else {
				signature, err = Sign(
					SectionFromHTTPHeader(signedHeader),
					SectionFromHTTPHeader(test.trailers),
					digest,
				)
			}
			if err != nil {
				t.Fatalf("sign request: %v", err)
			}
			signedHeader.Set(SignatureHeader, EncodeSignature(signature))

			request := parseRawChunkedRequest(t, signedHeader, "payload", test.wireTrailers)
			defer request.Body.Close()
			recorder := httptest.NewRecorder()
			gateway.ServeHTTP(recorder, request)
			status := recorder.Code
			if status != test.wantStatus {
				t.Fatalf("status = %d, want %d", status, test.wantStatus)
			}
			if test.wantError != "" && !strings.Contains(recorder.Body.String(), test.wantError) {
				t.Fatalf("error body = %q, want substring %q", recorder.Body.String(), test.wantError)
			}

			calls, header, body := downstream.snapshot()
			if calls-before != test.wantCalls {
				t.Fatalf("downstream calls = %d, want %d", calls-before, test.wantCalls)
			}
			if test.wantCalls == 0 {
				return
			}
			if body != test.wantBody {
				t.Fatalf("downstream body = %q, want %q", body, test.wantBody)
			}
			if got := header.Get("Digest"); got != test.wantMergedKey {
				t.Fatalf("downstream Digest = %q, want %q", got, test.wantMergedKey)
			}
			if got := header.Get(TrailerHeader); got != "" {
				t.Fatalf("downstream Trailer = %q, want removed", got)
			}
		})
	}
}

func TestGatewayRejectsTruncatedBody(t *testing.T) {
	downstream := &downstreamRecorder{}
	gateway := trailerGateway(hmacDigest([]byte("gateway-test-key")), downstream)

	// The chunk size claims more bytes than are delivered. The gateway must
	// abandon the message before invoking downstream.
	raw := "POST / HTTP/1.1\r\nHost: gateway.test\r\n" +
		"Content-Type: text/plain\r\nTrailer: Digest\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"10\r\nshort"
	request, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	defer request.Body.Close()

	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if calls, _, _ := downstream.snapshot(); calls != 0 {
		t.Fatalf("downstream calls = %d, want 0", calls)
	}
}

func trailerGateway(digest DigestFunc, downstream *downstreamRecorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		declared := CaptureDeclaredTrailers(r.Trailer)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		merged, err := MergeSignedTrailersWithDeclaration(r.Header, r.Trailer, declared, digest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		downstream.serve(merged, string(body))
		w.WriteHeader(http.StatusNoContent)
	})
}

func parseRawChunkedRequest(t *testing.T, header http.Header, body string, trailers http.Header) *http.Request {
	t.Helper()

	var request strings.Builder
	request.WriteString("POST / HTTP/1.1\r\nHost: gateway.test\r\n")
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range header[key] {
			fmt.Fprintf(&request, "%s: %s\r\n", key, value)
		}
	}
	request.WriteString("Transfer-Encoding: chunked\r\n\r\n")
	fmt.Fprintf(&request, "%x\r\n%s\r\n0\r\n", len(body), body)

	trailerKeys := make([]string, 0, len(trailers))
	for key := range trailers {
		trailerKeys = append(trailerKeys, key)
	}
	sort.Strings(trailerKeys)
	for _, key := range trailerKeys {
		for _, value := range trailers[key] {
			fmt.Fprintf(&request, "%s: %s\r\n", key, value)
		}
	}
	request.WriteString("\r\n")

	parsed, err := http.ReadRequest(bufio.NewReader(strings.NewReader(request.String())))
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	return parsed
}
