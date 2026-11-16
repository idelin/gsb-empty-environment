package netsec

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareRestoresBodyAndForwardsSafeRequest(t *testing.T) {
	gate := testGate(t)
	var forwarded string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("downstream ReadAll() error = %v", err)
		}
		forwarded = string(body)
		w.WriteHeader(http.StatusNoContent)
	})

	body := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":7}`
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	response := httptest.NewRecorder()

	gate.Middleware(next).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if forwarded != body {
		t.Fatalf("forwarded body = %q, want %q", forwarded, body)
	}
}

func TestMiddlewareRejectsBeforeForwarding(t *testing.T) {
	gate := testGate(t)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	body := `[
		{"jsonrpc":"2.0","method":"eth_call","params":[],"id":1},
		{"jsonrpc":"2.0","method":"personal_unlockAccount","params":[],"id":2}
	]`
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	response := httptest.NewRecorder()

	gate.Middleware(next).ServeHTTP(response, request)

	if called {
		t.Fatal("downstream handler was called for rejected batch")
	}
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	if !strings.Contains(response.Body.String(), `"jsonrpc":"2.0"`) {
		t.Fatalf("response body = %q, want JSON-RPC error", response.Body.String())
	}
}

func TestMiddlewareRejectsOversizedBody(t *testing.T) {
	gate := MustNew(Config{
		AllowedMethods: []string{"eth_call"},
		MaxBodyBytes:   32,
	})
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	body := `{"jsonrpc":"2.0","method":"eth_call","params":[],"id":1}`
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	response := httptest.NewRecorder()

	gate.Middleware(next).ServeHTTP(response, request)

	if called {
		t.Fatal("downstream handler was called for oversized request")
	}
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestValidateRequestRestoresBodyOnRejection(t *testing.T) {
	gate := testGate(t)
	body := `{"jsonrpc":"2.0","method":"personal_unlockAccount","params":[],"id":1}`
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))

	err := gate.ValidateRequest(request)
	if !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("ValidateRequest() error = %v, want ErrMethodNotAllowed", err)
	}
	restored, readErr := io.ReadAll(request.Body)
	if readErr != nil {
		t.Fatalf("ReadAll(restored body) error = %v", readErr)
	}
	if string(restored) != body {
		t.Fatalf("restored body = %q, want %q", restored, body)
	}
}
