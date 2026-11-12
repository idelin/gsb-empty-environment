package netsec

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type countingReadCloser struct {
	reader io.Reader
	read   int
}

func (b *countingReadCloser) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *countingReadCloser) Close() error {
	return nil
}

func newDirectRequest(t *testing.T, body string) (*http.Request, *countingReadCloser) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://example.test/upload", nil)
	counter := &countingReadCloser{reader: strings.NewReader(body)}
	req.Body = counter
	req.ContentLength = int64(len(body))
	req.Header.Set("Expect", "100-continue")
	return req, counter
}

func TestAuthFailureDoesNotReadBody(t *testing.T) {
	req, counter := newDirectRequest(t, "secret payload")
	nextCalled := false
	authCalled := false

	cfg := Config{
		MaxBodyBytes: 1024,
		Authenticate: func(*http.Request) (bool, int) {
			authCalled = true
			return false, http.StatusForbidden
		},
		Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			nextCalled = true
		}),
	}

	recorder := httptest.NewRecorder()
	cfg.ServeHTTP(recorder, req)

	if !authCalled {
		t.Fatal("authenticator was not called")
	}
	if nextCalled {
		t.Fatal("next handler was called after authentication failed")
	}
	if counter.read != 0 {
		t.Fatalf("read %d body bytes after authentication failed", counter.read)
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if got := recorder.Header().Get("Connection"); got != "close" {
		t.Fatalf("Connection = %q, want close", got)
	}
	if !req.Close {
		t.Fatal("request was not marked for connection close")
	}
}

func TestExpectValues(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		wantStatus   int
		wantNext     bool
		wantAuth     bool
		wantBodyRead int
	}{
		{
			name:         "exact value",
			value:        "100-continue",
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantAuth:     true,
			wantBodyRead: 3,
		},
		{
			name:         "case insensitive with optional whitespace",
			value:        " \t100-CONTINUE ",
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantAuth:     true,
			wantBodyRead: 3,
		},
		{
			name:       "unsupported expectation",
			value:      "200-ok",
			wantStatus: http.StatusExpectationFailed,
		},
		{
			name:       "comma separated expectation list",
			value:      "100-continue, 100-continue",
			wantStatus: http.StatusExpectationFailed,
		},
		{
			name:       "empty expectation",
			value:      "",
			wantStatus: http.StatusExpectationFailed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, counter := newDirectRequest(t, "abc")
			req.Header.Set("Expect", tc.value)

			authCalled, nextCalled := false, false
			cfg := Config{
				MaxBodyBytes: 1024,
				Authenticate: func(*http.Request) (bool, int) {
					authCalled = true
					return true, 0
				},
				Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					nextCalled = true
					if got := r.Header.Values("Expect"); len(got) != 0 {
						t.Errorf("Expect was forwarded: %q", got)
					}
					if _, err := io.ReadAll(r.Body); err != nil {
						t.Errorf("read body: %v", err)
					}
					w.WriteHeader(http.StatusOK)
				}),
			}

			recorder := httptest.NewRecorder()
			cfg.ServeHTTP(recorder, req)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
			if authCalled != tc.wantAuth {
				t.Fatalf("auth called = %t, want %t", authCalled, tc.wantAuth)
			}
			if nextCalled != tc.wantNext {
				t.Fatalf("next called = %t, want %t", nextCalled, tc.wantNext)
			}
			if counter.read != tc.wantBodyRead {
				t.Fatalf("read %d body bytes, want %d", counter.read, tc.wantBodyRead)
			}
		})
	}
}

func TestMultipleExpectHeadersRejected(t *testing.T) {
	req, counter := newDirectRequest(t, "data")
	req.Header["Expect"] = []string{"100-continue", "100-continue"}

	authCalled, nextCalled := false, false
	cfg := Config{
		MaxBodyBytes: 1024,
		Authenticate: func(*http.Request) (bool, int) {
			authCalled = true
			return true, 0
		},
		Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			nextCalled = true
		}),
	}

	recorder := httptest.NewRecorder()
	cfg.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if authCalled || nextCalled {
		t.Fatal("ambiguous request reached authentication or next handler")
	}
	if counter.read != 0 {
		t.Fatalf("read %d body bytes from rejected request", counter.read)
	}
}

func TestTransferEncodingWithContentLengthRejected(t *testing.T) {
	req, counter := newDirectRequest(t, "data")
	req.TransferEncoding = []string{"chunked"}

	authCalled, nextCalled := false, false
	cfg := Config{
		MaxBodyBytes: 1024,
		Authenticate: func(*http.Request) (bool, int) {
			authCalled = true
			return true, 0
		},
		Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			nextCalled = true
		}),
	}

	recorder := httptest.NewRecorder()
	cfg.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if authCalled || nextCalled {
		t.Fatal("ambiguous framing reached authentication or next handler")
	}
	if counter.read != 0 {
		t.Fatalf("read %d body bytes from rejected request", counter.read)
	}
}

func TestHTTP10ExpectIsIgnoredAndStripped(t *testing.T) {
	for _, value := range []string{"100-continue", "200-ok"} {
		t.Run(value, func(t *testing.T) {
			req, counter := newDirectRequest(t, "data")
			req.Proto = "HTTP/1.0"
			req.ProtoMajor = 1
			req.ProtoMinor = 0
			req.Header.Set("Expect", value)

			authCalled, nextCalled := false, false
			cfg := Config{
				MaxBodyBytes: 1024,
				Authenticate: func(*http.Request) (bool, int) {
					authCalled = true
					return true, 0
				},
				Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					nextCalled = true
					if got := r.Header.Values("Expect"); len(got) != 0 {
						t.Errorf("Expect was forwarded: %q", got)
					}
					if _, err := io.ReadAll(r.Body); err != nil {
						t.Errorf("read body: %v", err)
					}
					w.WriteHeader(http.StatusOK)
				}),
			}

			recorder := httptest.NewRecorder()
			cfg.ServeHTTP(recorder, req)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			if !authCalled || !nextCalled {
				t.Fatal("HTTP/1.0 request with Expect was not processed normally")
			}
			if counter.read != 4 {
				t.Fatalf("read %d body bytes, want 4", counter.read)
			}
		})
	}
}

func TestDeclaredContentLengthOverLimit(t *testing.T) {
	req, counter := newDirectRequest(t, "12345678")

	authCalled, nextCalled := false, false
	cfg := Config{
		MaxBodyBytes: 7,
		Authenticate: func(*http.Request) (bool, int) {
			authCalled = true
			return true, 0
		},
		Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			nextCalled = true
		}),
	}

	recorder := httptest.NewRecorder()
	cfg.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
	if authCalled || nextCalled {
		t.Fatal("oversized Content-Length reached authentication or next handler")
	}
	if counter.read != 0 {
		t.Fatalf("read %d body bytes from oversized request", counter.read)
	}
	if !req.Close {
		t.Fatal("oversized request was not marked for connection close")
	}
}

func TestStreamedBodyOverBudgetBecomes413(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.test/upload", nil)
	counter := &countingReadCloser{reader: strings.NewReader("0123456789")}
	req.Body = counter
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	req.Header.Set("Expect", "100-continue")

	var readErr error
	cfg := Config{
		MaxBodyBytes: 8,
		Authenticate: func(*http.Request) (bool, int) {
			return true, 0
		},
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, readErr = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}),
	}

	recorder := httptest.NewRecorder()
	cfg.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
	if !errors.Is(readErr, ErrBodyTooLarge) {
		t.Fatalf("body read error = %v, want ErrBodyTooLarge", readErr)
	}
	if counter.read > 9 {
		t.Fatalf("read %d body bytes; budget enforcement kept reading", counter.read)
	}
	if !req.Close {
		t.Fatal("request was not marked for connection close after overflow")
	}
}
