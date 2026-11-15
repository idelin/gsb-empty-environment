package methodgate

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_BlockedRequestNeverReachesUpstream(t *testing.T) {
	gate := newTestGate(t)

	upstreamCalled := false
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
	})

	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`[{"jsonrpc":"2.0","method":"eth_chainId","id":1},
		             {"jsonrpc":"2.0","method":"personal_unlockAccount","id":2}]`))
	rec := httptest.NewRecorder()

	gate.Handler(upstream).ServeHTTP(rec, req)

	if upstreamCalled {
		t.Fatal("upstream must not be invoked for a rejected batch")
	}
	var envelope struct {
		JSONRPC string    `json:"jsonrpc"`
		Error   *RPCError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not valid JSON-RPC: %v", err)
	}
	if envelope.JSONRPC != "2.0" || envelope.Error == nil || envelope.Error.Code != CodeMethodNotFound {
		t.Fatalf("unexpected response envelope: %+v", envelope)
	}
}

func TestHandler_AllowedRequestForwardsBody(t *testing.T) {
	gate := newTestGate(t)

	const payload = `{"jsonrpc":"2.0","method":"eth_chainId","id":1}`
	var seen string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		seen = string(data)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	gate.Handler(upstream).ServeHTTP(rec, req)

	if seen != payload {
		t.Fatalf("upstream received %q, want %q", seen, payload)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandler_RejectsNonPost(t *testing.T) {
	gate := newTestGate(t)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream must not be called for GET")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	gate.Handler(upstream).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
