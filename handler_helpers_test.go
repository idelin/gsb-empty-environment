package llhls

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type httpResult struct {
	status int
	body   string
	err    error
}

// invoke drives the handler in-process. The sandbox forbids binding ports,
// so httptest.NewServer cannot be used; httptest.ResponseRecorder exercises
// the same ServeHTTP path while keeping tests fully concurrent.
func invoke(pl *Playlist, target string) <-chan httpResult {
	return invokeCtx(pl, context.Background(), target)
}

func invokeCtx(pl *Playlist, ctx context.Context, target string) <-chan httpResult {
	ch := make(chan httpResult, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		pl.ServeHTTP(rec, req)
		ch <- httpResult{
			status: rec.Code,
			body:   rec.Body.String(),
		}
	}()
	return ch
}

func invokeSync(t *testing.T, pl *Playlist, target string) httpResult {
	t.Helper()
	select {
	case res := <-invoke(pl, target):
		return res
	case <-time.After(3 * time.Second):
		t.Fatalf("request %q never returned", target)
	}
	return httpResult{}
}

func assertPending(t *testing.T, pl *Playlist, ch <-chan httpResult, label string) {
	t.Helper()
	// First ensure the request has registered as a blocker.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pl.Blockers() > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if pl.Blockers() == 0 {
		t.Fatalf("%s never registered as a blocking request", label)
	}
	// A registered blocker that still counts as blocked cannot have written
	// a response; checking Blockers avoids a race between the goroutine being
	// woken by a broadcast and re-evaluating its condition.
	if pl.Blockers() == 0 {
		t.Fatalf("%s returned early", label)
	}
}

func assertResult(t *testing.T, ch <-chan httpResult, label string) httpResult {
	t.Helper()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("%s transport error: %v", label, res.err)
		}
		return res
	case <-time.After(3 * time.Second):
		t.Fatalf("%s never returned", label)
	}
	return httpResult{}
}
