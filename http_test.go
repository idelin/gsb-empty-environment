package netsec

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

type headerWrite struct {
	status int
	header http.Header
}

// wireRecorder is a response receiver that snapshots the exact header map
// passed to each WriteHeader call. It models the observable behavior of an
// HTTP response writer without requiring a listening socket.
type wireRecorder struct {
	header    http.Header
	mu        sync.Mutex
	writes    []headerWrite
	body      bytes.Buffer
	finalized bool
}

func newWireRecorder() *wireRecorder {
	return &wireRecorder{header: make(http.Header)}
}

func (w *wireRecorder) Header() http.Header {
	return w.header
}

func (w *wireRecorder) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finalized {
		return
	}
	w.writes = append(w.writes, headerWrite{
		status: status,
		header: w.header.Clone(),
	})
	if status < 100 || status > 199 || status == http.StatusSwitchingProtocols {
		w.finalized = true
	}
}

func (w *wireRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	if !w.finalized {
		w.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.mu.Lock()
	}
	defer w.mu.Unlock()
	return w.body.Write(p)
}

func (w *wireRecorder) snapshots() []headerWrite {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]headerWrite, len(w.writes))
	for i, write := range w.writes {
		out[i] = headerWrite{status: write.status, header: write.header.Clone()}
	}
	return out
}

func TestHTTPWriterInterleavedHeaderSequences(t *testing.T) {
	wireA := newWireRecorder()
	wireB := newWireRecorder()
	// Pre-existing final headers must not be copied into 103 responses, and
	// the adapter must restore them for the final response.
	wireA.Header().Set("X-Final-Only", "a")
	wireB.Header().Set("X-Final-Only", "b")

	respA, err := NewHTTPResponse(context.Background(), "tenant-a", wireA)
	if err != nil {
		t.Fatal(err)
	}
	respB, err := NewHTTPResponse(context.Background(), "tenant-b", wireB)
	if err != nil {
		t.Fatal(err)
	}

	if err := respA.Hint(http.Header{
		"Link":          {"</a1.css>; rel=preload"},
		"Authorization": {"Bearer tenant-a-secret"},
		"Set-Cookie":    {"tenant-a-session=secret"},
		"X-Tenant":      {"tenant-a"},
	}); err != nil {
		t.Fatalf("a hint 1: %v", err)
	}
	if err := respB.Hint(http.Header{
		"link":       {"</b1.js>; rel=preload"},
		"Cookie":     {"tenant-b-session=secret"},
		"X-Tenant":   {"tenant-b"},
		"User-Agent": {"tenant-b-agent"},
	}); err != nil {
		t.Fatalf("b hint 1: %v", err)
	}
	if err := respA.Hint(linkHeader("</a2.js>; rel=preload")); err != nil {
		t.Fatalf("a hint 2: %v", err)
	}
	if err := respB.Final(http.StatusOK, http.Header{
		"Content-Type": {"text/html"},
		"X-Tenant":     {"tenant-b"},
	}); err != nil {
		t.Fatalf("b final: %v", err)
	}
	if err := respA.Final(http.StatusCreated, http.Header{
		"Content-Type": {"application/json"},
		"X-Tenant":     {"tenant-a"},
	}); err != nil {
		t.Fatalf("a final: %v", err)
	}
	if err := respA.Hint(linkHeader("</a-late.css>")); !errors.Is(err, ErrLateHint) {
		t.Fatalf("a late hint = %v, want ErrLateHint", err)
	}

	assertHTTPHints(t, "tenant-a", wireA.snapshots(), []string{
		"</a1.css>; rel=preload",
		"</a2.js>; rel=preload",
	})
	assertHTTPHints(t, "tenant-b", wireB.snapshots(), []string{
		"</b1.js>; rel=preload",
	})
	assertHTTPFinal(t, "tenant-a", wireA.snapshots(), http.StatusCreated, "tenant-a")
	assertHTTPFinal(t, "tenant-b", wireB.snapshots(), http.StatusOK, "tenant-b")
}

func TestHTTPWriterCancellationStopsOneReceiver(t *testing.T) {
	wireA := newWireRecorder()
	wireB := newWireRecorder()
	ctxA, cancelA := context.WithCancel(context.Background())

	respA, err := NewHTTPResponse(ctxA, "tenant-a", wireA)
	if err != nil {
		t.Fatal(err)
	}
	respB, err := NewHTTPResponse(context.Background(), "tenant-b", wireB)
	if err != nil {
		t.Fatal(err)
	}

	if err := respA.Hint(linkHeader("</a1.css>")); err != nil {
		t.Fatalf("a hint 1: %v", err)
	}
	if err := respB.Hint(linkHeader("</b1.css>")); err != nil {
		t.Fatalf("b hint 1: %v", err)
	}

	cancelA()
	select {
	case <-respA.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("tenant-a did not observe cancellation")
	}
	if err := respA.Hint(linkHeader("</a2-late.css>")); !errors.Is(err, ErrCanceled) {
		t.Fatalf("a hint after cancel = %v, want ErrCanceled", err)
	}
	if err := respA.Final(http.StatusOK, nil); !errors.Is(err, ErrCanceled) {
		t.Fatalf("a final after cancel = %v, want ErrCanceled", err)
	}

	if err := respB.Hint(linkHeader("</b2.css>")); err != nil {
		t.Fatalf("b hint 2 after a cancellation: %v", err)
	}
	if err := respB.Final(http.StatusOK, http.Header{
		"Content-Type": {"text/plain"},
		"X-Tenant":     {"tenant-b"},
	}); err != nil {
		t.Fatalf("b final: %v", err)
	}

	assertHTTPHints(t, "tenant-a", wireA.snapshots(), []string{"</a1.css>"})
	assertHTTPHints(t, "tenant-b", wireB.snapshots(), []string{"</b1.css>", "</b2.css>"})
	assertHTTPFinal(t, "tenant-a", wireA.snapshots(), 0, "")
	assertHTTPFinal(t, "tenant-b", wireB.snapshots(), http.StatusOK, "tenant-b")
}

func TestHTTPEmitterRejectsWritesAfterFinal(t *testing.T) {
	wire := newWireRecorder()
	emitter, err := NewHTTPEmitter(wire)
	if err != nil {
		t.Fatal(err)
	}
	if err := emitter.Emit(Stage{Kind: StageFinal, Status: http.StatusNoContent}); err != nil {
		t.Fatal(err)
	}
	if err := emitter.Emit(Stage{Kind: StageHint, Status: StatusEarlyHints, Header: linkHeader("</late.css>")}); !errors.Is(err, ErrLateHint) {
		t.Fatalf("late emitter hint = %v, want ErrLateHint", err)
	}
	if err := emitter.Emit(Stage{Kind: StageFinal, Status: http.StatusOK}); !errors.Is(err, ErrFinalized) {
		t.Fatalf("second emitter final = %v, want ErrFinalized", err)
	}
	if got := len(wire.snapshots()); got != 1 {
		t.Fatalf("writes = %d, want 1", got)
	}
}

func TestHTTPEmitterFiltersDirectStages(t *testing.T) {
	wire := newWireRecorder()
	emitter, err := NewHTTPEmitter(wire)
	if err != nil {
		t.Fatal(err)
	}
	if err := emitter.Emit(Stage{
		Kind:   StageHint,
		Status: StatusEarlyHints,
		Header: http.Header{
			"Link":          {"</ok.css>"},
			"Authorization": {"Bearer secret"},
			"Set-Cookie":    {"sid=secret"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := emitter.Emit(Stage{
		Kind:   StageHint,
		Status: StatusEarlyHints,
		Header: http.Header{"Authorization": {"Bearer secret"}},
	}); !errors.Is(err, ErrNoHintFields) {
		t.Fatalf("auth-only direct hint = %v, want ErrNoHintFields", err)
	}
	writes := wire.snapshots()
	if len(writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(writes))
	}
	assertOnlyFields(t, writes[0].header, "Link")
}

func TestHTTPEmitterRejectsTypedNilWriter(t *testing.T) {
	var writer *wireRecorder
	if _, err := NewHTTPEmitter(writer); err == nil {
		t.Fatal("typed nil response writer was accepted")
	}
}

func TestHTTPWriterUsesResponseHintPolicy(t *testing.T) {
	policy, err := NewHintPolicy("X-Resource")
	if err != nil {
		t.Fatal(err)
	}
	wire := newWireRecorder()
	resp, err := NewHTTPResponse(context.Background(), "tenant-custom", wire, WithHintPolicy(policy))
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Hint(http.Header{
		"X-Resource":    {"/app.js"},
		"Link":          {"</not-allowed.css>"},
		"Authorization": {"Bearer secret"},
	}); err != nil {
		t.Fatalf("hint: %v", err)
	}
	writes := wire.snapshots()
	if len(writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(writes))
	}
	assertOnlyFields(t, writes[0].header, "X-Resource")
	if got := writes[0].header.Get("X-Resource"); got != "/app.js" {
		t.Fatalf("X-Resource = %q", got)
	}
}

func assertHTTPHints(t *testing.T, id string, writes []headerWrite, wantLinks []string) {
	t.Helper()
	if len(writes) < len(wantLinks) {
		t.Fatalf("%s writes = %d, want at least %d hints", id, len(writes), len(wantLinks))
	}
	for i, wantLink := range wantLinks {
		write := writes[i]
		if write.status != StatusEarlyHints {
			t.Fatalf("%s write %d status = %d, want 103", id, i, write.status)
		}
		assertOnlyFields(t, write.header, "Link")
		if got := write.header.Values("Link"); !equalStrings(got, []string{wantLink}) {
			t.Fatalf("%s write %d Link = %v, want %q", id, i, got, wantLink)
		}
		for _, forbidden := range []string{
			"Authorization", "Set-Cookie", "Cookie", "X-Tenant", "X-Final-Only", "User-Agent",
		} {
			if got := write.header.Get(forbidden); got != "" {
				t.Fatalf("%s write %d leaked %s = %q", id, i, forbidden, got)
			}
		}
	}
}

func assertHTTPFinal(t *testing.T, id string, writes []headerWrite, wantStatus int, wantTenant string) {
	t.Helper()
	if wantStatus == 0 {
		if len(writes) != 1 {
			t.Fatalf("%s got final write after cancellation: %v", id, writes)
		}
		return
	}
	if len(writes) == 0 {
		t.Fatalf("%s received no writes", id)
	}
	final := writes[len(writes)-1]
	if final.status != wantStatus {
		t.Fatalf("%s final status = %d, want %d", id, final.status, wantStatus)
	}
	if got := final.header.Get("X-Tenant"); got != wantTenant {
		t.Fatalf("%s final X-Tenant = %q, want %q", id, got, wantTenant)
	}
	for _, forbidden := range []string{"Link", "Alt-Svc", "Authorization", "Set-Cookie", "Cookie"} {
		if got := final.header.Get(forbidden); got != "" {
			t.Fatalf("%s final leaked hint field %s = %q", id, forbidden, got)
		}
	}
}
