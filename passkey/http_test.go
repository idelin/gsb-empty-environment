package passkey

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func doAssertion(t *testing.T, h http.Handler, in AssertionInput) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/login/complete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTP_InvalidSignatureThenValidRetry(t *testing.T) {
	f := newFixture(t, false, 5)
	h := NewLoginCompleteHandler(f.svc)

	bad := f.defaultOpt(false, 6)
	bad.damageSig = true
	rec := doAssertion(t, h, f.buildAssertion(t, bad))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	rec = doAssertion(t, h, f.buildAssertion(t, f.defaultOpt(false, 6)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var ok loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ok.Token == "" || ok.Counter != "advanced" {
		t.Fatalf("unexpected success body: %+v", ok)
	}
}

func TestHTTP_ConcurrentIndependentAssertionsOneSession(t *testing.T) {
	f := newFixture(t, false, 20)
	h := NewLoginCompleteHandler(f.svc)

	const n = 24
	inputs := make([]AssertionInput, n)
	for i := range inputs {
		inputs[i] = f.buildAssertion(t, f.defaultOpt(false, 21))
	}

	statuses := make(chan int, n)
	var wg sync.WaitGroup
	for i := range inputs {
		wg.Add(1)
		go func(in AssertionInput) {
			defer wg.Done()
			statuses <- doAssertion(t, h, in).Code
		}(inputs[i])
	}
	wg.Wait()
	close(statuses)

	var okCount, conflict int
	for code := range statuses {
		switch code {
		case http.StatusOK:
			okCount++
		case http.StatusConflict, http.StatusUnauthorized:
			// Loser either missed the single-use challenge (409) or the counter
			// CAS (401); neither may produce a session.
			conflict++
		default:
			t.Fatalf("unexpected status %d", code)
		}
	}
	if okCount != 1 {
		t.Fatalf("200 count = %d, want 1", okCount)
	}
	if okCount+conflict != n {
		t.Fatalf("handled = %d, want %d", okCount+conflict, n)
	}
	if f.svc.Sessions.Len() != 1 {
		t.Fatalf("sessions = %d, want 1", f.svc.Sessions.Len())
	}
}

func TestHTTP_StatusMappingAndMethod(t *testing.T) {
	f := newFixture(t, false, 1)
	h := NewLoginCompleteHandler(f.svc)

	// GET is rejected.
	req := httptest.NewRequest(http.MethodGet, "/login/complete", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}

	// Malformed JSON body.
	req = httptest.NewRequest(http.MethodPost, "/login/complete", bytes.NewReader([]byte("{not json")))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	// Unknown challenge id -> 404.
	in := f.buildAssertion(t, f.defaultOpt(false, 2))
	in.ChallengeID = "missing"
	rec = doAssertion(t, h, in)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	// Counter replay -> 401 and challenge retained; then valid -> 200.
	if rec = doAssertion(t, h, f.buildAssertion(t, f.defaultOpt(false, 1))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want 401", rec.Code)
	}
	if rec = doAssertion(t, h, f.buildAssertion(t, f.defaultOpt(false, 2))); rec.Code != http.StatusOK {
		t.Fatalf("valid status = %d, want 200; %s", rec.Code, rec.Body.String())
	}
}
